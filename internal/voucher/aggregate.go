package voucher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
)

// MaxScanItems caps how many queue items the summary endpoint will
// deserialize in one call. Larger than this and the summary returns a
// "queue larger than scan limit" hint without scanning further. Picked
// to keep the response payload under a few MB.
const MaxScanItems = 10_000

// Group identifies the natural aggregation key for backlog cleanup —
// (user, provider). On-chain billing keys nonce and balance by this pair,
// so all queued vouchers for the same pair can be merged regardless of
// which sandbox originally produced them.
type Group struct {
	User     string `json:"user"`     // checksum hex
	Provider string `json:"provider"` // checksum hex
}

// Summary is one row in the backlog summary table — count of queued
// vouchers and their total fee, for a (user, provider) pair.
type Summary struct {
	Group
	Count       int    `json:"count"`
	TotalFeeWei string `json:"total_fee_wei"`
}

// SummarizeQueue scans the first up-to MaxScanItems entries of queueKey and
// groups them by (user, provider). Returns rows whose count is at least
// minCount; pass 1 to surface every group including already-aggregated
// singletons (useful for showing the aggregated voucher after merge).
//
// If the queue has more than MaxScanItems items, "truncated" is set so the
// caller can warn the operator. Unmarshal failures are silently skipped —
// they are typically dead/malformed entries that the settler also can't
// process.
func SummarizeQueue(ctx context.Context, rdb *redis.Client, queueKey string, minCount int) (rows []Summary, scanned int, truncated bool, err error) {
	total, err := rdb.LLen(ctx, queueKey).Result()
	if err != nil {
		return nil, 0, false, err
	}
	stop := total - 1
	if total > int64(MaxScanItems) {
		stop = int64(MaxScanItems) - 1
		truncated = true
	}
	if total == 0 {
		return nil, 0, false, nil
	}
	items, err := rdb.LRange(ctx, queueKey, 0, stop).Result()
	if err != nil {
		return nil, 0, false, err
	}
	scanned = len(items)

	type acc struct {
		count int
		fee   *big.Int
	}
	buckets := make(map[Group]*acc, 16)
	for _, raw := range items {
		var v SandboxVoucher
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			continue
		}
		key := Group{
			User:     v.User.Hex(),
			Provider: v.Provider.Hex(),
		}
		a := buckets[key]
		if a == nil {
			a = &acc{fee: new(big.Int)}
			buckets[key] = a
		}
		a.count++
		if v.TotalFee != nil {
			a.fee.Add(a.fee, v.TotalFee)
		}
	}

	for g, a := range buckets {
		if a.count < minCount {
			continue
		}
		rows = append(rows, Summary{
			Group:       g,
			Count:       a.count,
			TotalFeeWei: a.fee.String(),
		})
	}
	return rows, scanned, truncated, nil
}

// AggregateResult is what Aggregate returns after a successful merge.
type AggregateResult struct {
	Matched     int    `json:"matched"`
	TotalFeeWei string `json:"total_fee_wei"`
}

// AggregatedSandboxID is the sentinel value placed in SandboxVoucher.SandboxID
// after Aggregate merges across sandboxes. Empty string is the unambiguous
// signal "this voucher does not correspond to a single sandbox" — picking
// any concrete sandbox_id as a representative would be misleading because
// settle-time stop logic would target only one of N merged sandboxes.
const AggregatedSandboxID = ""

// IsAggregated reports whether v was produced by Aggregate (vs. a normal
// per-period voucher). Used by the settler to skip per-sandbox stop logic
// on settlement failure.
func (v *SandboxVoucher) IsAggregated() bool {
	return v.SandboxID == AggregatedSandboxID
}

// Aggregate atomically replaces every queued voucher matching the target
// (user, provider) — regardless of which sandbox emitted it — with a single
// voucher whose total_fee is the sum of all matched fees.
//
// On-chain billing keys nonce and balance by (user, provider), so merging
// across sandboxes is semantically clean: same payer, same payee, same
// settlement bucket. The aggregated voucher's SandboxID is intentionally
// empty (AggregatedSandboxID) — picking any concrete sandbox as a
// representative would mislead the settle-time per-sandbox stop logic.
//
// The aggregated voucher's usage_hash follows the create-fee convention:
// BuildUsageHash("", now, now, 0) — a single-point synthetic stamp.
// The on-chain contract treats usage_hash as opaque metadata, so this is
// safe; downstream tooling that recognises the (now, now, 0) pattern will
// correctly interpret it as a non-periodic event.
//
// Uses WATCH/MULTI/EXEC for atomicity against concurrent BLPOP by the
// settler. Retries up to maxRetries times on transaction conflict.
func Aggregate(ctx context.Context, rdb *redis.Client, queueKey string, user, provider common.Address) (*AggregateResult, error) {
	const maxRetries = 5
	userLower := strings.ToLower(user.Hex())
	providerLower := strings.ToLower(provider.Hex())

	var result *AggregateResult
	for attempt := 0; attempt < maxRetries; attempt++ {
		txErr := rdb.Watch(ctx, func(tx *redis.Tx) error {
			items, err := tx.LRange(ctx, queueKey, 0, -1).Result()
			if err != nil {
				return err
			}

			kept := make([]string, 0, len(items))
			total := new(big.Int)
			matched := 0

			for _, raw := range items {
				var v SandboxVoucher
				if err := json.Unmarshal([]byte(raw), &v); err != nil {
					kept = append(kept, raw)
					continue
				}
				if !strings.EqualFold(v.User.Hex(), userLower) ||
					!strings.EqualFold(v.Provider.Hex(), providerLower) {
					kept = append(kept, raw)
					continue
				}
				matched++
				if v.TotalFee != nil {
					total.Add(total, v.TotalFee)
				}
			}

			if matched == 0 {
				result = &AggregateResult{Matched: 0, TotalFeeWei: "0"}
				return nil
			}

			now := time.Now().Unix()
			agg := SandboxVoucher{
				SandboxID: AggregatedSandboxID,
				User:      user,
				Provider:  provider,
				TotalFee:  total,
				UsageHash: BuildUsageHash(AggregatedSandboxID, now, now, 0),
			}
			rawAgg, err := json.Marshal(agg)
			if err != nil {
				return fmt.Errorf("marshal aggregated: %w", err)
			}

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Del(ctx, queueKey)
				if len(kept) > 0 {
					vals := make([]any, 0, len(kept))
					for _, s := range kept {
						vals = append(vals, s)
					}
					pipe.RPush(ctx, queueKey, vals...)
				}
				pipe.RPush(ctx, queueKey, string(rawAgg))
				return nil
			})
			if err != nil {
				return err
			}
			result = &AggregateResult{
				Matched:     matched,
				TotalFeeWei: total.String(),
			}
			return nil
		}, queueKey)

		if txErr == nil {
			return result, nil
		}
		if !errors.Is(txErr, redis.TxFailedErr) {
			return nil, txErr
		}
		// retry on conflict
	}
	return nil, fmt.Errorf("aggregate failed after %d retries (queue churning?)", maxRetries)
}
