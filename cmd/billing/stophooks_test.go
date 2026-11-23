package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/settler"
)

type stoppedRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (r *stoppedRecorder) hook(_ context.Context, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
}

func (r *stoppedRecorder) wait(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		for _, got := range r.ids {
			if got == id {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("onStopped not called for %s", id)
}

// A reason in stopHooks.keep only stops the sandbox: no archive. The session
// closes and the onStopped hooks run, as for any completed stop.
func TestRunStopHandler_KeepReason_StopsWithoutArchive(t *testing.T) {
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bg := context.Background()
	rdb.Set(bg, "billing:compute:sb-k", "session", 0)  //nolint:errcheck
	rdb.Set(bg, "stop:sandbox:sb-k", "keep_reason", 0) //nolint:errcheck

	rec := &stoppedRecorder{}
	hooks := stopHooks{keep: map[string]bool{"keep_reason": true}, onStopped: []func(context.Context, string){rec.hook}}
	stopCh := make(chan settler.StopSignal, 1)
	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil, hooks)
	stopCh <- settler.StopSignal{SandboxID: "sb-k", Reason: "keep_reason"}

	waitKeyGone(t, rdb, "stop:sandbox:sb-k", time.Second)
	waitKeyGone(t, rdb, "billing:compute:sb-k", time.Second)
	rec.wait(t, "sb-k")
	if ids := mock.stoppedIDs(); len(ids) != 1 || ids[0] != "sb-k" {
		t.Errorf("stopped %v, want [sb-k]", ids)
	}
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.archived["sb-k"] {
		t.Error("a keep reason archived the sandbox")
	}
}

// Any other reason archives, and the onStopped hooks run after it too.
func TestRunStopHandler_ArchiveReason_RunsOnStopped(t *testing.T) {
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rdb.Set(context.Background(), "stop:sandbox:sb-a", "insufficient_balance", 0) //nolint:errcheck

	rec := &stoppedRecorder{}
	hooks := stopHooks{keep: map[string]bool{"keep_reason": true}, onStopped: []func(context.Context, string){rec.hook}}
	stopCh := make(chan settler.StopSignal, 1)
	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil, hooks)
	stopCh <- settler.StopSignal{SandboxID: "sb-a", Reason: "insufficient_balance"}

	rec.wait(t, "sb-a")
	mock.mu.Lock()
	defer mock.mu.Unlock()
	if !mock.archived["sb-a"] {
		t.Error("an archive reason did not archive")
	}
}

// Not confirmed stopped: the session stays, the marker goes so the requester
// can queue the stop again, and no onStopped hook runs.
func TestFinishStopWithoutArchive_WaitFailed(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	rdb.Set(ctx, "billing:compute:sb-k", "session", 0)  //nolint:errcheck
	rdb.Set(ctx, "stop:sandbox:sb-k", "keep_reason", 0) //nolint:errcheck
	rec := &stoppedRecorder{}

	finishStopWithoutArchive(ctx, settler.StopSignal{SandboxID: "sb-k", Reason: "keep_reason"}, errors.New("timeout"),
		rdb, zap.NewNop(), nil, stopHooks{onStopped: []func(context.Context, string){rec.hook}})

	if n, _ := rdb.Exists(ctx, "billing:compute:sb-k").Result(); n != 1 {
		t.Error("billing session closed for a sandbox not confirmed stopped")
	}
	if n, _ := rdb.Exists(ctx, "stop:sandbox:sb-k").Result(); n != 0 {
		t.Error("stop marker kept: the requester could never queue it again")
	}
	if len(rec.ids) != 0 {
		t.Errorf("onStopped ran for an unconfirmed stop: %v", rec.ids)
	}
}
