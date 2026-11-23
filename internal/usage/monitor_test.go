package usage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

type fakeRunner struct {
	snap *Snapshot
	err  error
}

func (f *fakeRunner) Fetch(context.Context) (*Snapshot, error) { return f.snap, f.err }

type fakeLister struct{ sandboxes []daytona.Sandbox }

func (f *fakeLister) ListSandboxes(context.Context) ([]daytona.Sandbox, error) {
	return f.sandboxes, nil
}

type alertCall struct {
	kind    alert.Kind
	sev     alert.Severity
	details map[string]any
}

type recAlerter struct {
	mu    sync.Mutex
	calls []alertCall
}

func (r *recAlerter) Notify(_ context.Context, kind alert.Kind, sev alert.Severity, _ string, details map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, alertCall{kind, sev, details})
}

func (r *recAlerter) of(kind alert.Kind) []alertCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []alertCall
	for _, c := range r.calls {
		if c.kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// harness: sandbox sb-1 requests 10 GiB on a 100 GiB volume. queueStop sets
// the stop marker as settler.QueueStop does; finishStop plays the stop
// handler.
type harness struct {
	m      *Monitor
	runner *fakeRunner
	rdb    *redis.Client
	alerts *recAlerter
	stops  []string
	now    time.Time
}

func newHarness(t *testing.T, enforce bool) *harness {
	t.Helper()
	h := &harness{
		runner: &fakeRunner{},
		rdb:    redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()}),
		alerts: &recAlerter{},
		now:    time.Unix(1_800_000_000, 0),
	}
	h.m = NewMonitor(Config{
		Enforce: enforce, WarnFreeBytes: GiB, Grace: 15 * time.Minute,
		VolumeAlertPct: 80, VolumeCriticalPct: 95, OOMKillsPerHour: 3, CPUPinnedAfter: 6 * time.Hour,
	}, h.runner, &fakeLister{[]daytona.Sandbox{{ID: "sb-1", Disk: 10}}}, h.rdb, h.alerts,
		func(ctx context.Context, id string) error {
			h.stops = append(h.stops, id)
			return h.rdb.SetNX(ctx, "stop:sandbox:"+id, ReasonDiskOverSize, 0).Err()
		}, zap.NewNop())
	h.m.now = func() time.Time { return h.now }
	return h
}

// read makes the runner report sb-1 at the current time.
func (h *harness) read(running bool, layerGiB, volumeUsedGiB float64) {
	used := int64(layerGiB * float64(GiB))
	h.runner.snap = &Snapshot{
		RefreshedAt: h.now,
		Volume:      &Volume{SizeBytes: uint64(100 * GiB), UsedBytes: uint64(volumeUsedGiB * float64(GiB))},
		Sandboxes:   map[string]*Sandbox{"sb-1": {Running: running, LayerBytes: &used, LayerMeasuredAt: h.now}},
	}
}

func (h *harness) poll() { h.m.poll(context.Background()) }

// finishStop plays the stop handler completing the queued stop.
func (h *harness) finishStop(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	h.m.OnStopped(ctx, "sb-1")
	h.rdb.Del(ctx, "stop:sandbox:sb-1")
	if rec := h.record(t); rec == nil || rec.Phase != PhaseStopped {
		t.Fatalf("after the stop: %+v, want phase stopped", rec)
	}
}

// ownerStarts plays the proxy's call after the owner's start.
func (h *harness) ownerStarts(t *testing.T) {
	t.Helper()
	h.m.OnStarted(context.Background(), "sb-1")
}

