package notice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"

	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

type fakeChain struct {
	balances map[common.Address]*big.Int
	err      error
	asked    []common.Address
}

func (f *fakeChain) GetBalanceBatch(_ context.Context, users []common.Address, _ common.Address) ([]*big.Int, error) {
	f.asked = users
	if f.err != nil {
		return nil, f.err
	}
	out := make([]*big.Int, len(users))
	for i, u := range users {
		out[i] = f.balances[u]
	}
	return out, nil
}

var (
	provider = common.HexToAddress("0x00000000000000000000000000000000000000aa")
	alice    = common.HexToAddress("0x00000000000000000000000000000000000000a1")
	bob      = common.HexToAddress("0x00000000000000000000000000000000000000b2")
)

func newBalance(t *testing.T, chain *fakeChain) (*Balance, *redis.Client, time.Time) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	now := time.Unix(1_800_000_000, 0)
	return &Balance{Chain: chain, RDB: rdb, Provider: provider, FallbackPricePerSec: big.NewInt(7),
		Interval: time.Minute, Within: time.Hour, Now: func() time.Time { return now }}, rdb, now
}

func session(t *testing.T, rdb *redis.Client, id string, owner common.Address, price string) {
	t.Helper()
	if err := billing.CreateSession(context.Background(), rdb, billing.Session{
		SandboxID: id, Owner: owner.Hex(), Provider: provider.Hex(), PricePerSec: price,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBalance_WarnsAnOwnerWhoseBalanceRunsOut(t *testing.T) {
	ctx := context.Background()
	chain := &fakeChain{balances: map[common.Address]*big.Int{
		// After debts 100000-10000-5000-15000 = 70000: 58 periods of both
		// sandboxes (1200 each), and 400 short of the next 600.
		alice: big.NewInt(100_000),
		bob:   big.NewInt(1_000_000),
	}}
	b, rdb, now := newBalance(t, chain)
	session(t, rdb, "a-1", alice, "10")
	session(t, rdb, "a-2", alice, "10")
	session(t, rdb, "a-gone", alice, "1000") // not running: no session should count
	session(t, rdb, "b-1", bob, "10")
	if _, err := billing.Reserve(ctx, rdb, alice.Hex(), provider.Hex(), big.NewInt(10_000), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := voucher.PushHeld(ctx, rdb, voucher.SandboxVoucher{SandboxID: "a-1", User: alice, Provider: provider, TotalFee: big.NewInt(5_000)}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(voucher.SandboxVoucher{SandboxID: "a-2", User: alice, Provider: provider, TotalFee: big.NewInt(15_000)})
	rdb.RPush(ctx, fmt.Sprintf(voucher.VoucherQueueKeyFmt, provider.Hex()), raw)

	set, err := b.Notices(ctx, []string{"a-1", "a-2", "b-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(set["b-1"]) != 0 {
		t.Errorf("bob (100000 s left) warned: %+v", set["b-1"])
	}
	for _, id := range []string{"a-1", "a-2"} {
		if len(set[id]) != 1 {
			t.Fatalf("%s: %+v, want one notice", id, set[id])
		}
		n := set[id][0]
		want := now.Add(58 * time.Minute)
		if n.Kind != KindBalanceLow || n.Severity != SeverityWarning || n.Action != ActionStop || n.Deadline == nil || !n.Deadline.Equal(want) ||
			!strings.Contains(n.Message, "about 58 minutes") || !strings.Contains(n.Message, "(2, this one included)") {
			t.Errorf("%s: %+v, want a warning with deadline %v", id, n, want)
		}
	}
	if _, ok := set["a-gone"]; ok {
		t.Error("a sandbox not running was warned")
	}
}

func TestBalance_UsedUp(t *testing.T) {
	chain := &fakeChain{balances: map[common.Address]*big.Int{alice: big.NewInt(5)}}
	b, rdb, now := newBalance(t, chain)
	session(t, rdb, "a-1", alice, "") // no recorded price: the fallback, 7/s
	set, err := b.Notices(context.Background(), []string{"a-1"})
	if err != nil {
		t.Fatal(err)
	}
	n := set["a-1"]
	if len(n) != 1 || n[0].Severity != SeverityCritical || !n[0].Deadline.Equal(now) || !strings.Contains(n[0].Message, "used up") {
		t.Errorf("used up: %+v", n)
	}
}

func TestBalance_OffAndErrors(t *testing.T) {
	chain := &fakeChain{balances: map[common.Address]*big.Int{alice: big.NewInt(1)}}
	b, rdb, _ := newBalance(t, chain)
	session(t, rdb, "a-1", alice, "10")
	session(t, rdb, "free", bob, "0")

	b.Within = 0
	if set, err := b.Notices(context.Background(), []string{"a-1"}); err != nil || len(set) != 0 {
		t.Errorf("off: %+v, %v", set, err)
	}
	b.Within = time.Hour
	if set, err := b.Notices(context.Background(), []string{"free"}); err != nil || len(set) != 0 || chain.asked != nil {
		t.Errorf("free sandbox: %+v, %v; chain asked %v; want nothing", set, err, chain.asked)
	}
	chain.err = errors.New("rpc down")
	if _, err := b.Notices(context.Background(), []string{"a-1"}); err == nil {
		t.Error("chain error not returned")
	}
}

// Paying a voucher lowers the balance and moves the sandbox's next voucher
// time by one period: the deadline must not move, or every poll would look
// like a new notice to the sandbox.
func TestBalance_DeadlineStaysPutAsVouchersArePaid(t *testing.T) {
	ctx := context.Background()
	chain := &fakeChain{balances: map[common.Address]*big.Int{alice: big.NewInt(10_000)}}
	b, rdb, start := newBalance(t, chain)
	now := start
	b.Now = func() time.Time { return now }
	put := func(id string, next time.Time) {
		t.Helper()
		if err := billing.CreateSession(ctx, rdb, billing.Session{SandboxID: id, Owner: alice.Hex(), Provider: provider.Hex(),
			PricePerSec: "10", NextVoucherAt: next.Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	// Two sandboxes, 600 a period each, due 20 s and 50 s from now.
	put("a-1", start.Add(20*time.Second))
	put("a-2", start.Add(50*time.Second))
	deadline := func() time.Time {
		t.Helper()
		set, err := b.Notices(ctx, []string{"a-1", "a-2"})
		if err != nil || len(set["a-1"]) != 1 {
			t.Fatalf("notices = %+v, %v", set, err)
		}
		return *set["a-1"][0].Deadline
	}
	// 10000 pays 8 periods of both (9600), then 400: a-1's next 600 is not
	// covered, due 20 s + 8 min from now.
	want := start.Add(8*time.Minute + 20*time.Second)
	if got := deadline(); !got.Equal(want) {
		t.Fatalf("deadline = %v, want %v", got, want)
	}
	now = start.Add(15 * time.Second) // time passes, nothing paid
	if got := deadline(); !got.Equal(want) {
		t.Errorf("after 15 s: deadline = %v, want %v", got, want)
	}
	// a-1's voucher is paid: 600 less, next one a period later.
	now = start.Add(21 * time.Second)
	put("a-1", start.Add(80*time.Second))
	chain.balances[alice] = big.NewInt(9_400)
	if got := deadline(); !got.Equal(want) {
		t.Errorf("after a-1 paid: deadline = %v, want %v", got, want)
	}
	// And a-2's.
	now = start.Add(51 * time.Second)
	put("a-2", start.Add(110*time.Second))
	chain.balances[alice] = big.NewInt(8_800)
	if got := deadline(); !got.Equal(want) {
		t.Errorf("after both paid: deadline = %v, want %v", got, want)
	}
}

func TestRunsOut(t *testing.T) {
	c := func(at, fee int64) charge { return charge{at: at, fee: big.NewInt(fee)} }
	for name, tc := range map[string]struct {
		left    int64
		charges []charge
		by      int64
		want    int64
		ok      bool
	}{
		"nothing left":       {0, []charge{c(100, 60)}, 10_000, 100, true},
		"one period":         {60, []charge{c(100, 60)}, 10_000, 160, true},
		"order by due time":  {70, []charge{c(130, 50), c(100, 60)}, 10_000, 130, true},
		"past by":            {6000, []charge{c(100, 60)}, 1000, 0, false},
		"exactly by":         {600, []charge{c(100, 60)}, 700, 700, true},
		"free":               {10, []charge{c(100, 0)}, 10_000, 0, false},
		"huge balance stays": {1 << 62, []charge{c(100, 1)}, 10_000, 0, false},
	} {
		got, ok := runsOut(big.NewInt(tc.left), tc.charges, 60, tc.by)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: runsOut = %d, %v; want %d, %v", name, got, ok, tc.want, tc.ok)
		}
	}
}
