package settler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/config"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// ── degraded-mode (settler cannot submit) harness ─────────────────────────────

// outageChain simulates the outage that matters here: read calls (balances,
// node membership) still work, but every settlement submission fails — an
// empty settler wallet, an unreachable mempool. This is exactly the state
// where the on-chain stop path (INSUFFICIENT_BALANCE bounce → persistStop) is
// unreachable, because nothing settles at all.
type outageChain struct {
	provider common.Address

	mu      sync.Mutex
	bal     map[common.Address]*big.Int
	settles int
}

func (o *outageChain) SettleFeesWithTEE(context.Context, []voucher.SandboxVoucher) ([]chain.SettlementStatus, error) {
	o.mu.Lock()
	o.settles++
	o.mu.Unlock()
	return nil, errors.New("SettleFeesWithTEE tx: insufficient funds for gas * price + value")
}

func (o *outageChain) ProviderAddress() common.Address { return o.provider }

func (o *outageChain) IsLocalTEEActiveNode(context.Context) (bool, error) { return true, nil }

func (o *outageChain) GetBalanceBatch(_ context.Context, users []common.Address, _ common.Address) ([]*big.Int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]*big.Int, len(users))
	for i, u := range users {
		if b, ok := o.bal[u]; ok && b != nil {
			out[i] = b
		} else {
			out[i] = new(big.Int)
		}
	}
	return out, nil
}

// topUp changes a user balance mid-outage, as a deposit landing would.
func (o *outageChain) topUp(u common.Address, wei *big.Int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.bal[u] = wei
}

// settleAttempts snapshots the submit counter race-free.
func (o *outageChain) settleAttempts() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.settles
}

// nopSigner leaves vouchers unsigned: the faked settlement errors before any
// signature check, and the sweep's split logic never looks at signatures.
type nopSigner struct{}

func (nopSigner) Sign(context.Context, *voucher.SandboxVoucher) error { return nil }

// startOutageSettler runs the real settler loop against a miniredis and an
// outageChain. VoucherIntervalSec=1 keeps BLPOP and both sweep throttles fast;
// the 5-second submit-retry sleep is what paces the test.
func startOutageSettler(t *testing.T, balances map[common.Address]*big.Int) (*redis.Client, *outageChain, chan StopSignal, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prov := common.HexToAddress("0xPPP")
	onchain := &outageChain{provider: prov, bal: balances}
	cfg := &config.Config{Billing: config.BillingConfig{VoucherIntervalSec: 1}}
	stopCh := make(chan StopSignal, 32)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Run(ctx, cfg, rdb, onchain, nopSigner{}, stopCh, alert.Nop{}, zap.NewNop())
	return rdb, onchain, stopCh, cancel
}

func drainStops(stopCh <-chan StopSignal) map[string]string {
	stops := map[string]string{}
	for {
		select {
		case s := <-stopCh:
			stops[s.SandboxID] = s.Reason
		default:
			return stops
		}
	}
}

// The live-observed gap: settler cannot submit (dry wallet), the queue is
// small (far below backlogSweepThreshold), the user's balance is zero — and
// the sandbox keeps running unbilled. Before this fix the periodic sweep
// stayed dormant below the threshold and the on-chain bounce path was
// unreachable, so nothing ever called persistStop. With the forced sweep on
// failed submits, the first failed cycle parks the debt and stops the
// sandboxes that incurred it.
func TestOutage_StopsUnpayableSandboxOnSmallQueue(t *testing.T) {
	poor := common.HexToAddress("0xA11CE")
	rdb, onchain, stopCh, cancel := startOutageSettler(t, map[common.Address]*big.Int{poor: big.NewInt(0)})

	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, onchain.provider.Hex())
	pushVoucher(t, rdb, queueKey, "poor-sb-1", poor, onchain.provider, 100)
	pushVoucher(t, rdb, queueKey, "poor-sb-1", poor, onchain.provider, 100)
	pushVoucher(t, rdb, queueKey, "poor-sb-2", poor, onchain.provider, 100)

	// First cycle: BLPOP → sign → submit fails → forced sweep fires (lastForcedSweep
	// is zero) → debt parked, both sandboxes stopped. One retry cycle is plenty;
	// the 5s sleep paces it.
	time.Sleep(2 * time.Second)
	cancel()

	stops := drainStops(stopCh)
	if stops["poor-sb-1"] != "insufficient_balance" {
		t.Errorf("poor-sb-1 not stopped during outage; stops=%v", stops)
	}
	if stops["poor-sb-2"] != "insufficient_balance" {
		t.Errorf("poor-sb-2 not stopped during outage; stops=%v", stops)
	}
	for _, sb := range []string{"poor-sb-1", "poor-sb-2"} {
		got, err := rdb.Get(context.Background(), "stop:sandbox:"+sb).Result()
		if err != nil || got != "insufficient_balance" {
			t.Errorf("stop marker %s: got %q err=%v", sb, got, err)
		}
	}
	// All three vouchers moved to held debt; the queue no longer feeds the
	// (failing) settle path for this user.
	heldKey := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(poor.Hex()), strings.ToLower(onchain.provider.Hex()))
	if n, _ := rdb.LLen(context.Background(), heldKey).Result(); n != 3 {
		t.Errorf("held debt: got %d want 3", n)
	}
	if onchain.settleAttempts() == 0 {
		t.Error("test validity: no submit was attempted")
	}
}

