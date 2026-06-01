package voucher

// Package voucher's DLQ surface is intentionally Inspect + Discard only.
// We do NOT offer a "requeue from DLQ" path because re-injecting a frozen
// voucher into the live billing pipeline breaks the implicit user-trust
// contract:
//
//   * Users ack a specific (teeSigner, signerVersion, prices) tuple — the
//     world state at the time they read on-chain `services[provider]`.
//   * A DLQ voucher was signed under an OLDER world state and its totalFee
//     reflects pricing/usage at that older time. Submitting it now under the
//     user's current ack would charge them under terms they never saw.
//   * Even if every contract-level check (signature, nonce, ack-version)
//     happened to pass after a requeue, the semantic outcome is surprise
//     billing — a fee the user never consented to in the way it was computed.
//
// If a provider truly needs to collect a missed period's fee, the correct
// recovery path is off-chain: contact the user, generate a fresh voucher
// under the *current* world state (which the user has acked), and submit
// that. DLQ entries are historical records, not zombie invoices.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
)

// DLQEntry is a parsed snapshot of one voucher sitting in the dead-letter
// queue. Carries enough fields for the dashboard to identify it without
// re-parsing the raw JSON client-side.
type DLQEntry struct {
	Raw         string `json:"-"` // exact JSON used as the LREM target
	SandboxID   string `json:"sandbox_id"`
	User        string `json:"user"`     // checksum hex
	Provider    string `json:"provider"` // checksum hex
	TotalFeeWei string `json:"total_fee_wei"`
	Nonce       string `json:"nonce"`
	Aggregated  bool   `json:"aggregated"`
}

// ListDLQ returns all vouchers currently in the dead-letter queue for
// provider. Newest-first ordering (matches how RPUSH appended them — the
// caller can sort if a different order is desired).
func ListDLQ(ctx context.Context, rdb *redis.Client, provider common.Address) ([]DLQEntry, error) {
	dlqKey := fmt.Sprintf(VoucherDLQKeyFmt, provider.Hex())
	items, err := rdb.LRange(ctx, dlqKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]DLQEntry, 0, len(items))
	for _, raw := range items {
		var v SandboxVoucher
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			continue
		}
		entry := DLQEntry{
			Raw:        raw,
			SandboxID:  v.SandboxID,
			User:       v.User.Hex(),
			Provider:   v.Provider.Hex(),
			Aggregated: v.IsAggregated(),
		}
		if v.TotalFee != nil {
			entry.TotalFeeWei = v.TotalFee.String()
		}
		if v.Nonce != nil {
			entry.Nonce = v.Nonce.String()
		}
		out = append(out, entry)
	}
	return out, nil
}

// DiscardFromDLQ removes a single voucher from the DLQ permanently.
// Lookup key is (user, provider, nonce). Returns the number removed (0 or 1).
func DiscardFromDLQ(ctx context.Context, rdb *redis.Client, user, provider common.Address, nonce string) (int, error) {
	if nonce == "" {
		return 0, errors.New("nonce required")
	}
	dlqKey := fmt.Sprintf(VoucherDLQKeyFmt, provider.Hex())
	items, err := rdb.LRange(ctx, dlqKey, 0, -1).Result()
	if err != nil {
		return 0, err
	}
	for _, raw := range items {
		var v SandboxVoucher
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			continue
		}
		if v.User != user || v.Provider != provider {
			continue
		}
		if v.Nonce == nil || v.Nonce.String() != nonce {
			continue
		}
		removed, err := rdb.LRem(ctx, dlqKey, 1, raw).Result()
		if err != nil {
			return 0, err
		}
		return int(removed), nil
	}
	return 0, nil
}
