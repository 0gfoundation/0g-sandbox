package voucher

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
)

const testQueueKey = "voucher:queue:test"

func setup(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return rdb, mr
}

func enqueueRaw(t *testing.T, rdb *redis.Client, v SandboxVoucher) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := rdb.RPush(context.Background(), testQueueKey, string(raw)).Err(); err != nil {
		t.Fatalf("rpush: %v", err)
	}
}

func voucherFor(sandbox string, user, provider common.Address, fee int64) SandboxVoucher {
	var hash [32]byte
	copy(hash[:], []byte(fmt.Sprintf("hash-for-%s-%d", sandbox, fee)))
	return SandboxVoucher{
		SandboxID: sandbox,
		User:      user,
		Provider:  provider,
		TotalFee:  big.NewInt(fee),
		UsageHash: hash,
	}
}

func TestAggregate_MergesSameUserProvider(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()

	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")

	// Mix of sandboxes — all (user, provider) match, should merge
	enqueueRaw(t, rdb, voucherFor("sb-1", user, prov, 100))
	enqueueRaw(t, rdb, voucherFor("sb-2", user, prov, 200))
	enqueueRaw(t, rdb, voucherFor("sb-1", user, prov, 300))

	result, err := Aggregate(context.Background(), rdb, testQueueKey, user, prov)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result.Matched != 3 {
		t.Errorf("matched: got %d want 3", result.Matched)
	}
	if result.TotalFeeWei != "600" {
		t.Errorf("total: got %q want \"600\"", result.TotalFeeWei)
	}

	// Queue should now contain exactly 1 voucher with total 600
	llen, _ := rdb.LLen(context.Background(), testQueueKey).Result()
	if llen != 1 {
		t.Fatalf("queue len after aggregate: got %d want 1", llen)
	}
	items, _ := rdb.LRange(context.Background(), testQueueKey, 0, -1).Result()
	var v SandboxVoucher
	if err := json.Unmarshal([]byte(items[0]), &v); err != nil {
		t.Fatalf("unmarshal aggregated: %v", err)
	}
	if v.TotalFee.String() != "600" {
		t.Errorf("aggregated fee: %s", v.TotalFee.String())
	}
	// Aggregated voucher uses the sentinel empty SandboxID — picking any
	// concrete sandbox as a representative would mislead per-sandbox stop logic.
	if v.SandboxID != AggregatedSandboxID {
		t.Errorf("aggregated SandboxID: got %q want empty sentinel", v.SandboxID)
	}
	if !v.IsAggregated() {
		t.Error("IsAggregated should return true for aggregate result")
	}
}

func TestAggregate_PreservesOtherUsers(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()

	u := common.HexToAddress("0xAAA")
	p := common.HexToAddress("0xBBB")
	otherUser := common.HexToAddress("0xCCC")
	otherProv := common.HexToAddress("0xDDD")

	enqueueRaw(t, rdb, voucherFor("sb-1", u, p, 100))
	enqueueRaw(t, rdb, voucherFor("sb-2", u, p, 200))
	enqueueRaw(t, rdb, voucherFor("sb-1", otherUser, p, 555))         // different user — keep
	enqueueRaw(t, rdb, voucherFor("sb-1", u, otherProv, 999))         // different provider — keep
	enqueueRaw(t, rdb, voucherFor("sb-1", u, p, 300))

	result, err := Aggregate(context.Background(), rdb, testQueueKey, u, p)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result.Matched != 3 {
		t.Errorf("matched: got %d want 3", result.Matched)
	}
	if result.TotalFeeWei != "600" {
		t.Errorf("total: got %s", result.TotalFeeWei)
	}

	// Queue should have: otherUser voucher + otherProv voucher + aggregated
	llen, _ := rdb.LLen(context.Background(), testQueueKey).Result()
	if llen != 3 {
		t.Fatalf("queue len: got %d want 3", llen)
	}
}

