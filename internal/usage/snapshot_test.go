package usage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// runnerMetrics mirrors what 0g-daytona's internal/sandboxusage serves,
// mixed with the runner's other metrics.
const runnerMetrics = `# HELP container_operation_total Total number of container operations
# TYPE container_operation_total counter
container_operation_total{operation="create",status="success"} 3
# HELP daytona_runner_docker_volume_available_bytes x
# TYPE daytona_runner_docker_volume_available_bytes gauge
daytona_runner_docker_volume_available_bytes 4.294967296e+11
# TYPE daytona_runner_docker_volume_size_bytes gauge
daytona_runner_docker_volume_size_bytes 5.36870912e+11
# TYPE daytona_runner_docker_volume_used_bytes gauge
daytona_runner_docker_volume_used_bytes 6.7645734912e+10
# TYPE daytona_sandbox_cpu_limit_cores gauge
daytona_sandbox_cpu_limit_cores{sandbox_id="sb-1"} 2
# TYPE daytona_sandbox_cpu_seconds_total counter
daytona_sandbox_cpu_seconds_total{sandbox_id="sb-1"} 12.5
# TYPE daytona_sandbox_cpu_throttled_seconds_total counter
daytona_sandbox_cpu_throttled_seconds_total{sandbox_id="sb-1"} 3
# TYPE daytona_sandbox_memory_bytes gauge
daytona_sandbox_memory_bytes{sandbox_id="sb-1"} 2.097152e+08
# TYPE daytona_sandbox_memory_limit_bytes gauge
daytona_sandbox_memory_limit_bytes{sandbox_id="sb-1"} 4.294967296e+09
# TYPE daytona_sandbox_network_receive_bytes_total counter
daytona_sandbox_network_receive_bytes_total{sandbox_id="sb-1"} 1000
# TYPE daytona_sandbox_network_transmit_bytes_total counter
daytona_sandbox_network_transmit_bytes_total{sandbox_id="sb-1"} 2000
# TYPE daytona_sandbox_oom_kills_total counter
daytona_sandbox_oom_kills_total{sandbox_id="sb-1"} 2
# TYPE daytona_sandbox_running gauge
daytona_sandbox_running{sandbox_id="sb-1"} 1
daytona_sandbox_running{sandbox_id="sb-2"} 0
# TYPE daytona_sandbox_tasks gauge
daytona_sandbox_tasks{sandbox_id="sb-1"} 37
# TYPE daytona_sandbox_tasks_limit gauge
daytona_sandbox_tasks_limit{sandbox_id="sb-1"} 4096
# TYPE daytona_sandbox_usage_refreshed_timestamp_seconds gauge
daytona_sandbox_usage_refreshed_timestamp_seconds 1.8e+09
# TYPE daytona_sandbox_writable_layer_bytes gauge
daytona_sandbox_writable_layer_bytes{sandbox_id="sb-1"} 7.516192768e+09
daytona_sandbox_writable_layer_bytes{sandbox_id="sb-2"} 1.1811160064e+10
# TYPE daytona_sandbox_writable_layer_measured_timestamp_seconds gauge
daytona_sandbox_writable_layer_measured_timestamp_seconds{sandbox_id="sb-1"} 1.79999994e+09
daytona_sandbox_writable_layer_measured_timestamp_seconds{sandbox_id="sb-2"} 1.79999994e+09
# TYPE go_goroutines gauge
go_goroutines 42
`

func TestParseMetrics(t *testing.T) {
	s, err := ParseMetrics(strings.NewReader(runnerMetrics))
	if err != nil {
		t.Fatal(err)
	}
	if !s.RefreshedAt.Equal(time.Unix(1_800_000_000, 0)) {
		t.Errorf("RefreshedAt = %v", s.RefreshedAt)
	}
	if s.Volume == nil || s.Volume.SizeBytes != 500*1<<30 || s.Volume.UsedBytes != 63*1<<30 || s.Volume.AvailableBytes != 400*1<<30 {
		t.Errorf("Volume = %+v", s.Volume)
	}
	if len(s.Sandboxes) != 2 {
		t.Fatalf("sandboxes = %d, want 2", len(s.Sandboxes))
	}
	a := s.Sandboxes["sb-1"]
	if !a.Running || a.CPULimitCores != 2 || *a.CPUSecondsTotal != 12.5 || *a.CPUThrottledSeconds != 3 ||
		*a.MemoryBytes != 200<<20 || a.MemoryLimitBytes != 4<<30 || *a.OOMKillsTotal != 2 ||
		*a.Tasks != 37 || a.TasksLimit != 4096 || *a.ReceiveBytesTotal != 1000 || *a.TransmitBytesTotal != 2000 ||
		*a.LayerBytes != 7<<30 || !a.LayerMeasuredAt.Equal(time.Unix(1_799_999_940, 0)) {
		t.Errorf("sb-1 = %+v", a)
	}
	b := s.Sandboxes["sb-2"]
	if b.Running || b.CPUSecondsTotal != nil || b.MemoryBytes != nil || *b.LayerBytes != 11<<30 {
		t.Errorf("sb-2 (stopped) = %+v", b)
	}
}

func TestRunnerClient_SendsTokenAndRejectsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" || r.Header.Get("Authorization") != "Bearer runner-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(runnerMetrics))
	}))
	defer srv.Close()

	s, err := NewRunnerClient(srv.URL+"/", "runner-token").Fetch(context.Background())
	if err != nil || len(s.Sandboxes) != 2 {
		t.Fatalf("Fetch = %v, %v", s, err)
	}
	if _, err := NewRunnerClient(srv.URL, "wrong").Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token: err = %v, want status 401", err)
	}
}
