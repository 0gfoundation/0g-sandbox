package settler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/config"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

type stuckSettlementChain struct{ *outageChain }

func (s stuckSettlementChain) ResolveTxFate(context.Context, common.Hash, uint64) (chain.TxFate, *types.Receipt, error) {
	return chain.TxPending, nil, nil
}

func TestPendingSettlementPreservesQueueAndStopsUnpayableSandboxAfterRestart(t *testing.T) {
	oldPollInterval := pendingTxPollInterval
	pendingTxPollInterval = 5 * time.Millisecond
	t.Cleanup(func() { pendingTxPollInterval = oldPollInterval })

	rdb, _, p, _ := pendingFixture(t)
	provider := common.HexToAddress("0x0123")
	queue := fmt.Sprintf(voucher.VoucherQueueKeyFmt, provider.Hex())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paid := common.HexToAddress("0x1234")
	poor := common.HexToAddress("0x4321")
	for id, owner := range map[string]common.Address{"paid": paid, "poor": poor} {
		if err := billing.CreateSession(ctx, rdb, billing.Session{SandboxID: id, Owner: owner.Hex(), Provider: provider.Hex()}); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(id string, user common.Address, fee int64) string {
		v := voucher.SandboxVoucher{SandboxID: id, User: user, Provider: provider, TotalFee: big.NewInt(fee)}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	// Three raw entries were collapsed into one voucher. The first entry is
	// already popped. The other two must be ignored even if the balance has
	// already been debited and the receipt is temporarily unavailable.
	p.Consumed = 3
	p.Vouchers[0].SandboxID = "paid"
	p.Vouchers[0].User = paid
	p.Vouchers[0].Provider = provider
	p.Vouchers[0].TotalFee = big.NewInt(300)
	if err := savePendingTx(ctx, rdb, provider, p); err != nil {
		t.Fatal(err)
	}
	items := []any{entry("paid", paid, 100), entry("paid", paid, 100), entry("paid", paid, 5)}
	for i := 0; i < voucher.MaxScanItems; i++ {
		items = append(items, entry("unrelated", common.Address{}, 1))
	}
	items = append(items, entry("poor", poor, 2)) // scans past the summary API limit
	if err := rdb.RPush(ctx, queue, items...).Err(); err != nil {
		t.Fatal(err)
	}
	if err := voucher.PushHeld(ctx, rdb, voucher.SandboxVoucher{SandboxID: "archived", User: poor, Provider: provider, TotalFee: big.NewInt(4)}); err != nil {
		t.Fatal(err)
	}
	before, err := rdb.LRange(ctx, queue, 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	onchain := stuckSettlementChain{&outageChain{provider: provider, bal: map[common.Address]*big.Int{paid: big.NewInt(5), poor: big.NewInt(5)}}}
	stopCh := make(chan StopSignal, 32)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		Run(ctx, &config.Config{Billing: config.BillingConfig{VoucherIntervalSec: 1}}, rdb, onchain, nopSigner{}, stopCh, alert.Nop{}, zap.NewNop())
	}()
	defer func() {
		cancel()
		<-finished
	}()
	select {
	case got := <-stopCh:
		if want := (StopSignal{SandboxID: "poor", Reason: "insufficient_balance"}); got != want {
			t.Fatalf("stop = %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop check did not run while waiting for the persisted transaction")
	}
	select {
	case got := <-stopCh:
		t.Fatalf("unexpected stop signal %+v", got)
	default:
	}
	after, err := rdb.LRange(ctx, queue, 0, -1).Result()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("pending settlement changed the queue: %v", err)
	}
	stillPending, err := loadPendingTx(ctx, rdb, provider)
	if err != nil || stillPending == nil || stillPending.TxHash != p.TxHash || onchain.settleAttempts() != 0 {
		t.Fatalf("pending settlement was advanced: %v, submitted %d", err, onchain.settleAttempts())
	}
}