func TestAggregate_UsageHashFollowsCreateFeeConvention(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()

	u := common.HexToAddress("0xAAA")
	p := common.HexToAddress("0xBBB")

	enqueueRaw(t, rdb, voucherFor("sb-1", u, p, 100))
	enqueueRaw(t, rdb, voucherFor("sb-1", u, p, 200))

	before := time.Now().Unix()
	if _, err := Aggregate(context.Background(), rdb, testQueueKey, u, p); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	after := time.Now().Unix()

	items, _ := rdb.LRange(context.Background(), testQueueKey, 0, -1).Result()
	var v SandboxVoucher
	if err := json.Unmarshal([]byte(items[0]), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// hash should equal BuildUsageHash("", ts, ts, 0) for some ts in [before, after].
	found := false
	for ts := before; ts <= after; ts++ {
		want := BuildUsageHash(AggregatedSandboxID, ts, ts, 0)
		if v.UsageHash == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("aggregated usage_hash doesn't match BuildUsageHash(\"\", t, t, 0) for any t in [%d, %d]", before, after)
	}
}

func TestAggregate_NoMatch(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	enqueueRaw(t, rdb, voucherFor("sb-1", common.HexToAddress("0xA"), common.HexToAddress("0xB"), 100))

	result, err := Aggregate(context.Background(), rdb, testQueueKey, common.HexToAddress("0xZ"), common.HexToAddress("0xY"))
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result.Matched != 0 {
		t.Errorf("matched: %d", result.Matched)
	}
	llen, _ := rdb.LLen(context.Background(), testQueueKey).Result()
	if llen != 1 {
		t.Errorf("queue len: %d", llen)
	}
}

func TestAggregate_BigIntFees(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	u := common.HexToAddress("0xAAA")
	p := common.HexToAddress("0xBBB")

	bigFee, _ := new(big.Int).SetString("60000000000000000", 10) // 0.06 0G
	for i := 0; i < 100; i++ {
		v := SandboxVoucher{
			SandboxID: "sb-1", User: u, Provider: p, TotalFee: bigFee,
		}
		enqueueRaw(t, rdb, v)
	}
	result, err := Aggregate(context.Background(), rdb, testQueueKey, u, p)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := new(big.Int).Mul(bigFee, big.NewInt(100))
	if result.TotalFeeWei != want.String() {
		t.Errorf("total: got %s want %s", result.TotalFeeWei, want.String())
	}
}

func TestSummarizeQueue_GroupsByUserProvider(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	u := common.HexToAddress("0xAAA")
	p := common.HexToAddress("0xBBB")
	otherUser := common.HexToAddress("0xCCC")

	// 5 vouchers across sandboxes for (u, p) — should fold into a single row
	for i := 0; i < 3; i++ {
		enqueueRaw(t, rdb, voucherFor("sb-1", u, p, 100))
	}
	for i := 0; i < 2; i++ {
		enqueueRaw(t, rdb, voucherFor("sb-2", u, p, 50))
	}
	// Separate (otherUser, p) group with 4 vouchers
	for i := 0; i < 4; i++ {
		enqueueRaw(t, rdb, voucherFor("sb-3", otherUser, p, 200))
	}

	rows, scanned, _, err := SummarizeQueue(context.Background(), rdb, testQueueKey, 1)
	if err != nil {
		t.Fatalf("SummarizeQueue: %v", err)
	}
	if scanned != 9 {
		t.Errorf("scanned: %d", scanned)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d want 2; %+v", len(rows), rows)
	}

	// Build a quick index by user
	byUser := map[string]Summary{}
	for _, r := range rows {
		byUser[r.User] = r
	}
	r1 := byUser[u.Hex()]
	if r1.Count != 5 || r1.TotalFeeWei != "400" {
		t.Errorf("(u,p) row: count=%d total=%s want 5 / 400", r1.Count, r1.TotalFeeWei)
	}
	r2 := byUser[otherUser.Hex()]
	if r2.Count != 4 || r2.TotalFeeWei != "800" {
		t.Errorf("(otherUser,p) row: count=%d total=%s want 4 / 800", r2.Count, r2.TotalFeeWei)
	}
}

// helper: read the aggregated (SandboxID == "") voucher's fee from the queue, and
// the count of non-aggregated queue entries.
func queueAggFeeAndRest(t *testing.T, rdb *redis.Client) (aggFee *big.Int, rest int) {
	t.Helper()
	items, err := rdb.LRange(context.Background(), testQueueKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("lrange: %v", err)
	}
	for _, raw := range items {
		var v SandboxVoucher
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			continue
		}
		if v.IsAggregated() {
			aggFee = v.TotalFee
		} else {
			rest++
		}
	}
	return aggFee, rest
}

func heldLen(t *testing.T, rdb *redis.Client, user, prov common.Address) int64 {
	t.Helper()
	key := fmt.Sprintf(VoucherHeldKeyFmt, strings.ToLower(user.Hex()), strings.ToLower(prov.Hex()))
	n, err := rdb.LLen(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("held llen: %v", err)
	}
	return n
}

func TestAggregateCovered_PartialPrefix(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")
	other := common.HexToAddress("0xCCC")

	for i := 0; i < 4; i++ {
		enqueueRaw(t, rdb, voucherFor(fmt.Sprintf("sb-%d", i), user, prov, 100))
	}
	enqueueRaw(t, rdb, voucherFor("other", other, prov, 500)) // must be untouched

	// balance 250 covers the first two (sum 200); the third would overflow (300>250).
	res, err := AggregateCovered(context.Background(), rdb, testQueueKey, user, prov, big.NewInt(250))
	if err != nil {
		t.Fatal(err)
	}
	if res.Covered != 2 || res.CoveredFeeWei != "200" {
		t.Errorf("covered: %d / %s want 2 / 200", res.Covered, res.CoveredFeeWei)
	}
	if res.Held != 2 || res.HeldFeeWei != "200" {
		t.Errorf("held: %d / %s want 2 / 200", res.Held, res.HeldFeeWei)
	}
	aggFee, rest := queueAggFeeAndRest(t, rdb)
	if aggFee == nil || aggFee.String() != "200" {
		t.Errorf("queue aggregate fee: %v want 200", aggFee)
	}
	if rest != 1 { // only the other user's voucher remains non-aggregated
		t.Errorf("non-aggregated queue entries: %d want 1 (other user)", rest)
	}
	if got := heldLen(t, rdb, user, prov); got != 2 {
		t.Errorf("held list len: %d want 2", got)
	}
}

