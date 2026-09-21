package settler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/ethereum/go-ethereum/common"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/events"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// HandleStatuses processes settlement results for a batch of vouchers.
// firstItem is already BLPOP'd; the rest of the batch's queue entries are
// LPOP'd here.
//
// consumed is how many queue entries the batch took, which is NOT len(vouchers):
// the batch is collapsed by sandbox before submission, so one voucher can stand
// for several entries. Popping per voucher would leave the surplus entries in
// the queue to be settled a second time — the user charged twice for the same
// compute. Pass 0 for consumed to fall back to one entry per voucher, which is
// what a pending-tx record written before collapsing existed carries.
func HandleStatuses(
	ctx context.Context,
	rdb *redis.Client,
	stopCh chan<- StopSignal,
	queueKey string,
	firstItem string,
	consumed int,
	vouchers []voucher.SandboxVoucher,
	statuses []chain.SettlementStatus,
	alerter alert.Alerter,
	log *zap.Logger,
) {
	if consumed <= 0 {
		consumed = len(vouchers)
	}
	// Drop the entries this batch owns, minus the BLPOP'd first one. Done
	// before the statuses are processed, matching the previous ordering: an
	// entry is off the queue before anything acts on its settlement result.
	//
	// Popping by count rather than by value assumes the batch's remaining
	// entries are still at the head of the queue. That holds because a single
	// settler runs per provider and resolvePendingTx blocks its loop until the
	// fate is known, so nothing — not even the sweep — rewrites the queue in
	// between. A second concurrent drainer would break this.
	//
	// Entries that failed to deserialize are counted here and dropped without
	// a voucher to match: Run already logged each one, and the batch owns the
	// slot either way. Leaving them would re-read the same unparseable entry
	// on every drain.
	for i := 1; i < consumed; i++ {
		rdb.LPop(ctx, queueKey)
	}

	for i, status := range statuses {
		v := vouchers[i]

		sandboxID := extractSandboxID(v)

		switch status {
		case chain.StatusSuccess:
			isAgg := v.IsAggregated()
			log.Info("voucher settled",
				zap.String("user", v.User.Hex()),
				zap.String("nonce", v.Nonce.String()),
				zap.Bool("aggregated", isAgg),
			)
			msg := fmt.Sprintf("Voucher settled nonce #%s for %s", v.Nonce.String(), v.User.Hex())
			if isAgg {
				msg = fmt.Sprintf("Aggregated voucher settled nonce #%s for %s (%s wei)", v.Nonce.String(), v.User.Hex(), v.TotalFee.String())
			}
			_ = events.Push(ctx, rdb, events.Event{
				Type:      events.TypeSettled,
				Message:   msg,
				SandboxID: sandboxID,
				User:      v.User.Hex(),
				Amount:    v.TotalFee.String(),
			})

		case chain.StatusInsufficientBalance:
			if v.IsAggregated() {
				// An aggregated voucher is the settle-now half of the owner's
				// whole backlog for this provider, so INSUFFICIENT_BALANCE means
				// the account is exhausted outright — every sandbox they own is
				// unpayable, and the right answer is "stop them all".
				//
				// Stopping only v.SandboxID would be wrong in both aggregate
				// shapes: the operator-initiated collapse names no sandbox at
				// all, and a per-sandbox aggregate names just one of the several
				// the same exhausted account is running. The owner is the
				// identity that matters here, not the sandbox on the voucher.
				// persistStop dedups via SetNX, so sandboxes the sweep already
				// stopped are not re-killed.
				stopped := stopAllSandboxesOf(ctx, rdb, stopCh, v.User, v.Provider, log)
				log.Warn("aggregated voucher exhausted user balance — stopping the owner's sandboxes",
					zap.String("user", v.User.Hex()),
					zap.String("provider", v.Provider.Hex()),
					zap.String("amount", v.TotalFee.String()),
					zap.Int("sandboxes_stopped", stopped),
				)
				alerter.Notify(ctx, alert.KindVoucherRejected, alert.SeverityCritical,
					"Aggregated voucher exhausted user balance — multiple sandboxes affected",
					map[string]any{
						"user":     v.User.Hex(),
						"provider": v.Provider.Hex(),
						"amount":   v.TotalFee.String(),
					},
				)
			} else if err := persistStop(ctx, rdb, stopCh, sandboxID, "insufficient_balance", log); err != nil {
				log.Error("failed to persist stop marker; sandbox not queued for stop",
					zap.String("sandbox", sandboxID), zap.Error(err))
				alerter.Notify(ctx, alert.KindStopPersistFailure, alert.SeverityCritical,
					"Failed to persist stop marker — sandbox will keep billing until Redis recovers",
					map[string]any{"sandbox": sandboxID, "reason": "insufficient_balance", "error": err.Error()},
				)
			}

		case chain.StatusNotAcknowledged:
			// The contract rejects NOT_ACKNOWLEDGED before consuming the nonce,
			// so this revenue is collectable once the user acknowledges — park
			// it in the held list instead of dropping it with the pop. The
			// sweep reclaims it on the user's next balance change after ack.
			if err := voucher.PushHeld(ctx, rdb, v); err != nil {
				log.Error("failed to park not-acknowledged voucher; revenue dropped",
					zap.String("user", v.User.Hex()), zap.String("fee", v.TotalFee.String()), zap.Error(err))
			}
			if v.IsAggregated() {
				log.Warn("aggregated voucher rejected: user not acknowledged — parked as held",
					zap.String("user", v.User.Hex()),
					zap.String("provider", v.Provider.Hex()),
				)
			} else if err := persistStop(ctx, rdb, stopCh, sandboxID, "not_acknowledged", log); err != nil {
				log.Error("failed to persist stop marker; sandbox not queued for stop",
					zap.String("sandbox", sandboxID), zap.Error(err))
				alerter.Notify(ctx, alert.KindStopPersistFailure, alert.SeverityCritical,
					"Failed to persist stop marker — sandbox will keep billing until Redis recovers",
					map[string]any{"sandbox": sandboxID, "reason": "not_acknowledged", "error": err.Error()},
				)
			}

		case chain.StatusProviderMismatch, chain.StatusInvalidSignature:
			raw, _ := json.Marshal(v)
			dlqKey := fmt.Sprintf(voucher.VoucherDLQKeyFmt, v.Provider.Hex())
			rdb.RPush(ctx, dlqKey, string(raw))
			log.Error("voucher rejected — system config issue",
				zap.String("status", status.String()),
				zap.String("user", v.User.Hex()),
				zap.String("provider", v.Provider.Hex()),
				zap.String("nonce", v.Nonce.String()),
			)
			alerter.Notify(ctx, alert.KindVoucherRejected, alert.SeverityCritical,
				"Voucher rejected — system config issue",
				map[string]any{
					"status":   status.String(),
					"user":     v.User.Hex(),
					"provider": v.Provider.Hex(),
					"sandbox":  sandboxID,
					"nonce":    v.Nonce.String(),
				},
			)

		case chain.StatusInvalidNonce:
			// Self-heal: the local counter disagrees with the chain (stale
			// seed, operator surgery, a competing writer). Delete the Redis
			// counter so the NEXT voucher reseeds from on-chain lastNonce —
			// without this, every subsequent voucher for the pair keeps
			// getting rejected and discarded (unbilled compute, no recovery).
			nonceKey := fmt.Sprintf(voucher.NonceKeyFmt,
				strings.ToLower(v.User.Hex()), strings.ToLower(v.Provider.Hex()))
			if err := rdb.Del(ctx, nonceKey).Err(); err != nil {
				log.Error("invalid-nonce reseed: delete counter failed", zap.String("key", nonceKey), zap.Error(err))
			}
			log.Warn("voucher discarded: invalid nonce — counter reset for reseed",
				zap.String("user", v.User.Hex()),
				zap.String("nonce", v.Nonce.String()),
			)
			alerter.Notify(ctx, alert.KindVoucherInvalidNonce, alert.SeverityCritical,
				"Voucher with invalid nonce — possible replay or settler bug",
				map[string]any{
					"user":     v.User.Hex(),
					"provider": v.Provider.Hex(),
					"sandbox":  sandboxID,
					"nonce":    v.Nonce.String(),
				},
			)
		}
	}
}

