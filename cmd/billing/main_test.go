package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/0gfoundation/0g-sandbox/internal/settler"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

// mockDaytona returns a test HTTP server that records which sandbox IDs were
// stopped, and optionally injects failures for specific IDs.
type mockDaytona struct {
	mu             sync.Mutex
	stopped        []string
	failIDs        map[string]bool // POST /stop → 500
	archiveFailIDs map[string]bool // POST /archive → 500
	archived       map[string]bool // set once archive succeeds; drives GET state
	goneIDs        map[string]bool // GET → 404 (deleted / never existed)
	srv            *httptest.Server
}

func newMockDaytona(t *testing.T) *mockDaytona {
	t.Helper()
	m := &mockDaytona{
		failIDs:        make(map[string]bool),
		archiveFailIDs: make(map[string]bool),
		archived:       make(map[string]bool),
		goneIDs:        make(map[string]bool),
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // ["api","sandbox",id,(action)]
		if len(parts) < 3 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		id := parts[2]
		m.mu.Lock()
		defer m.mu.Unlock()

		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/stop"):
			if m.failIDs[id] {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			m.stopped = append(m.stopped, id)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/archive"):
			if m.archiveFailIDs[id] {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			m.archived[id] = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && len(parts) == 3:
			// GetSandbox — used by WaitStopped and the disposition check.
			if m.goneIDs[id] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			state := "stopped"
			if m.archived[id] {
				state = "archived"
			}
			w.Write([]byte(`{"id":"` + id + `","state":"` + state + `"}`)) //nolint:errcheck
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockDaytona) client() *daytona.Client {
	return daytona.NewClient(m.srv.URL, "test-key")
}

func (m *mockDaytona) stoppedIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.stopped))
	copy(out, m.stopped)
	return out
}

// waitKeyGone polls until the Redis key disappears or the timeout elapses.
func waitKeyGone(t *testing.T, rdb *redis.Client, key string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n, _ := rdb.Exists(context.Background(), key).Result()
		if n == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("key %q still exists after %v", key, timeout)
}

// ── recoverPendingStops ───────────────────────────────────────────────────────

func TestRecoverPendingStops_Empty(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan settler.StopSignal, 8)

	recoverPendingStops(context.Background(), rdb, stopCh, zap.NewNop())

	if len(stopCh) != 0 {
		t.Errorf("expected no signals for empty Redis, got %d", len(stopCh))
	}
}

func TestRecoverPendingStops_OneKey(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	stopCh := make(chan settler.StopSignal, 8)

	rdb.Set(ctx, "stop:sandbox:sb-crash-1", "insufficient_balance", 0) //nolint:errcheck

	recoverPendingStops(ctx, rdb, stopCh, zap.NewNop())

	if len(stopCh) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(stopCh))
	}
	sig := <-stopCh
	if sig.SandboxID != "sb-crash-1" {
		t.Errorf("SandboxID: got %q want sb-crash-1", sig.SandboxID)
	}
	if sig.Reason != "insufficient_balance" {
		t.Errorf("Reason: got %q want insufficient_balance", sig.Reason)
	}
}

func TestRecoverPendingStops_MultipleKeys(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	stopCh := make(chan settler.StopSignal, 16)

	pending := map[string]string{
		"stop:sandbox:sb-a": "insufficient_balance",
		"stop:sandbox:sb-b": "not_acknowledged",
		"stop:sandbox:sb-c": "insufficient_balance",
	}
	for key, reason := range pending {
		rdb.Set(ctx, key, reason, 0) //nolint:errcheck
	}

	recoverPendingStops(ctx, rdb, stopCh, zap.NewNop())

	if len(stopCh) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(stopCh))
	}

	// Collect and verify all signals
	got := map[string]string{}
	for len(stopCh) > 0 {
		sig := <-stopCh
		got[sig.SandboxID] = sig.Reason
	}
	for _, id := range []string{"sb-a", "sb-b", "sb-c"} {
		if _, ok := got[id]; !ok {
			t.Errorf("sandbox %q not recovered", id)
		}
	}
	if got["sb-b"] != "not_acknowledged" {
		t.Errorf("sb-b reason: got %q want not_acknowledged", got["sb-b"])
	}
}

