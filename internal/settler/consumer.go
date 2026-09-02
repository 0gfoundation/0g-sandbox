package settler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/config"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

const maxBatchSize = 50

// Run is the main settler loop: BLPOP → sign → settle → handle statuses.
// nonceSigner assigns nonces and signs vouchers sequentially, guaranteeing
// strict nonce ordering regardless of how many goroutines enqueued the vouchers.
// alerter receives operator alerts on tx failures and bug-class settle outcomes;
// pass alert.Nop{} to disable.
func Run(ctx context.Context, cfg *config.Config, rdb *redis.Client, onchain ChainClient, nonceSigner NonceSigner, stopCh chan<- StopSignal, alerter alert.Alerter, log *zap.Logger) {
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, onchain.ProviderAddress().Hex())
	// lockTime/2 as BLPOP timeout (half the lock window for responsiveness)
	blpopTimeout := time.Duration(cfg.Billing.VoucherIntervalSec) * time.Second / 2

	log.Info("settler started", zap.String("queue", queueKey))

	// Rotation gate state: throttle the on-chain node check and the warn log
	// so a long not-yet-registered window doesn't spam RPC or logs.
	var lastNodeCheck time.Time
	var nodeActive bool

	// Pre-settle sweep throttle. The sweep itself is gas-free (Redis + a
	// read-only balance call) and O(1) in steady state, so it runs even while
	// the rotation gate holds submissions — a backlog collapses and unpayable
	// sandboxes stop during an outage, not after it. lastSweep starts zero so a
	// cold start with a pre-existing backlog aggregates before the first submit.
	sweepInterval := time.Duration(cfg.Billing.VoucherIntervalSec) * time.Second
	if sweepInterval <= 0 {
		sweepInterval = time.Minute
	}
	var lastSweep time.Time
	// Balance memo for held users: skip re-splitting a held-only user whose
	// balance hasn't changed (their sandboxes are stopped, so the partition
	// couldn't change either — re-sweeping would just churn the held list).
	lastBal := map[common.Address]*big.Int{}

	for {
		if ctx.Err() != nil {
			log.Info("settler stopped")
			return
		}

		if time.Since(lastSweep) >= sweepInterval {
			maybeSweep(ctx, rdb, onchain, queueKey, stopCh, lastBal, log)
			lastSweep = time.Now()
		}

		// Rotation gate: while our signer is not a registered TappRegistry
		// node (fresh machine, add-node-onchain not run yet), hold the queue
		// instead of submitting — every voucher would settle
		// INVALID_SIGNATURE and dead-letter real revenue. Fail open on RPC
		// errors: a flaky RPC must not stall settlement.
		if time.Since(lastNodeCheck) > 30*time.Second {
			firstCheck := lastNodeCheck.IsZero()
			active, err := onchain.IsLocalTEEActiveNode(ctx)
			if err != nil {
				log.Warn("settler: node-membership check failed; assuming active", zap.Error(err))
				active = true
			}
			if !active && (firstCheck || nodeActive) {
				log.Warn("settler: local TEE signer is not an active TappRegistry node — holding voucher queue until add-node-onchain lands")
			}
			if active && !nodeActive && !firstCheck {
				log.Info("settler: local TEE signer registered on-chain — resuming settlement")
			}
			nodeActive = active
			lastNodeCheck = time.Now()
		}
		if !nodeActive {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// BLPOP blocks until an item appears or timeout
		results, err := rdb.BLPop(ctx, blpopTimeout, queueKey).Result()
		if err != nil {
			if err == redis.Nil {
				// Timeout: no items, loop back
				continue
			}
			if ctx.Err() != nil {
				return
			}
			log.Error("settler: BLPOP error", zap.Error(err))
			time.Sleep(time.Second)
			continue
		}

		// results[0] = key, results[1] = value (already popped by BLPOP)
		firstItem := results[1]

		// Peek remaining items (don't pop yet; pop happens in handler after settlement)
		remaining, err := rdb.LRange(ctx, queueKey, 0, int64(maxBatchSize-2)).Result()
		if err != nil {
			log.Error("settler: LRANGE", zap.Error(err))
			remaining = nil
		}

		// Deserialize batch
		rawItems := append([]string{firstItem}, remaining...)
		vouchers := make([]voucher.SandboxVoucher, 0, len(rawItems))
		for _, raw := range rawItems {
			var v voucher.SandboxVoucher
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				log.Error("settler: unmarshal voucher", zap.String("raw", raw), zap.Error(err))
				continue
			}
			vouchers = append(vouchers, v)
		}

		if len(vouchers) == 0 {
			continue
		}

		// Assign nonces and sign in order. The settler is the sole consumer,
		// so sequential Sign calls guarantee strictly-increasing nonces.
		signingOK := true
		for i := range vouchers {
			if err := nonceSigner.Sign(ctx, &vouchers[i]); err != nil {
				log.Error("settler: sign voucher",
					zap.String("sandbox", vouchers[i].SandboxID),
					zap.Error(err),
				)
				signingOK = false
				break
			}
		}
		if !signingOK {
			_ = rdb.LPush(ctx, queueKey, firstItem)
			time.Sleep(5 * time.Second)
			continue
		}

		// Submit to chain
		statuses, err := onchain.SettleFeesWithTEE(ctx, vouchers)
		if err != nil {
			log.Error("settler: SettleFeesWithTEE", zap.Error(err))
			errType := alert.ClassifyChainErr(err)
			sev := alert.SeverityCritical
			if errType == "timeout" || errType == "rpc_unreachable" {
				sev = alert.SeverityWarning // transient
			}
			alerter.Notify(ctx, alert.KindSettlerTxFailure, sev,
				"SettleFeesWithTEE submission failed",
				map[string]any{
					"err":      err.Error(),
					"err_type": errType,
					"batch":    len(vouchers),
				},
			)
			// Re-push first item back (it was already BLPOP'd)
			_ = rdb.LPush(ctx, queueKey, firstItem)
			time.Sleep(5 * time.Second)
			continue
		}

		// Handle results (first item already popped; handler pops the rest)
		HandleStatuses(ctx, rdb, stopCh, queueKey, firstItem, vouchers, statuses, alerter, log)

		// Targeted sweep: a user whose settlement just rejected
		// INSUFFICIENT_BALANCE is out of money NOW — park their remaining
		// queued vouchers as held debt immediately instead of burning one
		// nonce per interval until the periodic sweep catches up.
		broke := map[common.Address]bool{}
		var brokeUsers []common.Address
		for i, st := range statuses {
			if st == chain.StatusInsufficientBalance && !broke[vouchers[i].User] {
				broke[vouchers[i].User] = true
				brokeUsers = append(brokeUsers, vouchers[i].User)
			}
		}
		sweepUsers(ctx, rdb, onchain, queueKey, stopCh, brokeUsers, log)
	}
}