func TestAggregateCovered_FullCoverageHoldsNothing(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")
	for i := 0; i < 4; i++ {
		enqueueRaw(t, rdb, voucherFor(fmt.Sprintf("sb-%d", i), user, prov, 100))
	}
	res, err := AggregateCovered(context.Background(), rdb, testQueueKey, user, prov, big.NewInt(1000))
	if err != nil {
		t.Fatal(err)
	}
	if res.Covered != 4 || res.CoveredFeeWei != "400" || res.Held != 0 {
		t.Errorf("got covered=%d/%s held=%d want 4/400/0", res.Covered, res.CoveredFeeWei, res.Held)
	}
	if got := heldLen(t, rdb, user, prov); got != 0 {
		t.Errorf("held list len: %d want 0", got)
	}
}

func TestAggregateCovered_ZeroCoverageParksAll(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")
	for i := 0; i < 3; i++ {
		enqueueRaw(t, rdb, voucherFor(fmt.Sprintf("sb-%d", i), user, prov, 100))
	}
	// balance below the first voucher's fee → nothing settles, whole backlog parked.
	res, err := AggregateCovered(context.Background(), rdb, testQueueKey, user, prov, big.NewInt(50))
	if err != nil {
		t.Fatal(err)
	}
	if res.Covered != 0 || res.Held != 3 {
		t.Errorf("got covered=%d held=%d want 0/3", res.Covered, res.Held)
	}
	aggFee, rest := queueAggFeeAndRest(t, rdb)
	if aggFee != nil || rest != 0 {
		t.Errorf("queue should be empty: aggFee=%v rest=%d", aggFee, rest)
	}
	if got := heldLen(t, rdb, user, prov); got != 3 {
		t.Errorf("held list len: %d want 3", got)
	}
}

func TestHeldDebt_SumsHeldList(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")

	// no held list yet → zero
	d, err := HeldDebt(context.Background(), rdb, user, prov)
	if err != nil || d.Sign() != 0 {
		t.Fatalf("empty held: got %v err %v want 0", d, err)
	}

	// park three vouchers via AggregateCovered with balance 0 (covers nothing)
	for i := 0; i < 3; i++ {
		enqueueRaw(t, rdb, voucherFor(fmt.Sprintf("sb-%d", i), user, prov, 100))
	}
	if _, err := AggregateCovered(context.Background(), rdb, testQueueKey, user, prov, big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	d, err = HeldDebt(context.Background(), rdb, user, prov)
	if err != nil || d.String() != "300" {
		t.Errorf("held debt: got %v err %v want 300", d, err)
	}
}

func TestAggregateCovered_ReclaimsHeldOnTopUp(t *testing.T) {
	rdb, mr := setup(t)
	defer mr.Close()
	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")
	for i := 0; i < 4; i++ { // 4 × 100 = 400
		enqueueRaw(t, rdb, voucherFor(fmt.Sprintf("sb-%d", i), user, prov, 100))
	}
	// Pass 1: broke (balance 0) → nothing settles, whole backlog parked.
	r1, err := AggregateCovered(context.Background(), rdb, testQueueKey, user, prov, big.NewInt(0))
	if err != nil || r1.Covered != 0 || r1.Held != 4 {
		t.Fatalf("pass1: %+v err=%v", r1, err)
	}
	// Pass 2: topped up to 250 → reclaim the oldest two (200) as one aggregate, hold two.
	r2, err := AggregateCovered(context.Background(), rdb, testQueueKey, user, prov, big.NewInt(250))
	if err != nil || r2.Covered != 2 || r2.CoveredFeeWei != "200" || r2.Held != 2 {
		t.Fatalf("pass2: %+v err=%v", r2, err)
	}
	if aggFee, _ := queueAggFeeAndRest(t, rdb); aggFee == nil || aggFee.String() != "200" {
		t.Errorf("reclaimed aggregate fee: %v want 200", aggFee)
	}
	if got := heldLen(t, rdb, user, prov); got != 2 {
		t.Errorf("held after reclaim: got %d want 2", got)
	}
	if len(r2.HeldSandboxIDs) != 2 {
		t.Errorf("held sandbox ids: %v want 2 distinct", r2.HeldSandboxIDs)
	}
}
