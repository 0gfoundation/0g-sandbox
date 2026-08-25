package settler

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

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
