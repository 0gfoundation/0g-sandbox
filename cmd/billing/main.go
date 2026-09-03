package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/auth"
	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/config"
	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/0gfoundation/0g-sandbox/internal/events"
	"github.com/0gfoundation/0g-sandbox/internal/observability"
	"github.com/0gfoundation/0g-sandbox/internal/proxy"
	"github.com/0gfoundation/0g-sandbox/internal/registry"
	"github.com/0gfoundation/0g-sandbox/internal/settler"
	"github.com/0gfoundation/0g-sandbox/internal/tee"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
	"github.com/0gfoundation/0g-sandbox/web"
)

func main() {
	log, _ := zap.NewProduction()
	defer log.Sync() //nolint:errcheck

	cfg, err := config.Load()
	if err != nil {
		log.Fatal("config load failed", zap.Error(err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: cfg.Redis.Password,
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal("redis ping failed", zap.Error(err))
	}

	// ── TEE signing key ───────────────────────────────────────────────────────
	// Fetched from the tapp-daemon via gRPC in a real TDX environment, or from
	// MOCK_APP_PRIVATE_KEY when MOCK_TEE is set.
	appKey, err := tee.Get(ctx)
	if err != nil {
		log.Fatal("failed to retrieve TEE signing key", zap.Error(err))
	}
	cfg.Chain.TEEPrivateKey = appKey.PrivateKeyHex

	// ── Chain client (TEE private key + ABI binding) ──────────────────────────
	onchain, err := chain.NewClient(cfg)
	if err != nil {
		log.Fatal("chain client init failed", zap.Error(err))
	}

	// Provider identity = the TEE signer's own address (v2: provider IS the
	// TEE signer). It keys the voucher payee, the settler queue, and every
	// provider-bound lookup below.
	providerAddr := onchain.ProviderAddress()
	providerHex := providerAddr.Hex()

	// The appId owner is resolved from the chain, never configured:
	// getAppInfo(BACKEND_APP_NAME).owner. It is the standing admin for
	// operator endpoints and is surfaced through /api/info. TTL-cached; on
	// lookup failure the last known value is served so a flaky RPC can't
	// lock the owner out mid-session.
	backendAppName := os.Getenv("BACKEND_APP_NAME")
	if backendAppName == "" {
		log.Warn("BACKEND_APP_NAME not set — app owner cannot be resolved; only ADMIN_ADDRESSES wallets are admins")
	}
	appOwnerFn := newAppOwnerResolver(onchain, backendAppName, appOwnerTTL, appOwnerStaleCap, appOwnerRetryEvery, log)
	if owner, err := appOwnerFn(ctx); err != nil {
		log.Warn("app owner not resolvable yet", zap.String("app_id", backendAppName), zap.Error(err))
	} else {
		log.Info("provider identity derived from TEE key",
			zap.String("provider", providerHex),
			zap.String("app_id", backendAppName),
			zap.String("owner", owner))
	}
	// Drift check: the service this signer is registered under must be bound
	// to the same appId we fetch our TEE key for. A mismatch means the key
	// and the on-chain registration point at different apps.
	if svcAppId, err := onchain.GetServiceAppId(ctx, providerAddr); err == nil && svcAppId != "" && backendAppName != "" && svcAppId != backendAppName {
		log.Error("appId drift: on-chain service appId != BACKEND_APP_NAME",
			zap.String("on_chain", svcAppId), zap.String("configured", backendAppName))
	}

	// ── Pricing: on-chain service registration is the source of truth ────────
	// Read per-resource prices and createFee from the contract so users can
	// verify the actual billing rate on the chain explorer.
	// Fall back to env vars only when the service is not yet registered.
	chainCPUPerSec, chainMemPerSec, createFee, err := onchain.GetServicePricing(ctx, providerAddr)
	if err != nil {
		log.Warn("could not read on-chain service pricing; falling back to env vars", zap.Error(err))
	}

	// Per-CPU price: on-chain takes priority; fall back to env var.
	pricePerCPUPerSec := chainCPUPerSec
	if pricePerCPUPerSec == nil || pricePerCPUPerSec.Sign() == 0 {
		pricePerCPUPerSec = new(big.Int)
		if cfg.Billing.PricePerCPUPerSec != "0" && cfg.Billing.PricePerCPUPerSec != "" {
			if _, ok := pricePerCPUPerSec.SetString(cfg.Billing.PricePerCPUPerSec, 10); !ok {
				log.Fatal("invalid PRICE_PER_CPU_PER_SEC")
			}
		}
		log.Info("using env PRICE_PER_CPU_PER_SEC (service not on-chain or zero)", zap.String("value", pricePerCPUPerSec.String()))
	} else {
		log.Info("using on-chain pricePerCPUPerSec", zap.String("value", pricePerCPUPerSec.String()))
	}

	// Per-mem price: on-chain takes priority; fall back to env var.
	pricePerMemGBPerSec := chainMemPerSec
	if pricePerMemGBPerSec == nil || pricePerMemGBPerSec.Sign() == 0 {
		pricePerMemGBPerSec = new(big.Int)
		if cfg.Billing.PricePerMemGBPerSec != "0" && cfg.Billing.PricePerMemGBPerSec != "" {
			if _, ok := pricePerMemGBPerSec.SetString(cfg.Billing.PricePerMemGBPerSec, 10); !ok {
				log.Fatal("invalid PRICE_PER_MEM_GB_PER_SEC")
			}
		}
		log.Info("using env PRICE_PER_MEM_GB_PER_SEC (service not on-chain or zero)", zap.String("value", pricePerMemGBPerSec.String()))
	} else {
		log.Info("using on-chain pricePerMemGBPerSec", zap.String("value", pricePerMemGBPerSec.String()))
	}

	// Flat compute price (legacy fallback when both per-resource prices are 0).
	// Seeded from env var; not read from chain anymore (chain now stores per-resource).
	computePricePerSec := new(big.Int)
	if pricePerCPUPerSec.Sign() == 0 && pricePerMemGBPerSec.Sign() == 0 {
		var ok bool
		computePricePerSec, ok = new(big.Int).SetString(cfg.Billing.ComputePricePerSec, 10)
		if !ok {
			log.Fatal("invalid COMPUTE_PRICE_PER_SEC")
		}
		log.Info("using flat COMPUTE_PRICE_PER_SEC (both per-resource prices are 0)", zap.String("value", computePricePerSec.String()))
	}

	// Create fee: on-chain takes priority; fall back to env var.
	if createFee == nil || createFee.Sign() == 0 {
		var ok bool
		createFee, ok = new(big.Int).SetString(cfg.Billing.CreateFee, 10)
		if !ok {
			log.Fatal("invalid CREATE_FEE")
		}
		log.Info("using env CREATE_FEE (service not on-chain)", zap.String("value", createFee.String()))
	} else {
		log.Info("using on-chain create fee", zap.String("value", createFee.String()))
	}

	signer := billing.NewSigner(
		onchain.PrivateKey(),
		onchain.ChainID(),
		onchain.ContractAddress(),
		providerAddr,
		rdb,
		onchain,
		log,
	)

	// ── Daytona client ────────────────────────────────────────────────────────
	dtona := daytona.NewClient(cfg.Daytona.APIURL, cfg.Daytona.AdminKey)

	// ── Billing event handler ─────────────────────────────────────────────────
	billingHandler := billing.NewEventHandler(
		rdb,
		providerHex,
		computePricePerSec,
		createFee,
		pricePerCPUPerSec,
		pricePerMemGBPerSec,
		cfg.Billing.VoucherIntervalSec,
		signer,
		log,
	)

	// Minimum balance = createFee + one voucher interval of compute fees (per-second pricing).
	minBalance := new(big.Int).Add(createFee, new(big.Int).Mul(computePricePerSec, big.NewInt(cfg.Billing.VoucherIntervalSec)))

	// ── Stop channel (settler → stop handler, buffered) ───────────────────────
	stopCh := make(chan settler.StopSignal, 100)

	// ── Alerter ───────────────────────────────────────────────────────────────
	// Always construct: persists to Redis (for the dashboard) + logs even
	// without a webhook URL. With ALERT_WEBHOOK_URL set, also dispatches
	// to the configured destination (Slack/PagerDuty/etc).
	dedup := time.Duration(cfg.Alert.DedupWindowSec) * time.Second
	alerter := alert.NewWebhook(cfg.Alert.WebhookURL, providerHex, rdb, dedup, log)
	if cfg.Alert.WebhookURL != "" {
		log.Info("alert webhook configured", zap.Duration("dedup", dedup))
	} else {
		log.Info("alert webhook not configured — alerts persist to Redis + logs only")
	}

	// ── Goroutines ────────────────────────────────────────────────────────────
	// Recovery must start after stopCh is ready but before settler writes to it.
	go recoverPendingStops(ctx, rdb, stopCh, log)
	go settler.Run(ctx, cfg, rdb, onchain, signer, stopCh, alerter, log)
	go billing.RunGenerator(ctx, rdb, billingHandler, log)

	// Balance + queue depth + signer-mismatch monitors. All best-effort —
	// they surface problems but don't gate the hot path. Signer-mismatch in
	// particular is the only safety net against KMS rotation drift, since
	// INVALID_SIGNATURE on-chain emits no event and accumulates silently.
	go observability.RunBalanceMonitor(ctx, onchain, alerter, cfg.Alert.SettlerLowBalanceFactor, log)
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, providerHex)
	go observability.RunQueueDepthMonitor(ctx, rdb, queueKey, alerter, cfg.Alert.QueueBacklogThreshold, log)
	go observability.RunSignerMismatchMonitor(ctx, onchain, providerAddr, alerter, log)

	// ── HTTP server ───────────────────────────────────────────────────────────
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.RedirectTrailingSlash = false // prevent 307 redirect on CORS preflight for /sandbox/:id
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Wallet-Address, X-Signed-Message, X-Wallet-Signature, Daytona-Admin-Key")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	})
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	// Provider home is the operator dashboard. The user-facing market page
	// (UserHTML) is served by the broker; end users reach providers through it.
	r.GET("/", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", web.DashboardHTML)
	})
	r.GET("/dashboard", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", web.DashboardHTML)
	})
	r.GET("/static/ethers.js", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", web.EthersJS)
	})
	r.GET("/static/logo.svg", func(c *gin.Context) {
		c.Data(http.StatusOK, "image/svg+xml", web.LogoSVG)
	})
	// Public providers list — returns known providers with their on-chain service data.
	r.GET("/api/providers", func(c *gin.Context) {
		type ProviderInfo struct {
			Address             string `json:"address"`
			URL                 string `json:"url"`
			AppId               string `json:"app_id"`
			PricePerCPUPerMin   string `json:"price_per_cpu_per_min"`
			PricePerCPUPerSec   string `json:"price_per_cpu_per_sec"`
			PricePerMemGBPerMin string `json:"price_per_mem_gb_per_min"`
			PricePerMemGBPerSec string `json:"price_per_mem_gb_per_sec"`
			CreateFee           string `json:"create_fee"`
		}
		// For now: just the configured provider.  Extend via KNOWN_PROVIDERS in the future.
		addrs := []string{providerHex}
		var providers []ProviderInfo
		for _, addr := range addrs {
			if addr == "" {
				continue
			}
			svcInfo, err := onchain.GetServiceInfo(c.Request.Context(), common.HexToAddress(addr))
			if err != nil || svcInfo == nil {
				continue
			}
			cpuPerSec := new(big.Int).Div(svcInfo.PricePerCPUPerMin, big.NewInt(60))
			memPerSec := new(big.Int).Div(svcInfo.PricePerMemGBPerMin, big.NewInt(60))
			providers = append(providers, ProviderInfo{
				Address:             addr,
				URL:                 svcInfo.URL,
				AppId:               svcInfo.AppId,
				PricePerCPUPerMin:   svcInfo.PricePerCPUPerMin.String(),
				PricePerCPUPerSec:   cpuPerSec.String(),
				PricePerMemGBPerMin: svcInfo.PricePerMemGBPerMin.String(),
				PricePerMemGBPerSec: memPerSec.String(),
				CreateFee:           svcInfo.CreateFee.String(),
			})
		}
		if providers == nil {
			providers = []ProviderInfo{}
		}
		c.JSON(http.StatusOK, providers)
	})

	// ownerForInfo returns the resolved app owner or "" — /api/info is
	// best-effort display, not an auth surface.
	ownerForInfo := func(ctx context.Context) string {
		owner, err := appOwnerFn(ctx)
		if err != nil {
			return ""
		}
		return owner
	}

	rpcOrigin := cfg.Chain.RPCURL
	if u, err := url.Parse(cfg.Chain.RPCURL); err == nil {
		rpcOrigin = u.Scheme + "://" + u.Host
	}
	r.GET("/api/info", func(c *gin.Context) {
		ctx := c.Request.Context()
		settlerAddr := onchain.SettlerAddress()

		// Signer health — derived from sandbox.services[provider].appId and
		// tap.getNode(appId, settler).addedAt. All on-chain readable; no admin
		// wallet required.
		signer := gin.H{"local": settlerAddr.Hex(), "status": "unknown"}
		appId, appErr := onchain.GetServiceAppId(ctx, providerAddr)
		if appErr != nil {
			signer["error"] = appErr.Error()
		} else if appId == "" {
			signer["status"] = "unregistered"
		} else {
			signer["app_id"] = appId
			isNode, nodeErr := onchain.IsActiveNode(ctx, appId, settlerAddr)
			if nodeErr != nil {
				signer["error"] = nodeErr.Error()
			} else if isNode {
				signer["status"] = "aligned"
			} else {
				signer["status"] = "mismatch"
			}
		}

		// Settler balance — also public (any chain RPC reveals wallet balance).
		settler := gin.H{"address": settlerAddr.Hex(), "status": "unknown"}
		if bal, err := onchain.BalanceAt(ctx, settlerAddr); err == nil {
			if gasPrice, gErr := onchain.SuggestGasPrice(ctx); gErr == nil {
				oneTx := new(big.Int).Mul(gasPrice, big.NewInt(300_000))
				warningThreshold := new(big.Int).Mul(oneTx, big.NewInt(cfg.Alert.SettlerLowBalanceFactor))
				status := "healthy"
				switch {
				case bal.Cmp(oneTx) < 0:
					status = "critical"
				case bal.Cmp(warningThreshold) < 0:
					status = "warning"
				}
				settler["balance_wei"] = bal.String()
				settler["gas_price_wei"] = gasPrice.String()
				settler["one_tx_cost_wei"] = oneTx.String()
				settler["warning_threshold_wei"] = warningThreshold.String()
				settler["status"] = status
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"contract_address":      cfg.Chain.ContractAddress,
			"provider_address":      providerHex,
			"owner_address":         ownerForInfo(c.Request.Context()),
			"app_id":                backendAppName,
			"chain_id":              cfg.Chain.ChainID,
			"rpc_url":               rpcOrigin,
			"compute_price_per_sec": computePricePerSec.String(),
			"create_fee":            createFee.String(),
			"voucher_interval_sec":  cfg.Billing.VoucherIntervalSec,
			"min_balance":           minBalance.String(),
			"sealed_only":           cfg.Server.SealedOnly,
			"signer":                signer,
			"settler":               settler,
		})
	})

	// Public snapshots list — no signing required; snapshots are provider-managed
	// base images visible to all users.
	r.GET("/api/snapshots", func(c *gin.Context) {
		snaps, err := dtona.ListSnapshots(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
			return
		}
		if snaps == nil {
			snaps = []daytona.Snapshot{}
		}
		c.JSON(http.StatusOK, snaps)
	})

	// Registry images — lists Docker images in the internal registry.
	// Used by the provider dashboard to populate the snapshot image dropdown.
	r.GET("/api/registry/images", func(c *gin.Context) {
		registryURL := cfg.Daytona.RegistryURL
		httpClient := &http.Client{Timeout: 10 * time.Second}
		resp, err := httpClient.Get(registryURL + "/v2/_catalog")
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "registry unavailable"})
			return
		}
		defer resp.Body.Close()
		var catalog struct {
			Repositories []string `json:"repositories"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "decode catalog"})
			return
		}
		var images []string
		for _, repo := range catalog.Repositories {
			// Skip internal Daytona sandbox images and backup archives.
			base := repo
			if idx := strings.LastIndex(repo, "/"); idx >= 0 {
				base = repo[idx+1:]
			}
			if strings.HasPrefix(base, "daytona-") || strings.HasPrefix(base, "backup-") {
				continue
			}
			tagsResp, err := httpClient.Get(registryURL + "/v2/" + repo + "/tags/list")
			if err != nil {
				continue
			}
			var tagList struct {
				Tags []string `json:"tags"`
			}
			json.NewDecoder(tagsResp.Body).Decode(&tagList) //nolint:errcheck
			tagsResp.Body.Close()
			for _, tag := range tagList.Tags {
				images = append(images, "registry:6000/"+repo+":"+tag)
			}
		}
		if images == nil {
			images = []string{}
		}
		c.JSON(http.StatusOK, images)
	})

	// Public sandbox list — no signing required, filters by ?wallet= query param.
	// Sandbox ownership is public (on-chain labels), so this exposes no sensitive data.
	r.GET("/api/sandbox_list", func(c *gin.Context) {
		wallet := c.Query("wallet")
		if wallet == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "wallet required"})
			return
		}
		sandboxes, err := dtona.ListSandboxes(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
			return
		}
		var filtered []daytona.Sandbox
		for _, s := range sandboxes {
			if strings.EqualFold(s.Labels["daytona-owner"], wallet) {
				filtered = append(filtered, s)
			}
		}
		if filtered == nil {
			filtered = []daytona.Sandbox{}
		}
		c.JSON(http.StatusOK, filtered)
	})

	// Public read-only API surface — no wallet signature required.
	// Anything mounted here should be derivable from public chain RPC.
	apiPublic := r.Group("/api")
	api := r.Group("/api", auth.Middleware(rdb))
	proxyHandler := proxy.NewHandler(dtona, billingHandler, onchain, onchain, onchain, createFee, pricePerCPUPerSec, pricePerMemGBPerSec, computePricePerSec, providerHex, cfg.Chain.AdminList(), cfg.Server.SSHGatewayHost, rdb, log, cfg.Server.BrokerURL, onchain.PrivateKey(), cfg.Billing.VoucherIntervalSec)
	proxyHandler.SealedOnly = cfg.Server.SealedOnly
	proxyHandler.AppOwner = appOwnerFn
	proxyHandler.RegisterPublic(apiPublic)
	proxyHandler.Register(api)
	go runStopHandler(ctx, stopCh, dtona, rdb, log, proxyHandler.BrokerDeregister)

	// Admin-only: pull an image from an external registry into the internal registry.
	// The import runs synchronously (crane.Copy) — may take minutes for large images.
	api.POST("/registry/pull", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !proxyHandler.IsAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		var req struct {
			Src      string `json:"src"`      // e.g. "docker.io/library/ubuntu:22.04"
			Name     string `json:"name"`     // target repo name under registry:6000/daytona/
			Tag      string `json:"tag"`      // target tag (must not be "latest")
			Username string `json:"username"` // optional src registry username
			Password string `json:"password"` // optional src registry password
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.Src == "" || req.Name == "" || req.Tag == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "src, name, and tag are required"})
			return
		}
		dst, err := registry.Copy(c.Request.Context(), req.Src, req.Name, req.Tag, req.Username, req.Password)
		if err != nil {
			log.Warn("registry pull failed", zap.Error(err))
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"image": dst})
	})

	// Admin-only: garbage-collect orphan derived tags. Lists every ":d-<hex>"
	// tag in the registry, removes those no longer referenced by any Daytona
	// snapshot. Pre-fix snapshots that were deleted before the snapshot-delete
	// hook existed left their derived tags behind — this drains that backlog.
	// Pass ?dry_run=true to preview without deleting.
	api.POST("/registry/gc", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !proxyHandler.IsAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		dryRun := c.Query("dry_run") == "true"

		candidates, err := registry.ListDerivedTags(c.Request.Context(), cfg.Daytona.RegistryURL)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		snaps, err := dtona.ListSnapshots(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		inUse := make(map[string]bool, len(snaps))
		for _, s := range snaps {
			if s.ImageName != "" {
				inUse[s.ImageName] = true
			}
		}

		deleted := []string{}
		kept := []string{}
		skipped := []map[string]string{}
		failed := []map[string]string{}
		for _, ref := range candidates {
			if inUse[ref] {
				kept = append(kept, ref)
				continue
			}
			if dryRun {
				deleted = append(deleted, ref)
				continue
			}
			if err := registry.DeleteTag(c.Request.Context(), ref); err != nil {
				if errors.Is(err, registry.ErrTagSharesManifest) {
					skipped = append(skipped, map[string]string{"tag": ref, "reason": err.Error()})
					continue
				}
				failed = append(failed, map[string]string{"tag": ref, "error": err.Error()})
				continue
			}
			deleted = append(deleted, ref)
		}

		c.JSON(http.StatusOK, gin.H{
			"dry_run":    dryRun,
			"candidates": len(candidates),
			"deleted":    deleted,
			"kept":       kept,
			"skipped":    skipped,
			"failed":     failed,
		})
	})

	// Admin-only: voucher backlog summary, grouped by (user, provider).
	// Scans up to voucher.MaxScanItems items; sets `truncated` when the queue
	// is bigger. `min_count` query param (default 1) — pass 2+ to hide
	// already-aggregated singleton rows.
	api.GET("/queue/summary", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !proxyHandler.IsAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		minCount := 1
		if v := c.Query("min_count"); v != "" {
			if n, err := fmt.Sscanf(v, "%d", &minCount); err != nil || n != 1 {
				minCount = 1
			}
		}
		queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, providerHex)
		rows, scanned, truncated, err := voucher.SummarizeQueue(c.Request.Context(), rdb, queueKey, minCount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"rows":      rows,
			"scanned":   scanned,
			"truncated": truncated,
			"limit":     voucher.MaxScanItems,
		})
	})

	// Admin-only: list contents of the dead-letter queue. Each entry is a
	// voucher the on-chain settle path rejected as a system config issue
	// (INVALID_SIGNATURE / PROVIDER_MISMATCH). They sit here until a human
	// requeues or discards them.
	api.GET("/queue/dlq", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !proxyHandler.IsAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		entries, err := voucher.ListDLQ(c.Request.Context(), rdb, providerAddr)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries})
	})

	// Admin-only: permanently drop a single DLQ voucher. This is the ONLY
	// outbound operation on the DLQ — there's no requeue. Re-injecting a
	// frozen voucher into the live billing pipeline breaks the implicit
	// user-trust contract (totalFee was computed under a possibly different
	// world state than the user's current ack), so DLQ entries are
	// historical records, not zombie invoices. See dlq.go for the full
	// rationale.
	api.POST("/queue/dlq/discard", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !proxyHandler.IsAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		var req struct {
			User     string `json:"user"`
			Provider string `json:"provider"`
			Nonce    string `json:"nonce"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.User == "" || req.Provider == "" || req.Nonce == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user, provider, and nonce are required"})
			return
		}
		removed, err := voucher.DiscardFromDLQ(c.Request.Context(), rdb,
			common.HexToAddress(req.User),
			common.HexToAddress(req.Provider),
			req.Nonce,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"removed": removed})
	})

	// Admin-only: collapse every queued voucher matching (user, provider) —
	// regardless of sandbox — into a single voucher with summed total_fee.
	// Uses WATCH-based atomic queue rewrite; safe against concurrent settler BLPOP.
	api.POST("/queue/aggregate", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !proxyHandler.IsAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		var req struct {
			User     string `json:"user"`
			Provider string `json:"provider"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.User == "" || req.Provider == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user and provider are required"})
			return
		}
		queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, providerHex)
		result, err := voucher.Aggregate(c.Request.Context(), rdb, queueKey,
			common.HexToAddress(req.User),
			common.HexToAddress(req.Provider),
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// Audit trail: aggregations are otherwise invisible after the merged
		// voucher settles, since downstream events look the same as a normal
		// settle.
		if result != nil && result.Matched > 0 {
			_ = events.Push(c.Request.Context(), rdb, events.Event{
				Type:    events.TypeAggregated,
				Message: fmt.Sprintf("Aggregated %d vouchers for %s → 1 voucher (%s wei)", result.Matched, req.User, result.TotalFeeWei),
				User:    req.User,
				Amount:  result.TotalFeeWei,
			})
			log.Info("voucher aggregate",
				zap.String("by", wallet),
				zap.String("user", req.User),
				zap.String("provider", req.Provider),
				zap.Int("matched", result.Matched),
				zap.String("total_wei", result.TotalFeeWei),
			)
		}
		c.JSON(http.StatusOK, result)
	})

	// Admin-only: operator-internal state for the dashboard.
	// Returns voucher queue + DLQ depth and the recent alert history
	// (LPUSH'd by alert.Webhook). Signer-match and settler-balance health
	// live on the public `/api/info` instead, since both are derivable from
	// on-chain data.
	api.GET("/observability", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !proxyHandler.IsAdmin(wallet) {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin only"})
			return
		}
		ctx := c.Request.Context()
		queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, providerHex)
		dlqKey := fmt.Sprintf(voucher.VoucherDLQKeyFmt, providerHex)
		depth, _ := rdb.LLen(ctx, queueKey).Result()
		dlqDepth, _ := rdb.LLen(ctx, dlqKey).Result()
		queueStatus := "ok"
		if depth > cfg.Alert.QueueBacklogThreshold {
			queueStatus = "backlogged"
		}
		recent, _ := alert.History(ctx, rdb, 50)
		c.JSON(http.StatusOK, gin.H{
			"queue": gin.H{
				"depth":     depth,
				"dlq_depth": dlqDepth,
				"threshold": cfg.Alert.QueueBacklogThreshold,
				"status":    queueStatus,
			},
			"alerts_recent": recent,
		})
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: r,
	}

	go func() {
		log.Info("HTTP server starting", zap.Int("port", cfg.Server.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("HTTP server error", zap.Error(err))
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info("shutting down...")
	cancel()

	// Archive all running sandboxes before exiting so they can be restarted
	// after the stack comes back up (state is backed up to object storage).
	archiveCtx, archiveCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer archiveCancel()
	archiveRunningOnShutdown(archiveCtx, dtona, log)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP server shutdown error", zap.Error(err))
	}
	log.Info("shutdown complete")
}

// archiveRunningOnShutdown archives all started/starting/stopped sandboxes so
// their container state is preserved in object storage across a redeploy.
func archiveRunningOnShutdown(ctx context.Context, dtona *daytona.Client, log *zap.Logger) {
	sandboxes, err := dtona.ListSandboxes(ctx)
	if err != nil {
		log.Error("shutdown: list sandboxes", zap.Error(err))
		return
	}
	for _, s := range sandboxes {
		state := strings.ToLower(s.State)
		switch state {
		case "started", "starting":
			// Stop first (Daytona requires stopped state before archive).
			if err := dtona.StopSandbox(ctx, s.ID); err != nil {
				log.Warn("shutdown: stop sandbox failed",
					zap.String("id", s.ID), zap.Error(err))
			}
			if err := dtona.WaitStopped(ctx, s.ID); err != nil {
				log.Warn("shutdown: wait stopped failed",
					zap.String("id", s.ID), zap.Error(err))
				continue
			}
			fallthrough // now stopped — archive below
		case "stopped":
			if err := dtona.ArchiveSandbox(ctx, s.ID); err != nil {
				log.Warn("shutdown: archive sandbox failed",
					zap.String("id", s.ID), zap.Error(err))
			} else {
				log.Info("shutdown: archived sandbox", zap.String("id", s.ID))
			}
		}
	}
}

// recoverPendingStops scans stop:sandbox:* on startup and re-queues any
// sandboxes that were scheduled for stop but not yet processed (crash recovery).
func recoverPendingStops(ctx context.Context, rdb *redis.Client, stopCh chan<- settler.StopSignal, log *zap.Logger) {
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "stop:sandbox:*", 100).Result()
		if err != nil {
			log.Error("recoverPendingStops: scan", zap.Error(err))
			return
		}
		for _, key := range keys {
			reason, _ := rdb.Get(ctx, key).Result()
			sandboxID := key[len("stop:sandbox:"):]
			select {
			case stopCh <- settler.StopSignal{SandboxID: sandboxID, Reason: reason}:
				log.Info("recovered pending stop", zap.String("sandbox", sandboxID), zap.String("reason", reason))
			case <-ctx.Done():
				return
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
}

// runStopHandler consumes StopSignals, archives the sandbox (preserving state in
// object storage so it can be restarted later), and cleans up Redis.
func runStopHandler(ctx context.Context, stopCh <-chan settler.StopSignal, dtona *daytona.Client, rdb *redis.Client, log *zap.Logger, deregisterBroker func(context.Context, string)) {
	for {
		select {
		case sig := <-stopCh:
			// Daytona requires stopped state before archive.
			// Step 1: stop (removes container from runner).
			if err := dtona.StopSandbox(ctx, sig.SandboxID); err != nil {
				log.Warn("stop sandbox failed (may already be stopped/archived)",
					zap.String("sandbox", sig.SandboxID),
					zap.Error(err),
				)
			}
			// Step 2: wait for stopped state (stop is async in Daytona).
			// Use a 2-minute timeout so a stuck archive job doesn't block this goroutine forever.
			waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := dtona.WaitStopped(waitCtx, sig.SandboxID); err != nil {
				log.Warn("wait stopped failed",
					zap.String("sandbox", sig.SandboxID),
					zap.Error(err),
				)
			}
			cancel()
			// Step 3: archive (backup filesystem to MinIO for later restore).
			if err := dtona.ArchiveSandbox(ctx, sig.SandboxID); err != nil {
				log.Warn("archive sandbox failed (may already be archived)",
					zap.String("sandbox", sig.SandboxID),
					zap.Error(err),
				)
			}
			rdb.Del(ctx, "billing:compute:"+sig.SandboxID) //nolint:errcheck
			rdb.Del(ctx, "stop:sandbox:"+sig.SandboxID)    //nolint:errcheck
			if deregisterBroker != nil {
				deregisterBroker(ctx, sig.SandboxID)
			}
			log.Info("sandbox archived",
				zap.String("sandbox", sig.SandboxID),
				zap.String("reason", sig.Reason),
			)
			_ = events.Push(ctx, rdb, events.Event{
				Type:      events.TypeAutoStopped,
				Message:   fmt.Sprintf("Sandbox %s archived: %s", sig.SandboxID, sig.Reason),
				SandboxID: sig.SandboxID,
			})
		case <-ctx.Done():
			return
		}
	}
}

// appOwnerReader is the slice of the chain client the owner resolver needs.
type appOwnerReader interface {
	GetAppOwner(ctx context.Context, appId string) (common.Address, error)
}

const (
	// appOwnerTTL is how long a successful owner lookup is trusted. The
	// resolver's value gates ADMIN checks, so the TTL is also the window in
	// which an on-chain ownership transfer has not yet propagated — an
	// ex-owner keeps admin (archive-all, force-stop/delete across tenants)
	// for at most this long. Keep it short.
	appOwnerTTL = 15 * time.Second
	// appOwnerRetryEvery rate-limits probes at the dead RPC during an outage
	// (negative caching) so post-TTL requests don't serialize behind failing
	// fetches under the resolver mutex.
	appOwnerRetryEvery = 5 * time.Second
	// appOwnerStaleCap bounds how long the LAST GOOD value may be served when
	// the RPC is failing. Serving stale keeps a flaky RPC from locking the
	// real owner out mid-incident, but unbounded staleness let a REMOVED
	// owner keep provider-wide destructive admin for as long as the RPC
	// stayed degraded. Past the cap the resolver fails closed for the owner
	// path — static ADMIN_ADDRESSES wallets are unaffected.
	appOwnerStaleCap = 10 * time.Minute
)

// newAppOwnerResolver returns a TTL-cached resolver of the appId's TappRegistry
// owner with bounded stale-serving (see the constants above for the exact
// trust windows — the returned value is an ADMIN identity).
func newAppOwnerResolver(chainReader appOwnerReader, backendAppName string, ttl, staleCap, retryEvery time.Duration, log *zap.Logger) func(ctx context.Context) (string, error) {
	var mu sync.Mutex
	var cached string
	var fetchedAt time.Time
	var lastErr error
	var lastTry time.Time
	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != "" && time.Since(fetchedAt) < ttl {
			return cached, nil
		}
		if backendAppName == "" {
			return "", fmt.Errorf("BACKEND_APP_NAME not set")
		}
		// Negative caching: during an RPC outage every post-TTL request would
		// otherwise run its own failing (up to caller-timeout) fetch,
		// serialized under this mutex — every withOwnerOrAdmin route queueing
		// behind a lock draining at one request per timeout. Reuse the last
		// failure for a short window so at most one probe per interval hits
		// the dead RPC; the stale/fail-closed decision below is unchanged.
		if lastErr != nil && time.Since(lastTry) < retryEvery {
			if cached != "" && time.Since(fetchedAt) < staleCap {
				return cached, nil
			}
			return "", lastErr
		}
		owner, err := chainReader.GetAppOwner(ctx, backendAppName)
		lastTry = time.Now()
		lastErr = err
		if err != nil {
			if cached != "" && time.Since(fetchedAt) < staleCap {
				return cached, nil // bounded stale-over-fail-closed
			}
			if cached != "" {
				log.Error("app owner lookup failing beyond stale cap — owner-based admin fails closed until RPC recovers (static ADMIN_ADDRESSES unaffected)",
					zap.Duration("stale_for", time.Since(fetchedAt)), zap.Error(err))
			}
			return "", err
		}
		if owner == (common.Address{}) {
			return "", fmt.Errorf("app %q not registered in TappRegistry", backendAppName)
		}
		cached = strings.ToLower(owner.Hex())
		fetchedAt = time.Now()
		return cached, nil
	}
}
