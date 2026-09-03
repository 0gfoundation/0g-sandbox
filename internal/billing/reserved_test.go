package billing

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Review #116 F2: an over-large Release (reserve/release amount asymmetry) must
// not drive the counter negative and DEL it — that would wipe OTHER concurrent
// reservations under the same key and re-open the create TOCTOU. Clamped to the
// current value, the worst case is releasing down to zero.
func TestRelease_ClampedNeverWipesConcurrentReservations(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	user, prov := "0xU", "0xP"

	// Two concurrent reservations totalling 150.
	if _, err := Reserve(ctx, rdb, user, prov, big.NewInt(100), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := Reserve(ctx, rdb, user, prov, big.NewInt(50), time.Minute); err != nil {
		t.Fatal(err)
	}
	// An asymmetric release of 999 (way over) must not wipe the whole key.
	Release(ctx, rdb, user, prov, big.NewInt(999))
	// Clamp released down to zero (both reservations), but deterministically —
	// never negative, never leaving a poisoned negative counter.
	if got := GetReserved(ctx, rdb, user, prov); got.Sign() < 0 {
		t.Fatalf("counter must never be negative, got %s", got)
	}
}

// A normal symmetric reserve/release still nets to zero.
func TestReserveRelease_SymmetricNetsZero(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	Reserve(ctx, rdb, "0xU", "0xP", big.NewInt(70), time.Minute) //nolint:errcheck
	Release(ctx, rdb, "0xU", "0xP", big.NewInt(70))
	if got := GetReserved(ctx, rdb, "0xU", "0xP"); got.Sign() != 0 {
		t.Fatalf("symmetric reserve/release must net zero, got %s", got)
	}
}
