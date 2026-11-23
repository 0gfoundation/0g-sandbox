package usage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

// Config tunes the Monitor. Zero values disable the corresponding alert.
type Config struct {
	Interval time.Duration
	// Enforce stops a running sandbox that has written past its requested disk
	// size. Off, the Monitor only alerts and reports.
	Enforce bool
	// WarnFreeBytes marks a sandbox's disk "near_full" in the usage report
	// once less than this is left of its requested size.
	WarnFreeBytes int64
	// Grace is how long an owner has, after starting a sandbox that was
	// stopped for disk, to delete files before it is stopped again.
	Grace             time.Duration
	VolumeAlertPct    float64
	VolumeCriticalPct float64
	// CreateBlockPct refuses new sandboxes while the shared volume is at least
	// this full (AdmitCreate). 0 never refuses.
	CreateBlockPct float64
	// OOMKillsPerHour alerts when a sandbox has this many OOM kills within an
	// hour.
	OOMKillsPerHour int
	// CPUPinnedAfter alerts when a sandbox has used at least 95 % of its CPU
	// quota for this long.
	CPUPinnedAfter time.Duration
}

const (
	// Older runner readings are not acted on.
	maxSnapshotAge = 5 * time.Minute
	// Nor are older layer sizes: a sandbox may have deleted files since.
	maxLayerAge = 15 * time.Minute
	// Alert once the runner's readings have been unusable this long.
	unavailableAlertAfter = 5 * time.Minute
	cpuPinnedUtilization  = 0.95
)

// ErrVolumeFull is what AdmitCreate returns, wrapped, while the shared volume
// is too full for new sandboxes.
var ErrVolumeFull = errors.New("the shared volume is too full for new sandboxes")

// Fetcher reads the runner's metrics.
type Fetcher interface {
	Fetch(ctx context.Context) (*Snapshot, error)
}

// SandboxLister lists Daytona's sandboxes, for their requested disk sizes.
type SandboxLister interface {
	ListSandboxes(ctx context.Context) ([]daytona.Sandbox, error)
}

// Monitor polls the runner's per-sandbox metrics, alerts, enforces disk sizes
// and serves usage reports.
type Monitor struct {
	cfg       Config
	runner    Fetcher
	lister    SandboxLister
	rdb       *redis.Client
	alerter   alert.Alerter
	queueStop func(ctx context.Context, sandboxID string) error
	log       *zap.Logger
	now       func() time.Time

	mu           sync.Mutex
	latest       *Snapshot // never modified once stored
	failingSince time.Time
	oom          map[string][]oomSample
	cpu          map[string]*cpuTrack
	// prevAt and prevRunning describe the previous reading checkOOMAndCPU
	// saw: a sandbox not running in it has started its container since.
	prevAt      time.Time
	prevRunning map[string]bool
	// uncovered: running sandboxes the runner's readings miss, since when.
	uncovered map[string]time.Time
}

type oomSample struct {
	at    time.Time
	kills uint64
}

type cpuTrack struct {
	at          time.Time // runner reading time
	seconds     float64
	pinnedSince time.Time
}

// NewMonitor wires a Monitor. queueStop must stop the sandbox through the
// billing stop path with reason ReasonDiskOverSize.
func NewMonitor(cfg Config, runner Fetcher, lister SandboxLister, rdb *redis.Client, alerter alert.Alerter,
	queueStop func(ctx context.Context, sandboxID string) error, log *zap.Logger) *Monitor {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if alerter == nil {
		alerter = alert.Nop{}
	}
	return &Monitor{
		cfg: cfg, runner: runner, lister: lister, rdb: rdb, alerter: alerter, queueStop: queueStop, log: log,
		now: time.Now, oom: map[string][]oomSample{}, cpu: map[string]*cpuTrack{}, uncovered: map[string]time.Time{},
	}
}

// OnStopped is called by the stop handler after every completed stop,
// whatever its reason: a sandbox stopped for disk enters PhaseStopped.
func (m *Monitor) OnStopped(ctx context.Context, sandboxID string) {
	if err := markStopped(ctx, m.rdb, sandboxID, m.now()); err != nil {
		m.log.Warn("usage: record stop", zap.String("sandbox", sandboxID), zap.Error(err))
	}
}

