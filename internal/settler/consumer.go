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

// maxSettleInterval caps SETTLE_INTERVAL_SEC at half the contract's refund
// LOCK_TIME (2h), leaving a full hour of margin for a backlog to drain before a
// requested refund becomes withdrawable.
const maxSettleInterval = time.Hour

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

	// Startup recovery: a crash between broadcast and receipt leaves a
	// persisted pending tx. Resolve its fate BEFORE consuming the queue —
	// consuming first would re-sign the same usage while the old tx may still
	// mine (double charge).
	if p, err := loadPendingTx(ctx, rdb, onchain.ProviderAddress()); err != nil {
		log.Error("settler: load pending tx failed", zap.Error(err))
	} else if p != nil {
		if p.TxHash == (common.Hash{}) {
			log.Warn("settler: hashless intent record from previous run — reconciling against chain nonces")
			reconcileIntent(ctx, rdb, onchain, queueKey, onchain.ProviderAddress(), p, log)
		} else {
			log.Warn("settler: unresolved settlement tx from previous run — resolving before consuming",
				zap.String("tx", p.TxHash.Hex()), zap.Int("batch", len(p.Vouchers)))
			resolvePendingTx(ctx, rdb, onchain, queueKey, stopCh, onchain.ProviderAddress(), p, alerter, log)
		}
	}

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
	// Settlement cadence, independent of accounting cadence. The sweep above
	// deliberately stays on the ACCOUNTING clock: it is gas-free and it is what
	// stops unpayable sandboxes (maybeSweep → AggregateCovered → persistStop),
	// so a slower settlement cadence must not slow it down. Only the decision
	// to drain the queue moves.
	settleInterval := time.Duration(cfg.Billing.SettleIntervalSec) * time.Second
	if settleInterval <= 0 {
		settleInterval = sweepInterval // unset = today's behaviour
	}
	// Hard ceiling: the contract lets a user move funds to pendingRefunds and
	// withdraw them after LOCK_TIME (2h). Settlement can still seize
	// pendingRefunds while the lock runs (_settleOne sweeps balances +
	// pendingRefunds), so revenue is safe only while the settle window leaves
	// room to land inside that lock. A window at or past LOCK_TIME would let a
	// user request a refund, wait it out, withdraw, and have the settlement
	// arrive at an empty account. Clamp well under it.
	if settleInterval > maxSettleInterval {
		log.Warn("settler: SETTLE_INTERVAL_SEC exceeds the safe ceiling; clamping",
			zap.Duration("configured", settleInterval),
			zap.Duration("clamped_to", maxSettleInterval),
			zap.String("why", "contract refund LOCK_TIME is 2h; settlement must land inside it"))
		settleInterval = maxSettleInterval
	}
	// Zero value means "never submitted yet", so the first batch goes out
	// immediately rather than waiting out a full interval after a restart.
	var lastSubmit time.Time
	var lastSweep time.Time
	// Forced-sweep throttle: while the settler cannot submit (tx failure or
	// rotation hold), maybeSweep is forced at the same cadence so unpayable
	// sandboxes are stopped during the outage, not after it. Separate from
	// lastSweep so a just-run periodic sweep cannot delay the first forced one.
	var lastForcedSweep time.Time
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
			maybeSweep(ctx, rdb, onchain, queueKey, stopCh, lastBal, log, false)
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
			// Degraded-mode stop, rotation flavor: submissions are held for as
			// long as the operator has not run add-node-onchain, and the
			// periodic sweep's threshold guard leaves small queues dormant —
			// force the gas-free sweep so unpayable sandboxes stop meanwhile.
			if time.Since(lastForcedSweep) >= sweepInterval {
				maybeSweep(ctx, rdb, onchain, queueKey, stopCh, lastBal, log, true)
				lastForcedSweep = time.Now()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// Batching gate: hold off draining until the settle window has elapsed
		// or the queue already holds a full batch. Without this the loop sits in
		// BLPOP and submits each voucher the instant it lands, so the queue
		// never accumulates and maxBatchSize is never reached — one transaction
		// per voucher. Vouchers are never popped-and-pushed-back here, only the
		// decision to START draining moves, so the strictly-increasing nonce
		// order the contract requires is untouched.
		if !lastSubmit.IsZero() && time.Since(lastSubmit) < settleInterval {
			qlen, qerr := rdb.LLen(ctx, queueKey).Result()
			if qerr != nil {
				log.Warn("settler: LLEN failed; draining without the batching gate", zap.Error(qerr))
			} else if qlen < int64(maxBatchSize) {
				// Not due and not full: keep the loop turning (node checks,
				// sweep, stop protection) without consuming the queue.
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
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

		// Queue entries this batch owns. Tracked separately from the voucher
		// count because the collapse below merges several entries into one
		// voucher; the pop bookkeeping still has to clear every entry.
		consumed := len(rawItems)

		// Collapse by sandbox before signing. Batching alone puts many
		// vouchers in one transaction but the contract still runs _settleOne
		// per array element, so the per-voucher cost — signature recovery, the
		// ack lookup, the balance and nonce writes — scales with accounting
		// periods rather than with sandboxes. Merging here is what turns a
		// slower settlement cadence into less on-chain work instead of just
		// fewer transactions.
		//
		// Before signing, because the merge rewrites total_fee and usage_hash;
		// nonces are assigned below, so the strictly-increasing order the
		// contract requires is unaffected.
		vouchers = voucher.CollapseBySandbox(vouchers, time.Now().Unix())
		if consumed > len(vouchers) {
			log.Debug("settler: collapsed batch by sandbox",
				zap.Int("entries", consumed), zap.Int("vouchers", len(vouchers)))
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

		// Submit to chain: broadcast, persist the in-flight tx, then resolve
		// its fate. A broadcast error means nothing reached the chain — safe
		// to re-queue and re-sign. Past broadcast, the ONLY safe paths are
		// through resolvePendingTx: re-signing while the tx may still mine
		// settles the same usage twice.
		// Intent record BEFORE broadcast: a crash in the instant between the
		// broadcast returning and the hash being persisted would otherwise
		// lose the in-flight tx and re-sign on restart (the double-charge
		// shape again, just a much smaller window). A hashless record is
		// reconciled at startup against on-chain lastNonce per voucher.
		intent := pendingTx{Vouchers: vouchers, FirstItem: firstItem, Consumed: consumed}
		if err := savePendingTx(ctx, rdb, onchain.ProviderAddress(), intent); err != nil {
			log.Error("settler: persist intent failed; holding batch", zap.Error(err))
			_ = rdb.LPush(ctx, queueKey, firstItem)
			time.Sleep(5 * time.Second)
			continue
		}
		tx, err := onchain.SubmitSettleFees(ctx, vouchers)
		if err != nil {
			clearPendingTx(ctx, rdb, onchain.ProviderAddress())
			log.Error("settler: SubmitSettleFees", zap.Error(err))
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
			// Degraded-mode stop: the on-chain stop path is unreachable while
			// we cannot submit — a bounced voucher is what triggers
			// persistStop, and nothing bounces when nothing settles. Meanwhile
			// the periodic sweep's threshold guard (qlen > 100) leaves small
			// queues dormant, so a single user's sandbox runs unbilled for the
			// whole outage (observed live: 12 minutes on an empty bucket while
			// the settler wallet was dry). Force the gas-free sweep on every
			// failed submit, throttled to one per interval: it re-splits each
			// user's backlog against their on-chain balance, parks the
			// unpayable part as held debt and stops those sandboxes now.
			if time.Since(lastForcedSweep) >= sweepInterval {
				maybeSweep(ctx, rdb, onchain, queueKey, stopCh, lastBal, log, true)
				lastForcedSweep = time.Now()
			}
			time.Sleep(5 * time.Second)
			continue
		}
		p := intent.broadcast(tx.Hash(), tx.Nonce())
		if err := savePendingTx(ctx, rdb, onchain.ProviderAddress(), p); err != nil { // backfill hash onto the intent
			// Redis down right after broadcast: resolve in-memory — do NOT
			// re-queue (the tx is in flight).
			log.Error("settler: persist pending tx failed; resolving in-memory", zap.Error(err))
		}
		lastSubmit = time.Now()
		statuses := resolvePendingTx(ctx, rdb, onchain, queueKey, stopCh, onchain.ProviderAddress(), &p, alerter, log)
		if statuses == nil {
			continue // re-queued (dropped/reverted) or ctx done — nothing settled
		}

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
