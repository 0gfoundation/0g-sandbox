// Package usage watches each sandbox's own resource use (#148).
//
// The runner measures every sandbox and serves the numbers on its
// token-protected /metrics (0g-daytona internal/sandboxusage). The Monitor
// polls them to:
//   - show an owner their sandbox's usage (GET /api/sandbox/:id/usage);
//   - alert the operator (shared volume filling, a sandbox past its disk size,
//     repeated OOM kills, a CPU pinned at its cap for hours);
//   - stop a sandbox that has written past its requested disk size, when
//     enforcement is on.
//
// Disk is the only resource the Monitor acts on. CPU and memory have hard
// caps in the runner and are billed; a sandbox's disk has neither while
// Docker's backing filesystem is ext4.
package usage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

// GiB is the unit of Daytona's disk, memory and the runner's Docker "G".
const GiB = int64(1) << 30

// Snapshot is one reading of the runner's per-sandbox metrics.
type Snapshot struct {
	// RefreshedAt is when the runner last read the sandboxes; zero if it
	// reported no time.
	RefreshedAt time.Time
	Volume      *Volume             // nil if the runner reported none
	Sandboxes   map[string]*Sandbox // by sandbox ID
}

// Volume is the filesystem holding the sandboxes' writable layers. In the
// deploy template it also holds billing's Redis, Daytona's database, the
// registry and MinIO.
type Volume struct {
	SizeBytes, UsedBytes, AvailableBytes uint64
}

// UsedPct is the used share of the volume in percent.
func (v *Volume) UsedPct() float64 {
	if v == nil || v.SizeBytes == 0 {
		return 0
	}
	return float64(v.UsedBytes) / float64(v.SizeBytes) * 100
}

// Sandbox is one sandbox's numbers. Pointers are nil when the runner could
// not read that number.
type Sandbox struct {
	Running             bool
	CPULimitCores       float64
	CPUSecondsTotal     *float64
	CPUThrottledSeconds *float64
	MemoryBytes         *uint64
	MemoryLimitBytes    uint64
	OOMKillsTotal       *uint64
	Tasks               *uint64
	TasksLimit          uint64
	ReceiveBytesTotal   *uint64
	TransmitBytesTotal  *uint64
	LayerBytes          *int64
	LayerMeasuredAt     time.Time
}

// ParseMetrics reads the runner's /metrics in Prometheus text format,
// keeping the per-sandbox and volume metrics and ignoring the rest.
func ParseMetrics(r io.Reader) (*Snapshot, error) {
	var parser expfmt.TextParser
	families, err := parser.TextToMetricFamilies(r)
	if err != nil {
		return nil, fmt.Errorf("parse runner metrics: %w", err)
	}
	s := &Snapshot{Sandboxes: map[string]*Sandbox{}}
	volume := func() *Volume {
		if s.Volume == nil {
			s.Volume = &Volume{}
		}
		return s.Volume
	}
	for name, mf := range families {
		for _, m := range mf.GetMetric() {
			v := value(m)
			if id := label(m, "sandbox_id"); id != "" {
				sb := s.Sandboxes[id]
				if sb == nil {
					sb = &Sandbox{}
					s.Sandboxes[id] = sb
				}
				setSandbox(sb, name, v)
				continue
			}
			switch name {
			case "daytona_runner_docker_volume_size_bytes":
				volume().SizeBytes = uint64(v)
			case "daytona_runner_docker_volume_used_bytes":
				volume().UsedBytes = uint64(v)
			case "daytona_runner_docker_volume_available_bytes":
				volume().AvailableBytes = uint64(v)
			case "daytona_sandbox_usage_refreshed_timestamp_seconds":
				s.RefreshedAt = time.Unix(int64(v), 0)
			}
		}
	}
	return s, nil
}

func setSandbox(sb *Sandbox, name string, v float64) {
	u := uint64(v)
	switch name {
	case "daytona_sandbox_running":
		sb.Running = v == 1
	case "daytona_sandbox_cpu_limit_cores":
		sb.CPULimitCores = v
	case "daytona_sandbox_cpu_seconds_total":
		sb.CPUSecondsTotal = &v
	case "daytona_sandbox_cpu_throttled_seconds_total":
		sb.CPUThrottledSeconds = &v
	case "daytona_sandbox_memory_bytes":
		sb.MemoryBytes = &u
	case "daytona_sandbox_memory_limit_bytes":
		sb.MemoryLimitBytes = u
	case "daytona_sandbox_oom_kills_total":
		sb.OOMKillsTotal = &u
	case "daytona_sandbox_tasks":
		sb.Tasks = &u
	case "daytona_sandbox_tasks_limit":
		sb.TasksLimit = u
	case "daytona_sandbox_network_receive_bytes_total":
		sb.ReceiveBytesTotal = &u
	case "daytona_sandbox_network_transmit_bytes_total":
		sb.TransmitBytesTotal = &u
	case "daytona_sandbox_writable_layer_bytes":
		b := int64(v)
		sb.LayerBytes = &b
	case "daytona_sandbox_writable_layer_measured_timestamp_seconds":
		sb.LayerMeasuredAt = time.Unix(int64(v), 0)
	}
}

func value(m *dto.Metric) float64 {
	switch {
	case m.Gauge != nil:
		return m.GetGauge().GetValue()
	case m.Counter != nil:
		return m.GetCounter().GetValue()
	case m.Untyped != nil:
		return m.GetUntyped().GetValue()
	}
	return 0
}

func label(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

// RunnerClient reads the runner's /metrics with its API token.
type RunnerClient struct {
	url   string
	token string
	http  *http.Client
}

func NewRunnerClient(url, token string) *RunnerClient {
	return &RunnerClient{
		url:   strings.TrimRight(url, "/"),
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Fetch implements Fetcher.
func (c *RunnerClient) Fetch(ctx context.Context) (*Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/metrics", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "text/plain; version=0.0.4")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("runner metrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runner metrics: status %d", resp.StatusCode)
	}
	return ParseMetrics(io.LimitReader(resp.Body, 64<<20))
}