func (h *harness) record(t *testing.T) *DiskStop {
	t.Helper()
	rec, err := GetDiskStop(context.Background(), h.rdb, "sb-1")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestDisk_NotEnforced_AlertsOnly(t *testing.T) {
	h := newHarness(t, false)
	h.read(true, 11, 50)
	h.poll()
	if len(h.stops) != 0 || h.record(t) != nil {
		t.Errorf("stopped %v / recorded without enforcement", h.stops)
	}
	a := h.alerts.of(alert.KindSandboxDiskOverSize)
	if len(a) != 1 || a[0].details["sandbox"] != "sb-1" || a[0].details["enforced"] != false {
		t.Errorf("over-size alert = %+v", a)
	}
}

func TestDisk_StopsPastSizeOnceAndRecordsWhy(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 9.5, 50) // under 10 GiB
	h.poll()
	if len(h.stops) != 0 {
		t.Fatal("stopped under its size")
	}

	h.read(true, 10.5, 50)
	h.poll()
	if len(h.stops) != 1 {
		t.Fatalf("stops = %v, want one", h.stops)
	}
	rec := h.record(t)
	if rec == nil || rec.Phase != PhaseQueued || rec.UsedBytes != int64(10.5*float64(GiB)) || rec.RequestedBytes != 10*GiB {
		t.Errorf("record = %+v", rec)
	}

	h.now = h.now.Add(time.Minute) // stop still queued
	h.read(true, 10.6, 50)
	h.poll()
	if len(h.stops) != 1 {
		t.Errorf("stop queued twice while the first is pending: %v", h.stops)
	}

	h.rdb.Del(context.Background(), "stop:sandbox:sb-1") // the stop didn't take
	h.now = h.now.Add(time.Minute)
	h.read(true, 10.6, 50)
	h.poll()
	if len(h.stops) != 2 {
		t.Errorf("a stop that didn't take was not queued again: %v", h.stops)
	}
}

// Stopped, the sandbox waits for its owner's start: a reading that still shows
// it running (taken before the stop) neither stops it again nor starts a grace.
// After a restore the writable layer restarts near 0; the data carried over
// from the backup still counts.
func TestDisk_CountsDataCarriedOverARestore(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 0.001, 50) // writable layer: 1 MiB since the restore
	data := int64(10.5 * float64(GiB))
	h.runner.snap.Sandboxes["sb-1"].DataBytes = &data
	h.poll()
	if len(h.stops) != 1 {
		t.Errorf("a restored sandbox over its size on its carried data was not stopped: %v", h.stops)
	}
	if r := h.m.Report(context.Background(), "sb-1", 10); *r.Disk.UsedBytes != data || r.Disk.Status != "over" {
		t.Errorf("report used %d, status %s; want the carried data", *r.Disk.UsedBytes, r.Disk.Status)
	}
}

func TestDisk_StoppedWaitsForTheOwnersStart(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 11, 50)
	h.poll()
	h.finishStop(t)

	h.now = h.now.Add(time.Hour)
	h.read(true, 11, 50)
	h.poll()
	if rec := h.record(t); len(h.stops) != 1 || rec.Phase != PhaseStopped || rec.GraceUntil != nil {
		t.Errorf("acted without the owner's start: stops %v, record %+v", h.stops, rec)
	}
}

func TestDisk_GracePeriodAfterTheOwnersStart(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 11, 50)
	h.poll()
	h.finishStop(t)

	h.now = h.now.Add(time.Hour)
	h.ownerStarts(t)
	rec := h.record(t)
	if rec.Phase != PhaseGrace || !rec.GraceStartedAt.Equal(h.now) || !rec.GraceUntil.Equal(h.now.Add(15*time.Minute)) {
		t.Fatalf("grace not begun by the start: %+v", rec)
	}

	h.now = h.now.Add(14 * time.Minute)
	h.read(true, 10.8, 50)
	h.poll()
	if len(h.stops) != 1 {
		t.Fatal("stopped during its grace period")
	}

	h.now = h.now.Add(2 * time.Minute)
	h.read(true, 10.8, 50)
	h.poll()
	if len(h.stops) != 2 {
		t.Fatalf("still over after the grace period but not stopped: %v", h.stops)
	}
	if rec := h.record(t); rec.Phase != PhaseQueued || rec.GraceUntil != nil {
		t.Errorf("second stop should start a fresh record: %+v", rec)
	}
}

