package observability

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
)

// fakeClient lets the balance check be exercised without an RPC connection.
type fakeClient struct {
	addr     common.Address
	balance  *big.Int
	gasPrice *big.Int
	err      error
}

func (f *fakeClient) SettlerAddress() common.Address { return f.addr }
func (f *fakeClient) BalanceAt(context.Context, common.Address) (*big.Int, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.balance, nil
}
func (f *fakeClient) SuggestGasPrice(context.Context) (*big.Int, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.gasPrice, nil
}

type capturedAlert struct {
	kind     alert.Kind
	severity alert.Severity
	message  string
	details  map[string]any
}

type recordingAlerter struct {
	mu      sync.Mutex
	alerts  []capturedAlert
}

func (r *recordingAlerter) Notify(_ context.Context, k alert.Kind, s alert.Severity, m string, d map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.alerts = append(r.alerts, capturedAlert{k, s, m, d})
}

func (r *recordingAlerter) last() (capturedAlert, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.alerts) == 0 {
		return capturedAlert{}, false
	}
	return r.alerts[len(r.alerts)-1], true
}

// gasPrice * settleTxGas = 1 tx cost
// With gasPrice = 1 gwei (1e9) and settleTxGas = 3e5, one tx costs 3e14 wei.
const oneGwei = int64(1_000_000_000)

func TestCheck_CriticalWhenBelowOneTx(t *testing.T) {
	r := &recordingAlerter{}
	// Balance well below one tx
	c := &fakeClient{
		addr:     common.HexToAddress("0xAAA"),
		balance:  big.NewInt(100),
		gasPrice: big.NewInt(oneGwei),
	}
	check(context.Background(), c, c.addr, r, 100, zap.NewNop())

	got, ok := r.last()
	if !ok {
		t.Fatal("no alert fired")
	}
	if got.kind != alert.KindSettlerNoBalance {
		t.Errorf("kind: got %q want %q", got.kind, alert.KindSettlerNoBalance)
	}
	if got.severity != alert.SeverityCritical {
		t.Errorf("severity: got %q", got.severity)
	}
}

func TestCheck_WarningBetweenThresholds(t *testing.T) {
	r := &recordingAlerter{}
	// One tx cost = 1e9 * 3e5 = 3e14. Set balance to 5e15 → between 1 tx and 100 tx (3e16).
	c := &fakeClient{
		addr:     common.HexToAddress("0xAAA"),
		balance:  big.NewInt(5_000_000_000_000_000),
		gasPrice: big.NewInt(oneGwei),
	}
	check(context.Background(), c, c.addr, r, 100, zap.NewNop())

	got, ok := r.last()
	if !ok {
		t.Fatal("no alert fired")
	}
	if got.kind != alert.KindSettlerLowBalance {
		t.Errorf("kind: got %q want %q", got.kind, alert.KindSettlerLowBalance)
	}
	if got.severity != alert.SeverityWarning {
		t.Errorf("severity: got %q", got.severity)
	}
}

func TestCheck_SilentWhenHealthy(t *testing.T) {
	r := &recordingAlerter{}
	// Balance >> 100-tx threshold (3e16). Use 1 ether = 1e18.
	c := &fakeClient{
		addr:     common.HexToAddress("0xAAA"),
		balance:  new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
		gasPrice: big.NewInt(oneGwei),
	}
	check(context.Background(), c, c.addr, r, 100, zap.NewNop())

	if _, ok := r.last(); ok {
		t.Errorf("expected no alert with healthy balance, got %+v", r.alerts)
	}
}

func TestCheck_RpcErrorDoesNotAlert(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeClient{err: errors.New("rpc down")}
	check(context.Background(), c, common.Address{}, r, 100, zap.NewNop())
	if _, ok := r.last(); ok {
		t.Errorf("RPC error should not fire balance alert, got %+v", r.alerts)
	}
}
