package main

import (
	"context"
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
	// Read computePricePerSec and createFee from the on-chain service record so
	// that users can verify the actual billing rate on the contract explorer.
	// Fall back to env vars only when the service is not yet registered (dev /
	// first-time setup).
	computePricePerSec, createFee, err := onchain.GetServicePricing(ctx, common.HexToAddress(cfg.Chain.ProviderAddress))
	if err != nil {
		log.Warn("could not read on-chain service pricing; falling back to env vars", zap.Error(err))
	}
	if computePricePerSec == nil || computePricePerSec.Sign() == 0 {
		var ok bool
		computePricePerSec, ok = new(big.Int).SetString(cfg.Billing.ComputePricePerSec, 10)
		if !ok {
			log.Fatal("invalid COMPUTE_PRICE_PER_SEC")
		}
		log.Info("using env COMPUTE_PRICE_PER_SEC (service not on-chain)", zap.String("value", computePricePerSec.String()))
	} else {
		log.Info("using on-chain compute price", zap.String("per_sec", computePricePerSec.String()))
	}
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
