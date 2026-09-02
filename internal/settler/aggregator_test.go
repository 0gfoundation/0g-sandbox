package settler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

type mockAggChain struct {
	provider common.Address
	bal      map[common.Address]*big.Int
	calls    int
}

func (m *mockAggChain) ProviderAddress() common.Address { return m.provider }
func (m *mockAggChain) GetBalanceBatch(_ context.Context, users []common.Address, _ common.Address) ([]*big.Int, error) {
	m.calls++
	out := make([]*big.Int, len(users))
	for i, u := range users {
		if b, ok := m.bal[u]; ok {
			out[i] = b
		} else {
			out[i] = new(big.Int)
		}
	}
	return out, nil
}

func pushVoucher(t *testing.T, rdb *redis.Client, queueKey, sandbox string, user, prov common.Address, fee int64) {
	t.Helper()
	v := voucher.SandboxVoucher{SandboxID: sandbox, User: user, Provider: prov, TotalFee: big.NewInt(fee)}
	raw, _ := json.Marshal(v)
	rdb.RPush(context.Background(), queueKey, string(raw))
}

func aggSetup(t *testing.T) (*redis.Client, *miniredis.Miniredis, common.Address, string) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prov := common.HexToAddress("0xBBB")
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, prov.Hex())
	return rdb, mr, prov, queueKey
}

func TestSweepUsers_SplitsAndStops(t *testing.T) {
	rdb, _, prov, queueKey := aggSetup(t)
	poor := common.HexToAddress("0xA11CE")
	rich := common.HexToAddress("0xB0B")

	// poor: 3×100, balance 150 → covers 1, holds 2 (two distinct sandboxes)
	pushVoucher(t, rdb, queueKey, "poor-sb-1", poor, prov, 100)
	pushVoucher(t, rdb, queueKey, "poor-sb-1", poor, prov, 100)
	pushVoucher(t, rdb, queueKey, "poor-sb-2", poor, prov, 100)
	// rich: 2×100, balance 1000 → covers all, holds 0
	pushVoucher(t, rdb, queueKey, "rich-sb", rich, prov, 100)
	pushVoucher(t, rdb, queueKey, "rich-sb", rich, prov, 100)

	chainMock := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{
		poor: big.NewInt(150),
		rich: big.NewInt(1000),
	}}
	stopCh := make(chan StopSignal, 16)

	sweepUsers(context.Background(), rdb, chainMock, queueKey, stopCh, []common.Address{poor, rich}, zap.NewNop())

	poorHeld := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(poor.Hex()), strings.ToLower(prov.Hex()))
	if n, _ := rdb.LLen(context.Background(), poorHeld).Result(); n != 2 {
		t.Errorf("poor held: got %d want 2", n)
	}
	richHeld := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(rich.Hex()), strings.ToLower(prov.Hex()))
	if n, _ := rdb.LLen(context.Background(), richHeld).Result(); n != 0 {
		t.Errorf("rich held: got %d want 0", n)
	}
	// held-users index: poor in, rich out.
	users, err := voucher.HeldUsers(context.Background(), rdb, prov)
	if err != nil || len(users) != 1 || users[0] != poor {
		t.Errorf("held-users index: %v err=%v want [poor]", users, err)
	}
	close(stopCh)
	stopped := map[string]bool{}
	for s := range stopCh {
		stopped[s.SandboxID] = true
	}
	if stopped["rich-sb"] {
		t.Error("rich sandbox must not be stopped")
	}
	if !stopped["poor-sb-2"] {
		t.Errorf("poor's held sandbox not stopped; stopped=%v", stopped)
	}
}

// Steady state: small queue, no held debt → maybeSweep must do nothing at all —
// no balance call, queue byte-identical (vouchers keep per-sandbox identity).
func TestMaybeSweep_SteadyStateUntouched(t *testing.T) {
	rdb, _, prov, queueKey := aggSetup(t)
	u := common.HexToAddress("0xAAA")
	pushVoucher(t, rdb, queueKey, "sb-1", u, prov, 100)
	pushVoucher(t, rdb, queueKey, "sb-1", u, prov, 100)
	before, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()

	chainMock := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{u: big.NewInt(10_000)}}
	stopCh := make(chan StopSignal, 4)
	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, map[common.Address]*big.Int{}, zap.NewNop())

	if chainMock.calls != 0 {
		t.Errorf("steady state must not read balances; got %d calls", chainMock.calls)
	}
	after, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	if len(after) != len(before) {
		t.Fatalf("queue changed in steady state: %d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("queue item %d rewritten in steady state", i)
		}
	}
}

