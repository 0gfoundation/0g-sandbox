package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/0gfoundation/0g-sandbox-billing/internal/admin"
	"github.com/0gfoundation/0g-sandbox-billing/internal/auth"
	"github.com/0gfoundation/0g-sandbox-billing/internal/billing"
	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
	"github.com/0gfoundation/0g-sandbox-billing/internal/config"
	"github.com/0gfoundation/0g-sandbox-billing/internal/daytona"
	"github.com/0gfoundation/0g-sandbox-billing/internal/events"
	"github.com/0gfoundation/0g-sandbox-billing/internal/proxy"
	"github.com/0gfoundation/0g-sandbox-billing/internal/registry"
	"github.com/0gfoundation/0g-sandbox-billing/internal/settler"
	"github.com/0gfoundation/0g-sandbox-billing/internal/tee"
	"github.com/0gfoundation/0g-sandbox-billing/web"
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

	// Derive provider address from the TEE key if not explicitly configured.
	if cfg.Chain.ProviderAddress == "" {
		privKey, err := crypto.HexToECDSA(appKey.PrivateKeyHex)
		if err != nil {
			log.Fatal("invalid TEE private key", zap.Error(err))
		}
		cfg.Chain.ProviderAddress = crypto.PubkeyToAddress(privKey.PublicKey).Hex()
		log.Info("provider address derived from TEE key",
			zap.String("address", cfg.Chain.ProviderAddress))
	}

	// ── Chain client (TEE private key + ABI binding) ──────────────────────────
	onchain, err := chain.NewClient(cfg)
	if err != nil {
		log.Fatal("chain client init failed", zap.Error(err))
	}

	// ── Pricing: on-chain service registration is the source of truth ────────
	// Read per-resource prices and createFee from the contract so users can
	// verify the actual billing rate on the chain explorer.
	// Fall back to env vars only when the service is not yet registered.
	chainCPUPerSec, chainMemPerSec, createFee, err := onchain.GetServicePricing(ctx, common.HexToAddress(cfg.Chain.ProviderAddress))
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
		common.HexToAddress(cfg.Chain.ProviderAddress),
		rdb,
		onchain,
		log,
	)

	// ── Daytona client ────────────────────────────────────────────────────────
	dtona := daytona.NewClient(cfg.Daytona.APIURL, cfg.Daytona.AdminKey)

	// ── Billing event handler ─────────────────────────────────────────────────
	billingHandler := billing.NewEventHandler(
		rdb,
		cfg.Chain.ProviderAddress,
		computePricePerSec,
		createFee,
		pricePerCPUPerSec,
		pricePerMemGBPerSec,
		signer,
		log,
	)

	// Minimum balance = createFee + one voucher interval of compute fees (per-second pricing).
	minBalance := new(big.Int).Add(createFee, new(big.Int).Mul(computePricePerSec, big.NewInt(cfg.Billing.VoucherIntervalSec)))

	// ── Stop channel (settler → stop handler, buffered) ───────────────────────
	stopCh := make(chan settler.StopSignal, 100)

	// ── Goroutines ────────────────────────────────────────────────────────────
	// Recovery must start after stopCh is ready but before settler writes to it.
	go recoverPendingStops(ctx, rdb, stopCh, log)
	go settler.Run(ctx, cfg, rdb, onchain, stopCh, log)
	go runStopHandler(ctx, stopCh, dtona, rdb, log)
	go billing.RunGenerator(ctx, cfg, rdb, signer, computePricePerSec, log)

	// ── HTTP server ───────────────────────────────────────────────────────────
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET("/dashboard", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", web.DashboardHTML)
	})
	r.GET("/static/ethers.js", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/javascript; charset=utf-8", web.EthersJS)
	})
	// Public providers list — returns known providers with their on-chain service data.
	r.GET("/api/providers", func(c *gin.Context) {
		type ProviderInfo struct {
			Address               string `json:"address"`
			URL                   string `json:"url"`
			TEESigner             string `json:"tee_signer"`
			PricePerCPUPerMin     string `json:"price_per_cpu_per_min"`
			PricePerCPUPerSec     string `json:"price_per_cpu_per_sec"`
			PricePerMemGBPerMin   string `json:"price_per_mem_gb_per_min"`
			PricePerMemGBPerSec   string `json:"price_per_mem_gb_per_sec"`
			CreateFee             string `json:"create_fee"`
			SignerVersion         string `json:"signer_version"`
		}
		// For now: just the configured provider.  Extend via KNOWN_PROVIDERS in the future.
		addrs := []string{cfg.Chain.ProviderAddress}
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
				TEESigner:           svcInfo.TEESignerAddress.Hex(),
				PricePerCPUPerMin:   svcInfo.PricePerCPUPerMin.String(),
				PricePerCPUPerSec:   cpuPerSec.String(),
				PricePerMemGBPerMin: svcInfo.PricePerMemGBPerMin.String(),
				PricePerMemGBPerSec: memPerSec.String(),
				CreateFee:           svcInfo.CreateFee.String(),
				SignerVersion:       svcInfo.SignerVersion.String(),
			})
		}
		if providers == nil {
			providers = []ProviderInfo{}
		}
		c.JSON(http.StatusOK, providers)
	})

	r.GET("/info", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"contract_address":    cfg.Chain.ContractAddress,
			"provider_address":    cfg.Chain.ProviderAddress,
			"chain_id":            cfg.Chain.ChainID,
			"rpc_url":             cfg.Chain.RPCURL,
			"compute_price_per_sec": computePricePerSec.String(),
			"create_fee":          createFee.String(),
			"voucher_interval_sec": cfg.Billing.VoucherIntervalSec,
			"min_balance":         minBalance.String(),
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

	adminGroup := r.Group("/admin", admin.AuthMiddleware(cfg.Daytona.AdminKey))
	admin.New(rdb, cfg, dtona, log).Register(adminGroup)

	api := r.Group("/api", auth.Middleware(rdb))
	proxy.NewHandler(dtona, billingHandler, onchain, onchain, onchain, minBalance, computePricePerSec, cfg.Chain.ProviderAddress, rdb, log).Register(api)

	// Provider-only: pull an image from an external registry into the internal registry.
	// The import runs synchronously (crane.Copy) — may take minutes for large images.
	api.POST("/registry/pull", func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if !strings.EqualFold(wallet, cfg.Chain.ProviderAddress) {
			c.JSON(http.StatusForbidden, gin.H{"error": "provider only"})
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
func runStopHandler(ctx context.Context, stopCh <-chan settler.StopSignal, dtona *daytona.Client, rdb *redis.Client, log *zap.Logger) {
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
			if err := dtona.WaitStopped(ctx, sig.SandboxID); err != nil {
				log.Warn("wait stopped failed",
					zap.String("sandbox", sig.SandboxID),
					zap.Error(err),
				)
			}
			// Step 3: archive (backup filesystem to MinIO for later restore).
			if err := dtona.ArchiveSandbox(ctx, sig.SandboxID); err != nil {
				log.Warn("archive sandbox failed (may already be archived)",
					zap.String("sandbox", sig.SandboxID),
					zap.Error(err),
				)
			}
			rdb.Del(ctx, "billing:compute:"+sig.SandboxID) //nolint:errcheck
			rdb.Del(ctx, "stop:sandbox:"+sig.SandboxID)    //nolint:errcheck
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
