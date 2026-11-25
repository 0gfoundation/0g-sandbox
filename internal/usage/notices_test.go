package usage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/0gfoundation/0g-sandbox/internal/notice"
)

type fakePusher struct {
	sets     []notice.Set
	validFor time.Duration
	err      error
}

func (f *fakePusher) PushNotices(_ context.Context, set notice.Set, validFor time.Duration) error {
	f.sets, f.validFor = append(f.sets, set), validFor
	return f.err
}

func (f *fakePusher) last(t *testing.T) notice.Set {
	t.Helper()
	if len(f.sets) == 0 {
		t.Fatal("no notices sent")
	}
	return f.sets[len(f.sets)-1]
}

type fakeSource struct {
	set     notice.Set
	err     error
	running []string
}

func (f *fakeSource) Notices(_ context.Context, running []string) (notice.Set, error) {
	f.running = running
	return f.set, f.err
}

// withNotices makes sb-1 running in Daytona's list and turns notices on.
func withNotices(h *harness, sources ...notice.Source) *fakePusher {
	p := &fakePusher{}
	h.m.lister = &fakeLister{[]daytona.Sandbox{{ID: "sb-1", Disk: 10, State: "started"}, {ID: "sb-off", Disk: 10, State: "stopped"}}}
	h.m.EnableNotices(p, sources...)
	return p
}

func onlyNotice(t *testing.T, set notice.Set, id string) notice.Notice {
	t.Helper()
	if len(set[id]) != 1 {
		t.Fatalf("notices of %s = %+v, want one", id, set[id])
	}
	return set[id][0]
}

func TestNotices_DiskNearFull(t *testing.T) {
	h := newHarness(t, true)
	p := withNotices(h)
	h.read(true, 5, 20)
	h.poll()
	if s := p.last(t); len(s) != 0 {
		t.Errorf("at 5 of 10 GiB: %+v, want none", s)
	}
	if p.validFor != 5*time.Minute {
		t.Errorf("validity = %s, want 5m (3 polls, at least 5 min)", p.validFor)
	}

	h.read(true, 9.5, 20)
	h.poll()
	n := onlyNotice(t, p.last(t), "sb-1")
	if n.Kind != notice.KindDiskNearFull || n.Severity != notice.SeverityWarning || n.Action != notice.ActionStop || n.Deadline != nil ||
		!strings.Contains(n.Message, "9.5 GiB") || !strings.Contains(n.Message, "512 MiB is left") || !strings.Contains(n.Message, "Past 10.0 GiB this provider stops the sandbox") {
		t.Errorf("near full: %+v", n)
	}
	if r := h.m.Report(context.Background(), "sb-1", 10); len(r.Notices) != 1 || r.Notices[0].Kind != notice.KindDiskNearFull {
		t.Errorf("report notices = %+v", r.Notices)
	}
}

func TestNotices_DiskOverSize(t *testing.T) {
	// Not enforced: told, with no deadline.
	h := newHarness(t, false)
	p := withNotices(h)
	h.read(true, 12, 20)
	h.poll()
	if n := onlyNotice(t, p.last(t), "sb-1"); n.Kind != notice.KindDiskOverSize || n.Severity != notice.SeverityWarning || n.Deadline != nil ||
		!strings.Contains(n.Message, "does not stop sandboxes for that yet") {
		t.Errorf("not enforced: %+v", n)
	}

	// Enforced: being stopped now.
	h = newHarness(t, true)
	p = withNotices(h)
	h.read(true, 12, 20)
	h.poll()
	n := onlyNotice(t, p.last(t), "sb-1")
	if n.Severity != notice.SeverityCritical || n.Action != notice.ActionStop || n.Deadline == nil || !n.Deadline.Equal(h.now) ||
		!strings.Contains(n.Message, "is being stopped") || !strings.Contains(n.Message, "it has 15m0s to get back under 10.0 GiB") {
		t.Errorf("enforced: %+v", n)
	}

	// Started again: the grace's end is the deadline.
	h.finishStop(t)
	h.ownerStarts(t)
	h.now = h.now.Add(time.Minute)
	h.read(true, 12, 20)
	h.poll()
	n = onlyNotice(t, p.last(t), "sb-1")
	if rec := h.record(t); n.Deadline == nil || !n.Deadline.Equal(*rec.GraceUntil) || n.Action != notice.ActionStop ||
		!strings.Contains(n.Message, "Unless it is back under 10.0 GiB by") {
		t.Errorf("in grace: %+v (record %+v)", n, rec)
	}
}

func TestNotices_SourcesMergedForRunningSandboxes(t *testing.T) {
	h := newHarness(t, true)
	deadline := h.now.Add(time.Hour)
	bal := &fakeSource{set: notice.Set{"sb-1": {{Kind: notice.KindBalanceLow, Severity: notice.SeverityWarning, Message: "m", Deadline: &deadline}}}}
	broken := &fakeSource{err: errors.New("rpc down")}
	p := withNotices(h, bal, broken)
	h.read(true, 9.5, 20)
	h.poll()
	if len(bal.running) != 1 || bal.running[0] != "sb-1" {
		t.Errorf("sources asked about %v, want the running sb-1 only", bal.running)
	}
	if ns := p.last(t)["sb-1"]; len(ns) != 2 || ns[0].Kind != notice.KindDiskNearFull || ns[1].Kind != notice.KindBalanceLow {
		t.Errorf("sb-1 = %+v, want disk then balance (a failing source skipped)", ns)
	}
}

func TestNotices_OffUnlessEnabled(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 12, 20)
	h.poll() // no pusher: nothing to send to, and no panic
	if r := h.m.Report(context.Background(), "sb-1", 10); r.Notices != nil {
		t.Errorf("report notices = %+v, want none", r.Notices)
	}
}

func TestRunnerClient_PushNotices(t *testing.T) {
	var got struct {
		ValidForSec int64      `json:"valid_for_sec"`
		Sandboxes   notice.Set `json:"sandboxes"`
	}
	status := http.StatusNoContent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/sandbox-notices" || r.Header.Get("Authorization") != "Bearer runner-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"message":"no"}`))
	}))
	defer srv.Close()

	c := NewRunnerClient(srv.URL, "runner-token")
	set := notice.Set{"sb-1": {{Kind: notice.KindMaintenance, Severity: notice.SeverityInfo, Message: "m"}}}
	if err := c.PushNotices(context.Background(), set, 3*time.Minute); err != nil {
		t.Fatal(err)
	}
	if got.ValidForSec != 180 || len(got.Sandboxes["sb-1"]) != 1 {
		t.Errorf("sent %+v", got)
	}
	if err := c.PushNotices(context.Background(), nil, time.Minute); err != nil || got.Sandboxes == nil {
		t.Errorf("empty set: err = %v, sandboxes = %v; want an empty object", err, got.Sandboxes)
	}
	status = http.StatusNotImplemented
	if err := c.PushNotices(context.Background(), set, time.Minute); err == nil || !strings.Contains(err.Error(), "501") {
		t.Errorf("runner refusing: err = %v", err)
	}
}
