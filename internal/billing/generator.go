package billing

import (
	"context"
	"math/big"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// BillableSandboxes reports which sandboxes the runtime still has, so a
// session cannot outlive what it bills for. One call covers every session in
// a tick, rather than a lookup per session.
type BillableSandboxes interface {
	// BillableSandboxIDs returns the set of sandbox ids that exist and are not
	// in a terminal state. An error means "unknown", not "none".
	BillableSandboxIDs(ctx context.Context) (map[string]bool, error)
}

// RunGenerator periodically scans all billing sessions and pre-charges the next
// compute period for any session whose NextVoucherAt has elapsed.
//
// billable gates the scan against the runtime's own view. A session is the
// only record that drives billing, and nothing guarantees it matches reality:
// it is deleted on a user stop/delete or after a successful archive, and by
// nothing else. A stop that fails to archive deliberately keeps the session
// so a still-running sandbox is not billed for free — correct in itself, but
// it leaves the session with no remaining path to deletion once the retry
// stops happening, and the generator has no way to tell. Observed on dev:
// five sessions for sandboxes long gone from the runtime, each still emitting
// a voucher a minute. The account was empty so nothing was collected, which is
// what kept it invisible; a deposit would have been drained at the full rate
// for compute that did not exist.
//
// Pass nil to disable the gate (tests, or a deployment with no runtime view).
func RunGenerator(ctx context.Context, rdb *redis.Client, h *EventHandler, billable BillableSandboxes, log *zap.Logger) {
	interval := time.Duration(h.voucherIntervalSec) * time.Second

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("voucher generator started", zap.Duration("interval", interval), zap.Bool("existence_gate", billable != nil))

	for {
		select {
		case <-ctx.Done():
			log.Info("voucher generator stopped")
			return
		case <-ticker.C:
			runGeneration(ctx, rdb, h, billable, log)
		}
	}
}

func runGeneration(ctx context.Context, rdb *redis.Client, h *EventHandler, billable BillableSandboxes, log *zap.Logger) {
	sessions, err := ScanAllSessions(ctx, rdb)
	if err != nil {
		log.Error("generator: scan sessions", zap.Error(err))
		return
	}

	// Fetch once per tick, not once per session.
	//
	// A failed lookup bills as before rather than skipping. Sandboxes run on
	// the runners, so the control plane being unreachable does not stop the
	// compute the user is getting — declining to bill through an outage would
	// give it away. Unknown must not be read as gone.
	var live map[string]bool
	if billable != nil {
		live, err = billable.BillableSandboxIDs(ctx)
		if err != nil {
			log.Warn("generator: sandbox list unavailable; billing every session this tick", zap.Error(err))
			live = nil
		}
	}

	now := time.Now().Unix()

	for _, sess := range sessions {
		s := sess
		if now < s.NextVoucherAt {
			continue
		}
		if live != nil && !live[s.SandboxID] {
			// The runtime has no such sandbox. Skip rather than delete the
			// session: closing it is a lifecycle decision that belongs to the
			// stop path, and a sandbox absent from one listing may be a
			// listing that raced a create. Not billing is the part that has to
			// happen now.
			log.Warn("generator: session has no live sandbox; not billing this period",
				zap.String("sandbox", s.SandboxID), zap.String("owner", s.Owner))
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
