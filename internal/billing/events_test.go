package billing

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox-billing/internal/voucher"
)

// ── Mock signer ───────────────────────────────────────────────────────────────

type mockSigner struct {
	mu       sync.Mutex
	vouchers []*voucher.SandboxVoucher
	nonce    int64
	incrErr  error
	enqErr   error
}

func (m *mockSigner) IncrNonce(_ context.Context, _, _ string) (*big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.incrErr != nil {
		return nil, m.incrErr
	}
	m.nonce++
	return big.NewInt(m.nonce), nil
}

func (m *mockSigner) SignAndEnqueue(_ context.Context, v *voucher.SandboxVoucher) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enqErr != nil {
		return m.enqErr
	}
	cp := *v
	m.vouchers = append(m.vouchers, &cp)
	return nil
}

func (m *mockSigner) last() *voucher.SandboxVoucher {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.vouchers) == 0 {
		return nil
	}
	return m.vouchers[len(m.vouchers)-1]
}

func (m *mockSigner) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.vouchers)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

const (
	testProvider   = "0x1111111111111111111111111111111111111111"
	testOwner      = "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testSandbox    = "sb-events-001"
	pricePerSec    = int64(100) // 100 neuron/sec
	createFeeVal   = int64(500) // 500 neuron flat create fee
)

func newEventHandler(ms *mockSigner, rdb interface {
	// accepts the *redis.Client from session_test.go helpers
}) *EventHandler {
	panic("use newEventHandlerWithRedis")
}

func newTestHandler(t *testing.T, ms *mockSigner) (*EventHandler, func(sandboxID string) (*Session, error)) {
	t.Helper()
	rdb, _ := newTestRedis(t)
	ctx := context.Background()
	// zero unit prices → computePrice falls back to flat pricePerSec
	h := NewEventHandler(
		rdb,
		testProvider,
		big.NewInt(pricePerSec),
		big.NewInt(createFeeVal),
		new(big.Int), // pricePerCPUPerSec = 0
		new(big.Int), // pricePerMemGBPerSec = 0
		ms,
		zap.NewNop(),
	)
	get := func(id string) (*Session, error) { return GetSession(ctx, rdb, id) }
	return h, get
}

// ── OnCreate ─────────────────────────────────────────────────────────────────

func TestOnCreate_EmitsVoucher(t *testing.T) {
	ms := &mockSigner{}
	h, get := newTestHandler(t, ms)
	ctx := context.Background()

	before := time.Now().Unix()
	h.OnCreate(ctx, testSandbox, testOwner, 1, 1)
	after := time.Now().Unix()

	v := ms.last()
	if v == nil {
		t.Fatal("expected a voucher, got none")
	}
	if v.SandboxID != testSandbox {
		t.Errorf("SandboxID: got %q want %q", v.SandboxID, testSandbox)
	}
	if v.TotalFee.Int64() != createFeeVal {
		t.Errorf("TotalFee: got %d want %d", v.TotalFee.Int64(), createFeeVal)
	}
	if v.Nonce.Int64() != 1 {
		t.Errorf("Nonce: got %d want 1", v.Nonce.Int64())
	}
	// User and Provider addresses must be set
	zeroAddr := "0x0000000000000000000000000000000000000000"
	if v.User.Hex() == zeroAddr {
		t.Error("User address is zero")
	}
	if v.Provider.Hex() == zeroAddr {
		t.Error("Provider address is zero")
	}
	// OnCreate must also open a compute session immediately.
	sess, err := get(testSandbox)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected compute session after OnCreate, got nil")
	}
	if sess.Owner != testOwner {
		t.Errorf("session Owner: got %q want %q", sess.Owner, testOwner)
	}
	if sess.StartTime < before || sess.StartTime > after {
		t.Errorf("session StartTime %d not in [%d, %d]", sess.StartTime, before, after)
	}
}

