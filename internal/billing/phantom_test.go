package billing

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// countingSigner records what the generator tried to bill.
type countingSigner struct{ vouchers []voucher.SandboxVoucher }

func (c *countingSigner) Enqueue(_ context.Context, v *voucher.SandboxVoucher) error {
	c.vouchers = append(c.vouchers, *v)
	return nil
}

func (c *countingSigner) billed(sandboxID string) int {
	n := 0
	for _, v := range c.vouchers {
		if v.SandboxID == sandboxID {
			n++
		}
	}
	return n
}

type staticBillable struct {
	live map[string]bool
	err  error
	// gone is what the authoritative per-id lookup reports. Default false:
	// absent from the listing is not by itself proof of deletion.
	gone     map[string]bool
	confirms int
}

func (s staticBillable) BillableSandboxIDs(context.Context) (map[string]bool, error) {
	return s.live, s.err
}

func (s *staticBillable) ConfirmGone(_ context.Context, sandboxID string) bool {
	s.confirms++
	return s.gone[sandboxID]
}

// phantomFixture opens sessions for the given sandboxes, all due to be billed.
func phantomFixture(t *testing.T, sandboxes ...string) (*redis.Client, *EventHandler, *countingSigner) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	sig := &countingSigner{}
	h := NewEventHandler(rdb, "0xPROV",
		big.NewInt(1000), // flat compute price
		big.NewInt(0),    // create fee
		big.NewInt(0), big.NewInt(0),
		60, sig, zap.NewNop())

	past := time.Now().Unix() - 120 // overdue, so every session bills this tick
	for _, sb := range sandboxes {
		if err := CreateSession(context.Background(), rdb, Session{
			SandboxID: sb, Owner: "0xAAA", Provider: "0xPROV",
			NextVoucherAt: past, PricePerSec: "1000",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return rdb, h, sig
}

// A session whose sandbox the runtime no longer has must not be billed. This
// is the shape found on dev: five sessions still emitting a voucher a minute
// for sandboxes deleted long before. Nothing was collected only because the
// account was empty — a deposit would have been drained at the full rate for
// compute that never ran.
func TestGenerator_SkipsSessionWithNoLiveSandbox(t *testing.T) {
	rdb, h, sig := phantomFixture(t, "sb-live", "sb-phantom")
	billable := &staticBillable{live: map[string]bool{"sb-live": true}}

	runGeneration(context.Background(), rdb, h, billable, map[string]int{}, zap.NewNop())

	if got := sig.billed("sb-phantom"); got != 0 {
		t.Errorf("phantom sandbox billed %d times; the session outlived the sandbox "+
			"and the generator charged for compute that does not exist", got)
	}
	if got := sig.billed("sb-live"); got == 0 {
		t.Error("live sandbox was not billed; the gate must not block real usage")
	}
}

// An archived sandbox runs nothing, so a session pointed at one is as wrong as
// a session pointed at nothing.
func TestGenerator_ArchivedSandboxIsNotBillable(t *testing.T) {
	rdb, h, sig := phantomFixture(t, "sb-archived")
	// The adapter drops archived ids before they reach the generator, so an
	// archived sandbox is simply absent from the set.
	billable := &staticBillable{live: map[string]bool{}}

	runGeneration(context.Background(), rdb, h, billable, map[string]int{}, zap.NewNop())

	if got := sig.billed("sb-archived"); got != 0 {
		t.Errorf("archived sandbox billed %d times", got)
	}
}

// A listing failure means "unknown", not "gone". Sandboxes run on the runners,
// so an unreachable control plane does not stop the compute the user is
// getting — declining to bill through an outage would give it away. This is
// the deliberate fail-open direction.
func TestGenerator_BillsWhenSandboxListUnavailable(t *testing.T) {
	rdb, h, sig := phantomFixture(t, "sb-live")
	billable := &staticBillable{err: errors.New("connection refused")}

	runGeneration(context.Background(), rdb, h, billable, map[string]int{}, zap.NewNop())

	if got := sig.billed("sb-live"); got == 0 {
		t.Error("nothing billed while the sandbox list was unavailable — an outage " +
			"of the control plane would hand out free compute")
	}
}

// No gate configured keeps the previous behaviour exactly.
func TestGenerator_NilGateBillsEverySession(t *testing.T) {
	rdb, h, sig := phantomFixture(t, "sb-one", "sb-two")

	runGeneration(context.Background(), rdb, h, nil, map[string]int{}, zap.NewNop())

	for _, sb := range []string{"sb-one", "sb-two"} {
		if sig.billed(sb) == 0 {
			t.Errorf("%s not billed with the gate disabled", sb)
		}
	}
}

// Skipping must not advance the billing clock: if the session is later found
// to be legitimate, the periods it covers are still owed. Leaving
// NextVoucherAt in the past keeps the catch-up path able to collect them.
func TestGenerator_SkippedSessionKeepsItsClock(t *testing.T) {
	rdb, h, _ := phantomFixture(t, "sb-phantom")
	ctx := context.Background()

	before, err := GetSession(ctx, rdb, "sb-phantom")
	if err != nil || before == nil {
		t.Fatalf("setup: %v", err)
	}

	runGeneration(ctx, rdb, h, &staticBillable{live: map[string]bool{}}, map[string]int{}, zap.NewNop())

	after, err := GetSession(ctx, rdb, "sb-phantom")
	if err != nil || after == nil {
		t.Fatalf("session should survive a skip: %v", err)
	}
	if after.NextVoucherAt != before.NextVoucherAt {
		t.Errorf("skipped session's clock moved from %d to %d; the unbilled periods "+
			"would be silently forgiven", before.NextVoucherAt, after.NextVoucherAt)
	}
}

// A sandbox deleted straight through Daytona fires no billing hook and leaves
// no stop marker, so nothing downstream would ever close its session — it
// would be skipped every tick for the life of the process. After enough
// consecutive absences to rule out a listing that raced a create, the session
// is confirmed against the authoritative per-id lookup and closed.
func TestGenerator_ClosesSessionOnceRuntimeConfirmsItIsGone(t *testing.T) {
	rdb, h, _ := phantomFixture(t, "sb-deleted")
	ctx := context.Background()
	billable := &staticBillable{
		live: map[string]bool{},
		gone: map[string]bool{"sb-deleted": true},
	}
	skips := map[string]int{}

	// Below the threshold the session survives: one absent listing is not
	// proof, and the authoritative lookup is not worth a call per tick.
	for i := 0; i < phantomSkipsBeforeConfirm-1; i++ {
		runGeneration(ctx, rdb, h, billable, skips, zap.NewNop())
	}
	if s, _ := GetSession(ctx, rdb, "sb-deleted"); s == nil {
		t.Fatalf("session closed after %d skips; the threshold is %d",
			phantomSkipsBeforeConfirm-1, phantomSkipsBeforeConfirm)
	}
	if billable.confirms != 0 {
		t.Errorf("authoritative lookup called %d times below the threshold", billable.confirms)
	}

	runGeneration(ctx, rdb, h, billable, skips, zap.NewNop())

	if s, _ := GetSession(ctx, rdb, "sb-deleted"); s != nil {
		t.Error("session survived a confirmed deletion; it would be skipped forever")
	}
	if exists, _ := rdb.Exists(ctx, "stop:sandbox:sb-deleted").Result(); exists != 0 {
		t.Error("stop marker left behind for a sandbox that no longer exists")
	}
}

// Absence from the listing is not deletion. If the authoritative lookup cannot
// prove the sandbox is gone — it errored, or the sandbox is merely unlistable
// — the session stays. Deleting it would strand a live sandbox with no billing
// session, which is the failure the stop handler's preserve-on-error path
// exists to avoid.
func TestGenerator_KeepsSessionWhenDeletionIsUnconfirmed(t *testing.T) {
	rdb, h, _ := phantomFixture(t, "sb-unlisted")
	ctx := context.Background()
	billable := &staticBillable{live: map[string]bool{}, gone: map[string]bool{}}
	skips := map[string]int{}

	for i := 0; i < phantomSkipsBeforeConfirm+5; i++ {
		runGeneration(ctx, rdb, h, billable, skips, zap.NewNop())
	}

	if s, _ := GetSession(ctx, rdb, "sb-unlisted"); s == nil {
		t.Error("session deleted without the runtime confirming the sandbox is gone")
	}
	if billable.confirms == 0 {
		t.Error("authoritative lookup never consulted past the threshold")
	}
}

// A sandbox that reappears — a listing that raced a create, a transient gap —
// resets the count, so an intermittently-absent sandbox never accumulates its
// way to deletion.
func TestGenerator_ReappearingSandboxResetsTheSkipCount(t *testing.T) {
	rdb, h, _ := phantomFixture(t, "sb-flaky")
	ctx := context.Background()
	absent := &staticBillable{live: map[string]bool{}, gone: map[string]bool{"sb-flaky": true}}
	present := &staticBillable{live: map[string]bool{"sb-flaky": true}}
	skips := map[string]int{}

	for i := 0; i < phantomSkipsBeforeConfirm-1; i++ {
		runGeneration(ctx, rdb, h, absent, skips, zap.NewNop())
	}
	// One sighting clears the streak.
	runGeneration(ctx, rdb, h, present, skips, zap.NewNop())
	if got := skips["sb-flaky"]; got != 0 {
		t.Fatalf("skip count %d after the sandbox reappeared; want 0", got)
	}
	// Another near-threshold run must still not reach it.
	for i := 0; i < phantomSkipsBeforeConfirm-1; i++ {
		runGeneration(ctx, rdb, h, absent, skips, zap.NewNop())
	}

	if s, _ := GetSession(ctx, rdb, "sb-flaky"); s == nil {
		t.Error("session deleted even though the sandbox was seen in between; " +
			"the streak must be consecutive, not cumulative")
	}
}
