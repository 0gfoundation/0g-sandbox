package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/tee"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
)

type appInstanceReader interface {
	GetAppInstance(context.Context, string) (uint64, common.Address, error)
}

// A first successful bootstrap persists the authorized registration instance.
// Reusing the app name after removal never silently authorizes its new owner.
// Re-authorizing another instance is an explicit operator configuration change.
func newAttestorAuthorizer(chainID uint64, registry, appID string, rdb *redis.Client, chain appInstanceReader,
	resolve func(context.Context, uint64, string, string) (*tee.AppIdentity, error)) func(context.Context, string) (bool, error) {
	var mu sync.Mutex
	var cached *tee.AppIdentity
	key := fmt.Sprintf("identity:attestor:%d:%s:%s:instance", chainID, strings.ToLower(registry), appID)
	return func(ctx context.Context, wallet string) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		if !common.IsHexAddress(wallet) {
			return false, nil
		}
		if cached == nil || !time.Now().Before(cached.ExpiresAt) {
			identity, err := resolve(ctx, chainID, registry, appID)
			if err != nil {
				return false, err
			}
			if identity == nil || !time.Now().Before(identity.ExpiresAt) {
				return false, fmt.Errorf("attestor identity expired during discovery")
			}
			cached = identity
		}
		current, owner, err := chain.GetAppInstance(ctx, appID)
		if err != nil {
			return false, err
		}
		if current == 0 || owner == (common.Address{}) || current != cached.RegisteredAt {
			return false, fmt.Errorf("attestor registration instance changed")
		}
		// Redis persists the authorization alongside the provider's other state.
		instance := strconv.FormatUint(current, 10) + ":" + strings.ToLower(owner.Hex())
		if err := rdb.SetNX(ctx, key, instance, 0).Err(); err != nil {
			return false, err
		}
		pinned, err := rdb.Get(ctx, key).Result()
		if err != nil {
			return false, err
		}
		if pinned != instance {
			return false, fmt.Errorf("attestor instance requires explicit re-authorization")
		}
		if !time.Now().Before(cached.ExpiresAt) {
			return false, fmt.Errorf("attestor identity expired during authorization")
		}
		return common.HexToAddress(wallet) == cached.Signer, nil
	}
}
