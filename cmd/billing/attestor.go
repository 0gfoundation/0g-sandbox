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
	"golang.org/x/sync/singleflight"
)

// attestorLookupTimeout bounds one shared tapp lookup. It equals the admin
// check's budget (Handler.isAdmin) and runs detached from every caller.
const attestorLookupTimeout = 125 * time.Second

type appInstanceReader interface {
	GetAppInstance(context.Context, string) (uint64, common.Address, error)
}

// attestorIdentity is the configured attestor app's identity as discovered
// through the local tapp. Every use rechecks the live registration instance.
//
// The authorized registration instance is the app's registeredAt and owner.
// A first successful bootstrap persists it. Reusing the app name after
// removal never silently authorizes its new owner, and neither does an
// ownership transfer. Re-authorizing another instance is an explicit operator
// configuration change: ATTESTOR_INSTANCE names it and replaces the stored one.
type attestorIdentity struct {
	chainID  uint64
	registry string
	appID    string
	rdb      *redis.Client
	chain    appInstanceReader
	resolve  func(context.Context, uint64, string, string) (*tee.AppIdentity, error)
	key      string
	// authorized is ATTESTOR_INSTANCE in its stored form; empty keeps the
	// stored instance.
	authorized string

	lookups singleflight.Group
	mu      sync.Mutex
	cached  *tee.AppIdentity
}

// newAttestorIdentity takes authorized, the normalized ATTESTOR_INSTANCE, or
// empty to keep the stored instance.
func newAttestorIdentity(chainID uint64, registry, appID, authorized string, rdb *redis.Client, chain appInstanceReader,
	resolve func(context.Context, uint64, string, string) (*tee.AppIdentity, error)) *attestorIdentity {
	return &attestorIdentity{
		chainID: chainID, registry: registry, appID: appID, authorized: authorized, rdb: rdb, chain: chain, resolve: resolve,
		key: fmt.Sprintf("identity:attestor:%d:%s:%s:instance", chainID, strings.ToLower(registry), appID),
	}
}

// storeAuthorized replaces the stored instance with ATTESTOR_INSTANCE, so the
// authorization also holds once the setting is removed. It runs at startup and
// does nothing without the setting.
func (s *attestorIdentity) storeAuthorized(ctx context.Context) error {
	if s.authorized == "" {
		return nil
	}
	return s.rdb.Set(ctx, s.key, s.authorized, 0).Err()
}

// pinned returns the authorized instance: ATTESTOR_INSTANCE when set, else the
// stored one, which the first verified instance becomes.
func (s *attestorIdentity) pinned(ctx context.Context, instance string) (string, error) {
	if s.authorized != "" {
		return s.authorized, nil
	}
	// Redis persists the authorization alongside the provider's other state.
	if err := s.rdb.SetNX(ctx, s.key, instance, 0).Err(); err != nil {
		return "", err
	}
	return s.rdb.Get(ctx, s.key).Result()
}

// Current returns the attestor identity once the live registration instance
// matches it and the authorized instance (pinned). A cached discovery is reused
// while more than minLease of its lease remains.
func (s *attestorIdentity) Current(ctx context.Context, minLease time.Duration) (*tee.AppIdentity, error) {
	identity, err := s.discovered(ctx, minLease)
	if err != nil {
		return nil, err
	}
	current, owner, err := s.chain.GetAppInstance(ctx, s.appID)
	if err != nil {
		return nil, err
	}
	if current == 0 || owner == (common.Address{}) || current != identity.RegisteredAt {
		return nil, fmt.Errorf("attestor registration instance changed")
	}
	instance := strconv.FormatUint(current, 10) + ":" + strings.ToLower(owner.Hex())
	pinned, err := s.pinned(ctx, instance)
	if err != nil {
		return nil, err
	}
	if pinned != instance {
		// Both values are public registry data; the log names what to authorize
		// and whether ATTESTOR_INSTANCE reached this process, since a deployment
		// whose compose does not forward it would otherwise fail silently.
		source := "stored; ATTESTOR_INSTANCE is not set in the provider's environment"
		if s.authorized != "" {
			source = "by ATTESTOR_INSTANCE"
		}
		return nil, fmt.Errorf("attestor instance requires explicit re-authorization: live %s, authorized %s (%s)", instance, pinned, source)
	}
	if !time.Now().Before(identity.ExpiresAt) {
		return nil, fmt.Errorf("attestor identity expired during authorization")
	}
	return identity, nil
}

// discovered shares one tapp lookup among concurrent callers instead of
// serializing them behind a lock. The lookup runs on its own context, bounded
// by attestorLookupTimeout, so no caller's deadline or cancellation reaches
// another; each caller waits only as long as its own context allows.
func (s *attestorIdentity) discovered(ctx context.Context, minLease time.Duration) (*tee.AppIdentity, error) {
	if cached := s.cachedFor(minLease); cached != nil {
		return cached, nil
	}
	lookup := s.lookups.DoChan("resolve", func() (any, error) {
		// A lookup that finished since the check above already serves this caller.
		if cached := s.cachedFor(minLease); cached != nil {
			return cached, nil
		}
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attestorLookupTimeout)
		defer cancel()
		identity, err := s.resolve(lookupCtx, s.chainID, s.registry, s.appID)
		if err != nil {
			return nil, err
		}
		if identity == nil || !time.Now().Before(identity.ExpiresAt) {
			return nil, fmt.Errorf("attestor identity expired during discovery")
		}
		s.mu.Lock()
		s.cached = identity
		s.mu.Unlock()
		return identity, nil
	})
	select {
	case result := <-lookup:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(*tee.AppIdentity), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *attestorIdentity) cachedFor(minLease time.Duration) *tee.AppIdentity {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached != nil && time.Until(s.cached.ExpiresAt) > minLease {
		return s.cached
	}
	return nil
}

// IsAppSigner authorizes the attestor's stable app signer as an application
// admin (Handler.AppAdmin).
func (s *attestorIdentity) IsAppSigner(ctx context.Context, wallet string) (bool, error) {
	if !common.IsHexAddress(wallet) {
		return false, nil
	}
	identity, err := s.Current(ctx, 0)
	if err != nil {
		return false, err
	}
	return common.HexToAddress(wallet) == identity.Signer, nil
}

// TLSPins is the pin source of GET /api/attestor-tls.
func (s *attestorIdentity) TLSPins(ctx context.Context, minLease time.Duration) ([]string, time.Time, error) {
	identity, err := s.Current(ctx, minLease)
	if err != nil {
		return nil, time.Time{}, err
	}
	return append([]string(nil), identity.TLSPins...), identity.ExpiresAt, nil
}
