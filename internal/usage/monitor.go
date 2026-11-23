package usage

import (
	"context"
	"fmt"
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
		now: time.Now, oom: map[string][]oomSample{}, cpu: map[string]*cpuTrack{},
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
	for _, sb := range sandboxes {
		if u := snap.Sandboxes[sb.ID]; u != nil && sb.Disk > 0 {
			m.checkDisk(ctx, now, snap, sb.ID, int64(sb.Disk)*GiB, u)
		}
	}
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
	m.alerter.Notify(ctx, alert.KindDiskVolumeHigh, sev,
		fmt.Sprintf("Sandbox volume is %.0f%% full: a full volume stops billing and sandbox lifecycle on this machine", pct),
		map[string]any{"used_bytes": v.UsedBytes, "size_bytes": v.SizeBytes, "available_bytes": v.AvailableBytes})
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
	for id := range m.cpu {
		if snap.Sandboxes[id] == nil {
			delete(m.cpu, id)
		}
	}
}

// checkDisk alerts on, and with enforcement stops, a sandbox past its
// requested disk size.
func (m *Monitor) checkDisk(ctx context.Context, now time.Time, snap *Snapshot, id string, requested int64, u *Sandbox) {
	if u.LayerBytes == nil || now.Sub(u.LayerMeasuredAt) > maxLayerAge {
		return
	}
	used := *u.LayerBytes
	rec, err := GetDiskStop(ctx, m.rdb, id)
	if err != nil {
		m.log.Warn("usage: read disk-stop record", zap.String("sandbox", id), zap.Error(err))
		return
	}
	if used <= requested {
		// Back under its size after a restart: the owner cleaned up.
		if rec != nil && rec.StoppedAt != nil && u.Running && snap.RefreshedAt.After(*rec.StoppedAt) {
			if err := deleteDiskStop(ctx, m.rdb, id); err != nil {
				m.log.Warn("usage: clear disk-stop record", zap.String("sandbox", id), zap.Error(err))
			}
		}
		return
	}

	m.alerter.Notify(ctx, alert.KindSandboxDiskOverSize, alert.SeverityWarning,
		fmt.Sprintf("Sandbox %s has written %d MiB, past its %d MiB disk", id, used>>20, requested>>20),
		map[string]any{"sandbox": id, "used_bytes": used, "requested_bytes": requested, "enforced": m.cfg.Enforce})
	if !m.cfg.Enforce || !u.Running {
		return
	}

	critical := m.cfg.VolumeCriticalPct > 0 && snap.Volume.UsedPct() >= m.cfg.VolumeCriticalPct
	if rec != nil {
		if rec.StoppedAt == nil {
			// Queued or in flight. The stop handler drops the marker when a
			// stop doesn't take; queue it again then.
			if n, err := m.rdb.Exists(ctx, "stop:sandbox:"+id).Result(); err != nil || n > 0 {
				return
			}
		} else {
			if !snap.RefreshedAt.After(*rec.StoppedAt) {
				return // the reading predates the stop
			}
			if !critical {
				if rec.GraceUntil == nil {
					// The owner has started it again: time to delete files.
					start, g := now, now.Add(m.cfg.Grace)
					rec.GraceStartedAt, rec.GraceUntil = &start, &g
					if err := putDiskStop(ctx, m.rdb, id, rec); err != nil {
						m.log.Warn("usage: start grace", zap.String("sandbox", id), zap.Error(err))
					}
					m.log.Info("usage: sandbox past its disk size started again; grace period begins",
						zap.String("sandbox", id), zap.Time("until", g))
					return
				}
				if now.Before(*rec.GraceUntil) {
					return
				}
				// Act only on a size measured since the grace began: a layer
				// scan from before it misses the files the owner deleted.
				if rec.GraceStartedAt != nil && !u.LayerMeasuredAt.After(*rec.GraceStartedAt) {
					return
				}
			}
		}
	}
	m.stop(ctx, now, id, used, requested, critical)
}

func (m *Monitor) stop(ctx context.Context, now time.Time, id string, used, requested int64, critical bool) {
	// Record first, so the owner can see why the sandbox stopped.
	if err := putDiskStop(ctx, m.rdb, id, &DiskStop{QueuedAt: now, UsedBytes: used, RequestedBytes: requested}); err != nil {
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
	if u.LayerBytes != nil {
		used, measured := *u.LayerBytes, u.LayerMeasuredAt
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
