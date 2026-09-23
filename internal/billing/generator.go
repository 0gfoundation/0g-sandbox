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
	// ConfirmGone reports whether the runtime definitively has no such
	// sandbox — a 404, not a lookup that failed. Anything short of proof must
	// answer false: this is what authorises deleting billing state.
	ConfirmGone(ctx context.Context, sandboxID string) bool
}

// phantomSkipsBeforeConfirm is how many consecutive ticks a session must be
// absent from the listing before its existence is checked against the
// authoritative per-id lookup. Well beyond any create that raced one listing,
// and short enough that a session does not linger for hours.
const phantomSkipsBeforeConfirm = 10

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

	// Consecutive ticks each session has been absent from the listing. In
	// memory on purpose: a restart resetting the count only delays a cleanup,
	// while persisting it would add a write per session per tick to defend
	// against nothing.
	skips := map[string]int{}

	for {
		select {
		case <-ctx.Done():
			log.Info("voucher generator stopped")
			return
		case <-ticker.C:
			runGeneration(ctx, rdb, h, billable, skips, log)
		}
	}
}

func runGeneration(ctx context.Context, rdb *redis.Client, h *EventHandler, billable BillableSandboxes, skips map[string]int, log *zap.Logger) {
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
	// Reported once per tick rather than once per session: five phantoms at a
	// voucher a minute is 7,200 log lines a day, which buries the shape of the
	// problem instead of showing it. One line per tick also makes the
	// pathological case — every session skipped, i.e. an empty or wrongly
	// scoped listing — visible as a number rather than as noise.
	var skipped []string

	for _, sess := range sessions {
		s := sess
		if now < s.NextVoucherAt {
			continue
		}
		if live != nil && !live[s.SandboxID] {
			// The runtime has no such sandbox. Not billing has to happen now;
			// closing the session does not, because one listing that raced a
			// create is not proof of deletion.
			//
			// Sessions reached through a stop are cleaned up by the stop
			// handler once its marker is retried. A sandbox deleted straight
			// through Daytona fires no billing hook and leaves no marker, so
			// nothing would ever close it — skipped every tick for the life of
			// the process, one warning each time. After enough consecutive
			// absences to rule out a race, ask the authoritative per-id
			// lookup, and act only on a definitive answer.
			skips[s.SandboxID]++
			if skips[s.SandboxID] >= phantomSkipsBeforeConfirm && billable.ConfirmGone(ctx, s.SandboxID) {
				closePhantomSession(ctx, rdb, s, log)
				delete(skips, s.SandboxID)
				continue
			}
			skipped = append(skipped, s.SandboxID)
			continue
		}
		delete(skips, s.SandboxID)

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

	if len(skipped) > 0 {
		log.Warn("generator: sessions with no live sandbox were not billed",
			zap.Int("skipped", len(skipped)),
			zap.Int("sessions", len(sessions)),
			zap.Strings("sandboxes", skipped),
		)
	}
}

// closePhantomSession removes billing state for a sandbox the runtime has
// confirmed is gone. Mirrors the stop handler's gone-branch: the session is
// what drives billing and the marker is a stop order for something that no
// longer exists, so both are dropped together. Called only behind a definitive
// 404, never on a lookup that merely failed.
func closePhantomSession(ctx context.Context, rdb *redis.Client, s Session, log *zap.Logger) {
	if err := DeleteSession(ctx, rdb, s.SandboxID); err != nil {
		log.Error("generator: close phantom session", zap.String("sandbox", s.SandboxID), zap.Error(err))
		return
	}
	rdb.Del(ctx, "stop:sandbox:"+s.SandboxID) //nolint:errcheck
	log.Warn("generator: closed billing session for a sandbox the runtime confirms is gone",
		zap.String("sandbox", s.SandboxID), zap.String("owner", s.Owner))
}

const (
	// catchupChunkIntervals bounds how many billing periods one catch-up
	// voucher covers (60 × 60s = one hour of compute per voucher).
	catchupChunkIntervals = 60
	// catchupMaxVouchersPerTick bounds per-session work per tick; a 2-day
	// backlog catches up in ~5 ticks instead of never.
	catchupMaxVouchersPerTick = 10
)
