package settler

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Regression for the kill-storm (#69): a backlog of INSUFFICIENT_BALANCE
// rejections for one sandbox must collapse to a single kill order. The first
// persistStop writes the marker and signals; subsequent calls for the same
// sandbox are deduped via SetNX — no duplicate signal, marker unchanged.
func TestPersistStop_Dedup_CollapsesRepeatedKills(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 16)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := persistStop(ctx, rdb, stopCh, "sb-storm", "insufficient_balance", zap.NewNop()); err != nil {
			t.Fatalf("persistStop call %d: unexpected error: %v", i, err)
		}
	}

	if len(stopCh) != 1 {
		t.Fatalf("expected exactly 1 kill order after 5 rejections, got %d", len(stopCh))
	}
	if n, _ := rdb.Exists(ctx, "stop:sandbox:sb-storm").Result(); n != 1 {
		t.Fatalf("stop marker should exist exactly once, got %d", n)
	}
}

// Regression for bug-report #2: persistStop must NOT signal a stop when the
// crash-recovery marker cannot be persisted. If Redis is down it returns an
// error and dispatches nothing, so the caller can alert instead of silently
// stopping a sandbox with no way to recover after a crash.
func TestPersistStop_RedisFailure_ReturnsErrorAndDoesNotSignal(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	addr := mr.Addr()
	rdb := redis.NewClient(&redis.Options{Addr: addr})

	// Simulate Redis being unavailable at persist time.
	mr.Close()

	stopCh := make(chan StopSignal, 1)
	err = persistStop(context.Background(), rdb, stopCh, "sbx-123", "insufficient_balance", zap.NewNop())

	if err == nil {
		t.Fatalf("expected an error when Redis persist fails, got nil")
	}
	select {
	case sig := <-stopCh:
		t.Fatalf("stop signal must NOT be dispatched when persistence failed, got %+v", sig)
	default:
		// correct: no signal
	}
}
