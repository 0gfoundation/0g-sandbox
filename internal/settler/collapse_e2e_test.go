package settler

import (
	"context"
	"encoding/json"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/config"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// billedChain records what was actually charged, not just how many vouchers
// were submitted. Under-popping does not leave the queue permanently full —
// the loop drains again and again — so the symptom is the same fee being
// settled repeatedly, which only a running total exposes.
type billedChain struct {
	provider common.Address

	mu       sync.Mutex
	billed   *big.Int
	submits  int
	perBatch []int
}

func newBilledChain(provider common.Address) *billedChain {
	return &billedChain{provider: provider, billed: new(big.Int)}
}

func (b *billedChain) SubmitSettleFees(_ context.Context, vs []voucher.SandboxVoucher) (*types.Transaction, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.submits++
	b.perBatch = append(b.perBatch, len(vs))
	for _, v := range vs {
		b.billed.Add(b.billed, v.TotalFee)
	}
	return types.NewTx(&types.LegacyTx{Nonce: uint64(b.submits)}), nil
}

func (b *billedChain) total() (*big.Int, int, []int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return new(big.Int).Set(b.billed), b.submits, append([]int(nil), b.perBatch...)
}

func (b *billedChain) ResolveTxFate(context.Context, common.Hash, uint64) (chain.TxFate, *types.Receipt, error) {
	return chain.TxMined, &types.Receipt{Status: 1}, nil
}

func (b *billedChain) SettleStatusesFromReceipt(_ context.Context, _ *types.Receipt, vs []voucher.SandboxVoucher) ([]chain.SettlementStatus, error) {
	st := make([]chain.SettlementStatus, len(vs))
	for i := range st {
		st[i] = chain.StatusSuccess
	}
	return st, nil
}

func (b *billedChain) GetLastNonce(context.Context, common.Address, common.Address) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (b *billedChain) ProviderAddress() common.Address                    { return b.provider }
func (b *billedChain) IsLocalTEEActiveNode(context.Context) (bool, error) { return true, nil }
func (b *billedChain) GetBalanceBatch(_ context.Context, users []common.Address, _ common.Address) ([]*big.Int, error) {
	out := make([]*big.Int, len(users))
	for i := range out {
		out[i] = new(big.Int).SetUint64(1e18)
	}
	return out, nil
}

// Driving the real Run loop, a collapsed batch must charge each queued entry
// exactly once.
//
// The unit tests around HandleStatuses feed the consumed count in directly, so
// they pass even when Run never threads it through — which is what happened:
// the post-broadcast pending-tx record was rebuilt by hand and dropped
// Consumed, so every collapsed settle popped by voucher count and left the
// surplus entries queued. The queue still empties eventually, because the loop
// keeps draining; what changes is that the same compute is billed several
// times over. Only a running fee total catches it.
func TestRun_CollapsedBatchBillsEachEntryOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prov := common.HexToAddress("0xPROV")
	ch := newBilledChain(prov)
	queueKey := "voucher:queue:" + prov.Hex()
	ctx := context.Background()

	// Twelve entries, three sandboxes, one user, 100 each: collapsing folds
	// them into three vouchers of 400, total 1200 either way.
	user := common.HexToAddress("0xAAA")
	want := big.NewInt(0)
	for i := 0; i < 4; i++ {
		for _, sb := range []string{"sb-alpha", "sb-beta", "sb-gamma"} {
			raw, err := json.Marshal(voucher.SandboxVoucher{
				SandboxID: sb, User: user, Provider: prov, TotalFee: big.NewInt(100),
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := rdb.RPush(ctx, queueKey, string(raw)).Err(); err != nil {
				t.Fatal(err)
			}
			want.Add(want, big.NewInt(100))
		}
	}

	cfg := &config.Config{Billing: config.BillingConfig{VoucherIntervalSec: 1}}
	runCtx, cancel := context.WithCancel(ctx)
	go Run(runCtx, cfg, rdb, ch, nopSigner{}, make(chan StopSignal, 64), alert.Nop{}, zap.NewNop())

	// Drain, then keep the loop running a little longer: a re-settle shows up
	// as extra billing on a subsequent pass, not on the first one.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		left, err := rdb.LLen(ctx, queueKey).Result()
		if err != nil {
			t.Fatal(err)
		}
		if left == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
	cancel()

	billed, submits, perBatch := ch.total()
	if submits == 0 {
		t.Fatal("nothing was submitted; the batch never reached the chain client")
	}
	if perBatch[0] != 3 {
		t.Errorf("first submission carried %d vouchers, want 3 (one per sandbox)", perBatch[0])
	}
	if billed.Cmp(want) != 0 {
		t.Errorf("billed %s, want %s — the surplus queue entries were settled again "+
			"and the user charged for the same compute twice (submissions=%v)",
			billed, want, perBatch)
	}
}
