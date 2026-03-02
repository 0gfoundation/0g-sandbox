package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/0gfoundation/0g-sandbox-billing/internal/auth"
	"github.com/0gfoundation/0g-sandbox-billing/internal/billing"
	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
	"github.com/0gfoundation/0g-sandbox-billing/internal/config"
	"github.com/0gfoundation/0g-sandbox-billing/internal/daytona"
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

	// ── VoucherSigner (TEE key → sign → Redis queue) ──────────────────────────
	computePricePerSec, ok := new(big.Int).SetString(cfg.Billing.ComputePricePerSec, 10)
	if !ok {
		log.Fatal("invalid COMPUTE_PRICE_PER_SEC")
	}
	createFee, ok := new(big.Int).SetString(cfg.Billing.CreateFee, 10)
	if !ok {
		log.Fatal("invalid CREATE_FEE")
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
	go billing.RunGenerator(ctx, cfg, rdb, signer, log)

	// ── HTTP server ───────────────────────────────────────────────────────────
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET("/dashboard", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", web.DashboardHTML)
	})

	api := r.Group("/api", auth.Middleware(rdb))
	proxy.NewHandler(dtona, billingHandler, onchain, minBalance, log).Register(api)

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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP server shutdown error", zap.Error(err))
	}
	log.Info("shutdown complete")
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

// runStopHandler consumes StopSignals, calls Daytona STOP, and cleans up Redis.
func runStopHandler(ctx context.Context, stopCh <-chan settler.StopSignal, dtona *daytona.Client, rdb *redis.Client, log *zap.Logger) {
	for {
		select {
		case sig := <-stopCh:
			if err := dtona.StopSandbox(ctx, sig.SandboxID); err != nil {
				// Daytona STOP is idempotent; "already stopped" is not an error.
				log.Warn("stop sandbox failed (may already be stopped)",
					zap.String("sandbox", sig.SandboxID),
					zap.Error(err),
				)
			}
			rdb.Del(ctx, "billing:compute:"+sig.SandboxID) //nolint:errcheck
			rdb.Del(ctx, "stop:sandbox:"+sig.SandboxID)    //nolint:errcheck
			log.Info("sandbox stopped",
				zap.String("sandbox", sig.SandboxID),
				zap.String("reason", sig.Reason),
			)
		case <-ctx.Done():
			return
		}
	}
}
