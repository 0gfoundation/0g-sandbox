package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/tee"
	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
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
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
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
	check := newAttestorAuthorizer(16602, "0x3333333333333333333333333333333333333333", "attestor", rdb, chain, resolve)
	if ok, err := check(ctx, signer.Hex()); !ok || err != nil {
		t.Fatalf("app signer rejected: %v", err)
	}
	if ok, err := check(ctx, node.Hex()); ok || err != nil {
		t.Fatalf("node signer authorized: %v", err)
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
	check = newAttestorAuthorizer(16602, "0x3333333333333333333333333333333333333333", "attestor", rdb, chain, resolve)
	if ok, err := check(ctx, signer.Hex()); ok || err == nil {
		t.Fatal("re-registration inherited persisted authority")
	}
}
