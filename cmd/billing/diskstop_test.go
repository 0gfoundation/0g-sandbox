package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/settler"
	"github.com/0gfoundation/0g-sandbox/internal/usage"
)

// A sandbox past its disk size is stopped but not archived: archiving would
// copy its filesystem onto the same volume.
func TestRunStopHandler_DiskOverSize_StopsWithoutArchive(t *testing.T) {
	rdb := newTestRedis(t)
	mock := newMockDaytona(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bg := context.Background()
	rdb.Set(bg, "billing:compute:sb-d", "session", 0)                              //nolint:errcheck
	rdb.Set(bg, "stop:sandbox:sb-d", usage.ReasonDiskOverSize, 0)                  //nolint:errcheck
	rdb.Set(bg, "usage:disk_stop:sb-d", `{"queued_at":"2026-11-23T00:00:00Z"}`, 0) //nolint:errcheck

	stopCh := make(chan settler.StopSignal, 1)
	go runStopHandler(ctx, stopCh, mock.client(), rdb, &noopAlerter{}, zap.NewNop(), nil)
	stopCh <- settler.StopSignal{SandboxID: "sb-d", Reason: usage.ReasonDiskOverSize}

	waitKeyGone(t, rdb, "stop:sandbox:sb-d", time.Second)
	waitKeyGone(t, rdb, "billing:compute:sb-d", time.Second)
	if ids := mock.stoppedIDs(); len(ids) != 1 || ids[0] != "sb-d" {
		t.Errorf("stopped %v, want [sb-d]", ids)
	}
	mock.mu.Lock()
	archived := mock.archived["sb-d"]
	mock.mu.Unlock()
	if archived {
		t.Error("a disk stop archived the sandbox")
	}
	deadline := time.Now().Add(time.Second)
	for {
		rec, err := usage.GetDiskStop(bg, rdb, "sb-d")
		if err == nil && rec != nil && rec.StoppedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("disk-stop record not marked stopped: %+v, %v", rec, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Not confirmed stopped: the session stays and the marker goes, so the usage
// monitor queues the stop again.
func TestFinishDiskStop_WaitFailedKeepsSession(t *testing.T) {
	rdb := newTestRedis(t)
	ctx := context.Background()
	rdb.Set(ctx, "billing:compute:sb-d", "session", 0)             //nolint:errcheck
	rdb.Set(ctx, "stop:sandbox:sb-d", usage.ReasonDiskOverSize, 0) //nolint:errcheck

	finishDiskStop(ctx, "sb-d", errors.New("timeout"), rdb, zap.NewNop(), nil)

	if n, _ := rdb.Exists(ctx, "billing:compute:sb-d").Result(); n != 1 {
		t.Error("billing session closed for a sandbox not confirmed stopped")
	}
	if n, _ := rdb.Exists(ctx, "stop:sandbox:sb-d").Result(); n != 0 {
		t.Error("stop marker kept: the monitor would never queue it again")
	}
}
