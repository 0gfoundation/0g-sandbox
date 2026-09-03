package billing

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// ── Mock signer ───────────────────────────────────────────────────────────────

type mockSigner struct {
	mu       sync.Mutex
	vouchers []*voucher.SandboxVoucher
	enqErr   error
}

func (m *mockSigner) Enqueue(_ context.Context, v *voucher.SandboxVoucher) error {
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
	testProvider    = "0x1111111111111111111111111111111111111111"
	testOwner       = "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testSandbox     = "sb-events-001"
	pricePerSec     = int64(100) // 100 neuron/sec
	createFeeVal    = int64(500) // 500 neuron flat create fee
	testIntervalSec = int64(60)  // 60-second billing interval
)

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
		testIntervalSec,
		ms,
		zap.NewNop(),
	)
	get := func(id string) (*Session, error) { return GetSession(ctx, rdb, id) }
	return h, get
}

// ── OnCreate ─────────────────────────────────────────────────────────────────

// OnCreate must emit 2 vouchers: createFee + first compute period.
func TestOnCreate_EmitsTwoVouchers(t *testing.T) {
	ms := &mockSigner{}
	h, get := newTestHandler(t, ms)
	ctx := context.Background()

	before := time.Now().Unix()
	h.OnCreate(ctx, testSandbox, testOwner, 1, 1)
	after := time.Now().Unix()

	if ms.count() != 2 {
		t.Fatalf("expected 2 vouchers (createFee + first period), got %d", ms.count())
	}
	// First voucher = createFee
	v0 := ms.vouchers[0]
	if v0.TotalFee.Int64() != createFeeVal {
		t.Errorf("voucher[0] TotalFee: got %d want %d", v0.TotalFee.Int64(), createFeeVal)
	}
	// Second voucher = first period (intervalSec × pricePerSec)
	v1 := ms.vouchers[1]
	wantPeriodFee := testIntervalSec * pricePerSec
	if v1.TotalFee.Int64() != wantPeriodFee {
		t.Errorf("voucher[1] TotalFee: got %d want %d", v1.TotalFee.Int64(), wantPeriodFee)
	}

	// Session must be created with NextVoucherAt = now + intervalSec
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
	wantNextMin := before + testIntervalSec
	wantNextMax := after + testIntervalSec
	if sess.NextVoucherAt < wantNextMin || sess.NextVoucherAt > wantNextMax {
		t.Errorf("NextVoucherAt %d not in [%d, %d]", sess.NextVoucherAt, wantNextMin, wantNextMax)
	}
}

// A signer error inside OnCreate must not panic. (Session-still-opened is
// asserted by TestOnCreate_EnqueueFailure_StillOpensSession — finding #2.)
func TestOnCreate_SignEnqueueError_NoPanic(t *testing.T) {
	ms := &mockSigner{enqErr: errors.New("redis down")}
	h, _ := newTestHandler(t, ms)
	h.OnCreate(context.Background(), testSandbox, testOwner, 1, 1)
}

// ── OnStart ───────────────────────────────────────────────────────────────────

func TestOnStart_CreatesSessionAndEmitsFirstPeriod(t *testing.T) {
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
	wantNextMin := before + testIntervalSec
	wantNextMax := after + testIntervalSec
	if sess.NextVoucherAt < wantNextMin || sess.NextVoucherAt > wantNextMax {
		t.Errorf("NextVoucherAt %d not in [%d, %d]", sess.NextVoucherAt, wantNextMin, wantNextMax)
	}
	// OnStart pre-charges first period
	if ms.count() != 1 {
		t.Errorf("OnStart must emit 1 voucher (first period), got %d", ms.count())
	}
	wantFee := testIntervalSec * pricePerSec
	if ms.last().TotalFee.Int64() != wantFee {
		t.Errorf("first-period TotalFee: got %d want %d", ms.last().TotalFee.Int64(), wantFee)
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
	origNext := sess1.NextVoucherAt
	origCount := ms.count()

	time.Sleep(2 * time.Millisecond)
	h.OnStart(ctx, testSandbox, testOwner, 1, 1)

	sess2, _ := get(testSandbox)
	if sess2 == nil {
		t.Fatal("expected session still present after second OnStart")
	}
	// NextVoucherAt must not be reset — OnStart is a no-op when session exists.
	if sess2.NextVoucherAt != origNext {
		t.Errorf("OnStart overwrote existing session NextVoucherAt: got %d want %d",
			sess2.NextVoucherAt, origNext)
	}
	// No additional voucher emitted
	if ms.count() != origCount {
		t.Errorf("OnStart emitted extra vouchers: count was %d, now %d", origCount, ms.count())
	}
}

// ── OnStop ────────────────────────────────────────────────────────────────────

func TestOnStop_NoSession_NoVoucher(t *testing.T) {
	ms := &mockSigner{}
	h, _ := newTestHandler(t, ms)

	h.OnStop(context.Background(), "sb-nonexistent")

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers for missing session, got %d", ms.count())
	}
}

