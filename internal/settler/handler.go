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
			} else {
				persistStop(ctx, rdb, stopCh, sandboxID, "insufficient_balance", log)
			}

		case chain.StatusNotAcknowledged:
			if v.IsAggregated() {
				log.Warn("aggregated voucher rejected: user not acknowledged",
					zap.String("user", v.User.Hex()),
					zap.String("provider", v.Provider.Hex()),
				)
			} else {
				persistStop(ctx, rdb, stopCh, sandboxID, "not_acknowledged", log)
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
					"status":    status.String(),
					"user":      v.User.Hex(),
					"provider":  v.Provider.Hex(),
					"sandbox":   sandboxID,
					"nonce":     v.Nonce.String(),
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

func persistStop(ctx context.Context, rdb *redis.Client, stopCh chan<- StopSignal, sandboxID, reason string, log *zap.Logger) {
	// 1. Persist first (crash-safe)
	stopKey := "stop:sandbox:" + sandboxID
	rdb.Set(ctx, stopKey, reason, 0)

	// 2. Notify stop handler via channel
	select {
	case stopCh <- StopSignal{SandboxID: sandboxID, Reason: reason}:
	default:
		log.Warn("stopCh full, signal dropped — will recover from Redis on restart",
			zap.String("sandbox", sandboxID),
		)
	}
}

func extractSandboxID(v voucher.SandboxVoucher) string {
	return v.SandboxID
}
