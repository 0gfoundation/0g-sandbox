package alert

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func newWebhook(t *testing.T) (*Webhook, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return NewWebhook("", "0xPROV", rdb, time.Hour, zap.NewNop()), rdb, mr
}

func TestWebhook_NoURL_PersistsButNoDispatch(t *testing.T) {
	w, rdb, mr := newWebhook(t)
	defer mr.Close()
	w.Notify(context.Background(), KindSettlerNoBalance, SeverityCritical, "test", map[string]any{"k": "v"})

	hist, err := History(context.Background(), rdb, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("history len: got %d want 1", len(hist))
	}
	if hist[0].Kind != KindSettlerNoBalance {
		t.Errorf("kind: got %q", hist[0].Kind)
	}
	if hist[0].Details["k"] != "v" {
		t.Errorf("details lost: %+v", hist[0].Details)
	}
}

func TestHistory_NewestFirst(t *testing.T) {
	w, rdb, mr := newWebhook(t)
	defer mr.Close()
	w.Notify(context.Background(), KindSettlerLowBalance, SeverityWarning, "a", nil)
	w.Notify(context.Background(), KindSettlerNoBalance, SeverityCritical, "b", nil)
	w.Notify(context.Background(), KindQueueBacklog, SeverityWarning, "c", nil)

	hist, _ := History(context.Background(), rdb, 10)
	if len(hist) != 3 {
		t.Fatalf("len: %d", len(hist))
	}
	// LPUSH semantics: newest is index 0
	if hist[0].Message != "c" || hist[2].Message != "a" {
		t.Errorf("order wrong: %+v", hist)
	}
}

func TestHistory_BoundedToMaxLen(t *testing.T) {
	w, rdb, mr := newWebhook(t)
	defer mr.Close()
	for i := 0; i < HistoryMaxLen+50; i++ {
		w.Notify(context.Background(), KindQueueBacklog, SeverityWarning, "x", nil)
	}
	llen, _ := rdb.LLen(context.Background(), HistoryKey).Result()
	if llen != int64(HistoryMaxLen) {
		t.Errorf("history not bounded: got %d want %d", llen, HistoryMaxLen)
	}
}

func TestWebhook_DispatchesPayload(t *testing.T) {
	var captured atomic.Pointer[payload]
	var wg sync.WaitGroup
	wg.Add(1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		body, _ := io.ReadAll(r.Body)
		var p payload
		if err := json.Unmarshal(body, &p); err != nil {
			t.Errorf("unmarshal: %v", err)
		}
		captured.Store(&p)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	w := NewWebhook(srv.URL, "0xPROV", rdb, time.Hour, zap.NewNop())

	w.Notify(context.Background(), KindSettlerNoBalance, SeverityCritical, "out of gas", map[string]any{"factor": 1})
	wg.Wait()

	got := captured.Load()
	if got == nil {
		t.Fatal("no payload captured")
	}
	if got.Kind != KindSettlerNoBalance {
		t.Errorf("kind: got %q want %q", got.Kind, KindSettlerNoBalance)
	}
	if got.Severity != SeverityCritical {
		t.Errorf("severity: got %q", got.Severity)
	}
	if got.Provider != "0xPROV" {
		t.Errorf("provider: got %q", got.Provider)
	}
	if got.Message != "out of gas" {
		t.Errorf("message: got %q", got.Message)
	}
	if got.Timestamp == "" {
		t.Error("timestamp missing")
	}
}

func TestWebhook_DedupSuppressesSameKind(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	w := NewWebhook(srv.URL, "0xPROV", rdb, time.Hour, zap.NewNop())

	for i := 0; i < 5; i++ {
		w.Notify(context.Background(), KindQueueBacklog, SeverityWarning, "depth high", nil)
	}
	// Give async goroutines a chance.
	time.Sleep(200 * time.Millisecond)

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Errorf("hits: got %d want 1 (dedup must suppress)", got)
	}
}

func TestWebhook_DifferentKinds_NotDeduped(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	w := NewWebhook(srv.URL, "0xPROV", rdb, time.Hour, zap.NewNop())

	w.Notify(context.Background(), KindSettlerNoBalance, SeverityCritical, "1", nil)
	w.Notify(context.Background(), KindVoucherRejected, SeverityCritical, "2", nil)
	time.Sleep(200 * time.Millisecond)

	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("hits: got %d want 2 (different kinds must not be deduped)", got)
	}
}

func TestWebhook_DedupExpires(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	// 100ms dedup window so we can test expiry without sleeping forever.
	w := NewWebhook(srv.URL, "0xPROV", rdb, 100*time.Millisecond, zap.NewNop())

	w.Notify(context.Background(), KindQueueBacklog, SeverityWarning, "1", nil)
	time.Sleep(50 * time.Millisecond)
	w.Notify(context.Background(), KindQueueBacklog, SeverityWarning, "2", nil)
	// FastForward miniredis past the TTL.
	mr.FastForward(200 * time.Millisecond)
	w.Notify(context.Background(), KindQueueBacklog, SeverityWarning, "3", nil)
	time.Sleep(200 * time.Millisecond)

	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Errorf("hits: got %d want 2 (first should fire, second deduped, third fires after expiry)", got)
	}
}

func TestClassifyChainErr(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("insufficient funds for transfer"), "low_funds"},
		{errors.New("nonce too low"), "nonce_drift"},
		{errors.New("context deadline exceeded"), "timeout"},
		{errors.New("dial tcp: connection refused"), "rpc_unreachable"},
		{errors.New("unexpected EOF"), "rpc_unreachable"},
		{errors.New("some other thing"), "other"},
	}
	for _, c := range cases {
		got := ClassifyChainErr(c.err)
		if got != c.want {
			t.Errorf("ClassifyChainErr(%v): got %q want %q", c.err, got, c.want)
		}
	}
}

func TestNop_NoOp(t *testing.T) {
	// Should not panic and should not require a context check or anything.
	Nop{}.Notify(context.Background(), KindQueueBacklog, SeverityWarning, "", nil)
}