func TestRecoverPendingStops_IgnoresUnrelatedKeys(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	stopCh := make(chan settler.StopSignal, 8)

	// Unrelated keys that must NOT be recovered
	rdb.Set(ctx, "billing:compute:sb-x", "session-data", 0) //nolint:errcheck
	rdb.Set(ctx, "nonce:some-uuid", "1", 0)                 //nolint:errcheck
	// One real stop key
	rdb.Set(ctx, "stop:sandbox:sb-real", "insufficient_balance", 0) //nolint:errcheck

	recoverPendingStops(ctx, rdb, stopCh, zap.NewNop())

	if len(stopCh) != 1 {
		t.Fatalf("expected 1 signal (only real stop key), got %d", len(stopCh))
	}
	sig := <-stopCh
	if sig.SandboxID != "sb-real" {
		t.Errorf("SandboxID: got %q want sb-real", sig.SandboxID)
	}
}

func TestRecoverPendingStops_ContextCancelled(t *testing.T) {
	rdb := newTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	// Zero-capacity channel: any send blocks unless ctx is cancelled
	stopCh := make(chan settler.StopSignal, 0)

	rdb.Set(context.Background(), "stop:sandbox:sb-block", "insufficient_balance", 0) //nolint:errcheck

	// Cancel before calling so the blocked send exits via ctx.Done()
	cancel()

	done := make(chan struct{})
	go func() {
		recoverPendingStops(ctx, rdb, stopCh, zap.NewNop())
		close(done)
	}()

	select {
	case <-done:
		// Good: returned promptly
	case <-time.After(500 * time.Millisecond):
		t.Error("recoverPendingStops did not respect context cancellation")
	}
}

// ── runStopHandler ────────────────────────────────────────────────────────────

func TestRunStopHandler_StopsAndCleansRedis(t *testing.T) {
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopCh := make(chan settler.StopSignal, 4)

	// Pre-populate both Redis keys that the handler should delete
	bg := context.Background()
	rdb.Set(bg, "billing:compute:sb-1", "session", 0)           //nolint:errcheck
	rdb.Set(bg, "stop:sandbox:sb-1", "insufficient_balance", 0) //nolint:errcheck

	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil)

	stopCh <- settler.StopSignal{SandboxID: "sb-1", Reason: "insufficient_balance"}

	waitKeyGone(t, rdb, "stop:sandbox:sb-1", time.Second)
	waitKeyGone(t, rdb, "billing:compute:sb-1", time.Second)

	ids := mock.stoppedIDs()
	if len(ids) != 1 || ids[0] != "sb-1" {
		t.Errorf("Daytona stopped: got %v want [sb-1]", ids)
	}
}

func TestRunStopHandler_DaytonaError_StillCleansRedis(t *testing.T) {
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	mock.failIDs["sb-err"] = true // simulate Daytona returning 500

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopCh := make(chan settler.StopSignal, 4)

	bg := context.Background()
	rdb.Set(bg, "billing:compute:sb-err", "session", 0)       //nolint:errcheck
	rdb.Set(bg, "stop:sandbox:sb-err", "not_acknowledged", 0) //nolint:errcheck

	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil)

	stopCh <- settler.StopSignal{SandboxID: "sb-err", Reason: "not_acknowledged"}

	// Even though Daytona errored, Redis must still be cleaned up
	waitKeyGone(t, rdb, "stop:sandbox:sb-err", time.Second)
	waitKeyGone(t, rdb, "billing:compute:sb-err", time.Second)
}

func TestRunStopHandler_MultipleSignals(t *testing.T) {
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopCh := make(chan settler.StopSignal, 8)

	bg := context.Background()
	for _, id := range []string{"sb-x", "sb-y", "sb-z"} {
		rdb.Set(bg, "stop:sandbox:"+id, "insufficient_balance", 0) //nolint:errcheck
	}

	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil)

	for _, id := range []string{"sb-x", "sb-y", "sb-z"} {
		stopCh <- settler.StopSignal{SandboxID: id, Reason: "insufficient_balance"}
	}

	for _, id := range []string{"sb-x", "sb-y", "sb-z"} {
		waitKeyGone(t, rdb, "stop:sandbox:"+id, time.Second)
	}

	ids := mock.stoppedIDs()
	sort.Strings(ids)
	want := []string{"sb-x", "sb-y", "sb-z"}
	for i, w := range want {
		if ids[i] != w {
			t.Errorf("stopped[%d]: got %q want %q", i, ids[i], w)
		}
	}
}

func TestRunStopHandler_ContextCancel_Exits(t *testing.T) {
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	ctx, cancel := context.WithCancel(context.Background())
	stopCh := make(chan settler.StopSignal)

	done := make(chan struct{})
	go func() {
		runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil)
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// Good
	case <-time.After(500 * time.Millisecond):
		t.Error("runStopHandler did not exit after context cancellation")
	}
}
