package billing

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox-billing/internal/config"
	"github.com/0gfoundation/0g-sandbox-billing/internal/voucher"
)

// RunGenerator periodically scans all billing sessions and emits signed vouchers.
func RunGenerator(ctx context.Context, cfg *config.Config, rdb *redis.Client, signer VoucherSigner, computePricePerSec *big.Int, log *zap.Logger) {
	interval := time.Duration(cfg.Billing.VoucherIntervalSec) * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("voucher generator started", zap.Duration("interval", interval))

	for {
		select {
		case <-ctx.Done():
			log.Info("voucher generator stopped")
			return
		case <-ticker.C:
			runGeneration(ctx, cfg, rdb, signer, computePricePerSec, log)
		}
	}
}

func runGeneration(
	ctx context.Context,
	cfg *config.Config,
	rdb *redis.Client,
	signer VoucherSigner,
	computePricePerSec *big.Int,
	log *zap.Logger,
) {
	sessions, err := ScanAllSessions(ctx, rdb)
	if err != nil {
		log.Error("generator: scan sessions", zap.Error(err))
		return
	}

	now := time.Now().Unix()
	intervalSec := cfg.Billing.VoucherIntervalSec

	for _, sess := range sessions {
		func(s Session) {
			periodStart := s.LastVoucherAt
			// Hard cap: single voucher covers at most one interval
			periodEnd := periodStart + intervalSec
			if periodEnd > now {
				periodEnd = now
			}

			elapsedSec := periodEnd - periodStart
			if elapsedSec <= 0 {
				return
			}

			nonce, err := signer.IncrNonce(ctx, s.Owner, s.Provider)
			if err != nil {
				log.Error("generator: incr nonce", zap.String("sandbox", s.SandboxID), zap.Error(err))
				return
			}

			// Use the per-sandbox rate stored in the session; fall back to the
		// global flat rate for sessions created before per-resource pricing.
		price := computePricePerSec
		if s.PricePerSec != "" {
			if p, ok := new(big.Int).SetString(s.PricePerSec, 10); ok && p.Sign() > 0 {
				price = p
			}
		}
		totalFee := new(big.Int).Mul(big.NewInt(elapsedSec), price)
			v := &voucher.SandboxVoucher{
				SandboxID: s.SandboxID,
				User:      common.HexToAddress(s.Owner),
				Provider:  common.HexToAddress(s.Provider),
				TotalFee:  totalFee,
				Nonce:     nonce,
				UsageHash: voucher.BuildUsageHash(s.SandboxID, periodStart, periodEnd, elapsedSec),
			}

			if err := signer.SignAndEnqueue(ctx, v); err != nil {
				log.Error("generator: sign/enqueue", zap.String("sandbox", s.SandboxID), zap.Error(err))
				return
			}

			if err := UpdateLastVoucherAt(ctx, rdb, s.SandboxID, periodEnd); err != nil {
				log.Error("generator: update last_voucher_at", zap.String("sandbox", s.SandboxID), zap.Error(err))
			}
		}(sess)
	}
}
