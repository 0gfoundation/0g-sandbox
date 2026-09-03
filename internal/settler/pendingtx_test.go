package settler

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
	"math/big"
	"time"
)

type mockFateResolver struct {
	fates     []chain.TxFate // consumed per call; last repeats
	receipt   *types.Receipt
	statuses  []chain.SettlementStatus
	lastNonce *big.Int
	calls     int
}

func (m *mockFateResolver) ResolveTxFate(context.Context, common.Hash, uint64) (chain.TxFate, *types.Receipt, error) {
	f := m.fates[min(m.calls, len(m.fates)-1)]
	m.calls++
	if f == chain.TxMined {
		return f, m.receipt, nil
	}
	return f, nil, nil
}

func (m *mockFateResolver) SettleStatusesFromReceipt(context.Context, *types.Receipt, []voucher.SandboxVoucher) ([]chain.SettlementStatus, error) {
	return m.statuses, nil
}

func (m *mockFateResolver) GetLastNonce(context.Context, common.Address, common.Address) (*big.Int, error) {
	if m.lastNonce == nil {
		return big.NewInt(0), nil
	}
	return m.lastNonce, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func pendingFixture(t *testing.T) (*redis.Client, common.Address, pendingTx, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	provider := common.HexToAddress("0xP")
	v := voucher.SandboxVoucher{SandboxID: "sb-1", User: common.HexToAddress("0xU"), Provider: provider, TotalFee: big.NewInt(5), Nonce: big.NewInt(7)}
	raw := `{"sandbox_id":"sb-1"}`
	p := pendingTx{TxHash: common.HexToHash("0xabc"), AccountNonce: 3, Vouchers: []voucher.SandboxVoucher{v}, FirstItem: raw}
	return rdb, provider, p, "q"
}

// Finding #23/#75: a mined pending tx must apply its receipt's statuses —
// NEVER be re-queued for a re-sign (the double-charge path).
func TestResolvePendingTx_Mined_AppliesStatuses(t *testing.T) {
	old := pendingTxPollInterval
	pendingTxPollInterval = time.Millisecond
	t.Cleanup(func() { pendingTxPollInterval = old })
	rdb, provider, p, queueKey := pendingFixture(t)
	if err := savePendingTx(context.Background(), rdb, provider, p); err != nil {
		t.Fatal(err)
	}
	resolver := &mockFateResolver{
		fates:    []chain.TxFate{chain.TxPending, chain.TxMined}, // one wait cycle, then mined
		receipt:  &types.Receipt{Status: 1, TxHash: p.TxHash},
		statuses: []chain.SettlementStatus{chain.StatusSuccess},
	}
	stopCh := make(chan StopSignal, 1)
	got := resolvePendingTx(context.Background(), rdb, resolver, queueKey, stopCh, provider, &p, alert.Nop{}, zap.NewNop())

	if len(got) != 1 || got[0] != chain.StatusSuccess {
		t.Fatalf("statuses = %v", got)
	}
	if n, _ := rdb.LLen(context.Background(), queueKey).Result(); n != 0 {
		t.Errorf("mined tx must not re-queue vouchers, queue len %d", n)
	}
	if p2, _ := loadPendingTx(context.Background(), rdb, provider); p2 != nil {
		t.Error("pending record must be cleared after resolution")
	}
}

// A provably-dropped tx re-queues the BLPOP'd item so the next round re-signs.
func TestResolvePendingTx_Dropped_Requeues(t *testing.T) {
	rdb, provider, p, queueKey := pendingFixture(t)
	savePendingTx(context.Background(), rdb, provider, p) //nolint:errcheck
	resolver := &mockFateResolver{fates: []chain.TxFate{chain.TxDropped}}
	got := resolvePendingTx(context.Background(), rdb, resolver, queueKey, make(chan StopSignal, 1), provider, &p, alert.Nop{}, zap.NewNop())

	if got != nil {
		t.Fatalf("dropped tx must return nil statuses, got %v", got)
	}
	items, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	if len(items) != 1 || items[0] != p.FirstItem {
		t.Errorf("dropped tx must re-queue the popped item, queue: %v", items)
	}
	if p2, _ := loadPendingTx(context.Background(), rdb, provider); p2 != nil {
		t.Error("pending record must be cleared")
	}
}

// Persistence roundtrip: the record survives (crash-restart shape).
func TestPendingTx_SaveLoadClear(t *testing.T) {
	rdb, provider, p, _ := pendingFixture(t)
	ctx := context.Background()
	if err := savePendingTx(ctx, rdb, provider, p); err != nil {
		t.Fatal(err)
	}
	got, err := loadPendingTx(ctx, rdb, provider)
	if err != nil || got == nil {
		t.Fatalf("load: %v %v", got, err)
	}
	if got.TxHash != p.TxHash || got.AccountNonce != 3 || len(got.Vouchers) != 1 || got.Vouchers[0].Nonce.Int64() != 7 {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
	clearPendingTx(ctx, rdb, provider)
	if got, _ := loadPendingTx(ctx, rdb, provider); got != nil {
		t.Error("clear failed")
	}
}

// Crash between intent write and broadcast-result write: a hashless record.
// Chain nonce says the voucher DID settle → must be dropped, not re-queued.
func TestReconcileIntent_SettledOnChain_Drops(t *testing.T) {
	rdb, provider, p, queueKey := pendingFixture(t)
	p.TxHash = common.Hash{}                                // intent shape
	resolver := &mockFateResolver{lastNonce: big.NewInt(7)} // voucher nonce is 7 → consumed
	reconcileIntent(context.Background(), rdb, resolver, queueKey, provider, &p, zap.NewNop())

	if n, _ := rdb.LLen(context.Background(), queueKey).Result(); n != 0 {
		t.Errorf("settled voucher must not re-queue, queue len %d", n)
	}
	if got, _ := loadPendingTx(context.Background(), rdb, provider); got != nil {
		t.Error("record must be cleared")
	}
}

// Same shape, but the chain shows the nonce unconsumed → the tx never went
// out (or never mined and can't now) → re-queue for a fresh sign.
func TestReconcileIntent_NotOnChain_Requeues(t *testing.T) {
	rdb, provider, p, queueKey := pendingFixture(t)
	p.TxHash = common.Hash{}
	resolver := &mockFateResolver{lastNonce: big.NewInt(3)} // < voucher nonce 7
	reconcileIntent(context.Background(), rdb, resolver, queueKey, provider, &p, zap.NewNop())

	items, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	if len(items) != 1 || items[0] != p.FirstItem {
		t.Errorf("unconsumed intent must re-queue, queue: %v", items)
	}
	if got, _ := loadPendingTx(context.Background(), rdb, provider); got != nil {
		t.Error("record must be cleared")
	}
}
