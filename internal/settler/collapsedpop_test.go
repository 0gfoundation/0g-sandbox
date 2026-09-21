package settler

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// Settling a collapsed batch must clear every queue entry the batch consumed,
// not one per submitted voucher. Collapsing merges several entries into one
// voucher, so popping per voucher leaves the surplus entries queued — the next
// drain picks them up and settles the same compute a second time, charging the
// user twice.
func TestHandleStatuses_CollapsedBatchClearsEveryConsumedEntry(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	prov := common.HexToAddress("0x2222222222222222222222222222222222222222")
	const queueKey = "voucher:queue:test"

	// Six queue entries: three periods each for two sandboxes.
	var entries []string
	for i := 0; i < 3; i++ {
		for _, sb := range []string{"sb-alpha", "sb-beta"} {
			raw, err := json.Marshal(voucher.SandboxVoucher{
				SandboxID: sb, User: user, Provider: prov,
				TotalFee: big.NewInt(100),
			})
			if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, string(raw))
		}
	}
	if err := rdb.RPush(ctx, queueKey, toAny(entries)...).Err(); err != nil {
		t.Fatal(err)
	}

	// The settler BLPOPs the first entry, then peeks the rest.
	firstItem, err := rdb.LPop(ctx, queueKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	consumed := len(entries)

	// Collapsing turns six entries into two vouchers, one per sandbox.
	var batch []voucher.SandboxVoucher
	for _, raw := range entries {
		var v voucher.SandboxVoucher
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatal(err)
		}
		batch = append(batch, v)
	}
	collapsed := voucher.CollapseBySandbox(batch, 1000)
	if len(collapsed) != 2 {
		t.Fatalf("setup: want 2 collapsed vouchers, got %d", len(collapsed))
	}
	for i := range collapsed {
		collapsed[i].Nonce = big.NewInt(int64(i + 1))
	}

	statuses := []chain.SettlementStatus{chain.StatusSuccess, chain.StatusSuccess}
	stopCh := make(chan StopSignal, 8)
	HandleStatuses(ctx, rdb, stopCh, queueKey, firstItem, consumed, collapsed, statuses,
		alert.Nop{}, zap.NewNop())

	left, err := rdb.LLen(ctx, queueKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		remaining, _ := rdb.LRange(ctx, queueKey, 0, -1).Result()
		t.Errorf("%d settled entries left in the queue — they would settle again and double-charge: %v",
			left, remaining)
	}
}

// A pending-tx record written before collapsing existed carries no Consumed
// value. Those batches were one entry per voucher, so a zero must keep meaning
// exactly that rather than clearing nothing.
func TestHandleStatuses_ZeroConsumedFallsBackToPerVoucher(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()

	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	prov := common.HexToAddress("0x2222222222222222222222222222222222222222")
	const queueKey = "voucher:queue:test"

	var batch []voucher.SandboxVoucher
	var entries []string
	for i := 0; i < 3; i++ {
		v := voucher.SandboxVoucher{
			SandboxID: "sb-alpha", User: user, Provider: prov,
			TotalFee: big.NewInt(100), Nonce: big.NewInt(int64(i + 1)),
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		batch = append(batch, v)
		entries = append(entries, string(raw))
	}
	if err := rdb.RPush(ctx, queueKey, toAny(entries)...).Err(); err != nil {
		t.Fatal(err)
	}
	firstItem, err := rdb.LPop(ctx, queueKey).Result()
	if err != nil {
		t.Fatal(err)
	}

	statuses := []chain.SettlementStatus{chain.StatusSuccess, chain.StatusSuccess, chain.StatusSuccess}
	HandleStatuses(ctx, rdb, make(chan StopSignal, 4), queueKey, firstItem, 0, batch, statuses,
		alert.Nop{}, zap.NewNop())

	left, err := rdb.LLen(ctx, queueKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("legacy batch left %d entries queued; want 0", left)
	}
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
