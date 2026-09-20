package settler

import (
	"context"
	"math/big"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// The sweep emits per-sandbox aggregates, which carry a REAL sandbox id rather
// than the empty sentinel. That shape must still take the stop-all branch on
// INSUFFICIENT_BALANCE: the rejection says the account is exhausted, so every
// sandbox the owner runs is unpayable — not just the one named on the voucher.
//
// This guards the seam between per-sandbox aggregation and this handler. The
// explicit Aggregated flag is what keeps the branch reachable; without it,
// identification falls back to the empty-sentinel check, which a per-sandbox
// aggregate fails — the owner's other sandboxes would then keep billing against
// a dead account while the tests stayed green.
func TestInsufficientBalance_PerSandboxAggregateStopsEveryOwnedSandbox(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	owner := common.HexToAddress("0x1111111111111111111111111111111111111111")
	prov := common.HexToAddress("0x2222222222222222222222222222222222222222")

	owned := []string{"sb-alpha", "sb-beta", "sb-gamma"}
	for _, sb := range owned {
		if err := billing.CreateSession(ctx, rdb, billing.Session{
			SandboxID: sb, Owner: owner.Hex(), Provider: prov.Hex(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	agg := voucher.SandboxVoucher{
		SandboxID:  "sb-alpha", // a real sandbox, not AggregatedSandboxID
		Aggregated: true,
		User:       owner,
		Provider:   prov,
		TotalFee:   big.NewInt(999),
		Nonce:      big.NewInt(7),
	}
	if !agg.IsAggregated() {
		t.Fatal("a per-sandbox aggregate must still identify as aggregated")
	}

	stopCh := make(chan StopSignal, len(owned)+4)
	HandleStatuses(ctx, rdb, stopCh, "q", "raw", []voucher.SandboxVoucher{agg},
		[]chain.SettlementStatus{chain.StatusInsufficientBalance}, alert.Nop{}, zap.NewNop())

	stopped := map[string]bool{}
	for len(stopCh) > 0 {
		stopped[(<-stopCh).SandboxID] = true
	}
	for _, sb := range owned {
		if !stopped[sb] {
			t.Errorf("%s kept running on an exhausted account; stopped = %v", sb, stopped)
		}
	}
}