// The forced sweep must not over-stop: a user who CAN pay keeps running
// through the outage — their backlog folds into one covered aggregate that
// settles on recovery, and no stop signal is ever emitted for them.
func TestOutage_PayableUserNotStopped(t *testing.T) {
	rich := common.HexToAddress("0xB0B")
	rdb, onchain, stopCh, cancel := startOutageSettler(t, map[common.Address]*big.Int{rich: big.NewInt(1_000_000)})

	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, onchain.provider.Hex())
	pushVoucher(t, rdb, queueKey, "rich-sb", rich, onchain.provider, 100)
	pushVoucher(t, rdb, queueKey, "rich-sb", rich, onchain.provider, 100)

	// Two full retry cycles: the forced sweep runs on each failed submit, and
	// neither may stop a payable sandbox.
	time.Sleep(7 * time.Second)
	cancel()

	if stops := drainStops(stopCh); len(stops) != 0 {
		t.Errorf("payable user's sandbox stopped during outage: %v", stops)
	}
	// Backlog folded to one covered aggregate, nothing held.
	items, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	if len(items) != 1 {
		t.Fatalf("queue after forced sweeps: %d items, want 1 aggregate", len(items))
	}
	var agg voucher.SandboxVoucher
	if err := json.Unmarshal([]byte(items[0]), &agg); err != nil || !agg.IsAggregated() || agg.TotalFee.String() != "200" {
		t.Errorf("covered aggregate: %+v err=%v want fee 200", agg, err)
	}
	heldKey := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(rich.Hex()), strings.ToLower(onchain.provider.Hex()))
	if n, _ := rdb.LLen(context.Background(), heldKey).Result(); n != 0 {
		t.Errorf("payable user must have no held debt, got %d", n)
	}
}

// A mid-outage top-up must reclaim: the next forced sweep sees the new
// balance, folds the parked debt back onto the queue as a covered aggregate
// and clears the held-users index. (Recovery does not wait for the settler
// wallet — only settlement does.)
func TestOutage_TopUpReclaimsHeldDebt(t *testing.T) {
	u := common.HexToAddress("0xC0C")
	rdb, onchain, stopCh, cancel := startOutageSettler(t, map[common.Address]*big.Int{u: big.NewInt(0)})
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, onchain.provider.Hex())
	pushVoucher(t, rdb, queueKey, "sb-1", u, onchain.provider, 100)
	pushVoucher(t, rdb, queueKey, "sb-1", u, onchain.provider, 100)

	time.Sleep(2 * time.Second) // cycle 1: parked + stopped
	if stops := drainStops(stopCh); stops["sb-1"] != "insufficient_balance" {
		t.Fatalf("precondition: sandbox not parked/stopped, stops=%v", stops)
	}

	onchain.topUp(u, big.NewInt(1_000)) // top-up lands mid-outage
	time.Sleep(7 * time.Second)         // forced sweeps on subsequent failed submits
	cancel()

	heldKey := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(u.Hex()), strings.ToLower(onchain.provider.Hex()))
	if n, _ := rdb.LLen(context.Background(), heldKey).Result(); n != 0 {
		t.Errorf("held debt not reclaimed after top-up: %d left", n)
	}
	items, _ := rdb.LRange(context.Background(), queueKey, 0, -1).Result()
	if len(items) != 1 {
		t.Fatalf("queue after reclaim: %d items, want 1 aggregate", len(items))
	}
	var agg voucher.SandboxVoucher
	if err := json.Unmarshal([]byte(items[0]), &agg); err != nil || !agg.IsAggregated() || agg.TotalFee.String() != "200" {
		t.Errorf("reclaimed aggregate: %+v err=%v want fee 200", agg, err)
	}
}

