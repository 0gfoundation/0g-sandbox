package proxy

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

const (
	attestorTLSVersion       = 3
	attestorTLSDomainName    = "0G Sandbox AttestorTLS"
	attestorTLSDomainVersion = "3"
	attestorTLSMaxPins       = 16
	attestorTLSMaxLease      = 300 // seconds from issued_at to expires_at

	// Serving windows, measured back from the statement's expires_at.
	attestorTLSRefreshAhead = 60 * time.Second // start a background refresh
	attestorTLSServeMargin  = 30 * time.Second // serve without waiting for a refresh
	attestorTLSFailMargin   = 5 * time.Second  // keep serving while refreshes fail

	attestorTLSRetryInterval  = 5 * time.Second // minimum gap between refresh attempts
	attestorTLSRefreshTimeout = 30 * time.Second

	// Requests that wait for a running refresh, per second, all callers
	// together. Serving a cached statement takes no token.
	attestorTLSRate  = 20
	attestorTLSBurst = 50
)

var attestorTLSTypeHash = crypto.Keccak256Hash([]byte(
	"AttestorTLS(bytes32[] tlsSpkiSha256,uint64 issuedAt,uint64 expiresAt)",
))

// AttestorTLSStatement is the body of GET /api/attestor-tls: the attestor's
// current TLS SPKI pins, signed by the provider key. Field order is the wire
// order. The signature is EIP-712 over AttestorTLS in the domain of ChainID
// and RegistryAddress.
type AttestorTLSStatement struct {
	Version         uint64         `json:"version"`
	ChainID         uint64         `json:"chain_id"`
	RegistryAddress common.Address `json:"registry_address"`
	TLSSPKISHA256   []string       `json:"tls_spki_sha256"`
	IssuedAt        uint64         `json:"issued_at"`
	ExpiresAt       uint64         `json:"expires_at"`
	Signature       string         `json:"signature"`
}

func (s AttestorTLSStatement) domainSeparator() common.Hash {
	return eip712DomainSeparator(attestorTLSDomainName, attestorTLSDomainVersion, s.ChainID, s.RegistryAddress)
}

// pinsHash is the EIP-712 encoding of the bytes32[] pins: keccak256 of the
// concatenated 32-byte elements.
func (s AttestorTLSStatement) pinsHash() (common.Hash, error) {
	if len(s.TLSSPKISHA256) == 0 || len(s.TLSSPKISHA256) > attestorTLSMaxPins {
		return common.Hash{}, fmt.Errorf("attestor tls statement needs 1..%d pins", attestorTLSMaxPins)
	}
	concat := make([]byte, 0, 32*len(s.TLSSPKISHA256))
	for i, pin := range s.TLSSPKISHA256 {
		decoded, err := hex.DecodeString(pin)
		if err != nil || len(decoded) != 32 || pin != strings.ToLower(pin) {
			return common.Hash{}, fmt.Errorf("attestor tls pin must be 64 lowercase hex chars")
		}
		if i > 0 && pin <= s.TLSSPKISHA256[i-1] {
			return common.Hash{}, fmt.Errorf("attestor tls pins must be strictly ascending")
		}
		concat = append(concat, decoded...)
	}
	return crypto.Keccak256Hash(concat), nil
}

func (s AttestorTLSStatement) structHash() (common.Hash, error) {
	if s.Version != attestorTLSVersion {
		return common.Hash{}, fmt.Errorf("unsupported attestor tls statement version")
	}
	if s.ChainID == 0 || s.RegistryAddress == (common.Address{}) {
		return common.Hash{}, fmt.Errorf("invalid attestor tls statement scope")
	}
	if s.IssuedAt == 0 || s.ExpiresAt <= s.IssuedAt || s.ExpiresAt > s.IssuedAt+attestorTLSMaxLease {
		return common.Hash{}, fmt.Errorf("attestor tls statement needs issued_at < expires_at <= issued_at+%d", attestorTLSMaxLease)
	}
	pins, err := s.pinsHash()
	if err != nil {
		return common.Hash{}, err
	}
	encoded := make([]byte, 4*32)
	copy(encoded[0:32], attestorTLSTypeHash[:])
	copy(encoded[32:64], pins[:])
	putUint64Word(encoded[64:96], s.IssuedAt)
	putUint64Word(encoded[96:128], s.ExpiresAt)
	return crypto.Keccak256Hash(encoded), nil
}

func (s AttestorTLSStatement) signingDigest() (common.Hash, error) {
	structHash, err := s.structHash()
	if err != nil {
		return common.Hash{}, err
	}
	return eip712Digest(s.domainSeparator(), structHash), nil
}