func TestOnCreate_MonotonicallyIncrementsNonce(t *testing.T) {
	ms := &mockSigner{}
	h, _ := newTestHandler(t, ms)
	ctx := context.Background()

	h.OnCreate(ctx, "sb-a", testOwner, 1, 1)
	h.OnCreate(ctx, "sb-b", testOwner, 1, 1)

	if ms.count() != 2 {
		t.Fatalf("expected 2 vouchers, got %d", ms.count())
	}
	n0 := ms.vouchers[0].Nonce.Int64()
	n1 := ms.vouchers[1].Nonce.Int64()
	if n1 != n0+1 {
		t.Errorf("nonces not monotone: %d, %d", n0, n1)
	}
}

func TestOnCreate_IncrNonceError_NoVoucher(t *testing.T) {
	ms := &mockSigner{incrErr: errors.New("nonce service down")}
	h, _ := newTestHandler(t, ms)

	h.OnCreate(context.Background(), testSandbox, testOwner, 1, 1)

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers on nonce error, got %d", ms.count())
	}
}

func TestOnCreate_SignEnqueueError_NoSessionCreated(t *testing.T) {
	ms := &mockSigner{enqErr: errors.New("redis down")}
	h, _ := newTestHandler(t, ms)

	// Should not panic
	h.OnCreate(context.Background(), testSandbox, testOwner, 1, 1)
}

// ── OnStart ───────────────────────────────────────────────────────────────────

func TestOnStart_CreatesSession(t *testing.T) {
	ms := &mockSigner{}
	h, get := newTestHandler(t, ms)
	ctx := context.Background()

	before := time.Now().Unix()
	h.OnStart(ctx, testSandbox, testOwner, 1, 1)
	after := time.Now().Unix()

	sess, err := get(testSandbox)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.SandboxID != testSandbox {
		t.Errorf("SandboxID: got %q want %q", sess.SandboxID, testSandbox)
	}
	if sess.Owner != testOwner {
		t.Errorf("Owner: got %q want %q", sess.Owner, testOwner)
	}
	if sess.Provider != testProvider {
		t.Errorf("Provider: got %q want %q", sess.Provider, testProvider)
	}
	if sess.StartTime < before || sess.StartTime > after {
		t.Errorf("StartTime %d not in [%d, %d]", sess.StartTime, before, after)
	}
	if sess.LastVoucherAt < before || sess.LastVoucherAt > after {
		t.Errorf("LastVoucherAt %d not in [%d, %d]", sess.LastVoucherAt, before, after)
	}
	// OnStart should not emit any voucher
	if ms.count() != 0 {
		t.Errorf("OnStart must not emit vouchers, got %d", ms.count())
	}
}

func TestOnStart_IdempotentIfSessionExists(t *testing.T) {
	ms := &mockSigner{}
	h, get := newTestHandler(t, ms)
	ctx := context.Background()

	h.OnStart(ctx, testSandbox, testOwner, 1, 1)
	sess1, _ := get(testSandbox)
	if sess1 == nil {
		t.Fatal("expected session after first OnStart")
	}
	origStart := sess1.StartTime

	time.Sleep(2 * time.Millisecond) // ensure clock would differ if overwritten
	h.OnStart(ctx, testSandbox, testOwner, 1, 1)

	sess2, _ := get(testSandbox)
	if sess2 == nil {
		t.Fatal("expected session still present after second OnStart")
	}
	// StartTime must not be reset — OnStart is a no-op when session exists.
	if sess2.StartTime != origStart {
		t.Errorf("OnStart overwrote existing session StartTime: got %d want %d",
			sess2.StartTime, origStart)
	}
}

// ── OnStop ────────────────────────────────────────────────────────────────────

func TestOnStop_NoSession_NoVoucher(t *testing.T) {
	ms := &mockSigner{}
	h, _ := newTestHandler(t, ms)

	// No session exists; should be a no-op for vouchers
	h.OnStop(context.Background(), "sb-nonexistent")

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers for missing session, got %d", ms.count())
	}
}

