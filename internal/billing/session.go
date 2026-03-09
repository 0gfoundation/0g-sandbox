package billing

import (
	"context"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

const sessionKeyPrefix = "billing:compute:"

// Session holds the billing state for a running sandbox.
type Session struct {
	SandboxID     string
	Owner         string
	Provider      string
	StartTime     int64
	LastVoucherAt int64
	PricePerSec   string // neuron/sec as decimal; empty = use flat rate fallback
}

func sessionKey(sandboxID string) string {
	return sessionKeyPrefix + sandboxID
}

func CreateSession(ctx context.Context, rdb *redis.Client, s Session) error {
	key := sessionKey(s.SandboxID)
	return rdb.HSet(ctx, key,
		"sandbox_id", s.SandboxID,
		"owner", s.Owner,
		"provider", s.Provider,
		"start_time", s.StartTime,
		"last_voucher_at", s.LastVoucherAt,
		"price_per_sec", s.PricePerSec,
	).Err()
}

func GetSession(ctx context.Context, rdb *redis.Client, sandboxID string) (*Session, error) {
	vals, err := rdb.HGetAll(ctx, sessionKey(sandboxID)).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, nil
	}
	return sessionFromMap(vals)
}

func UpdateLastVoucherAt(ctx context.Context, rdb *redis.Client, sandboxID string, t int64) error {
	return rdb.HSet(ctx, sessionKey(sandboxID), "last_voucher_at", t).Err()
}

func DeleteSession(ctx context.Context, rdb *redis.Client, sandboxID string) error {
	return rdb.Del(ctx, sessionKey(sandboxID)).Err()
}

// ScanAllSessions returns all active billing sessions.
func ScanAllSessions(ctx context.Context, rdb *redis.Client) ([]Session, error) {
	var sessions []Session
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, sessionKeyPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan sessions: %w", err)
		}
		for _, key := range keys {
			vals, err := rdb.HGetAll(ctx, key).Result()
			if err != nil || len(vals) == 0 {
				continue
			}
			s, err := sessionFromMap(vals)
			if err != nil {
				continue
			}
			sessions = append(sessions, *s)
		}
		if next == 0 {
			break
		}
		cursor = next
	}
	return sessions, nil
}

func sessionFromMap(m map[string]string) (*Session, error) {
	startTime, _ := strconv.ParseInt(m["start_time"], 10, 64)
	lastVoucherAt, _ := strconv.ParseInt(m["last_voucher_at"], 10, 64)
	return &Session{
		SandboxID:     m["sandbox_id"],
		Owner:         m["owner"],
		Provider:      m["provider"],
		StartTime:     startTime,
		LastVoucherAt: lastVoucherAt,
		PricePerSec:   m["price_per_sec"],
	}, nil
}
