package billing

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// newTestHandlerWithInterval creates an EventHandler with a custom interval.
func newTestHandlerWithInterval(t *testing.T, ms *mockSigner, intervalSec int64) (*EventHandler, *testRedisWrapper) {
	t.Helper()
	rdb, _ := newTestRedis(t)
	h := NewEventHandler(
		rdb,
		testProvider,
		big.NewInt(pricePerSec),
		big.NewInt(createFeeVal),
		new(big.Int),
		new(big.Int),
		intervalSec,
		ms,
		zap.NewNop(),
	)
	return h, &testRedisWrapper{rdb: rdb}
}

type testRedisWrapper struct {
	rdb interface {
		// placeholder — direct access to rdb via h is sufficient
	}
}

// ── No sessions ───────────────────────────────────────────────────────────────

func TestRunGeneration_NoSessions_NoVouchers(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	h := NewEventHandler(rdb, testProvider, big.NewInt(100), big.NewInt(0), new(big.Int), new(big.Int), 3600, ms, zap.NewNop())

	runGeneration(context.Background(), rdb, h, zap.NewNop())

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers for empty Redis, got %d", ms.count())
	}
}

// ── Session whose NextVoucherAt is in the future: no voucher ─────────────────

func TestRunGeneration_SessionNotDue_NoVoucher(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(3600)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	// NextVoucherAt = future → not due yet
	future := time.Now().Unix() + intervalSec
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-future", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: future,
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	if ms.count() != 0 {
		t.Errorf("expected 0 vouchers for future NextVoucherAt, got %d", ms.count())
	}
}

// ── Normal: NextVoucherAt has elapsed → pre-charge next period ───────────────

func TestRunGeneration_SessionDue_EmitsPeriodVoucher(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(3600)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	// NextVoucherAt = now - 10s → period is due
	due := time.Now().Unix() - 10
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-due", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: due, PricePerSec: "100",
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	v := ms.last()
	if v == nil {
		t.Fatal("expected voucher, got none")
	}
	if v.SandboxID != "sb-due" {
		t.Errorf("SandboxID: got %q want %q", v.SandboxID, "sb-due")
	}
	// Fee = intervalSec × pricePerSec
	wantFee := intervalSec * pricePerSec
	if v.TotalFee.Int64() != wantFee {
		t.Errorf("TotalFee: got %d want %d", v.TotalFee.Int64(), wantFee)
	}
}

// ── NextVoucherAt is updated after pre-charge ─────────────────────────────────

func TestRunGeneration_UpdatesNextVoucherAt(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(3600)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	due := time.Now().Unix() - 10
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-adv", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: due, PricePerSec: "100",
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	sess, err := GetSession(ctx, rdb, "sb-adv")
	if err != nil || sess == nil {
		t.Fatalf("GetSession: err=%v sess=%v", err, sess)
	}
	// NextVoucherAt must advance by intervalSec
	expected := due + intervalSec
	if sess.NextVoucherAt != expected {
		t.Errorf("NextVoucherAt: got %d want %d", sess.NextVoucherAt, expected)
	}
}

// ── Idempotent: second run with updated NextVoucherAt does nothing ────────────

func TestRunGeneration_AfterUpdate_NoDoubleVoucher(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(3600)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	due := time.Now().Unix() - 10
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-idem", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: due, PricePerSec: "100",
	})

	// First run → voucher emitted, NextVoucherAt = due + interval (future)
	runGeneration(ctx, rdb, h, zap.NewNop())
	if ms.count() != 1 {
		t.Fatalf("first run: expected 1 voucher, got %d", ms.count())
	}

	// Second run immediately → NextVoucherAt is now in the future → no voucher
	runGeneration(ctx, rdb, h, zap.NewNop())
	if ms.count() != 1 {
		t.Errorf("second run: expected still 1 voucher total, got %d", ms.count())
	}
}

// ── Multiple sessions ─────────────────────────────────────────────────────────