func TestOnStop_NoFinalVoucher(t *testing.T) {
	// Scheme 3: period already pre-charged; OnStop just deletes the session.
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal),
		new(big.Int), new(big.Int), testIntervalSec, ms, zap.NewNop())
	ctx := context.Background()

	now := time.Now().Unix()
	err := CreateSession(ctx, rdb, Session{
		SandboxID:     testSandbox,
		Owner:         testOwner,
		Provider:      testProvider,
		NextVoucherAt: now + testIntervalSec,
		PricePerSec:   "100",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	h.OnStop(ctx, testSandbox)

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers on stop (already pre-charged), got %d", ms.count())
	}
}

func TestOnStop_DeletesSession(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal),
		new(big.Int), new(big.Int), testIntervalSec, ms, zap.NewNop())
	ctx := context.Background()

	now := time.Now().Unix()
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		NextVoucherAt: now + testIntervalSec,
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

// ── OnDelete ──────────────────────────────────────────────────────────────────

func TestOnDelete_DeletesSessionNoVoucher(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal),
		new(big.Int), new(big.Int), testIntervalSec, ms, zap.NewNop())
	ctx := context.Background()

	now := time.Now().Unix()
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		NextVoucherAt: now + testIntervalSec,
	})

	h.OnDelete(ctx, testSandbox)

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers on delete, got %d", ms.count())
	}
	sess, _ := GetSession(ctx, rdb, testSandbox)
	if sess != nil {
		t.Error("session should be deleted after OnDelete")
	}
}

// ── OnArchive ─────────────────────────────────────────────────────────────────

func TestOnArchive_DeletesSessionNoVoucher(t *testing.T) {
	ms := &mockSigner{}
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(createFeeVal),
		new(big.Int), new(big.Int), testIntervalSec, ms, zap.NewNop())
	ctx := context.Background()

	now := time.Now().Unix()
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: testSandbox, Owner: testOwner, Provider: testProvider,
		NextVoucherAt: now + testIntervalSec,
	})

	h.OnArchive(ctx, testSandbox)

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers from OnArchive, got %d", ms.count())
	}
	sess, _ := GetSession(ctx, rdb, testSandbox)
	if sess != nil {
		t.Error("session should be deleted after OnArchive")
	}
}

// Finding #2 (live-fire on #109): a voucher-enqueue failure inside OnCreate must
// NOT skip session creation. Pre-fix it returned early, leaving the sandbox
// unbilled (no session → the generator never ticks → catch-up can't help). Now
// the session is opened regardless, with NextVoucherAt defaulting to now so the
// generator bills the first period on its next tick.
func TestOnCreate_EnqueueFailure_StillOpensSession(t *testing.T) {
	ms := &mockSigner{enqErr: errors.New("nonce reseed failed (fail-closed)")}
	h, get := newTestHandler(t, ms)

	before := time.Now().Unix()
	h.OnCreate(context.Background(), "sb-fail", testOwner, 2, 4)

	sess, err := get("sb-fail")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess == nil {
		t.Fatal("session must be created even when voucher enqueue fails — otherwise the sandbox runs unbilled")
	}
	if sess.Owner != testOwner || sess.Provider != testProvider {
		t.Errorf("session fields wrong: %+v", sess)
	}
	// NextVoucherAt defaults to ~now so the first period is due immediately
	// (the generator + catch-up bill it), not now+interval.
	if sess.NextVoucherAt < before || sess.NextVoucherAt > before+testIntervalSec {
		t.Errorf("NextVoucherAt = %d, want ~now (%d) so the missed period is due", sess.NextVoucherAt, before)
	}
}
