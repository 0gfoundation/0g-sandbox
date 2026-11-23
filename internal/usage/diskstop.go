package usage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ReasonDiskOverSize is the stop reason for a sandbox that has written past
// its requested disk size. The stop handler stops such a sandbox without
// archiving it.
const ReasonDiskOverSize = "disk_over_size"

const (
	diskStopKeyPrefix = "usage:disk_stop:"
	// A record outlives any sensible restart window but not the sandbox's
	// whole life: a sandbox deleted while stopped leaves nothing behind for
	// long.
	diskStopTTL = 30 * 24 * time.Hour
)

// DiskStop records that the Monitor stopped a sandbox for going past its disk
// size. The owner sees it through GET /api/sandbox/:id/usage, and the Monitor
// uses it to give the owner time to delete files after starting it again.
type DiskStop struct {
	QueuedAt       time.Time `json:"queued_at"`
	UsedBytes      int64     `json:"used_bytes"`
	RequestedBytes int64     `json:"requested_bytes"`
	// StoppedAt is set by the stop handler once the sandbox has stopped.
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	// GraceStartedAt and GraceUntil are set when the Monitor first sees the
	// sandbox running again after StoppedAt. Until GraceUntil it is not
	// stopped for disk again (unless the shared volume is critically full),
	// and after it only on a size measured since GraceStartedAt.
	GraceStartedAt *time.Time `json:"grace_started_at,omitempty"`
	GraceUntil     *time.Time `json:"grace_until,omitempty"`
}

func diskStopKey(sandboxID string) string { return diskStopKeyPrefix + sandboxID }

// GetDiskStop returns the sandbox's record, or nil if it has none.
func GetDiskStop(ctx context.Context, rdb *redis.Client, sandboxID string) (*DiskStop, error) {
	raw, err := rdb.Get(ctx, diskStopKey(sandboxID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec DiskStop
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func putDiskStop(ctx context.Context, rdb *redis.Client, sandboxID string, rec *DiskStop) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, diskStopKey(sandboxID), raw, diskStopTTL).Err()
}

func deleteDiskStop(ctx context.Context, rdb *redis.Client, sandboxID string) error {
	return rdb.Del(ctx, diskStopKey(sandboxID)).Err()
}

// MarkStopped records that a stop of sandboxID has completed, if the sandbox
// has a disk-stop record waiting for one. The stop handler calls it after
// every successful stop: a disk stop queued while another stop (insufficient
// balance) was already pending is completed by that one.
func MarkStopped(ctx context.Context, rdb *redis.Client, sandboxID string, at time.Time) error {
	rec, err := GetDiskStop(ctx, rdb, sandboxID)
	if err != nil || rec == nil || rec.StoppedAt != nil {
		return err
	}
	rec.StoppedAt = &at
	rec.GraceStartedAt, rec.GraceUntil = nil, nil
	return putDiskStop(ctx, rdb, sandboxID, rec)
}
