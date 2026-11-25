package settler

import (
	"context"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestCheckPendingSettlementDoesNotChangeRecord(t *testing.T) {
	ctx := context.Background()
	rdb, provider, p, _ := pendingFixture(t)
	if err := savePendingTx(ctx, rdb, provider, p); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf(PendingTxKeyFmt, provider.Hex())
	before, err := rdb.Get(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPendingSettlement(ctx, rdb, provider, p.TxHash, p.AccountNonce); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		provider common.Address
		hash     common.Hash
		nonce    uint64
	}{
		{common.HexToAddress("0x1"), p.TxHash, p.AccountNonce},
		{provider, common.HexToHash("0x1"), p.AccountNonce},
		{provider, p.TxHash, p.AccountNonce + 1},
	} {
		if err := CheckPendingSettlement(ctx, rdb, check.provider, check.hash, check.nonce); err == nil {
			t.Fatalf("accepted mismatched pending transaction: %+v", check)
		}
	}
	if after, err := rdb.Get(ctx, key).Result(); err != nil || after != before {
		t.Fatalf("pending record changed after verification: %v", err)
	}
}
