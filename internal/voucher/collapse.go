package voucher

import (
	"math/big"
	"strings"
)

// CollapseBySandbox merges a settle batch by (user, provider, sandbox) so the
// contract runs _settleOne once per sandbox instead of once per accounting
// period. Batching vouchers into one transaction only removes the per-tx fixed
// cost; the per-voucher work — EIP-712 recover, the TappRegistry ack lookup,
// the balance and nonce storage writes — is paid once per array element, and
// that is the larger share. Four sandboxes billed every minute and settled
// every three produce twelve array elements where four would do.
//
// Grouping is by sandbox rather than by user so a settled receipt still names
// the sandbox that ran the compute. A user-wide collapse would settle for less
// gas still, but the on-chain record could no longer be attributed.
//
// Order of first appearance is preserved: the settler signs sequentially and
// the contract requires strictly-increasing nonces, so a stable order keeps
// nonce assignment deterministic across retries of the same batch.
//
// Vouchers must be unsigned — merging rewrites total_fee and usage_hash, which
// a signature would no longer cover. Call this before nonce assignment.
//
// Callers must not infer how many queue entries a batch consumed from the
// returned length: a collapsed voucher stands for several entries, and popping
// per returned voucher would leave the rest behind to settle a second time.
func CollapseBySandbox(vs []SandboxVoucher, now int64) []SandboxVoucher {
	if len(vs) < 2 {
		return vs
	}

	type groupKey struct{ user, provider, sandbox string }
	index := make(map[groupKey]int, len(vs))
	out := make([]SandboxVoucher, 0, len(vs))
	merged := make([]int, 0, len(vs))

	for _, v := range vs {
		k := groupKey{
			user:     strings.ToLower(v.User.Hex()),
			provider: strings.ToLower(v.Provider.Hex()),
			sandbox:  v.SandboxID,
		}
		if i, ok := index[k]; ok {
			out[i].TotalFee = new(big.Int).Add(out[i].TotalFee, v.TotalFee)
			merged[i]++
			continue
		}
		cp := v
		// Copy the fee: summing into the caller's big.Int would mutate the
		// voucher it still holds (the pending-tx record, the re-queue path).
		cp.TotalFee = new(big.Int).Set(v.TotalFee)
		index[k] = len(out)
		out = append(out, cp)
		merged = append(merged, 1)
	}

	for i := range out {
		if merged[i] < 2 {
			// A lone voucher is left exactly as it arrived, usage hash and
			// all, so the common single-period receipt stays byte-identical.
			continue
		}
		// The merged voucher covers a span rather than one period. Reuse the
		// create-fee convention (start == end, interval 0): the hash commits
		// to the sandbox and the settle instant, and the off-chain queue
		// record carries the detail.
		out[i].UsageHash = BuildUsageHash(out[i].SandboxID, now, now, 0)
		// Marks this as spanning several periods, which is what tells the
		// settler that INSUFFICIENT_BALANCE means the account is exhausted
		// outright rather than one period being unaffordable.
		out[i].Aggregated = true
	}

	return out
}
