package broker

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// ── Mocks ─────────────────────────────────────────────────────────────────────

type mockBalanceChecker struct {
	mu       sync.Mutex
	calls    []balanceBatchCall // recorded calls
	balances map[string][]*big.Int // provider hex → balances (ordered by users slice)
	unregistered map[string]bool   // provider hex → service removed on-chain
	err      error
}

type balanceBatchCall struct {
	users    []common.Address
	provider common.Address
}

func (m *mockBalanceChecker) GetBalanceBatch(_ context.Context, users []common.Address, provider common.Address) ([]*big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, balanceBatchCall{users: users, provider: provider})
	if m.err != nil {
		return nil, m.err
	}
	return m.balances[provider.Hex()], nil
}

// GetServicePricing: registered by default (non-nil price) so existing tests
// exercise the top-up path; set unregistered to simulate a rotated-out signer.
func (m *mockBalanceChecker) GetServicePricing(_ context.Context, provider common.Address) (*big.Int, *big.Int, *big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.unregistered[provider.Hex()] {
		return nil, nil, nil, nil
	}
	return big.NewInt(1), big.NewInt(1), big.NewInt(1), nil
}

func (m *mockBalanceChecker) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

type mockPaymentLayer struct {
	mu      sync.Mutex
	calls   []depositCall
	failFor map[string]bool // user hex → should fail
}

type depositCall struct {
	user     common.Address
	provider common.Address
	amount   *big.Int
}

func (m *mockPaymentLayer) RequestDeposit(_ context.Context, user, provider common.Address, amount *big.Int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, depositCall{user: user, provider: provider, amount: amount})
	if m.failFor[user.Hex()] {
		return errors.New("payment layer error")
	}
	return nil
}

func (m *mockPaymentLayer) depositCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

var (
	userA     = common.HexToAddress("0x000000000000000000000000000000000000000A")
	userB     = common.HexToAddress("0x000000000000000000000000000000000000000B")
	provider1 = common.HexToAddress("0x0000000000000000000000000000000000000001")
	provider2 = common.HexToAddress("0x0000000000000000000000000000000000000002")
)

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

func seedSession(t *testing.T, rdb *redis.Client, s SessionEntry) {
	t.Helper()
	data, _ := json.Marshal(s)
	rdb.Set(context.Background(), sessionPrefix+s.SandboxID, data, 0) //nolint:errcheck
}

func newMonitor(rdb *redis.Client, chain balanceChecker, payment PaymentLayer) *Monitor {
	return NewMonitor(rdb, chain, payment, zap.NewNop(), 300, 3, 2)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestCheck_emptySessions(t *testing.T) {
	mc := &mockBalanceChecker{}
	mp := &mockPaymentLayer{}
	mon := newMonitor(newTestRedis(t), mc, mp)
	mon.check(context.Background())

	if mc.callCount() != 0 {
		t.Errorf("GetBalanceBatch called %d times, want 0", mc.callCount())
	}
	if mp.depositCount() != 0 {
		t.Errorf("RequestDeposit called %d times, want 0", mp.depositCount())
	}
}

func TestCheck_aboveThreshold_noTopup(t *testing.T) {
	rdb := newTestRedis(t)
	// price_per_sec=100, interval=60 → threshold = 100×60×2 = 12000
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-1", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	mc := &mockBalanceChecker{
		balances: map[string][]*big.Int{
			provider1.Hex(): {big.NewInt(50_000)}, // well above 12000
		},
	}
	mp := &mockPaymentLayer{}
	mon := newMonitor(rdb, mc, mp)
	mon.check(context.Background())

	if mp.depositCount() != 0 {
		t.Errorf("RequestDeposit called %d times, want 0 (balance sufficient)", mp.depositCount())
	}
}

func TestCheck_belowThreshold_triggersTopup(t *testing.T) {
	rdb := newTestRedis(t)
	// price_per_sec=100, interval=60
	// threshold = 100×60×2 = 12000
	// topup     = 100×60×3 = 18000
	// deficit   = 18000 - 100 = 17900
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-1", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	mc := &mockBalanceChecker{
		balances: map[string][]*big.Int{
			provider1.Hex(): {big.NewInt(100)}, // below threshold
		},
	}
	mp := &mockPaymentLayer{}
	mon := newMonitor(rdb, mc, mp)
	mon.check(context.Background())

	if mp.depositCount() != 1 {
		t.Fatalf("RequestDeposit called %d times, want 1", mp.depositCount())
	}
	got := mp.calls[0].amount
	want := big.NewInt(17900)
	if got.Cmp(want) != 0 {
		t.Errorf("top-up amount = %s, want %s", got, want)
	}
	if mp.calls[0].user != userA {
		t.Errorf("user = %s, want %s", mp.calls[0].user.Hex(), userA.Hex())
	}
	if mp.calls[0].provider != provider1 {
		t.Errorf("provider = %s, want %s", mp.calls[0].provider.Hex(), provider1.Hex())
	}
}

// v2: provider = TEE signer. After rotation the owner removes the old
// signer's service; the monitor must stop topping up that bucket (deposits
// there can never settle and would strand user funds behind the refund flow).
func TestCheck_removedService_noTopup(t *testing.T) {
	rdb := newTestRedis(t)
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-1", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	mc := &mockBalanceChecker{
		unregistered: map[string]bool{provider1.Hex(): true},
		balances: map[string][]*big.Int{
			provider1.Hex(): {big.NewInt(0)}, // way below threshold
		},
	}
	mp := &mockPaymentLayer{}
	mon := newMonitor(rdb, mc, mp)
	mon.check(context.Background())

	if mp.depositCount() != 0 {
		t.Fatalf("RequestDeposit called %d times for a removed service, want 0", mp.depositCount())
	}
	if mc.callCount() != 0 {
		t.Errorf("GetBalanceBatch called %d times for a removed service, want 0", mc.callCount())
	}
}