func TestRunGeneration_MultipleSessions_OneVoucherEach(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(3600)
	h := NewEventHandler(rdb, testProvider, big.NewInt(10), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	due := time.Now().Unix() - 10
	for _, id := range []string{"sb-m1", "sb-m2", "sb-m3"} {
		CreateSession(ctx, rdb, Session{ //nolint:errcheck
			SandboxID: id, Owner: testOwner, Provider: testProvider,
			NextVoucherAt: due, PricePerSec: "10",
		})
	}

	runGeneration(ctx, rdb, h, zap.NewNop())

	if ms.count() != 3 {
		t.Errorf("expected 3 vouchers for 3 sessions, got %d", ms.count())
	}

	var ids []string
	for _, v := range ms.vouchers {
		ids = append(ids, v.SandboxID)
	}
	sort.Strings(ids)
	want := []string{"sb-m1", "sb-m2", "sb-m3"}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("voucher[%d]: got %q want %q", i, ids[i], w)
		}
	}
}

// selectiveErrSigner fails Enqueue for sessions owned by failOwner.
type selectiveErrSigner struct {
	mockSigner
	failOwner string
}

func (s *selectiveErrSigner) Enqueue(ctx context.Context, v *voucher.SandboxVoucher) error {
	if strings.EqualFold(v.User.Hex(), common.HexToAddress(s.failOwner).Hex()) {
		return errors.New("selective enqueue error")
	}
	return s.mockSigner.Enqueue(ctx, v)
}

// ── Enqueue error on one session, others still processed ─────────────────────

func TestRunGeneration_EnqueueError_OtherSessionsUnaffected(t *testing.T) {
	rdb, _ := newTestRedis(t)

	failOwner := "0xFAILFAILFAILFAILFAILFAILFAILFAILFAILFA"
	okOwner := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	ms := &selectiveErrSigner{failOwner: failOwner}
	const intervalSec = int64(3600)
	h := NewEventHandler(rdb, testProvider, big.NewInt(10), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	due := time.Now().Unix() - 10
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-fail", Owner: failOwner, Provider: testProvider,
		NextVoucherAt: due, PricePerSec: "10",
	})
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-ok", Owner: okOwner, Provider: testProvider,
		NextVoucherAt: due, PricePerSec: "10",
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	if ms.count() != 1 {
		t.Errorf("expected 1 voucher (ok session only), got %d", ms.count())
	}
	if ms.last().SandboxID != "sb-ok" {
		t.Errorf("wrong voucher sandbox: got %q", ms.last().SandboxID)
	}
}

// ── Enqueue error: NextVoucherAt NOT updated ──────────────────────────────────

func TestRunGeneration_EnqueueError_NextVoucherAtUnchanged(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{enqErr: errors.New("enqueue failed")}
	const intervalSec = int64(3600)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	due := time.Now().Unix() - 10
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-enq-err", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: due, PricePerSec: "100",
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	sess, _ := GetSession(ctx, rdb, "sb-enq-err")
	if sess.NextVoucherAt != due {
		t.Errorf("NextVoucherAt must not advance on enqueue error: got %d want %d",
			sess.NextVoucherAt, due)
	}
}

// ── Voucher fields: User/Provider addresses ───────────────────────────────────

func TestRunGeneration_VoucherHasCorrectAddresses(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), 3600, ms, zap.NewNop())
	ctx := context.Background()

	due := time.Now().Unix() - 10
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-addr", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: due, PricePerSec: "100",
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	v := ms.last()
	if v == nil {
		t.Fatal("expected voucher")
	}
	zeroAddr := "0x0000000000000000000000000000000000000000"
	if v.User.Hex() == zeroAddr {
		t.Error("User address is zero")
	}
	if v.Provider.Hex() == zeroAddr {
		t.Error("Provider address is zero")
	}
	// Note: Nonce is assigned by the settler (not the generator), so it remains nil here.
}

// ── Flat-rate fallback when PricePerSec is empty ─────────────────────────────

