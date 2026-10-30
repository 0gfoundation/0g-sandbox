package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/tee"
	"github.com/ethereum/go-ethereum/common"
)

type instanceStub struct {
	epoch uint64
	owner common.Address
	err   error
}

func (s *instanceStub) GetAppInstance(context.Context, string) (uint64, common.Address, error) {
	return s.epoch, s.owner, s.err
}

func TestAttestorAppSignerAuthorizationAndInstancePin(t *testing.T) {
	ctx := context.Background()
	rdb := newTestRedis(t)
	signer := common.HexToAddress("0x1111111111111111111111111111111111111111")
	node := common.HexToAddress("0x2222222222222222222222222222222222222222")
	chain := &instanceStub{epoch: 100, owner: common.HexToAddress("0x4444444444444444444444444444444444444444")}
	lookups := 0
	resolve := func(_ context.Context, id uint64, registry, app string) (*tee.AppIdentity, error) {
		lookups++
		if id != 16602 || app != "attestor" {
			t.Fatal("wrong identity scope")
		}
		return &tee.AppIdentity{AppID: app, Signer: signer, RegisteredAt: chain.epoch, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	check := newAttestorIdentity(16602, "0x3333333333333333333333333333333333333333", "attestor", rdb, chain, resolve).IsAppSigner
	if ok, err := check(ctx, signer.Hex()); !ok || err != nil {
		t.Fatalf("app signer rejected: %v", err)
	}
	if ok, err := check(ctx, node.Hex()); ok || err != nil {
		t.Fatalf("node signer authorized: %v", err)
	}
	if ok, err := check(ctx, "not-an-address"); ok || err != nil {
		t.Fatalf("malformed wallet authorized: %v", err)
	}
	if lookups != 1 {
		t.Fatalf("fresh result not cached: %d", lookups)
	}
	chain.err = errors.New("RPC unavailable")
	if ok, err := check(ctx, signer.Hex()); ok || err == nil {
		t.Fatal("chain outage accepted stale authority")
	}
	chain.err = nil
	// registeredAt is a timestamp; another owner can re-register in the same block.
	previousOwner := chain.owner
	chain.owner = common.HexToAddress("0x5555555555555555555555555555555555555555")
	if ok, err := check(ctx, signer.Hex()); ok || err == nil {
		t.Fatal("same-timestamp owner change inherited authority")
	}
	chain.owner = previousOwner
	chain.epoch = 200
	if ok, err := check(ctx, signer.Hex()); ok || err == nil {
		t.Fatal("registration change accepted")
	}
	// A process restart cannot turn a new instance into the old authorization.
	check = newAttestorIdentity(16602, "0x3333333333333333333333333333333333333333", "attestor", rdb, chain, resolve).IsAppSigner
	if ok, err := check(ctx, signer.Hex()); ok || err == nil {
		t.Fatal("re-registration inherited persisted authority")
	}
}

func TestAttestorIdentityTLSPins(t *testing.T) {
	ctx := context.Background()
	chain := &instanceStub{epoch: 100, owner: common.HexToAddress("0x4444444444444444444444444444444444444444")}
	pins := []string{"bb", "aa"}
	lease := time.Minute
	var lookups atomic.Int32
	resolve := func(context.Context, uint64, string, string) (*tee.AppIdentity, error) {
		lookups.Add(1)
		return &tee.AppIdentity{AppID: "attestor", Signer: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			TLSPins: pins, RegisteredAt: chain.epoch, ExpiresAt: time.Now().Add(lease)}, nil
	}
	identity := newAttestorIdentity(16602, "0x3333333333333333333333333333333333333333", "attestor", newTestRedis(t), chain, resolve)

	got, expiresAt, err := identity.TLSPins(ctx, 30*time.Second)
	if err != nil || len(got) != 2 || got[0] != "bb" || got[1] != "aa" || time.Until(expiresAt) <= 30*time.Second {
		t.Fatalf("pins %v until %s: %v", got, expiresAt, err)
	}
	got[0] = "changed"
	if again, _, _ := identity.TLSPins(ctx, 30*time.Second); again[0] != "bb" || lookups.Load() != 1 {
		t.Fatalf("cached identity mutated or re-resolved: %v, %d lookups", again, lookups.Load())
	}
	// Asking for more lease than the cached identity has left re-resolves it.
	if _, _, err := identity.TLSPins(ctx, 2*time.Minute); err != nil || lookups.Load() != 2 {
		t.Fatalf("minLease did not re-resolve: %v, %d lookups", err, lookups.Load())
	}
	// The registration-instance check applies to the pins as well.
	chain.epoch = 200
	if _, _, err := identity.TLSPins(ctx, 0); err == nil {
		t.Fatal("pins served for a changed registration instance")
	}
}

func TestAttestorIdentitySharesConcurrentDiscovery(t *testing.T) {
	chain := &instanceStub{epoch: 100, owner: common.HexToAddress("0x4444444444444444444444444444444444444444")}
	release := make(chan struct{})
	var lookups atomic.Int32
	resolve := func(context.Context, uint64, string, string) (*tee.AppIdentity, error) {
		lookups.Add(1)
		<-release
		return &tee.AppIdentity{AppID: "attestor", Signer: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			TLSPins: []string{"aa"}, RegisteredAt: chain.epoch, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	identity := newAttestorIdentity(16602, "0x3333333333333333333333333333333333333333", "attestor", newTestRedis(t), chain, resolve)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := identity.Current(context.Background(), 0)
			errs <- err
		}()
	}
	for lookups.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if lookups.Load() != 1 {
		t.Fatalf("%d concurrent discoveries, want 1", lookups.Load())
	}
}

// The shared lookup runs on its own context: a caller's deadline neither
// cancels it for the others nor makes the caller wait past that deadline.
func TestAttestorIdentityLookupKeepsEachCallersDeadline(t *testing.T) {
	chain := &instanceStub{epoch: 100, owner: common.HexToAddress("0x4444444444444444444444444444444444444444")}
	release := make(chan struct{})
	started := make(chan context.Context, 1)
	var lookups atomic.Int32
	resolve := func(ctx context.Context, _ uint64, _, _ string) (*tee.AppIdentity, error) {
		lookups.Add(1)
		started <- ctx
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &tee.AppIdentity{AppID: "attestor", Signer: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			TLSPins: []string{"aa"}, RegisteredAt: chain.epoch, ExpiresAt: time.Now().Add(time.Minute)}, nil
	}
	identity := newAttestorIdentity(16602, "0x3333333333333333333333333333333333333333", "attestor", newTestRedis(t), chain, resolve)

	// A short-deadline caller starts the lookup and gives up on its own deadline.
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := identity.Current(short, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("short caller: %v", err)
	}
	lookupCtx := <-started
	if lookupCtx.Err() != nil {
		t.Fatal("caller deadline cancelled the shared lookup")
	}
	if deadline, ok := lookupCtx.Deadline(); !ok || time.Until(deadline) < attestorLookupTimeout-10*time.Second {
		t.Fatalf("lookup deadline %v (set %v)", deadline, ok)
	}

	// A caller joining the running lookup gets its result.
	result := make(chan error, 1)
	go func() {
		_, err := identity.Current(context.Background(), 0)
		result <- err
	}()
	// Another joiner still leaves on its own deadline.
	joiner, cancelJoiner := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelJoiner()
	if _, err := identity.Current(joiner, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("joiner: %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 1 {
		t.Fatalf("%d lookups, want 1", lookups.Load())
	}
}