// A grace shorter than the runner's layer-scan interval can end before the
// layer is measured again: the files the owner deleted during it must count.
func TestDisk_AfterGraceWaitsForAFreshLayerSize(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 11, 50)
	h.poll()
	h.finishStop(t)
	h.now = h.now.Add(time.Hour)
	h.ownerStarts(t)
	graceStart := h.now

	h.now = h.now.Add(16 * time.Minute) // grace over; the last layer scan predates it
	h.read(true, 11, 50)
	h.runner.snap.Sandboxes["sb-1"].LayerMeasuredAt = graceStart.Add(-time.Second)
	h.poll()
	if len(h.stops) != 1 {
		t.Fatalf("stopped again on a layer size measured before the grace: %v", h.stops)
	}

	h.now = h.now.Add(time.Minute)
	h.read(true, 11, 50) // measured now, still over
	h.poll()
	if len(h.stops) != 2 {
		t.Errorf("still over on a fresh measurement after the grace, not stopped: %v", h.stops)
	}
}

func TestDisk_CriticalVolumeOverridesGrace(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 11, 50)
	h.poll()
	h.finishStop(t)
	h.now = h.now.Add(time.Hour)
	h.ownerStarts(t)
	h.read(true, 11, 96) // the shared volume is 96 % full
	h.poll()
	if len(h.stops) != 2 {
		t.Errorf("not stopped with the shared volume critically full: %v", h.stops)
	}
}

func TestDisk_CleanedUpDuringGraceClearsRecord(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 11, 50)
	h.poll()
	h.finishStop(t)
	h.now = h.now.Add(time.Hour)
	h.ownerStarts(t)

	h.read(true, 6, 50)
	h.runner.snap.Sandboxes["sb-1"].LayerMeasuredAt = h.now.Add(-time.Second) // before the grace
	h.poll()
	if h.record(t) == nil {
		t.Fatal("cleared on a size measured before the grace")
	}

	h.now = h.now.Add(time.Minute)
	h.read(true, 6, 50)
	h.poll()
	if h.record(t) != nil {
		t.Error("record kept after the owner got back under the size")
	}
}

func TestOnStarted_OnlyFromStopped(t *testing.T) {
	h := newHarness(t, true)
	h.ownerStarts(t) // no record: nothing to do
	if h.record(t) != nil {
		t.Fatal("a start created a record")
	}
	h.read(true, 11, 50)
	h.poll() // queued
	h.ownerStarts(t)
	if rec := h.record(t); rec.Phase != PhaseQueued {
		t.Errorf("a start moved a queued stop to %s", rec.Phase)
	}
}

func TestGetDiskStop_RecordsWithoutPhase(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	for raw, want := range map[string]DiskPhase{
		`{"queued_at":"2026-11-23T00:00:00Z"}`:                                     PhaseQueued,
		`{"queued_at":"2026-11-23T00:00:00Z","stopped_at":"2026-11-23T00:01:00Z"}`: PhaseStopped,
		`{"queued_at":"2026-11-23T00:00:00Z","stopped_at":"2026-11-23T00:01:00Z","grace_started_at":"2026-11-23T01:00:00Z","grace_until":"2026-11-23T01:15:00Z"}`: PhaseGrace,
	} {
		h.rdb.Set(ctx, diskStopKey("sb-1"), raw, 0)
		if rec := h.record(t); rec.Phase != want {
			t.Errorf("%s → %s, want %s", raw, rec.Phase, want)
		}
	}
}

func TestDisk_IgnoresStaleLayerAndStoppedSandbox(t *testing.T) {
	h := newHarness(t, true)
	h.read(true, 11, 50)
	h.runner.snap.Sandboxes["sb-1"].LayerMeasuredAt = h.now.Add(-20 * time.Minute)
	h.poll()
	if len(h.stops) != 0 || len(h.alerts.of(alert.KindSandboxDiskOverSize)) != 0 {
		t.Error("acted on a layer size 20 minutes old")
	}

	h.read(false, 11, 50)
	h.poll()
	if len(h.stops) != 0 {
		t.Error("queued a stop for a sandbox that is not running")
	}
	if len(h.alerts.of(alert.KindSandboxDiskOverSize)) != 1 {
		t.Error("no alert for a stopped sandbox past its size")
	}
}