// Rotation-gate hold: same degraded semantics — the wallet may be fine, but
// nothing can settle while the signer is not a registered node, so unpayable
// sandboxes must be stopped by the forced sweep, not left running until the
// operator finishes add-node-onchain.
func TestOutage_RotationHoldStopsUnpayable(t *testing.T) {
	poor := common.HexToAddress("0xA11CE")
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	prov := common.HexToAddress("0xPPP")

	// node-never-registered chain: active=false, submissions never attempted.
	rotating := &rotationChain{provider: prov, bal: map[common.Address]*big.Int{poor: big.NewInt(0)}}
	cfg := &config.Config{Billing: config.BillingConfig{VoucherIntervalSec: 1}}
	stopCh := make(chan StopSignal, 32)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Run(ctx, cfg, rdb, rotating, nopSigner{}, stopCh, alert.Nop{}, zap.NewNop())

	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, prov.Hex())
	pushVoucher(t, rdb, queueKey, "poor-sb", poor, prov, 100)

	time.Sleep(2 * time.Second)
	cancel()

	if stops := drainStops(stopCh); stops["poor-sb"] != "insufficient_balance" {
		t.Errorf("unpayable sandbox not stopped during rotation hold; stops=%v", stops)
	}
	if rotating.settles != 0 {
		t.Errorf("rotation hold must not submit, got %d attempts", rotating.settles)
	}
}

type rotationChain struct {
	provider common.Address
	bal      map[common.Address]*big.Int
	settles  int
}

func (r *rotationChain) SettleFeesWithTEE(context.Context, []voucher.SandboxVoucher) ([]chain.SettlementStatus, error) {
	r.settles++
	return nil, errors.New("must not be called during rotation hold")
}

func (r *rotationChain) ProviderAddress() common.Address { return r.provider }

func (r *rotationChain) IsLocalTEEActiveNode(context.Context) (bool, error) { return false, nil }

func (r *rotationChain) GetBalanceBatch(_ context.Context, users []common.Address, _ common.Address) ([]*big.Int, error) {
	out := make([]*big.Int, len(users))
	for i, u := range users {
		if b, ok := r.bal[u]; ok && b != nil {
			out[i] = b
		} else {
			out[i] = new(big.Int)
		}
	}
	return out, nil
}

// ── unit: the force flag itself ───────────────────────────────────────────────

// Regression guard for the gap this PR closes: WITHOUT force, a small queue
// with no held debt must stay completely dormant (zero balance reads) — that
// dormancy is exactly what left a small deployment unprotected during an
// outage, so it must remain an explicit opt-out, not an accident.
func TestMaybeSweep_ForceOffStaysDormantBelowThreshold(t *testing.T) {
	rdb, _, prov, queueKey := aggSetup(t)
	u := common.HexToAddress("0xAAA")
	pushVoucher(t, rdb, queueKey, "sb-1", u, prov, 100)

	chainMock := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{u: big.NewInt(0)}}
	stopCh := make(chan StopSignal, 4)
	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, map[common.Address]*big.Int{}, zap.NewNop(), false)

	if chainMock.calls != 0 {
		t.Errorf("force=false must stay dormant below threshold; got %d balance calls", chainMock.calls)
	}
	if stops := drainStops(stopCh); len(stops) != 0 {
		t.Errorf("force=false must not stop anything, got %v", stops)
	}
}

// WITH force, the same small queue is swept: an unpayable user's vouchers are
// parked as held debt and their sandbox stopped — the outage protection the
// periodic path only grants past 100 queued vouchers.
func TestMaybeSweep_ForcedSweepsBelowThreshold(t *testing.T) {
	rdb, _, prov, queueKey := aggSetup(t)
	poor := common.HexToAddress("0xA11CE")
	rich := common.HexToAddress("0xB0B")
	pushVoucher(t, rdb, queueKey, "poor-sb", poor, prov, 100)
	pushVoucher(t, rdb, queueKey, "rich-sb", rich, prov, 100)

	chainMock := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{
		poor: big.NewInt(0),
		rich: big.NewInt(1_000_000),
	}}
	stopCh := make(chan StopSignal, 4)
	maybeSweep(context.Background(), rdb, chainMock, queueKey, stopCh, map[common.Address]*big.Int{}, zap.NewNop(), true)

	if chainMock.calls != 1 {
		t.Errorf("expected one batched balance call, got %d", chainMock.calls)
	}
	stops := drainStops(stopCh)
	if stops["poor-sb"] != "insufficient_balance" {
		t.Errorf("poor sandbox not stopped by forced sweep; stops=%v", stops)
	}
	if stops["rich-sb"] != "" {
		t.Errorf("rich sandbox must not be stopped, got %q", stops["rich-sb"])
	}
	poorHeld := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(poor.Hex()), strings.ToLower(prov.Hex()))
	if n, _ := rdb.LLen(context.Background(), poorHeld).Result(); n != 1 {
		t.Errorf("poor held debt: got %d want 1", n)
	}
}
