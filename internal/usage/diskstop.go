package usage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// ReasonDiskOverSize is the stop reason for a sandbox that has written past
// its requested disk size. cmd/billing registers it as a reason that only
// stops the sandbox, without archiving it.
const ReasonDiskOverSize = "disk_over_size"

const (
	diskStopKeyPrefix = "usage:disk_stop:"
	// A record outlives any sensible restart window but not the sandbox's
	// whole life: a sandbox deleted while stopped leaves nothing behind for
	// long.
	diskStopTTL = 30 * 24 * time.Hour
)

// DiskPhase is where a disk stop stands.
type DiskPhase string

const (
	// PhaseQueued: the Monitor queued the stop; it has not completed yet.
	PhaseQueued DiskPhase = "queued"
	// PhaseStopped: the sandbox is stopped and its owner has not started it
	// again.
	PhaseStopped DiskPhase = "stopped"
	// PhaseGrace: the owner started it again and has until GraceUntil to
	// delete files.
	PhaseGrace DiskPhase = "grace"
)

// DiskStop records a stop of a sandbox past its disk size: why it happened
// and where it stands. The owner sees it through GET /api/sandbox/:id/usage.
//
// Each phase is entered by the event that causes it:
//   - the Monitor queues the stop (PhaseQueued);
//   - the stop handler completes it (Monitor.OnStopped → PhaseStopped);
//   - the owner starts the sandbox again (Monitor.OnStarted → PhaseGrace).
//
// The Monitor removes the record once a size measured during the grace is
// back under the requested one. A grace is not renewed by the owner's own
// stop and start: only a completed stop through the stop handler resets it.
type DiskStop struct {
	Phase          DiskPhase  `json:"phase"`
	QueuedAt       time.Time  `json:"queued_at"`
	UsedBytes      int64      `json:"used_bytes"`
	RequestedBytes int64      `json:"requested_bytes"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
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
	if rec.Phase == "" { // written before phases were recorded
		switch {
		case rec.GraceUntil != nil && rec.GraceStartedAt != nil:
			rec.Phase = PhaseGrace
		case rec.StoppedAt != nil:
			rec.Phase = PhaseStopped
		default:
			rec.Phase = PhaseQueued
		}
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

// markStopped moves the sandbox's record, if it has one, to PhaseStopped after
// a completed stop of any reason. A disk stop queued while another stop was
// pending is completed by that one. A sandbox stopped again during its grace
// gets a fresh grace when started again.
func markStopped(ctx context.Context, rdb *redis.Client, sandboxID string, at time.Time) error {
	rec, err := GetDiskStop(ctx, rdb, sandboxID)
	if err != nil || rec == nil {
		return err
	}
	rec.Phase = PhaseStopped
	rec.StoppedAt = &at
	rec.GraceStartedAt, rec.GraceUntil = nil, nil
	return putDiskStop(ctx, rdb, sandboxID, rec)
}

// beginGrace moves a record in PhaseStopped to PhaseGrace when the owner
// starts the sandbox. It reports whether a grace began.
func beginGrace(ctx context.Context, rdb *redis.Client, sandboxID string, at time.Time, grace time.Duration) (bool, error) {
	rec, err := GetDiskStop(ctx, rdb, sandboxID)
	if err != nil || rec == nil || rec.Phase != PhaseStopped {
		return false, err
	}
	until := at.Add(grace)
	rec.Phase = PhaseGrace
	rec.GraceStartedAt, rec.GraceUntil = &at, &until
	return true, putDiskStop(ctx, rdb, sandboxID, rec)
}
