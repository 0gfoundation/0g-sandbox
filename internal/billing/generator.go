package billing

import (
	"context"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RunGenerator periodically scans all billing sessions and pre-charges the next
// compute period for any session whose NextVoucherAt has elapsed.
func RunGenerator(ctx context.Context, rdb *redis.Client, h *EventHandler, log *zap.Logger) {
	interval := time.Duration(h.voucherIntervalSec) * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("voucher generator started", zap.Duration("interval", interval))

	for {
		select {
		case <-ctx.Done():
			log.Info("voucher generator stopped")
			return
		case <-ticker.C:
			runGeneration(ctx, rdb, h, log)
		}
	}
}

func runGeneration(ctx context.Context, rdb *redis.Client, h *EventHandler, log *zap.Logger) {
	sessions, err := ScanAllSessions(ctx, rdb)
	if err != nil {
		log.Error("generator: scan sessions", zap.Error(err))
		return
	}

	now := time.Now().Unix()

	for _, sess := range sessions {
		s := sess
		if now < s.NextVoucherAt {
			continue
		}

		// Use per-sandbox rate stored in session; fall back to global flat rate.
		price := h.computePricePerSec
		if s.PricePerSec != "" {
			if p, ok := new(big.Int).SetString(s.PricePerSec, 10); ok && p.Sign() > 0 {
				price = p
			}
		}

		// Catch-up: after downtime NextVoucherAt lags by many periods, and
		// "one period per tick" can never close the gap (the tick interval
		// equals the billing interval) — the missed compute would simply
		// never be billed. Emit in CHUNKS instead: each voucher covers up to
		// catchupChunkIntervals periods, at most catchupMaxVouchersPerTick
		// vouchers per session per tick. Chunking (vs one giant voucher)
		// keeps the debt partially collectable when the user's balance covers
		// only part of the backlog. Steady state is exactly one one-period
		// voucher, same as before.
		overdue := (now-s.NextVoucherAt)/h.voucherIntervalSec + 1
		periodStart := s.NextVoucherAt
		for emitted := 0; overdue > 0 && emitted < catchupMaxVouchersPerTick; emitted++ {
			chunk := overdue
			if chunk > catchupChunkIntervals {
				chunk = catchupChunkIntervals
			}
			nextVoucherAt, err := h.emitPeriodVoucher(ctx, s.SandboxID, s.Owner, price, periodStart, chunk)
			if err != nil {
				log.Error("generator: emit period voucher", zap.String("sandbox", s.SandboxID), zap.Error(err))
				break
			}
			if err := UpdateNextVoucherAt(ctx, rdb, s.SandboxID, nextVoucherAt); err != nil {
				log.Error("generator: update next_voucher_at", zap.String("sandbox", s.SandboxID), zap.Error(err))
				break
			}
			if chunk > 1 {
				log.Info("generator: backlog catch-up voucher",
					zap.String("sandbox", s.SandboxID), zap.Int64("intervals", chunk), zap.Int64("remaining", overdue-chunk))
			}
			periodStart = nextVoucherAt
			overdue -= chunk
		}
	}
}

const (
	// catchupChunkIntervals bounds how many billing periods one catch-up
	// voucher covers (60 × 60s = one hour of compute per voucher).
	catchupChunkIntervals = 60
	// catchupMaxVouchersPerTick bounds per-session work per tick; a 2-day
	// backlog catches up in ~5 ticks instead of never.
	catchupMaxVouchersPerTick = 10
)
