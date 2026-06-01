package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
)

type fakeSignerClient struct {
	settler common.Address
	onchain common.Address
	err     error
}

func (f *fakeSignerClient) SettlerAddress() common.Address { return f.settler }
func (f *fakeSignerClient) GetServiceTEESignerAddress(context.Context, common.Address) (common.Address, error) {
	if f.err != nil {
		return common.Address{}, f.err
	}
	return f.onchain, nil
}

func TestCheckSigner_AlertsOnMismatch(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{
		settler: common.HexToAddress("0x8401"),
		onchain: common.HexToAddress("0x3Dc1"),
	}
	checkSigner(context.Background(), c, common.HexToAddress("0xB831"), r, zap.NewNop())

	got, ok := r.last()
	if !ok {
		t.Fatal("no alert fired")
	}
	if got.kind != alert.KindSettlerSignerMismatch {
		t.Errorf("kind: got %q", got.kind)
	}
	if got.severity != alert.SeverityCritical {
		t.Errorf("severity: got %q", got.severity)
	}
	if got.details["settler_addr"] == got.details["onchain_signer_addr"] {
		t.Errorf("addresses should differ in alert details")
	}
}

func TestCheckSigner_SilentWhenAligned(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{
		settler: common.HexToAddress("0x8401"),
		onchain: common.HexToAddress("0x8401"),
	}
	checkSigner(context.Background(), c, common.HexToAddress("0xB831"), r, zap.NewNop())
	if _, ok := r.last(); ok {
		t.Errorf("expected no alert when aligned, got %+v", r.alerts)
	}
}

func TestCheckSigner_SilentWhenRPCFails(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{err: errors.New("rpc down")}
	checkSigner(context.Background(), c, common.HexToAddress("0xB831"), r, zap.NewNop())
	if _, ok := r.last(); ok {
		t.Errorf("RPC error should not fire mismatch alert, got %+v", r.alerts)
	}
}

func TestCheckSigner_SilentWhenProviderNotRegistered(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{
		settler: common.HexToAddress("0x8401"),
		onchain: common.Address{}, // zero = not registered
	}
	checkSigner(context.Background(), c, common.HexToAddress("0xB831"), r, zap.NewNop())
	if _, ok := r.last(); ok {
		t.Errorf("expected no alert when provider unregistered, got %+v", r.alerts)
	}
}
