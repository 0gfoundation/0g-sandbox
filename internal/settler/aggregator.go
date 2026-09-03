package settler

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// backlogSweepThreshold is the queue length above which the pre-settle sweep
// kicks in and aggregates per-user backlogs. Steady state produces one voucher
// per session per interval and the settler drains promptly, so a queue this
// deep means the settler has been unable to submit (outage, no gas, node not
// registered) — exactly when per-voucher submission would burn gas on
// guaranteed rejects. Below it, vouchers settle one-by-one and keep their
// per-sandbox identity (sandbox_id + real usage_hash).
const backlogSweepThreshold = 100

// AggregatorChain is the slice of the chain client the sweep needs: the
// provider identity and a batched read-only balance call (no gas required).
// ChainClient includes both, so the settler's client can be passed directly.
type AggregatorChain interface {
	ProviderAddress() common.Address
	GetBalanceBatch(ctx context.Context, users []common.Address, provider common.Address) ([]*big.Int, error)
}

// maybeSweep is the settler's pre-settle preprocessing step. It is designed to
// cost two O(1) Redis commands (LLEN + SCARD) in steady state — no chain call,
// no queue parsing, no aggregation — and only does real work when one of two
// signals fires:
//
//   - queue length > backlogSweepThreshold: a settler-outage backlog; every
//     backlogged user's (held + queued) vouchers are re-split against their
//     balance, folding the affordable prefix into one aggregate and parking the
//     unpayable remainder as held debt (see voucher.AggregateCovered).
//   - held-users index non-empty: someone has parked debt; only those users'
//     balances are re-read so a top-up reclaims and settles the debt,
//     oldest-first, before any newer voucher.
//
// force bypasses the backlogSweepThreshold guard: the caller knows submissions
// are currently impossible (tx submission failed, or the rotation gate is
// holding the queue) and wants the sweep's protection now, on a queue of any
// size. Without it, a small deployment — a single user, a handful of vouchers —
// never crosses the threshold, so the sweep stays dormant and an unpayable
// sandbox runs unbilled for as long as the outage lasts. Forced sweeps are
// gas-free like periodic ones (Redis + a read-only balance call), so they are
// safe to run exactly while the settler cannot pay for anything else.
//
// lastBal memoizes the balance each held user was last split against.
// Re-splitting a held-only user at an unchanged balance yields an equivalent
// partition, so skipping it stops the sweep from rewriting the whole held list
// every interval while the user simply hasn't topped up (and from oscillating
// between equivalent partitions). Sandboxes with HELD vouchers are stopped, but
// the user may still have covered sandboxes emitting new vouchers — that stays
// safe because new activity reaches the memo-free paths: the rejection-targeted
// sweep and the >threshold backlog path.
// Users enumerated from a deep queue are always swept: same balance over a NEW
// backlog is a different input. The memo is in-memory; a restart just costs one
// redundant sweep per held user.
//
// Sandboxes whose owner cannot cover their oldest voucher are stopped
// (persistStop dedups via SetNX, so re-sweeping never re-kills).
func maybeSweep(ctx context.Context, rdb *redis.Client, onchain AggregatorChain, queueKey string, stopCh chan<- StopSignal, lastBal map[common.Address]*big.Int, log *zap.Logger, force bool) {
	provider := onchain.ProviderAddress()
	heldUsersKey := fmt.Sprintf(voucher.VoucherHeldUsersKeyFmt, strings.ToLower(provider.Hex()))

	qlen, err := rdb.LLen(ctx, queueKey).Result()
	if err != nil {
		log.Warn("sweep guard: LLEN", zap.Error(err))
		return
	}
	heldUserCount, err := rdb.SCard(ctx, heldUsersKey).Result()
	if err != nil {
		log.Warn("sweep guard: SCARD", zap.Error(err))
		return
	}
	if !force && qlen <= backlogSweepThreshold && heldUserCount == 0 {
		return // steady state: nothing to aggregate, nothing to reclaim
	}

	seen := map[common.Address]bool{}
	fromQueue := map[common.Address]bool{}
	var users []common.Address
	if force || qlen > backlogSweepThreshold {
		rows, _, truncated, err := voucher.SummarizeQueue(ctx, rdb, queueKey, 1)
		if err != nil {
			log.Warn("sweep: summarize queue", zap.Error(err))
			return
		}
		if truncated {
			log.Info("sweep: queue larger than scan limit; further users picked up next pass", zap.Int64("qlen", qlen))
		}
		for _, r := range rows {
			u := common.HexToAddress(r.User)
			if !seen[u] {
				seen[u] = true
				fromQueue[u] = true
				users = append(users, u)
			}
		}
	}
	if heldUserCount > 0 {
		held, err := voucher.HeldUsers(ctx, rdb, provider)
		if err != nil {
			log.Warn("sweep: held users", zap.Error(err))
		}
		for _, u := range held {
			if !seen[u] {
				seen[u] = true
				users = append(users, u)
			}
		}
	}
	sweepUsersMemo(ctx, rdb, onchain, queueKey, stopCh, users, fromQueue, lastBal, log)
}