func TestRunGeneration_FlatRateFallback(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(60)
	flatRate := int64(50)
	h := NewEventHandler(rdb, testProvider, big.NewInt(flatRate), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	due := time.Now().Unix() - 10
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-flat", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: due,
		// PricePerSec intentionally empty → falls back to h.computePricePerSec
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	v := ms.last()
	if v == nil {
		t.Fatal("expected voucher with flat rate")
	}
	wantFee := intervalSec * flatRate
	if v.TotalFee.Int64() != wantFee {
		t.Errorf("flat rate TotalFee: got %d want %d", v.TotalFee.Int64(), wantFee)
	}
}

// ── Backlog catch-up (#76) ───────────────────────────────────────────────────

// After downtime the generator owes many periods. One tick must close a
// moderate backlog entirely — in CHUNKS (≤catchupChunkIntervals per voucher),
// with the total fee exactly covering every missed period.
func TestRunGeneration_Backlog_CatchesUpInChunks(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(60)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	// 100 intervals behind (+30s slack so integer division lands on 100).
	start := time.Now().Unix() - 100*intervalSec + 30
	CreateSession(ctx, rdb, Session{ //nolint:errcheck
		SandboxID: "sb-backlog", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: start, PricePerSec: "100",
	})

	runGeneration(ctx, rdb, h, zap.NewNop())

	ms.mu.Lock()
	n := len(ms.vouchers)
	total := new(big.Int)
	for _, v := range ms.vouchers {
		total.Add(total, v.TotalFee)
	}
	ms.mu.Unlock()
	if n != 2 { // 60 + 40
		t.Fatalf("want 2 chunked vouchers (60+40 intervals), got %d", n)
	}
	wantTotal := int64(100) * intervalSec * 100 // intervals × sec × price(session "100")
	if total.Int64() != wantTotal {
		t.Errorf("total fee: got %d want %d (every missed period billed once)", total.Int64(), wantTotal)
	}
	sess, _ := GetSession(ctx, rdb, "sb-backlog")
	if got := sess.NextVoucherAt; got != start+100*intervalSec {
		t.Errorf("NextVoucherAt: got %d want %d (fully caught up)", got, start+100*intervalSec)
	}
}

// A very deep backlog is bounded per tick (catchupMaxVouchersPerTick chunks)
// and resumes next tick — no unbounded work, no lost periods.
func TestRunGeneration_DeepBacklog_BoundedPerTick(t *testing.T) {
	rdb, _ := newTestRedis(t)
	ms := &mockSigner{}
	const intervalSec = int64(60)
	h := NewEventHandler(rdb, testProvider, big.NewInt(pricePerSec), big.NewInt(0), new(big.Int), new(big.Int), intervalSec, ms, zap.NewNop())
	ctx := context.Background()

	start := time.Now().Unix() - 1000*intervalSec + 30 // 1000 intervals behind
	CreateSession(ctx, rdb, Session{                   //nolint:errcheck
		SandboxID: "sb-deep", Owner: testOwner, Provider: testProvider,
		NextVoucherAt: start, PricePerSec: "100",
	})

	runGeneration(ctx, rdb, h, zap.NewNop())
	ms.mu.Lock()
	n1 := len(ms.vouchers)
	ms.mu.Unlock()
	if n1 != catchupMaxVouchersPerTick {
		t.Fatalf("first tick: want %d chunks, got %d", catchupMaxVouchersPerTick, n1)
	}
	sess, _ := GetSession(ctx, rdb, "sb-deep")
	advanced := (sess.NextVoucherAt - start) / intervalSec
	if advanced != catchupMaxVouchersPerTick*catchupChunkIntervals {
		t.Fatalf("first tick advanced %d intervals, want %d", advanced, catchupMaxVouchersPerTick*catchupChunkIntervals)
	}

	// Second tick continues from where the first stopped.
	runGeneration(ctx, rdb, h, zap.NewNop())
	sess2, _ := GetSession(ctx, rdb, "sb-deep")
	if sess2.NextVoucherAt <= sess.NextVoucherAt {
		t.Error("second tick must keep advancing the backlog")
	}
}