func TestVolumeAlert(t *testing.T) {
	h := newHarness(t, false)
	h.read(true, 1, 79)
	h.poll()
	h.read(true, 1, 85)
	h.poll()
	h.read(true, 1, 96)
	h.poll()
	a := h.alerts.of(alert.KindDiskVolumeHigh)
	if len(a) != 2 || a[0].sev != alert.SeverityWarning || a[1].sev != alert.SeverityCritical {
		t.Errorf("volume alerts = %+v", a)
	}
}

func TestOOMAlert_KillsWithinAnHour(t *testing.T) {
	h := newHarness(t, false)
	step := func(d time.Duration, kills uint64) {
		h.now = h.now.Add(d)
		h.read(true, 1, 10)
		h.runner.snap.Sandboxes["sb-1"].OOMKillsTotal = &kills
		h.poll()
	}
	step(0, 5)
	step(10*time.Minute, 7)
	if len(h.alerts.of(alert.KindSandboxOOMKills)) != 0 {
		t.Fatal("alerted after 2 kills")
	}
	step(10*time.Minute, 8)
	if len(h.alerts.of(alert.KindSandboxOOMKills)) != 1 {
		t.Fatal("no alert after 3 kills within an hour")
	}
	step(2*time.Hour, 9) // the old kills have aged out
	step(time.Minute, 0) // container recreated: counter reset
	step(time.Minute, 1)
	if n := len(h.alerts.of(alert.KindSandboxOOMKills)); n != 1 {
		t.Errorf("alerts = %d, want still 1", n)
	}
}

// A sandbox killed at its cap right after it starts, before the Monitor first
// sees it, is still counted: its counter began with its container.
func TestOOMAlert_KillsBeforeFirstSight(t *testing.T) {
	h := newHarness(t, false)
	h.runner.snap = &Snapshot{RefreshedAt: h.now, Sandboxes: map[string]*Sandbox{}} // not there yet
	h.poll()
	h.now = h.now.Add(time.Minute)
	h.read(true, 1, 10)
	kills := uint64(3)
	h.runner.snap.Sandboxes["sb-1"].OOMKillsTotal = &kills
	h.poll()
	if len(h.alerts.of(alert.KindSandboxOOMKills)) != 1 {
		t.Error("kills before the first sight of a new sandbox not counted")
	}

	// On the Monitor's own first reading the history is unknown: no alert.
	h2 := newHarness(t, false)
	h2.read(true, 1, 10)
	h2.runner.snap.Sandboxes["sb-1"].OOMKillsTotal = &kills
	h2.poll()
	if len(h2.alerts.of(alert.KindSandboxOOMKills)) != 0 {
		t.Error("alerted on kills of unknown age at the Monitor's first reading")
	}
}

func TestCPUPinnedAlert(t *testing.T) {
	h := newHarness(t, false)
	seconds := 0.0
	step := func(d time.Duration, cores float64) {
		h.now = h.now.Add(d)
		seconds += cores * d.Seconds()
		h.read(true, 1, 10)
		s := seconds
		h.runner.snap.Sandboxes["sb-1"].CPUSecondsTotal = &s
		h.runner.snap.Sandboxes["sb-1"].CPULimitCores = 2
		h.poll()
	}
	step(0, 0)
	for i := 0; i < 5; i++ {
		step(time.Hour, 1.99)
	}
	step(time.Hour, 1.0) // dropped: resets
	for i := 0; i < 5; i++ {
		step(time.Hour, 2)
	}
	if len(h.alerts.of(alert.KindSandboxCPUPinned)) != 0 {
		t.Fatal("alerted before 6 hours pinned")
	}
	step(time.Hour, 2)
	if len(h.alerts.of(alert.KindSandboxCPUPinned)) != 1 {
		t.Error("no alert after 6 hours pinned")
	}
}

