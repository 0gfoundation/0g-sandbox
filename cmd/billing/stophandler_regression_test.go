package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/settler"
	"go.uber.org/zap"
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
	rdb.Set(bg, "billing:compute:"+id, "session", 0)      //nolint:errcheck
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
	rdb.Set(bg, "billing:compute:"+id, "session", 0)      //nolint:errcheck
	rdb.Set(bg, "stop:sandbox:"+id, "insufficient_balance", 0) //nolint:errcheck

	stopCh := make(chan settler.StopSignal, 1)
	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil)
	stopCh <- settler.StopSignal{SandboxID: id, Reason: "insufficient_balance"}

	waitKeyGone(t, rdb, "stop:sandbox:"+id, time.Second)
	waitKeyGone(t, rdb, "billing:compute:"+id, time.Second)
}