// OnStarted is called after the owner starts a sandbox. One stopped for its
// disk size then gets Config.Grace to delete files.
func (m *Monitor) OnStarted(ctx context.Context, sandboxID string) {
	began, err := beginGrace(ctx, m.rdb, sandboxID, m.now(), m.cfg.Grace)
	if err != nil {
		m.log.Warn("usage: begin grace", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	if began {
		m.log.Info("usage: sandbox stopped for disk started again; grace period begins",
			zap.String("sandbox", sandboxID), zap.Duration("grace", m.cfg.Grace))
	}
}

// Run polls until ctx is done.
func (m *Monitor) Run(ctx context.Context) {
	t := time.NewTicker(m.cfg.Interval)
	defer t.Stop()
	for {
		m.poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Monitor) poll(ctx context.Context) {
	now := m.now()
	snap, err := m.runner.Fetch(ctx)
	if err == nil && (snap.RefreshedAt.IsZero() || now.Sub(snap.RefreshedAt) > maxSnapshotAge) {
		err = fmt.Errorf("runner readings are stale (last refreshed %s)", snap.RefreshedAt.UTC().Format(time.RFC3339))
	}
	if err != nil {
		m.unavailable(ctx, now, err)
		return
	}
	m.mu.Lock()
	m.latest = snap
	m.failingSince = time.Time{}
	m.mu.Unlock()

	m.checkVolume(ctx, snap)
	m.checkOOMAndCPU(ctx, snap)

	sandboxes, err := m.lister.ListSandboxes(ctx)
	if err != nil {
		m.log.Warn("usage: list sandboxes for disk sizes", zap.Error(err))
		return
	}
	m.checkCoverage(ctx, now, snap, sandboxes)
	for _, sb := range sandboxes {
		if u := snap.Sandboxes[sb.ID]; u != nil && sb.Disk > 0 {
			m.checkDisk(ctx, now, snap, sb.ID, int64(sb.Disk)*GiB, u)
		}
	}
}

// checkCoverage alerts when the runner's readings miss a sandbox Daytona says
// is running: absent from them, or without a recent layer size. Such a
// sandbox's disk is not checked, and nothing else would say so. That is what
// a renamed metric, or a runner that cannot size layers, looks like from here.
func (m *Monitor) checkCoverage(ctx context.Context, now time.Time, snap *Snapshot, sandboxes []daytona.Sandbox) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var missing []string
	seen := map[string]bool{}
	for _, sb := range sandboxes {
		if !strings.EqualFold(sb.State, "started") {
			continue
		}
		var after time.Duration
		switch u := snap.Sandboxes[sb.ID]; {
		case u == nil:
			after = unavailableAlertAfter // the runner refreshes every 30 s
		case u.Used() == nil || now.Sub(u.LayerMeasuredAt) > maxLayerAge:
			after = maxLayerAge // it sizes layers every 5 min
		default:
			continue
		}
		seen[sb.ID] = true
		since, ok := m.uncovered[sb.ID]
		if !ok {
			m.uncovered[sb.ID] = now
			continue
		}
		if now.Sub(since) >= after {
			missing = append(missing, sb.ID)
		}
	}
	for id := range m.uncovered {
		if !seen[id] {
			delete(m.uncovered, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	if len(missing) > 20 {
		missing = missing[:20]
	}
	m.alerter.Notify(ctx, alert.KindUsageIncomplete, alert.SeverityCritical,
		fmt.Sprintf("Runner usage readings miss %d running sandbox(es): their disk sizes are not being checked", len(missing)),
		map[string]any{"sandboxes": missing})
}

func (m *Monitor) unavailable(ctx context.Context, now time.Time, err error) {
	m.mu.Lock()
	if m.failingSince.IsZero() {
		m.failingSince = now
	}
	since := m.failingSince
	m.mu.Unlock()
	m.log.Warn("usage: runner metrics unavailable", zap.Error(err), zap.Time("since", since))
	if now.Sub(since) >= unavailableAlertAfter {
		m.alerter.Notify(ctx, alert.KindUsageUnavailable, alert.SeverityCritical,
			"Runner usage metrics unavailable: sandbox disk sizes are not being checked",
			map[string]any{"since": since.UTC().Format(time.RFC3339), "error": err.Error()})
	}
}

// AdmitCreate tells whether a new sandbox may be created. It returns an error
// wrapping ErrVolumeFull while the latest reading has the shared volume at
// least Config.CreateBlockPct full. Without a reading from the last
// maxSnapshotAge it admits: a monitoring outage must not stop every create,
// and the usage_unavailable alert already reports it.
func (m *Monitor) AdmitCreate() error {
	if m.cfg.CreateBlockPct <= 0 {
		return nil
	}
	m.mu.Lock()
	snap := m.latest
	m.mu.Unlock()
	if snap == nil || snap.Volume == nil || snap.Volume.SizeBytes == 0 || m.now().Sub(snap.RefreshedAt) > maxSnapshotAge {
		return nil
	}
	if pct := snap.Volume.UsedPct(); pct >= m.cfg.CreateBlockPct {
		return fmt.Errorf("%w: %.1f%% used, new sandboxes refused from %.0f%%", ErrVolumeFull, pct, m.cfg.CreateBlockPct)
	}
	return nil
}

func (m *Monitor) checkVolume(ctx context.Context, snap *Snapshot) {
	v := snap.Volume
	if v == nil || v.SizeBytes == 0 || m.cfg.VolumeAlertPct <= 0 {
		return
	}
	pct := v.UsedPct()
	if pct < m.cfg.VolumeAlertPct {
		return
	}
	sev := alert.SeverityWarning
	if m.cfg.VolumeCriticalPct > 0 && pct >= m.cfg.VolumeCriticalPct {
		sev = alert.SeverityCritical
	}
	refusing := m.cfg.CreateBlockPct > 0 && pct >= m.cfg.CreateBlockPct
	msg := fmt.Sprintf("Sandbox volume is %.0f%% full: a full volume stops billing and sandbox lifecycle on this machine", pct)
	if refusing {
		msg += "; new sandboxes are refused"
	}
	m.alerter.Notify(ctx, alert.KindDiskVolumeHigh, sev, msg,
		map[string]any{"used_bytes": v.UsedBytes, "size_bytes": v.SizeBytes, "available_bytes": v.AvailableBytes, "creates_refused": refusing})
}

func (m *Monitor) checkOOMAndCPU(ctx context.Context, snap *Snapshot) {
	at := snap.RefreshedAt
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, u := range snap.Sandboxes {
		if u.OOMKillsTotal != nil {
			h := m.oom[id]
			if n := len(h); n > 0 && *u.OOMKillsTotal < h[n-1].kills {
				h = nil // counter reset: the container was recreated
			}
			if len(h) == 0 && !m.prevAt.IsZero() && !m.prevRunning[id] {
				// Its container started after the previous reading, and the
				// counter with it: kills before this first sight count too.
				h = []oomSample{{at: m.prevAt, kills: 0}}
			}
			if n := len(h); n == 0 || at.After(h[n-1].at) {
				h = append(h, oomSample{at: at, kills: *u.OOMKillsTotal})
			}
			for len(h) > 1 && at.Sub(h[0].at) > time.Hour {
				h = h[1:]
			}
			m.oom[id] = h
			if kills := h[len(h)-1].kills - h[0].kills; m.cfg.OOMKillsPerHour > 0 && kills >= uint64(m.cfg.OOMKillsPerHour) {
				m.alerter.Notify(ctx, alert.KindSandboxOOMKills, alert.SeverityWarning,
					fmt.Sprintf("Sandbox %s: %d processes killed at its memory cap within an hour", id, kills),
					map[string]any{"sandbox": id, "kills": kills, "memory_limit_bytes": u.MemoryLimitBytes})
			}
		}
		if u.CPUSecondsTotal != nil && u.CPULimitCores > 0 && u.Running {
			t := m.cpu[id]
			if t == nil {
				m.cpu[id] = &cpuTrack{at: at, seconds: *u.CPUSecondsTotal}
				continue
			}
			if !at.After(t.at) {
				continue // no new reading
			}
			if *u.CPUSecondsTotal < t.seconds {
				t.pinnedSince = time.Time{} // counter reset
			} else if (*u.CPUSecondsTotal-t.seconds)/at.Sub(t.at).Seconds()/u.CPULimitCores >= cpuPinnedUtilization {
				if t.pinnedSince.IsZero() {
					t.pinnedSince = t.at
				}
			} else {
				t.pinnedSince = time.Time{}
			}
			t.at, t.seconds = at, *u.CPUSecondsTotal
			if m.cfg.CPUPinnedAfter > 0 && !t.pinnedSince.IsZero() && at.Sub(t.pinnedSince) >= m.cfg.CPUPinnedAfter {
				m.alerter.Notify(ctx, alert.KindSandboxCPUPinned, alert.SeverityWarning,
					fmt.Sprintf("Sandbox %s has run at its CPU cap for %s", id, at.Sub(t.pinnedSince).Round(time.Minute)),
					map[string]any{"sandbox": id, "since": t.pinnedSince.UTC().Format(time.RFC3339), "cpu_limit_cores": u.CPULimitCores})
			}
		}
	}
	for id := range m.oom {
		if snap.Sandboxes[id] == nil {
			delete(m.oom, id)
		}
	}
	m.prevAt = at
	m.prevRunning = make(map[string]bool, len(snap.Sandboxes))
	for id, u := range snap.Sandboxes {
		m.prevRunning[id] = u.Running
	}
	for id := range m.cpu {
		if snap.Sandboxes[id] == nil {
			delete(m.cpu, id)
		}
	}
}

// checkDisk alerts on, and with enforcement stops, a sandbox past its
// requested disk size. What it may do depends on the sandbox's DiskStop phase,
// which the events themselves record (see DiskStop); nothing here infers them.
func (m *Monitor) checkDisk(ctx context.Context, now time.Time, snap *Snapshot, id string, requested int64, u *Sandbox) {
	if u.Used() == nil || now.Sub(u.LayerMeasuredAt) > maxLayerAge {
		return
	}
	used := *u.Used()
	rec, err := GetDiskStop(ctx, m.rdb, id)
	if err != nil {
		m.log.Warn("usage: read disk-stop record", zap.String("sandbox", id), zap.Error(err))
		return
	}
	// freshInGrace: measured since the grace began, so it counts the files
	// the owner deleted during it.
	freshInGrace := rec != nil && rec.Phase == PhaseGrace && u.LayerMeasuredAt.After(*rec.GraceStartedAt)

	if used <= requested {
		if freshInGrace { // the owner cleaned up
			if err := deleteDiskStop(ctx, m.rdb, id); err != nil {
				m.log.Warn("usage: clear disk-stop record", zap.String("sandbox", id), zap.Error(err))
			}
		}
		return
	}

	details := map[string]any{"sandbox": id, "used_bytes": used, "requested_bytes": requested, "enforced": m.cfg.Enforce}
	if rec != nil {
		details["phase"] = rec.Phase
	}
	m.alerter.Notify(ctx, alert.KindSandboxDiskOverSize, alert.SeverityWarning,
		fmt.Sprintf("Sandbox %s has written %d MiB, past its %d MiB disk", id, used>>20, requested>>20), details)
	if !m.cfg.Enforce || !u.Running {
		return
	}

	critical := m.cfg.VolumeCriticalPct > 0 && snap.Volume.UsedPct() >= m.cfg.VolumeCriticalPct
	if rec != nil {
		switch rec.Phase {
		case PhaseQueued:
			// The stop handler drops the marker of a stop that didn't take;
			// queue it again then.
			if n, err := m.rdb.Exists(ctx, "stop:sandbox:"+id).Result(); err != nil || n > 0 {
				return
			}
		case PhaseStopped:
			// Reported running before the owner's start reached OnStarted: a
			// reading from before the stop. Wait for the start.
			if !critical {
				return
			}
		case PhaseGrace:
			if !critical && (now.Before(*rec.GraceUntil) || !freshInGrace) {
				return
			}
		}
	}
	m.stop(ctx, now, id, used, requested, critical)
}

func (m *Monitor) stop(ctx context.Context, now time.Time, id string, used, requested int64, critical bool) {
	// Record first, so the owner can see why the sandbox stopped.
	if err := putDiskStop(ctx, m.rdb, id, &DiskStop{Phase: PhaseQueued, QueuedAt: now, UsedBytes: used, RequestedBytes: requested}); err != nil {
		m.log.Error("usage: record disk stop", zap.String("sandbox", id), zap.Error(err))
		return
	}
	if err := m.queueStop(ctx, id); err != nil {
		m.log.Error("usage: queue disk stop", zap.String("sandbox", id), zap.Error(err))
		m.alerter.Notify(ctx, alert.KindStopPersistFailure, alert.SeverityCritical,
			"Could not queue the stop of a sandbox past its disk size",
			map[string]any{"sandbox": id, "reason": ReasonDiskOverSize, "error": err.Error()})
		return
	}
	m.log.Warn("usage: stopping sandbox past its disk size", zap.String("sandbox", id),
		zap.Int64("used_bytes", used), zap.Int64("requested_bytes", requested), zap.Bool("volume_critical", critical))
}

// Report is a sandbox's usage as its owner sees it: numbers only.
type Report struct {
	SandboxID string `json:"sandbox_id"`
	// Measured is false until the runner has reported this sandbox.
	Measured   bool       `json:"measured"`
	MeasuredAt *time.Time `json:"measured_at,omitempty"`
	Running    *bool      `json:"running,omitempty"`
	Disk       DiskReport `json:"disk"`
	// DiskEnforced tells whether a sandbox past its disk size is stopped.
	DiskEnforced bool           `json:"disk_enforced"`
	DiskStop     *DiskStop      `json:"disk_stop,omitempty"`
	CPU          *CPUReport     `json:"cpu,omitempty"`
	Memory       *MemoryReport  `json:"memory,omitempty"`
	Tasks        *TasksReport   `json:"tasks,omitempty"`
	Network      *NetworkReport `json:"network,omitempty"`
}

type DiskReport struct {
	RequestedBytes int64      `json:"requested_bytes"`
	UsedBytes      *int64     `json:"used_bytes,omitempty"`
	FreeBytes      *int64     `json:"free_bytes,omitempty"`
	MeasuredAt     *time.Time `json:"measured_at,omitempty"`
	// Status is ok, near_full (less than WarnBelowFreeBytes left), over, or
	// unknown (no measurement or no requested size).
	Status             string `json:"status"`
	WarnBelowFreeBytes int64  `json:"warn_below_free_bytes"`
}

type CPUReport struct {
	LimitCores            float64  `json:"limit_cores,omitempty"`
	UsedSecondsTotal      *float64 `json:"used_seconds_total,omitempty"`
	ThrottledSecondsTotal *float64 `json:"throttled_seconds_total,omitempty"`
}

type MemoryReport struct {
	LimitBytes    uint64  `json:"limit_bytes,omitempty"`
	UsedBytes     *uint64 `json:"used_bytes,omitempty"`
	OOMKillsTotal *uint64 `json:"oom_kills_total,omitempty"`
}

type TasksReport struct {
	Current *uint64 `json:"current,omitempty"`
	Limit   uint64  `json:"limit,omitempty"`
}

type NetworkReport struct {
	ReceiveBytesTotal  *uint64 `json:"receive_bytes_total,omitempty"`
	TransmitBytesTotal *uint64 `json:"transmit_bytes_total,omitempty"`
}

// Report builds the usage report for one sandbox from the latest reading.
func (m *Monitor) Report(ctx context.Context, sandboxID string, requestedDiskGiB int) Report {
	r := Report{
		SandboxID:    sandboxID,
		DiskEnforced: m.cfg.Enforce,
		Disk: DiskReport{
			RequestedBytes:     int64(requestedDiskGiB) * GiB,
			Status:             "unknown",
			WarnBelowFreeBytes: m.cfg.WarnFreeBytes,
		},
	}
	m.mu.Lock()
	snap := m.latest
	m.mu.Unlock()
	if snap != nil {
		if u := snap.Sandboxes[sandboxID]; u != nil {
			m.fill(&r, snap, u)
		}
	}
	if rec, err := GetDiskStop(ctx, m.rdb, sandboxID); err != nil {
		m.log.Warn("usage: read disk-stop record", zap.String("sandbox", sandboxID), zap.Error(err))
	} else {
		r.DiskStop = rec
	}
	return r
}

func (m *Monitor) fill(r *Report, snap *Snapshot, u *Sandbox) {
	r.Measured = true
	at, running := snap.RefreshedAt, u.Running
	r.MeasuredAt, r.Running = &at, &running
	if u.Used() != nil {
		used, measured := *u.Used(), u.LayerMeasuredAt
		r.Disk.UsedBytes, r.Disk.MeasuredAt = &used, &measured
		if req := r.Disk.RequestedBytes; req > 0 {
			free := max(req-used, 0)
			r.Disk.FreeBytes = &free
			switch {
			case used > req:
				r.Disk.Status = "over"
			case free < m.cfg.WarnFreeBytes:
				r.Disk.Status = "near_full"
			default:
				r.Disk.Status = "ok"
			}
		}
	}
	if u.CPULimitCores > 0 || u.CPUSecondsTotal != nil {
		r.CPU = &CPUReport{LimitCores: u.CPULimitCores, UsedSecondsTotal: u.CPUSecondsTotal, ThrottledSecondsTotal: u.CPUThrottledSeconds}
	}
	if u.MemoryLimitBytes > 0 || u.MemoryBytes != nil || u.OOMKillsTotal != nil {
		r.Memory = &MemoryReport{LimitBytes: u.MemoryLimitBytes, UsedBytes: u.MemoryBytes, OOMKillsTotal: u.OOMKillsTotal}
	}
	if u.TasksLimit > 0 || u.Tasks != nil {
		r.Tasks = &TasksReport{Current: u.Tasks, Limit: u.TasksLimit}
	}
	if u.ReceiveBytesTotal != nil || u.TransmitBytesTotal != nil {
		r.Network = &NetworkReport{ReceiveBytesTotal: u.ReceiveBytesTotal, TransmitBytesTotal: u.TransmitBytesTotal}
	}
}
