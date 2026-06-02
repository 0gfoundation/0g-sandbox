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
	settler   common.Address
	appId     string
	isNode    bool
	appIdErr  error
	nodeErr   error
}

func (f *fakeSignerClient) SettlerAddress() common.Address { return f.settler }
func (f *fakeSignerClient) GetServiceAppId(context.Context, common.Address) (string, error) {
	if f.appIdErr != nil {
		return "", f.appIdErr
	}
	return f.appId, nil
}
func (f *fakeSignerClient) IsLocalTEEActiveNode(context.Context) (bool, error) {
	if f.nodeErr != nil {
		return false, f.nodeErr
	}
	return f.isNode, nil
}

func TestCheckSigner_AlertsWhenNotANode(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{
		settler: common.HexToAddress("0x8401"),
		appId:   "sandbox-prod",
		isNode:  false,
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
	if got.details["app_id"] != "sandbox-prod" {
		t.Errorf("app_id missing: %+v", got.details)
	}
}

func TestCheckSigner_SilentWhenIsNode(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{
		settler: common.HexToAddress("0x8401"),
		appId:   "sandbox-prod",
		isNode:  true,
	}
	checkSigner(context.Background(), c, common.HexToAddress("0xB831"), r, zap.NewNop())
	if _, ok := r.last(); ok {
		t.Errorf("expected no alert when local TEE is an active node, got %+v", r.alerts)
	}
}

func TestCheckSigner_SilentWhenAppIdEmpty(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{
		settler: common.HexToAddress("0x8401"),
		appId:   "",
	}
	checkSigner(context.Background(), c, common.HexToAddress("0xB831"), r, zap.NewNop())
	if _, ok := r.last(); ok {
		t.Errorf("expected no alert when service not bound, got %+v", r.alerts)
	}
}

func TestCheckSigner_SilentWhenRPCFails(t *testing.T) {
	r := &recordingAlerter{}
	c := &fakeSignerClient{
		settler:  common.HexToAddress("0x8401"),
		appIdErr: errors.New("rpc down"),
	}
	checkSigner(context.Background(), c, common.HexToAddress("0xB831"), r, zap.NewNop())
	if _, ok := r.last(); ok {
		t.Errorf("RPC error should not fire mismatch alert, got %+v", r.alerts)
	}
}
