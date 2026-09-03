package billing

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const reservedKeyPrefix = "billing:reserved:"

func reservedKey(user, provider string) string {
	return reservedKeyPrefix + strings.ToLower(user) + ":" + strings.ToLower(provider)
}

// Reserve atomically adds amount to the pending reservation for (user, provider),
// refreshes the TTL so crashed reservations auto-expire, and returns the NEW
// TOTAL including this amount. The INCRBY is the serialization point for the
// balance gate: callers reserve FIRST and judge affordability against the
// returned total — check-then-reserve had a TOCTOU window where N concurrent
// creates all read the pre-reservation balance and all passed (#74).
var reserveScript = redis.NewScript(`
	local v = redis.call('INCRBY', KEYS[1], ARGV[1])
	redis.call('EXPIRE', KEYS[1], ARGV[2])
	return tostring(v)
`)

func Reserve(ctx context.Context, rdb *redis.Client, user, provider string, amount *big.Int, ttl time.Duration) (*big.Int, error) {
	val, err := reserveScript.Run(ctx, rdb, []string{reservedKey(user, provider)}, amount.String(), int64(ttl.Seconds())).Text()
	if err != nil {
		return nil, err
	}
	total, ok := new(big.Int).SetString(val, 10)
	if !ok {
		return nil, fmt.Errorf("reserve: bad total %q", val)
	}
	return total, nil
}

// Release subtracts amount from the reservation. If the result drops to zero or
// below the key is deleted. Best-effort: errors are silently ignored.
var releaseScript = redis.NewScript(`
	local v = redis.call('DECRBY', KEYS[1], ARGV[1])
	if tonumber(v) <= 0 then
		redis.call('DEL', KEYS[1])
	end
	return v
`)

func Release(ctx context.Context, rdb *redis.Client, user, provider string, amount *big.Int) {
	releaseScript.Run(ctx, rdb, []string{reservedKey(user, provider)}, amount.String()) //nolint:errcheck
}

// GetReserved returns the current pending reservation for (user, provider).
// Returns zero on any error or if the key does not exist.
func GetReserved(ctx context.Context, rdb *redis.Client, user, provider string) *big.Int {
	val, err := rdb.Get(ctx, reservedKey(user, provider)).Result()
	if err != nil {
		return new(big.Int)
	}
	n, ok := new(big.Int).SetString(val, 10)
	if !ok {
		return new(big.Int)
	}
	return n
}