// sign returns the statement's compact JSON with its signature.
func (s AttestorTLSStatement) sign(key *ecdsa.PrivateKey) ([]byte, error) {
	digest, err := s.signingDigest()
	if err != nil {
		return nil, err
	}
	if s.Signature, err = signDigest(digest, key); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// AttestorTLSPins returns the attestor's current TLS SPKI pins (lowercase hex
// SHA-256) and the end of their lease, re-resolving the identity when less
// than minLease of a cached lease remains.
type AttestorTLSPins func(ctx context.Context, minLease time.Duration) ([]string, time.Time, error)

// AttestorTLS serves GET /api/attestor-tls. It signs one statement per
// identity resolution and serves the same bytes to every caller until the
// statement nears expiry.
type AttestorTLS struct {
	key      *ecdsa.PrivateKey
	chainID  uint64
	registry common.Address
	pins     AttestorTLSPins // nil: the endpoint is unavailable
	limiter  *rate.Limiter   // bounds requests that wait for a running refresh
	now      func() time.Time
	log      *zap.Logger

	mu         sync.Mutex
	body       []byte
	expiresAt  time.Time
	lastTry    time.Time
	refreshing chan struct{} // closed when the running refresh ends; nil when none runs
}

// NewAttestorTLS builds the endpoint. A nil pins source, key or scope makes
// every request answer 503.
func NewAttestorTLS(key *ecdsa.PrivateKey, chainID uint64, registry common.Address, pins AttestorTLSPins, log *zap.Logger) *AttestorTLS {
	if key == nil || chainID == 0 || registry == (common.Address{}) {
		pins = nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &AttestorTLS{
		key: key, chainID: chainID, registry: registry, pins: pins,
		limiter: rate.NewLimiter(attestorTLSRate, attestorTLSBurst),
		now:     time.Now,
		log:     log,
	}
}

// Handle is the public, unauthenticated GET /api/attestor-tls handler.
func (a *AttestorTLS) Handle(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if a.pins == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "attestor tls unavailable"})
		return
	}
	body, status := a.statement(c.Request.Context())
	switch status {
	case http.StatusOK:
		c.Data(http.StatusOK, "application/json", body)
	case http.StatusTooManyRequests:
		c.Header("Retry-After", "1")
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
	default:
		c.Header("Retry-After", "5")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "attestor tls unavailable"})
	}
}

func (a *AttestorTLS) cached() ([]byte, time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.body, a.expiresAt
}

// statement returns the statement to serve and the response status.
//
// Inside the serving margin the cached statement is served at once, without a
// rate-limit token; from attestorTLSRefreshAhead on it also starts a background
// refresh. Past the margin a request waits for the running refresh if the
// limiter allows, and is served the cached statement until
// attestorTLSFailMargin before its expiry. A request the limiter refuses never
// waits: it still gets a valid cached statement, and otherwise 429.
func (a *AttestorTLS) statement(ctx context.Context) ([]byte, int) {
	body, expiresAt := a.cached()
	if now := a.now(); body != nil && now.Before(expiresAt.Add(-attestorTLSServeMargin)) {
		if !now.Before(expiresAt.Add(-attestorTLSRefreshAhead)) {
			a.startRefresh()
		}
		return body, http.StatusOK
	}
	limited := false
	done := a.startRefresh()
	select {
	case <-done:
		// No refresh is running: nothing to wait for.
	default:
		if limited = !a.limiter.Allow(); !limited {
			select {
			case <-done:
			case <-ctx.Done():
			}
		}
	}
	body, expiresAt = a.cached()
	if body != nil && a.now().Before(expiresAt.Add(-attestorTLSFailMargin)) {
		return body, http.StatusOK
	}
	if limited {
		return nil, http.StatusTooManyRequests
	}
	return nil, http.StatusServiceUnavailable
}

// refreshIdle is the channel startRefresh returns when no refresh runs.
var refreshIdle = func() chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}()

// startRefresh starts a background refresh unless one is running or the last
// attempt began less than attestorTLSRetryInterval ago. The returned channel
// closes when the running refresh ends; it is already closed when none runs.
// Every caller shares the one refresh and its channel.
func (a *AttestorTLS) startRefresh() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refreshing != nil {
		return a.refreshing
	}
	now := a.now()
	if !a.lastTry.IsZero() && now.Sub(a.lastTry) < attestorTLSRetryInterval {
		return refreshIdle
	}
	a.lastTry = now
	done := make(chan struct{})
	a.refreshing = done
	go a.refresh(done)
	return done
}

func (a *AttestorTLS) refresh(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), attestorTLSRefreshTimeout)
	defer cancel()
	body, expiresAt, err := a.issue(ctx)
	if err != nil {
		a.log.Warn("attestor tls statement refresh failed", zap.Error(err))
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err == nil {
		a.body, a.expiresAt = body, expiresAt
	}
	a.refreshing = nil
	close(done)
}

func (a *AttestorTLS) issue(ctx context.Context) ([]byte, time.Time, error) {
	pins, leaseEnd, err := a.pins(ctx, attestorTLSRefreshAhead)
	if err != nil {
		return nil, time.Time{}, err
	}
	sorted := append([]string(nil), pins...)
	sort.Strings(sorted)
	issuedAt := a.now().Unix()
	expiresAt := leaseEnd.Unix()
	if limit := issuedAt + attestorTLSMaxLease; expiresAt > limit {
		expiresAt = limit
	}
	if issuedAt <= 0 || expiresAt <= issuedAt {
		return nil, time.Time{}, fmt.Errorf("attestor identity lease has ended")
	}
	body, err := AttestorTLSStatement{
		Version: attestorTLSVersion, ChainID: a.chainID, RegistryAddress: a.registry,
		TLSSPKISHA256: sorted, IssuedAt: uint64(issuedAt), ExpiresAt: uint64(expiresAt),
	}.sign(a.key)
	if err != nil {
		return nil, time.Time{}, err
	}
	return body, time.Unix(expiresAt, 0), nil
}
