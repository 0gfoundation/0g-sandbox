package settler

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// AggregatorChain is the slice of the chain client the aggregator needs: the
// provider identity and a batched balance read. The real chain.Client satisfies
// it alongside ChainClient.
type AggregatorChain interface {
	ProviderAddress() common.Address
	GetBalanceBatch(ctx context.Context, users []common.Address, provider common.Address) ([]*big.Int, error)
}

// RunAggregator periodically re-splits every backlogged user's outstanding
// vouchers (queued + already-held) against their current on-chain balance:
// the affordable prefix is folded into one settle-now aggregate the settler
// then drains, and the rest is parked as held debt. Users whose balance can no
// longer cover their oldest voucher have their sandboxes stopped — they would
// otherwise keep generating unpayable vouchers every interval.
//
// Running every interval also collapses a settler-outage backlog before the
// settler ever submits it (turning thousands of guaranteed-reject settlements
// into a handful of aggregates) and reclaims held debt after a top-up — the two
// halves of issue #69's fix beyond the SetNX kill-order dedup.
func RunAggregator(ctx context.Context, rdb *redis.Client, onchain AggregatorChain, stopCh chan<- StopSignal, intervalSec int64, log *zap.Logger) {
	provider := onchain.ProviderAddress()
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, provider.Hex())
	interval := time.Duration(intervalSec) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("settler aggregator started", zap.Duration("interval", interval), zap.String("queue", queueKey))
	for {
		select {
		case <-ctx.Done():
			log.Info("settler aggregator stopped")
			return
		case <-ticker.C:
			sweepBacklog(ctx, rdb, onchain, provider, queueKey, stopCh, log)
		}
	}
}

// sweepBacklog runs one aggregation pass over every candidate (user, provider).
func sweepBacklog(ctx context.Context, rdb *redis.Client, onchain AggregatorChain, provider common.Address, queueKey string, stopCh chan<- StopSignal, log *zap.Logger) {
	users, err := candidateUsers(ctx, rdb, queueKey, provider)
	if err != nil {
		log.Warn("aggregator: enumerate users", zap.Error(err))
		return
	}
	if len(users) == 0 {
		return
	}
	balances, err := onchain.GetBalanceBatch(ctx, users, provider)
	if err != nil {
		log.Warn("aggregator: batch balance read", zap.Int("users", len(users)), zap.Error(err))
		return
	}
	for i, u := range users {
		var bal *big.Int
		if i < len(balances) {
			bal = balances[i]
		}
		res, err := voucher.AggregateCovered(ctx, rdb, queueKey, u, provider, bal)
		if err != nil {
			log.Warn("aggregator: split backlog", zap.String("user", u.Hex()), zap.Error(err))
			continue
		}
		if res.Covered == 0 && res.Held == 0 {
			continue
		}
		// Stop sandboxes whose owner can no longer pay. persistStop dedups via
		// SetNX, so re-holding the same sandbox next pass does not re-kill it.
		for _, sb := range res.HeldSandboxIDs {
			if err := persistStop(ctx, rdb, stopCh, sb, "insufficient_balance", log); err != nil {
				log.Error("aggregator: persist stop", zap.String("sandbox", sb), zap.Error(err))
			}
		}
		log.Info("aggregator swept backlog",
			zap.String("user", u.Hex()),
			zap.Int("covered", res.Covered),
			zap.String("covered_fee", res.CoveredFeeWei),
			zap.Int("held", res.Held),
			zap.String("held_fee", res.HeldFeeWei),
			zap.Int("stopped_sandboxes", len(res.HeldSandboxIDs)),
		)
	}
}

// candidateUsers is the union of users with vouchers currently queued and users
// with a held (debt) list — the latter must be revisited each pass so a top-up
// on an already-drained account still gets its debt reclaimed and settled.
func candidateUsers(ctx context.Context, rdb *redis.Client, queueKey string, provider common.Address) ([]common.Address, error) {
	seen := map[string]bool{}
	var users []common.Address

	// From the live queue.
	rows, _, _, err := voucher.SummarizeQueue(ctx, rdb, queueKey, 1)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		lower := strings.ToLower(r.User)
		if !seen[lower] {
			seen[lower] = true
			users = append(users, common.HexToAddress(r.User))
		}
	}

	// From held lists: voucher:held:<user>:<provider>.
	providerLower := strings.ToLower(provider.Hex())
	pattern := fmt.Sprintf(voucher.VoucherHeldKeyFmt, "*", providerLower)
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, pattern, 256).Result()
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			// key = voucher:held:<userLower>:<providerLower>
			parts := strings.Split(k, ":")
			if len(parts) != 4 {
				continue
			}
			lower := parts[2]
			if !seen[lower] {
				seen[lower] = true
				users = append(users, common.HexToAddress(lower))
			}
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return users, nil
}