func TestOnStop_EmitsFinalVoucher(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal), new(big.Int), new(big.Int), ms, zap.NewNop())
	ctx := context.Background()

	// Session started 180 seconds ago
	periodStart := time.Now().Unix() - 180
	err := CreateSession(ctx, rdb, Session{
		SandboxID:     testSandbox,
		Owner:         testOwner,
		Provider:      testProvider,
		StartTime:     periodStart,
		LastVoucherAt: periodStart,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	h.OnStop(ctx, testSandbox)

	v := ms.last()
	if v == nil {
		t.Fatal("expected final voucher, got none")
	}
	if v.SandboxID != testSandbox {
		t.Errorf("SandboxID: got %q want %q", v.SandboxID, testSandbox)
	}
	// 180 seconds × 100 neuron/sec = 18000 neuron
	if v.TotalFee.Int64() != 18000 {
		t.Errorf("TotalFee: got %d want 18000", v.TotalFee.Int64())
	}
}

func TestOnStop_DeletesSession(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal), new(big.Int), new(big.Int), ms, zap.NewNop())
	ctx := context.Background()

	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		StartTime: time.Now().Unix() - 120, LastVoucherAt: time.Now().Unix() - 120,
	})

	h.OnStop(ctx, testSandbox)

	sess, err := GetSession(ctx, rdb, testSandbox)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Error("session should be deleted after OnStop")
	}
}

func TestOnStop_ZeroMinutes_NoVoucher(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal), new(big.Int), new(big.Int), ms, zap.NewNop())
	ctx := context.Background()

	// LastVoucherAt = now → elapsed ≈ 0 s → no voucher
	now := time.Now().Unix()
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		StartTime: now, LastVoucherAt: now,
	})

	h.OnStop(ctx, testSandbox)

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers for sub-minute session, got %d", ms.count())
	}
	// Session must still be deleted
	sess, _ := GetSession(ctx, rdb, testSandbox)
	if sess != nil {
		t.Error("session should be deleted even when computeMinutes==0")
	}
}

func TestOnStop_SignEnqueueError_StillDeletesSession(t *testing.T) {
	ms := &mockSigner{enqErr: errors.New("enqueue failed")}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal), new(big.Int), new(big.Int), ms, zap.NewNop())
	ctx := context.Background()

	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		StartTime: time.Now().Unix() - 120, LastVoucherAt: time.Now().Unix() - 120,
	})

	h.OnStop(ctx, testSandbox) // enqueue will fail, but delete must still happen

	sess, _ := GetSession(ctx, rdb, testSandbox)
	if sess != nil {
		t.Error("session must be deleted even when SignAndEnqueue errors")
	}
}

// ── OnDelete ──────────────────────────────────────────────────────────────────

func TestOnDelete_BehavesLikeOnStop(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal), new(big.Int), new(big.Int), ms, zap.NewNop())
	ctx := context.Background()

	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		StartTime: time.Now().Unix() - 120, LastVoucherAt: time.Now().Unix() - 120,
	})

	h.OnDelete(ctx, testSandbox)

	// Voucher emitted
	if ms.count() != 1 {
		t.Errorf("expected 1 voucher, got %d", ms.count())
	}
	// Session deleted
	sess, _ := GetSession(ctx, rdb, testSandbox)
	if sess != nil {
		t.Error("session should be deleted after OnDelete")
	}
}

// ── OnArchive ─────────────────────────────────────────────────────────────────

func TestOnArchive_DelegatesToDelete(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal), new(big.Int), new(big.Int), ms, zap.NewNop())
	ctx := context.Background()

	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		StartTime: time.Now().Unix() - 60, LastVoucherAt: time.Now().Unix() - 60,
	})

	h.OnArchive(ctx, testSandbox)

	// Must emit voucher and delete session, same as OnStop/OnDelete
	if ms.count() != 1 {
		t.Errorf("expected 1 voucher from OnArchive, got %d", ms.count())
	}
	sess, _ := GetSession(ctx, rdb, testSandbox)
	if sess != nil {
		t.Error("session should be deleted after OnArchive")
	}
}
