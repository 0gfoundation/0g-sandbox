package voucher

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func mkVoucher(user, sandbox string, fee int64) SandboxVoucher {
	return SandboxVoucher{
		SandboxID: sandbox,
		User:      common.HexToAddress(user),
		Provider:  common.HexToAddress("0x2222222222222222222222222222222222222222"),
		TotalFee:  big.NewInt(fee),
		UsageHash: BuildUsageHash(sandbox, fee, fee, 60),
	}
}

const (
	userA = "0x1111111111111111111111111111111111111111"
	userB = "0x3333333333333333333333333333333333333333"
)

func TestCollapseBySandbox_MergesPerSandbox(t *testing.T) {
	// Interleaved so grouping cannot succeed by accident on contiguous runs.
	in := []SandboxVoucher{
		mkVoucher(userA, "sb-alpha", 100),
		mkVoucher(userA, "sb-beta", 100),
		mkVoucher(userA, "sb-alpha", 100),
		mkVoucher(userA, "sb-beta", 100),
		mkVoucher(userA, "sb-alpha", 100),
	}

	out := CollapseBySandbox(in, 1000)

	if len(out) != 2 {
		t.Fatalf("want 2 vouchers (one per sandbox), got %d", len(out))
	}
	// First appearance order: alpha before beta.
	if out[0].SandboxID != "sb-alpha" || out[1].SandboxID != "sb-beta" {
		t.Fatalf("first-appearance order not preserved: %s, %s", out[0].SandboxID, out[1].SandboxID)
	}
	if out[0].TotalFee.Int64() != 300 {
		t.Errorf("sb-alpha fee = %s, want 300", out[0].TotalFee)
	}
	if out[1].TotalFee.Int64() != 200 {
		t.Errorf("sb-beta fee = %s, want 200", out[1].TotalFee)
	}
	for _, v := range out {
		if !v.Aggregated {
			t.Errorf("%s: merged voucher must be marked aggregated", v.SandboxID)
		}
	}
}

func TestCollapseBySandbox_KeepsUsersApart(t *testing.T) {
	// Same sandbox id under two owners must never merge — the contract debits
	// a (user, provider) balance, so a cross-user merge would bill one user
	// for another's compute.
	in := []SandboxVoucher{
		mkVoucher(userA, "sb-shared", 100),
		mkVoucher(userB, "sb-shared", 100),
		mkVoucher(userA, "sb-shared", 100),
	}

	out := CollapseBySandbox(in, 1000)

	if len(out) != 2 {
		t.Fatalf("want 2 vouchers (one per user), got %d", len(out))
	}
	byUser := map[common.Address]int64{}
	for _, v := range out {
		byUser[v.User] = v.TotalFee.Int64()
	}
	if got := byUser[common.HexToAddress(userA)]; got != 200 {
		t.Errorf("userA fee = %d, want 200", got)
	}
	if got := byUser[common.HexToAddress(userB)]; got != 100 {
		t.Errorf("userB fee = %d, want 100", got)
	}
}

func TestCollapseBySandbox_SingletonUntouched(t *testing.T) {
	// A sandbox with one voucher in the batch keeps its exact usage hash, so
	// the ordinary single-period receipt is unchanged by collapsing.
	in := []SandboxVoucher{
		mkVoucher(userA, "sb-alpha", 100),
		mkVoucher(userA, "sb-beta", 100),
	}
	wantHash := in[1].UsageHash

	out := CollapseBySandbox(in, 1000)

	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	for _, v := range out {
		if v.Aggregated {
			t.Errorf("%s: unmerged voucher must not be marked aggregated", v.SandboxID)
		}
	}
	if out[1].UsageHash != wantHash {
		t.Error("singleton usage hash was rewritten")
	}
}

func TestCollapseBySandbox_DoesNotMutateInput(t *testing.T) {
	// The caller still holds the originals — they go into the pending-tx
	// record and the re-queue path. Summing into their big.Int would corrupt
	// both.
	in := []SandboxVoucher{
		mkVoucher(userA, "sb-alpha", 100),
		mkVoucher(userA, "sb-alpha", 100),
	}

	_ = CollapseBySandbox(in, 1000)

	for i, v := range in {
		if v.TotalFee.Int64() != 100 {
			t.Errorf("input[%d] fee mutated to %s", i, v.TotalFee)
		}
	}
}

func TestCollapseBySandbox_ShortInputPassesThrough(t *testing.T) {
	if got := CollapseBySandbox(nil, 1000); got != nil {
		t.Errorf("nil input should pass through, got %v", got)
	}
	one := []SandboxVoucher{mkVoucher(userA, "sb-alpha", 100)}
	out := CollapseBySandbox(one, 1000)
	if len(out) != 1 || out[0].Aggregated {
		t.Errorf("single voucher should pass through unchanged, got %+v", out)
	}
}

func TestCollapseBySandbox_TotalFeeIsConserved(t *testing.T) {
	// Collapsing must never change what the batch charges in total.
	in := []SandboxVoucher{
		mkVoucher(userA, "sb-alpha", 7),
		mkVoucher(userB, "sb-beta", 11),
		mkVoucher(userA, "sb-alpha", 13),
		mkVoucher(userA, "sb-gamma", 17),
		mkVoucher(userB, "sb-beta", 19),
	}
	want := new(big.Int)
	for _, v := range in {
		want.Add(want, v.TotalFee)
	}

	got := new(big.Int)
	for _, v := range CollapseBySandbox(in, 1000) {
		got.Add(got, v.TotalFee)
	}

	if want.Cmp(got) != 0 {
		t.Errorf("total fee changed: %s → %s", want, got)
	}
}
