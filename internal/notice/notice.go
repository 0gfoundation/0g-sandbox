// Package notice tells a sandbox's own program what the provider is about to
// do to it (#148): its disk is past its size and when it is stopped for that,
// its owner's balance runs out, maintenance is planned.
//
// A sealed sandbox's owner cannot get in to save work or delete files; only
// the program inside can, and only if it knows. The usage Monitor gathers
// the notices on every poll and sends the whole set to the runner, which
// shows each sandbox its own in /run/0g/environment.json (0g-daytona
// internal/sandboxusage). A sandbox missing from the set has none.
package notice

import (
	"context"
	"time"
)

// Notice is one warning to a sandbox. The runner checks the same fields.
type Notice struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Message says what happens and what the sandbox can do about it. It is
	// read by the sandbox's program, often a model.
	Message string `json:"message"`
	// Deadline is when the provider acts, if it has a time.
	Deadline *time.Time `json:"deadline,omitempty"`
	// Action is what the provider does then.
	Action string `json:"action,omitempty"`
}

const (
	KindDiskNearFull = "disk_near_full"
	KindDiskOverSize = "disk_over_size"
	KindBalanceLow   = "balance_low"
	KindMaintenance  = "maintenance"

	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"

	ActionStop    = "stop"
	ActionArchive = "archive"
)

// Set is the notices by sandbox ID.
type Set map[string][]Notice

// Add appends n to the sandbox's notices.
func (s Set) Add(sandboxID string, n Notice) {
	s[sandboxID] = append(s[sandboxID], n)
}

// Merge appends every notice of o.
func (s Set) Merge(o Set) {
	for id, ns := range o {
		s[id] = append(s[id], ns...)
	}
}

// Source gives notices for the running sandboxes.
type Source interface {
	Notices(ctx context.Context, running []string) (Set, error)
}

// soon is how close a deadline makes a notice critical.
const soon = 15 * time.Minute

func severityBy(deadline, now time.Time) string {
	if deadline.Sub(now) <= soon {
		return SeverityCritical
	}
	return SeverityWarning
}

// utc formats a deadline for a message.
func utc(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }
