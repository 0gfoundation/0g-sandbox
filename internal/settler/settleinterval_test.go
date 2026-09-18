package settler

import (
	"context"
	"encoding/json"
	"math/big"
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

// batchChain records how many submissions happened and how many vouchers each
// carried — the two numbers the batching gate exists to change.
type batchChain struct {
	provider common.Address
	submits  []int // vouchers per submission
}

func (b *batchChain) SubmitSettleFees(_ context.Context, vs []voucher.SandboxVoucher) (*types.Transaction, error) {
	b.submits = append(b.submits, len(vs))
	return types.NewTx(&types.LegacyTx{Nonce: uint64(len(b.submits))}), nil
}
func (b *batchChain) ResolveTxFate(context.Context, common.Hash, uint64) (chain.TxFate, *types.Receipt, error) {
	return chain.TxMined, &types.Receipt{Status: 1}, nil
}
func (b *batchChain) SettleStatusesFromReceipt(_ context.Context, _ *types.Receipt, vs []voucher.SandboxVoucher) ([]chain.SettlementStatus, error) {
	st := make([]chain.SettlementStatus, len(vs))
	for i := range st {
		st[i] = chain.StatusSuccess
	}
	return st, nil
}
func (b *batchChain) GetLastNonce(context.Context, common.Address, common.Address) (*big.Int, error) {
	return big.NewInt(0), nil
}
func (b *batchChain) ProviderAddress() common.Address                    { return b.provider }
func (b *batchChain) IsLocalTEEActiveNode(context.Context) (bool, error) { return true, nil }
func (b *batchChain) GetBalanceBatch(_ context.Context, users []common.Address, _ common.Address) ([]*big.Int, error) {
	out := make([]*big.Int, len(users))
	for i := range out {
		out[i] = new(big.Int).SetUint64(1e18) // everyone can pay; no sweep churn
	}
	return out, nil
}

func queueVouchers(t *testing.T, rdb *redis.Client, provider common.Address, n int) {
	t.Helper()
	key := "voucher:queue:" + provider.Hex()
	for i := 0; i < n; i++ {
		v := voucher.SandboxVoucher{
			SandboxID: "sb", User: common.HexToAddress("0xAAA"), Provider: provider,
			TotalFee: big.NewInt(1),
		}
		raw, _ := json.Marshal(v)
		rdb.RPush(context.Background(), key, string(raw))
	}
}

// #121: with SettleIntervalSec set, vouchers that arrive inside the window are
// batched into ONE submission instead of one transaction each.
func TestSettleInterval_BatchesWithinWindow(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prov := common.HexToAddress("0xPROV")
	ch := &batchChain{provider: prov}

	// accounting 1s, settlement 5s
	cfg := &config.Config{Billing: config.BillingConfig{VoucherIntervalSec: 1, SettleIntervalSec: 5}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, cfg, rdb, ch, nopSigner{}, make(chan StopSignal, 8), alert.Nop{}, zap.NewNop())

	// First batch goes out immediately (lastSubmit zero), then the gate holds.
	time.Sleep(300 * time.Millisecond)
	before := len(ch.submits)

	// Six vouchers trickle in over ~3s — all inside the 5s settle window.
	for i := 0; i < 6; i++ {
		queueVouchers(t, rdb, prov, 1)
		time.Sleep(500 * time.Millisecond)
	}
	added := len(ch.submits) - before
	if added > 1 {
		t.Fatalf("vouchers arriving inside the settle window must not each trigger a submission; got %d submissions (%v)", added, ch.submits)
	}
}

// Unset SettleIntervalSec must behave exactly as before (submit on arrival).
func TestSettleInterval_UnsetKeepsLegacyCadence(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prov := common.HexToAddress("0xPROV")
	ch := &batchChain{provider: prov}

	// SettleIntervalSec unset → falls back to VoucherIntervalSec (1s)
	cfg := &config.Config{Billing: config.BillingConfig{VoucherIntervalSec: 1}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Run(ctx, cfg, rdb, ch, nopSigner{}, make(chan StopSignal, 8), alert.Nop{}, zap.NewNop())

	time.Sleep(300 * time.Millisecond)
	before := len(ch.submits)
	for i := 0; i < 3; i++ {
		queueVouchers(t, rdb, prov, 1)
		time.Sleep(1200 * time.Millisecond) // > 1s window each time
	}
	if added := len(ch.submits) - before; added < 2 {
		t.Fatalf("with the interval unset each arrival past the window should submit; got %d (%v)", added, ch.submits)
	}
}
