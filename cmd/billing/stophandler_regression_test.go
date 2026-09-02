package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/settler"
)

// noopAlerter satisfies alert.Alerter for tests and counts notifications.
type noopAlerter struct{ count int32 }

func (n *noopAlerter) Notify(ctx context.Context, kind alert.Kind, sev alert.Severity, message string, details map[string]any) {
	atomic.AddInt32(&n.count, 1)
}

// Regression for bug-report #1: when ArchiveSandbox fails and the sandbox is NOT
// already archived, runStopHandler must PRESERVE billing:compute:<id> and
// stop:sandbox:<id> so recoverPendingStops can retry on restart — and alert.
func TestStopHandler_ArchiveFailure_PreservesRetryState(t *testing.T) {
	const id = "sb-archfail"
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	mock.archiveFailIDs[id] = true // archive 500s; sandbox never reaches "archived"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bg := context.Background()
	rdb.Set(bg, "billing:compute:"+id, "session", 0)           //nolint:errcheck
	rdb.Set(bg, "stop:sandbox:"+id, "insufficient_balance", 0) //nolint:errcheck

	al := &noopAlerter{}
	stopCh := make(chan settler.StopSignal, 1)
	go runStopHandler(ctx, stopCh, mock.client(), rdb, al, zap.NewNop(), nil)
	stopCh <- settler.StopSignal{SandboxID: id, Reason: "insufficient_balance"}

	// Give the handler time to run its (failing) archive pass.
	time.Sleep(400 * time.Millisecond)

	if n, _ := rdb.Exists(bg, "stop:sandbox:"+id).Result(); n == 0 {
		t.Errorf("stop marker deleted after archive failure — recovery can never retry")
	}
	if n, _ := rdb.Exists(bg, "billing:compute:"+id).Result(); n == 0 {
		t.Errorf("billing session deleted after archive failure — billing/accounting drift")
	}
	if atomic.LoadInt32(&al.count) == 0 {
		t.Errorf("expected an alert on archive failure")
	}
}

// Companion: an already-archived sandbox is an idempotent terminal success, so a
// failing archive call must still clean up (no poison-pill that never converges).
func TestStopHandler_AlreadyArchived_CleansUp(t *testing.T) {
	const id = "sb-already"
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	mock.archiveFailIDs[id] = true // archive call errors...
	mock.archived[id] = true       // ...but sandbox is already in terminal archived state

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bg := context.Background()
	rdb.Set(bg, "billing:compute:"+id, "session", 0)           //nolint:errcheck
	rdb.Set(bg, "stop:sandbox:"+id, "insufficient_balance", 0) //nolint:errcheck

	stopCh := make(chan settler.StopSignal, 1)
	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil)
	stopCh <- settler.StopSignal{SandboxID: id, Reason: "insufficient_balance"}

	waitKeyGone(t, rdb, "stop:sandbox:"+id, time.Second)
	waitKeyGone(t, rdb, "billing:compute:"+id, time.Second)
}

// Review F1 (#68): a stop marker for a DELETED (or never-existing) sandbox must
// converge — stop/wait/archive all fail on a nonexistent id, and preserving the
// marker would re-queue it via recoverPendingStops on every restart, forever,
// paging the operator each time. A definitive 404 is terminal: clean up.
func TestRunStopHandler_GoneSandbox_CleansUpMarkers(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	m := newMockDaytona(t)
	m.goneIDs["sb-gone"] = true
	rdb.Set(context.Background(), "stop:sandbox:sb-gone", "insufficient_balance", 0)
	rdb.Set(context.Background(), "billing:compute:sb-gone", "{}", 0)

	ctx, cancel := context.WithCancel(context.Background())
	stopCh := make(chan settler.StopSignal, 1)
	stopCh <- settler.StopSignal{SandboxID: "sb-gone", Reason: "insufficient_balance"}
	done := make(chan struct{})
	go func() { runStopHandler(ctx, stopCh, m.client(), rdb, alert.Nop{}, zap.NewNop(), nil); close(done) }()

	deadline := time.After(3 * time.Second)
	for {
		if n, _ := rdb.Exists(context.Background(), "stop:sandbox:sb-gone", "billing:compute:sb-gone").Result(); n == 0 {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("markers for a 404 sandbox must be cleaned up, not preserved")
		case <-time.After(20 * time.Millisecond):
		}
	}
	cancel()
	<-done
}