func persistStop(ctx context.Context, rdb *redis.Client, stopCh chan<- StopSignal, sandboxID, reason string, log *zap.Logger) error {
	// 1. Persist first (crash-safe), deduped with SetNX. A settler outage can
	//    back up thousands of vouchers for one sandbox; when they batch-settle and
	//    the balance runs out, every rejection lands here. SetNX collapses that
	//    storm into a single kill order: if the marker already exists the sandbox
	//    is already queued for (or being) stopped, so we neither overwrite the
	//    reason nor push a duplicate signal. The marker is deleted only after the
	//    stop handler finishes, so a later rejection after a real stop re-queues.
	//    If SetNX fails there is no recovery marker, so we must NOT pretend the
	//    stop is queued — return the error and let the caller alert.
	stopKey := "stop:sandbox:" + sandboxID
	created, err := rdb.SetNX(ctx, stopKey, reason, 0).Result()
	if err != nil {
		return fmt.Errorf("persist stop marker %s: %w", stopKey, err)
	}
	if !created {
		return nil // already queued — dedup the kill order
	}

	// 2. Notify stop handler via channel. Safe to drop here: the marker is
	//    persisted, so recoverPendingStops re-queues it on restart.
	select {
	case stopCh <- StopSignal{SandboxID: sandboxID, Reason: reason}:
	default:
		log.Warn("stopCh full, signal dropped — will recover from Redis on restart",
			zap.String("sandbox", sandboxID),
		)
	}
	return nil
}

func extractSandboxID(v voucher.SandboxVoucher) string {
	return v.SandboxID
}

// stopAllSandboxesOf queues a stop for every open billing session owned by user
// under provider. Used when an AGGREGATED voucher settles INSUFFICIENT_BALANCE:
// that voucher spans the user's whole backlog, so its rejection means the
// account is empty and none of their sandboxes can be paid for — there is no
// "which sandbox" to pick, the answer is all of them.
//
// The gas-free sweep (maybeSweep → AggregateCovered → persistStop) normally
// stops these first, since it runs before submission and knows each held
// voucher's sandbox id. This is the narrow tail it cannot cover: a balance that
// empties between the sweep's split and the settlement landing on-chain.
func stopAllSandboxesOf(ctx context.Context, rdb *redis.Client, stopCh chan<- StopSignal, user, provider common.Address, log *zap.Logger) int {
	sessions, err := billing.ScanAllSessions(ctx, rdb)
	if err != nil {
		log.Error("aggregated-insufficient: scan sessions failed; sandboxes not stopped",
			zap.String("user", user.Hex()), zap.Error(err))
		return 0
	}
	stopped := 0
	for _, s := range sessions {
		if !strings.EqualFold(s.Owner, user.Hex()) || !strings.EqualFold(s.Provider, provider.Hex()) {
			continue
		}
		if err := persistStop(ctx, rdb, stopCh, s.SandboxID, "insufficient_balance", log); err != nil {
			log.Error("aggregated-insufficient: persist stop failed",
				zap.String("sandbox", s.SandboxID), zap.Error(err))
			continue
		}
		stopped++
	}
	return stopped
}
