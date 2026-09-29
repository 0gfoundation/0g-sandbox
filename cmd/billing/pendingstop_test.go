package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/settler"
)

// A marker written after the startup pass must still be picked up. Recovery
// used to run once, at startup — the moment Daytona is least likely to be
// reachable — and a marker that survived that pass then blocked persistStop
// from ever queueing another signal for its sandbox, so nothing tried again
// until the next restart, which replayed the same race.
func TestPendingStopRecovery_RetriesAfterTheStartupPass(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prev := pendingStopRetryInterval
	pendingStopRetryInterval = 50 * time.Millisecond
	defer func() { pendingStopRetryInterval = prev }()

	stopCh := make(chan settler.StopSignal, 8)
	go runPendingStopRecovery(ctx, rdb, stopCh, zap.NewNop())

	// Written after the loop has started, so only a later pass can find it.
	time.Sleep(20 * time.Millisecond)
	if err := rdb.Set(ctx, "stop:sandbox:sb-late", "insufficient_balance", 0).Err(); err != nil {
		t.Fatal(err)
	}

	select {
	case sig := <-stopCh:
		if sig.SandboxID != "sb-late" {
			t.Errorf("got %q, want sb-late", sig.SandboxID)
		}
		if sig.Reason != "insufficient_balance" {
			t.Errorf("reason %q not carried through", sig.Reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("marker written after startup was never re-queued; recovery is still " +
			"a one-shot and a marker that outlives it waits for a restart")
	}
}

// The startup pass itself still happens — the interval is an addition, not a
// replacement.
func TestPendingStopRecovery_StillRunsAtStartup(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := rdb.Set(ctx, "stop:sandbox:sb-existing", "not_acknowledged", 0).Err(); err != nil {
		t.Fatal(err)
	}

	prev := pendingStopRetryInterval
	pendingStopRetryInterval = time.Hour // only the startup pass can deliver
	defer func() { pendingStopRetryInterval = prev }()

	stopCh := make(chan settler.StopSignal, 8)
	go runPendingStopRecovery(ctx, rdb, stopCh, zap.NewNop())

	select {
	case sig := <-stopCh:
		if sig.SandboxID != "sb-existing" {
			t.Errorf("got %q, want sb-existing", sig.SandboxID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("pre-existing marker not recovered at startup")
	}
}

// Cancelling the context stops the loop rather than leaking a goroutine that
// keeps writing to a channel nobody reads.
func TestPendingStopRecovery_StopsOnContextCancel(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx, cancel := context.WithCancel(context.Background())

	prev := pendingStopRetryInterval
	pendingStopRetryInterval = 10 * time.Millisecond
	defer func() { pendingStopRetryInterval = prev }()

	done := make(chan struct{})
	go func() {
		runPendingStopRecovery(ctx, rdb, make(chan settler.StopSignal, 8), zap.NewNop())
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("recovery loop outlived its context")
	}
}
