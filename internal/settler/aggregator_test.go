package settler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

type mockAggChain struct {
	provider common.Address
	bal      map[common.Address]*big.Int
}

func (m *mockAggChain) ProviderAddress() common.Address { return m.provider }
func (m *mockAggChain) GetBalanceBatch(_ context.Context, users []common.Address, _ common.Address) ([]*big.Int, error) {
	out := make([]*big.Int, len(users))
	for i, u := range users {
		if b, ok := m.bal[u]; ok {
			out[i] = b
		} else {
			out[i] = new(big.Int)
		}
	}
	return out, nil
}

func pushVoucher(t *testing.T, rdb *redis.Client, queueKey, sandbox string, user, prov common.Address, fee int64) {
	t.Helper()
	v := voucher.SandboxVoucher{SandboxID: sandbox, User: user, Provider: prov, TotalFee: big.NewInt(fee)}
	raw, _ := json.Marshal(v)
	rdb.RPush(context.Background(), queueKey, string(raw))
}

func TestSweepBacklog_SplitsAndStops(t *testing.T) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	prov := common.HexToAddress("0xBBB")
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, prov.Hex())
	poor := common.HexToAddress("0xA11CE")
	rich := common.HexToAddress("0xB0B")

	// poor: 3×100, balance 150 → covers 1, holds 2 (two distinct sandboxes)
	pushVoucher(t, rdb, queueKey, "poor-sb-1", poor, prov, 100)
	pushVoucher(t, rdb, queueKey, "poor-sb-1", poor, prov, 100)
	pushVoucher(t, rdb, queueKey, "poor-sb-2", poor, prov, 100)
	// rich: 2×100, balance 1000 → covers all, holds 0
	pushVoucher(t, rdb, queueKey, "rich-sb", rich, prov, 100)
	pushVoucher(t, rdb, queueKey, "rich-sb", rich, prov, 100)

	chain := &mockAggChain{provider: prov, bal: map[common.Address]*big.Int{
		poor: big.NewInt(150),
		rich: big.NewInt(1000),
	}}
	stopCh := make(chan StopSignal, 16)

	sweepBacklog(context.Background(), rdb, chain, prov, queueKey, stopCh, zap.NewNop())

	// poor: held list should have 2 (the overflow), and stop signals for its held sandboxes.
	poorHeld := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(poor.Hex()), strings.ToLower(prov.Hex()))
	if n, _ := rdb.LLen(context.Background(), poorHeld).Result(); n != 2 {
		t.Errorf("poor held: got %d want 2", n)
	}
	// rich: no held list.
	richHeld := fmt.Sprintf(voucher.VoucherHeldKeyFmt, strings.ToLower(rich.Hex()), strings.ToLower(prov.Hex()))
	if n, _ := rdb.LLen(context.Background(), richHeld).Result(); n != 0 {
		t.Errorf("rich held: got %d want 0", n)
	}
	// stop signals only for poor's sandboxes (2 distinct: poor-sb-1, poor-sb-2).
	close(stopCh)
	stopped := map[string]bool{}
	for s := range stopCh {
		stopped[s.SandboxID] = true
	}
	if stopped["rich-sb"] {
		t.Error("rich sandbox must not be stopped")
	}
	if !stopped["poor-sb-2"] {
		t.Errorf("poor's held sandbox not stopped; stopped=%v", stopped)
	}
}