func TestCheck_aggregatesMultipleSandboxes(t *testing.T) {
	rdb := newTestRedis(t)
	// Same (userA, provider1), two sandboxes: 100 + 200 = 300/sec
	// threshold = 300×60×2 = 36000
	// topup     = 300×60×3 = 54000
	// deficit   = 54000 - 1000 = 53000
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-1", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-2", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "200", VoucherIntervalSec: 60,
	})
	mc := &mockBalanceChecker{
		balances: map[string][]*big.Int{
			provider1.Hex(): {big.NewInt(1000)}, // below threshold
		},
	}
	mp := &mockPaymentLayer{}
	mon := newMonitor(rdb, mc, mp)
	mon.check(context.Background())

	if mp.depositCount() != 1 {
		t.Fatalf("RequestDeposit called %d times, want 1", mp.depositCount())
	}
	want := big.NewInt(53000)
	if mp.calls[0].amount.Cmp(want) != 0 {
		t.Errorf("aggregated top-up = %s, want %s", mp.calls[0].amount, want)
	}
}

func TestCheck_groupsByProvider(t *testing.T) {
	rdb := newTestRedis(t)
	// userA → provider1, userB → provider2; both below threshold
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-1", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-2", User: userB.Hex(), Provider: provider2.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	mc := &mockBalanceChecker{
		balances: map[string][]*big.Int{
			provider1.Hex(): {big.NewInt(0)},
			provider2.Hex(): {big.NewInt(0)},
		},
	}
	mp := &mockPaymentLayer{}
	mon := newMonitor(rdb, mc, mp)
	mon.check(context.Background())

	// One GetBalanceBatch call per provider.
	if mc.callCount() != 2 {
		t.Errorf("GetBalanceBatch called %d times, want 2 (one per provider)", mc.callCount())
	}
	// Both users topped up.
	if mp.depositCount() != 2 {
		t.Errorf("RequestDeposit called %d times, want 2", mp.depositCount())
	}
}

func TestCheck_paymentFailureIsolated(t *testing.T) {
	rdb := newTestRedis(t)
	// Both userA and userB are below threshold under provider1.
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-1", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-2", User: userB.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	mc := &mockBalanceChecker{
		balances: map[string][]*big.Int{
			provider1.Hex(): {big.NewInt(0), big.NewInt(0)},
		},
	}
	// userA's payment will fail; userB's should still be attempted.
	mp := &mockPaymentLayer{failFor: map[string]bool{userA.Hex(): true}}
	mon := newMonitor(rdb, mc, mp)
	mon.check(context.Background())

	// Both RequestDeposit calls must have been attempted despite the first failure.
	if mp.depositCount() != 2 {
		t.Errorf("RequestDeposit called %d times, want 2 (failure must not skip others)", mp.depositCount())
	}
}

func TestGetSessions_returnsAll(t *testing.T) {
	rdb := newTestRedis(t)
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-1", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60, RegisteredAt: time.Now(),
	})
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-2", User: userB.Hex(), Provider: provider2.Hex(),
		PricePerSec: "200", VoucherIntervalSec: 60, RegisteredAt: time.Now(),
	})
	mon := newMonitor(rdb, &mockBalanceChecker{}, &mockPaymentLayer{})
	sessions := mon.GetSessions(context.Background())
	if len(sessions) != 2 {
		t.Errorf("GetSessions returned %d sessions, want 2", len(sessions))
	}
}

func TestNoopPaymentLayer_returnsNil(t *testing.T) {
	noop := NewNoopPaymentLayer(zap.NewNop())
	err := noop.RequestDeposit(context.Background(), userA, provider1, big.NewInt(12345))
	if err != nil {
		t.Errorf("NoopPaymentLayer.RequestDeposit returned error: %v", err)
	}
}

// ── Monitor.Run integration tests ─────────────────────────────────────────────

// TestMonitorRun_firesImmediatelyThenPeriodic verifies that Monitor.Run:
//  1. Fires check() immediately on startup (without waiting for the first tick).
//  2. Fires check() again after the configured interval has elapsed.
func TestMonitorRun_firesImmediatelyThenPeriodic(t *testing.T) {
	rdb := newTestRedis(t)
	// price=100/sec, interval=60 → threshold=12000, topup=18000; balance=0 → deficit=18000
	seedSession(t, rdb, SessionEntry{
		SandboxID: "sb-run", User: userA.Hex(), Provider: provider1.Hex(),
		PricePerSec: "100", VoucherIntervalSec: 60,
	})
	mc := &mockBalanceChecker{
		balances: map[string][]*big.Int{
			provider1.Hex(): {big.NewInt(0)},
		},
	}
	mp := &mockPaymentLayer{}
	// monitorIntervalSec=1 so the periodic tick fires quickly in tests.
	mon := NewMonitor(rdb, mc, mp, zap.NewNop(), 1, 3, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mon.Run(ctx)

	// 1. Immediate check: should trigger within 500 ms.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if mp.depositCount() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mp.depositCount() < 1 {
		t.Fatalf("Monitor.Run: no top-up after immediate check (waited 2s)")
	}
	t.Logf("immediate check fired: %d deposit(s)", mp.depositCount())

	// 2. Periodic check: interval=1s, allow up to 3s for at least one more tick.
	firstCount := mp.depositCount()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mp.depositCount() > firstCount {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mp.depositCount() <= firstCount {
		t.Errorf("Monitor.Run: periodic check did not fire within 3s (count stayed at %d)", firstCount)
	}
	t.Logf("periodic check fired: total %d deposit(s)", mp.depositCount())
}