// sweepUsers re-splits the given users' outstanding backlog against their
// current on-chain balance. Also used targeted (single user) when a settlement
// rejects INSUFFICIENT_BALANCE, so the user's remaining queued vouchers are
// parked as debt immediately instead of burning one nonce per interval.
func sweepUsers(ctx context.Context, rdb *redis.Client, onchain AggregatorChain, queueKey string, stopCh chan<- StopSignal, users []common.Address, log *zap.Logger) {
	sweepUsersMemo(ctx, rdb, onchain, queueKey, stopCh, users, nil, nil, log)
}

// sweepUsersMemo is sweepUsers with the held-user balance memo: a user NOT in
// fromQueue (held-only) whose balance equals lastBal's entry is skipped — same
// balance over the same stopped backlog re-derives the same partition, so the
// rewrite would be pure churn. Passing nil maps disables the memo (targeted
// sweeps always run).
func sweepUsersMemo(ctx context.Context, rdb *redis.Client, onchain AggregatorChain, queueKey string, stopCh chan<- StopSignal, users []common.Address, fromQueue map[common.Address]bool, lastBal map[common.Address]*big.Int, log *zap.Logger) {
	if len(users) == 0 {
		return
	}
	provider := onchain.ProviderAddress()
	balances, err := onchain.GetBalanceBatch(ctx, users, provider)
	if err != nil {
		log.Warn("sweep: batch balance read", zap.Int("users", len(users)), zap.Error(err))
		return
	}
	for i, u := range users {
		var bal *big.Int
		if i < len(balances) {
			bal = balances[i]
		}
		if lastBal != nil && !fromQueue[u] {
			if prev, ok := lastBal[u]; ok && prev != nil && bal != nil && prev.Cmp(bal) == 0 {
				continue // held-only user, balance unchanged: nothing to reclaim or re-park
			}
		}
		res, err := voucher.AggregateCovered(ctx, rdb, queueKey, u, provider, bal)
		if err != nil {
			log.Warn("sweep: split backlog", zap.String("user", u.Hex()), zap.Error(err))
			continue // memo NOT recorded: a failed split must be retried at this balance
		}
		// Record the memo only after a successful split — writing it first would
		// poison the skip on error: a top-up whose split failed would then be
		// skipped forever (until the balance moves again or a restart).
		if lastBal != nil && bal != nil {
			lastBal[u] = new(big.Int).Set(bal)
		}
		if res.Dropped > 0 {
			log.Warn("sweep: dropped unparseable backlog entries (permanent)",
				zap.String("user", u.Hex()), zap.Int("dropped", res.Dropped))
		}
		if res.Covered == 0 && res.Held == 0 {
			continue
		}
		// Stop sandboxes whose owner can no longer pay. persistStop dedups via
		// SetNX, so re-holding the same sandbox next pass does not re-kill it.
		for _, sb := range res.HeldSandboxIDs {
			if err := persistStop(ctx, rdb, stopCh, sb, "insufficient_balance", log); err != nil {
				log.Error("sweep: persist stop", zap.String("sandbox", sb), zap.Error(err))
			}
		}
		log.Info("sweep split backlog",
			zap.String("user", u.Hex()),
			zap.Int("covered", res.Covered),
			zap.String("covered_fee", res.CoveredFeeWei),
			zap.Int("held", res.Held),
			zap.String("held_fee", res.HeldFeeWei),
			zap.Int("stopped_sandboxes", len(res.HeldSandboxIDs)),
		)
	}
}
