package billing

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// An over-large Release floors the counter at "gone" (DEL at <= 0), never a
// negative value that a later read could misinterpret. It does NOT isolate
// concurrent reservations — that would need per-request keys or an exact
// big-int clamp Redis Lua (64-bit doubles) can't express at neuron scale; the
// snapshot-only policy (#118) makes reserve == release, so over-release does
// not arise in practice.
func TestRelease_OverLargeFloorsAtZero(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	user, prov := "0xU", "0xP"
	if _, err := Reserve(ctx, rdb, user, prov, big.NewInt(150), time.Minute); err != nil {
		t.Fatal(err)
	}
	Release(ctx, rdb, user, prov, big.NewInt(999)) // way over
	if got := GetReserved(ctx, rdb, user, prov); got.Sign() < 0 {
		t.Fatalf("counter must never be negative, got %s", got)
	}
}

// Regression for the live dev breakage: Reserve must return the EXACT
// post-increment total at neuron scale. The old script returned tostring(v) of
// a Lua number — Redis Lua uses 64-bit doubles, so a total past 2^53 came back
// as "3.9999999999998e+15", which Go could not parse ("bad total") and which
// had already lost precision, silently degrading every reservation to the racy
// advisory path (re-opening #74). Returning GET (Redis's exact integer string)
// fixes it.
func TestReserve_ExactAtNeuronScale(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	// ~0.004 0G in neuron, well past 2^53 (~9.0e15).
	amt, _ := new(big.Int).SetString("3999999999999800", 10)
	total, err := Reserve(ctx, rdb, "0xU", "0xP", amt, time.Minute)
	if err != nil {
		t.Fatalf("reserve must not error at neuron scale: %v", err)
	}
	if total.Cmp(amt) != 0 {
		t.Fatalf("returned total = %s, want exact %s (no double-rounding)", total, amt)
	}
	// Second reserve accumulates exactly.
	total2, err := Reserve(ctx, rdb, "0xU", "0xP", amt, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := new(big.Int).Add(amt, amt)
	if total2.Cmp(want) != 0 {
		t.Fatalf("accumulated total = %s, want %s", total2, want)
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
