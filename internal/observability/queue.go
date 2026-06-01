package observability

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
)

// RunQueueDepthMonitor periodically samples LLEN on queueKey and fires a
// KindQueueBacklog alert if the depth stays above threshold for two
// consecutive ticks (~2 minutes by default). Returns when ctx is cancelled.
//
// Sustained backlog with successful settles in the logs means generator
// outpaces settler — needs bigger batch or shorter interval. With failing
// settles it's a symptom of one of the more specific alerts already firing.
func RunQueueDepthMonitor(ctx context.Context, rdb *redis.Client, queueKey string, alerter alert.Alerter, threshold int64, log *zap.Logger) {
	log = log.With(zap.String("monitor", "queue"), zap.String("queue", queueKey))
	log.Info("queue depth monitor started", zap.Int64("threshold", threshold))

	consecutiveOver := 0
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("queue depth monitor stopped")
			return
		case <-ticker.C:
			depth, err := rdb.LLen(ctx, queueKey).Result()
			if err != nil {
				log.Warn("LLEN failed", zap.Error(err))
				consecutiveOver = 0
				continue
			}
			if depth > threshold {
				consecutiveOver++
				if consecutiveOver >= 2 {
					alerter.Notify(ctx, alert.KindQueueBacklog, alert.SeverityWarning,
						"Voucher queue depth above threshold — settler may be lagging",
						map[string]any{
							"depth":     depth,
							"threshold": threshold,
						},
					)
					// Stay armed: dedup at the alert layer suppresses repeats.
					// Reset only when we drop back below threshold.
				}
			} else {
				consecutiveOver = 0
			}
		}
	}
}
