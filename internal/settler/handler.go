package settler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/events"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// HandleStatuses processes settlement results for a batch of vouchers.
// firstItem is already BLPOP'd; remaining items are LPOP'd here as they are processed.
func HandleStatuses(
	ctx context.Context,
	rdb *redis.Client,
	stopCh chan<- StopSignal,
	queueKey string,
	firstItem string,
	vouchers []voucher.SandboxVoucher,
	statuses []chain.SettlementStatus,
	alerter alert.Alerter,
	log *zap.Logger,
) {
	for i, status := range statuses {
		v := vouchers[i]

		// For items after the first (already BLPOP'd), pop from queue
		if i > 0 {
			rdb.LPop(ctx, queueKey)
		}

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
				// Aggregated voucher: no specific sandbox to stop. Alert so the
				// operator can intervene; per-(user, provider) stop sweep is
				// future work if this becomes common.
				log.Warn("aggregated voucher exhausted user balance",
					zap.String("user", v.User.Hex()),
					zap.String("provider", v.Provider.Hex()),
					zap.String("amount", v.TotalFee.String()),
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
			log.Warn("voucher discarded: invalid nonce",
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
