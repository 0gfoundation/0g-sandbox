// Package events provides a lightweight Redis-backed event log for operator visibility.
package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	listKey   = "billing:events"
	maxEvents = 50
)

// Type constants for event classification.
const (
	TypeCreated    = "created"
	TypeStopped    = "stopped"
	TypeAutoStopped = "auto_stopped"
	TypeSettled    = "settled"
)

// Event is a single operator-visible billing event stored in Redis.
type Event struct {
	Time      time.Time `json:"time"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	SandboxID string    `json:"sandbox_id,omitempty"`
	User      string    `json:"user,omitempty"`
	Amount    string    `json:"amount,omitempty"`
}

// Push prepends an event to the Redis list and trims it to maxEvents.
func Push(ctx context.Context, rdb *redis.Client, e Event) error {
	e.Time = time.Now().UTC()
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	pipe := rdb.Pipeline()
	pipe.LPush(ctx, listKey, string(data))
	pipe.LTrim(ctx, listKey, 0, maxEvents-1)
	_, err = pipe.Exec(ctx)
	return err
}

// List returns up to maxEvents recent events, newest first.
func List(ctx context.Context, rdb *redis.Client) ([]Event, error) {
	vals, err := rdb.LRange(ctx, listKey, 0, maxEvents-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(vals))
	for _, v := range vals {
		var e Event
		if json.Unmarshal([]byte(v), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}