func TestUnavailable_AlertsAfterFiveMinutes(t *testing.T) {
	h := newHarness(t, true)
	h.runner.err = errors.New("connection refused")
	h.poll()
	h.now = h.now.Add(4 * time.Minute)
	h.poll()
	if len(h.alerts.of(alert.KindUsageUnavailable)) != 0 {
		t.Fatal("alerted before 5 minutes")
	}
	h.now = h.now.Add(time.Minute)
	h.poll()
	if a := h.alerts.of(alert.KindUsageUnavailable); len(a) != 1 || a[0].sev != alert.SeverityCritical {
		t.Fatalf("unavailable alerts = %+v", a)
	}

	// A stale reading counts as unavailable, and is not acted on.
	h.runner.err = nil
	h.read(true, 20, 50)
	h.runner.snap.RefreshedAt = h.now.Add(-10 * time.Minute)
	h.poll()
	if len(h.stops) != 0 || len(h.alerts.of(alert.KindUsageUnavailable)) != 2 {
		t.Errorf("stale reading: stops %v, unavailable alerts %d", h.stops, len(h.alerts.of(alert.KindUsageUnavailable)))
	}
}

func TestReport(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()

	r := h.m.Report(ctx, "sb-1", 10)
	if r.Measured || r.Disk.Status != "unknown" || r.Disk.RequestedBytes != 10*GiB || !r.DiskEnforced {
		t.Errorf("before any reading: %+v", r)
	}

	for _, tc := range []struct {
		layerGiB float64
		status   string
	}{{5, "ok"}, {9.5, "near_full"}, {10.5, "over"}} {
		h.read(true, tc.layerGiB, 50)
		h.runner.snap.Sandboxes["sb-1"].MemoryLimitBytes = 4 << 30
		h.m.poll(ctx)
		r := h.m.Report(ctx, "sb-1", 10)
		if !r.Measured || r.Disk.Status != tc.status || *r.Disk.UsedBytes != int64(tc.layerGiB*float64(GiB)) {
			t.Errorf("%v GiB: status %q, report %+v", tc.layerGiB, r.Disk.Status, r.Disk)
		}
		if r.Memory == nil || r.Memory.LimitBytes != 4<<30 {
			t.Errorf("memory = %+v", r.Memory)
		}
	}
	r = h.m.Report(ctx, "sb-1", 10)
	if r.DiskStop == nil || r.DiskStop.Phase != PhaseQueued || r.DiskStop.RequestedBytes != 10*GiB {
		t.Errorf("disk stop missing from the report: %+v", r.DiskStop)
	}
	if r := h.m.Report(ctx, "sb-other", 5); r.Measured || r.Disk.Status != "unknown" {
		t.Errorf("unmeasured sandbox: %+v", r)
	}
}

// The runner's readings must cover every running sandbox, or its disk goes
// unchecked without a word: absent for 5 minutes, or without a layer size for
// 15, alerts.
func TestCoverage_AlertsOnRunningSandboxesTheRunnerMisses(t *testing.T) {
	h := newHarness(t, true)
	h.m.lister = &fakeLister{[]daytona.Sandbox{
		{ID: "sb-1", Disk: 10, State: "started"},
		{ID: "sb-gone", Disk: 10, State: "started"},    // absent from the readings
		{ID: "sb-nolayer", Disk: 10, State: "started"}, // never sized
		{ID: "sb-stopped", Disk: 10, State: "stopped"}, // not running: not expected
	}}
	step := func(d time.Duration) {
		h.now = h.now.Add(d)
		h.read(true, 1, 10)
		h.runner.snap.Sandboxes["sb-nolayer"] = &Sandbox{Running: true}
		h.poll()
	}
	step(0)
	step(4 * time.Minute)
	if len(h.alerts.of(alert.KindUsageIncomplete)) != 0 {
		t.Fatal("alerted within 5 minutes")
	}
	step(time.Minute)
	a := h.alerts.of(alert.KindUsageIncomplete)
	if len(a) != 1 || len(a[0].details["sandboxes"].([]string)) != 1 || a[0].details["sandboxes"].([]string)[0] != "sb-gone" {
		t.Fatalf("at 5 minutes: %+v, want sb-gone only", a)
	}
	step(10 * time.Minute)
	a = h.alerts.of(alert.KindUsageIncomplete)
	if got := a[len(a)-1].details["sandboxes"].([]string); len(got) != 2 || got[0] != "sb-gone" || got[1] != "sb-nolayer" {
		t.Errorf("at 15 minutes: %v, want sb-gone and sb-nolayer", got)
	}
}
