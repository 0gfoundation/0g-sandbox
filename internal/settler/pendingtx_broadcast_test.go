package settler

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// broadcast must carry every field of the intent forward, changing only the
// transaction identity. The post-broadcast record is what survives in Redis
// and what resolvePendingTx hands to HandleStatuses, so a field lost here is
// lost on the main settle path, not just in crash recovery.
//
// This is checked by reflection rather than field by field: the bug it guards
// against was a hand-written second construction that omitted Consumed, and an
// explicit list would have to be remembered for the next field added — the
// same thing that was forgotten the first time.
func TestPendingTx_BroadcastPreservesEveryIntentField(t *testing.T) {
	intent := pendingTx{
		Vouchers: []voucher.SandboxVoucher{{
			SandboxID: "sb-alpha",
			User:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
			Provider:  common.HexToAddress("0x2222222222222222222222222222222222222222"),
			TotalFee:  big.NewInt(300),
		}},
		FirstItem: "raw-entry",
		Consumed:  27,
	}

	txHash := common.HexToHash("0xdeadbeef")
	const accountNonce = uint64(4242)
	got := intent.broadcast(txHash, accountNonce)

	if got.TxHash != txHash {
		t.Errorf("TxHash = %s, want %s", got.TxHash.Hex(), txHash.Hex())
	}
	if got.AccountNonce != accountNonce {
		t.Errorf("AccountNonce = %d, want %d", got.AccountNonce, accountNonce)
	}

	// Every other field must be untouched. Blank out the two that broadcast
	// is allowed to set and the result has to equal the intent.
	normalized := got
	normalized.TxHash = intent.TxHash
	normalized.AccountNonce = intent.AccountNonce
	if !reflect.DeepEqual(normalized, intent) {
		v := reflect.ValueOf(got)
		typ := v.Type()
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			if name == "TxHash" || name == "AccountNonce" {
				continue
			}
			want := reflect.ValueOf(intent).Field(i).Interface()
			if !reflect.DeepEqual(v.Field(i).Interface(), want) {
				t.Errorf("field %s not carried forward: got %v, want %v", name, v.Field(i).Interface(), want)
			}
		}
		t.Error("broadcast dropped intent state; a record persisted after broadcast " +
			"would under-pop the queue and settle the surplus entries again")
	}
}

// The count the intent records is what HandleStatuses pops by, so a collapsed
// batch must reach it intact: entries consumed, not vouchers submitted.
func TestPendingTx_BroadcastKeepsConsumedForCollapsedBatch(t *testing.T) {
	user := common.HexToAddress("0x1111111111111111111111111111111111111111")
	prov := common.HexToAddress("0x2222222222222222222222222222222222222222")

	// Six queue entries across two sandboxes collapse to two vouchers.
	var batch []voucher.SandboxVoucher
	for i := 0; i < 3; i++ {
		for _, sb := range []string{"sb-alpha", "sb-beta"} {
			batch = append(batch, voucher.SandboxVoucher{
				SandboxID: sb, User: user, Provider: prov, TotalFee: big.NewInt(100),
			})
		}
	}
	consumed := len(batch)
	collapsed := voucher.CollapseBySandbox(batch, 1000)
	if len(collapsed) != 2 {
		t.Fatalf("setup: want 2 collapsed vouchers, got %d", len(collapsed))
	}

	intent := pendingTx{Vouchers: collapsed, FirstItem: "raw", Consumed: consumed}
	got := intent.broadcast(common.HexToHash("0xabc"), 1)

	if got.Consumed != consumed {
		t.Fatalf("Consumed = %d, want %d", got.Consumed, consumed)
	}
	if got.Consumed == len(got.Vouchers) {
		t.Fatal("Consumed collapsed to the voucher count; the surplus entries would settle twice")
	}
}