// Deep backlog trips the LLEN guard: everything aggregates/held-parks.
func TestMaybeSweep_BacklogTriggers(t *testing.T) {
	rdb, _, prov, queueKey := aggSetup(t)
	u := common.HexToAddress("0xAAA")
	n := backlogSweepThreshold + 20
	for i := 0; i < n; i++ {
		pushVoucher(t, rdb, queueKey, "sb-1", u, prov, 100)
	}
	// balance covers half the backlog
	chainMock := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{u: big.NewInt(int64(n/2) * 100)}}
	stopCh := make(chan StopSignal, 4)
	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, map[common.Address]*big.Int{}, zap.NewNop())

	if chainMock.calls != 1 {
		t.Errorf("expected one balance batch call, got %d", chainMock.calls)
	}
	qlen, _ := rdb.LLen(context.Background(), queueKey).Result()
	if qlen != 1 { // one aggregate
		t.Errorf("queue after sweep: %d want 1 aggregate", qlen)
	}
	heldKey := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(u.Hex()), strings.ToLower(prov.Hex()))
	if hl, _ := rdb.LLen(context.Background(), heldKey).Result(); hl != int64(n-n/2) {
		t.Errorf("held: %d want %d", hl, n-n/2)
	}
}

// Held debt + top-up: SCARD guard fires even with an empty queue, and the
// now-affordable debt is reclaimed onto the queue as one aggregate.
func TestMaybeSweep_ReclaimAfterTopUp(t *testing.T) {
	rdb, _, prov, queueKey := aggSetup(t)
	u := common.HexToAddress("0xAAA")
	for i := 0; i < 3; i++ {
		pushVoucher(t, rdb, queueKey, "sb-1", u, prov, 100)
	}
	// Broke: park everything.
	chainMock := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{u: big.NewInt(0)}}
	stopCh := make(chan StopSignal, 4)
	sweepUsers(context.Background(), rdb, chainMock, queueKey, stopCh, []common.Address{u}, zap.NewNop())
	if qlen, _ := rdb.LLen(context.Background(), queueKey).Result(); qlen != 0 {
		t.Fatalf("queue should be empty after parking, got %d", qlen)
	}

	// Top-up: maybeSweep (queue empty, held-users non-empty) reclaims all 3.
	chainMock.bal[u] = big.NewInt(1000)
	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, map[common.Address]*big.Int{}, zap.NewNop())

	items, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	if len(items) != 1 {
		t.Fatalf("queue after reclaim: %d want 1 aggregate", len(items))
	}
	var agg voucher.SandboxVoucher
	if err := json.Unmarshal([]byte(items[0]), &agg); err != nil || !agg.IsAggregated() || agg.TotalFee.String() != "300" {
		t.Errorf("reclaimed aggregate: %+v err=%v want fee 300", agg, err)
	}
	// Debt cleared → index empty.
	if users, _ := voucher.HeldUsers(context.Background(), rdb, prov); len(users) != 0 {
		t.Errorf("held-users index should be empty, got %v", users)
	}
}

// Anti-churn: a held-only user whose balance has not changed must be skipped
// entirely on subsequent sweeps — one balance read, zero queue/held rewrites —
// instead of oscillating between equivalent partitions every interval.
func TestMaybeSweep_HeldUnchangedBalanceSkipped(t *testing.T) {
	rdb, _, prov, queueKey := aggSetup(t)
	u := common.HexToAddress("0xAAA")
	for i := 0; i < 4; i++ {
		pushVoucher(t, rdb, queueKey, "sb-1", u, prov, 100)
	}
	chainMock := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{u: big.NewInt(250)}}
	stopCh := make(chan StopSignal, 4)
	lastBal := map[common.Address]*big.Int{}

	// Pass 1: splits (covered 2, held 2).
	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, lastBal, zap.NewNop())
	// held-users guard keeps firing, so pass 2 runs — but must be a no-op.
	heldKey := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(u.Hex()), strings.ToLower(prov.Hex()))
	qBefore, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	hBefore, _ := rdb.LRange(context.Background(), heldKey, 0, -1).Result()

	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, lastBal, zap.NewNop())

	qAfter, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	hAfter, _ := rdb.LRange(context.Background(), heldKey, 0, -1).Result()
	if fmt.Sprint(qBefore) != fmt.Sprint(qAfter) || fmt.Sprint(hBefore) != fmt.Sprint(hAfter) {
		t.Fatal("unchanged balance must not rewrite queue or held list")
	}

	// Top-up: balance change re-enables the sweep and reclaims everything.
	chainMock.bal[u] = big.NewInt(1000)
	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, lastBal, zap.NewNop())
	if n, _ := rdb.LLen(context.Background(), heldKey).Result(); n != 0 {
		t.Errorf("after top-up held should be reclaimed, got %d", n)
	}
}
