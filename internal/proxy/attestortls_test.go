package proxy

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// attestorTLSVector is testdata/attestor-tls-v3.json, shared byte-identical
// with the attestor and the sealed runtime.
type attestorTLSVector struct {
	ProviderKey vectorKey `json:"test_only_provider_key"`
	TypedData   struct {
		Domain  vectorDomain `json:"domain"`
		Message struct {
			TLSSPKISHA256 []string `json:"tlsSpkiSha256"`
			IssuedAt      uint64   `json:"issuedAt"`
			ExpiresAt     uint64   `json:"expiresAt"`
		} `json:"message"`
	} `json:"typed_data"`
	TypeHash         string `json:"type_hash"`
	PinsHash         string `json:"tls_spki_sha256_array_hash"`
	DomainSeparator  string `json:"domain_separator"`
	StructHash       string `json:"struct_hash"`
	Digest           string `json:"digest"`
	Signature        string `json:"signature"`
	RecoveredAddress string `json:"recovered_address"`
	ResponseBody     string `json:"response_body"`
}

func loadAttestorTLSVector(t *testing.T) attestorTLSVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/attestor-tls-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	var v attestorTLSVector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (v attestorTLSVector) pins() []string {
	out := make([]string, len(v.TypedData.Message.TLSSPKISHA256))
	for i, pin := range v.TypedData.Message.TLSSPKISHA256 {
		out[i] = strings.TrimPrefix(pin, "0x")
	}
	return out
}

func TestAttestorTLSV3KnownAnswer(t *testing.T) {
	v := loadAttestorTLSVector(t)
	key := v.ProviderKey.key(t)
	if attestorTLSTypeHash.Hex() != v.TypeHash {
		t.Fatalf("type hash %s", attestorTLSTypeHash.Hex())
	}
	if v.TypedData.Domain.Name != attestorTLSDomainName || v.TypedData.Domain.Version != attestorTLSDomainVersion {
		t.Fatalf("domain %q/%q", v.TypedData.Domain.Name, v.TypedData.Domain.Version)
	}
	statement := AttestorTLSStatement{
		Version: attestorTLSVersion, ChainID: v.TypedData.Domain.ChainID, RegistryAddress: v.TypedData.Domain.VerifyingContract,
		TLSSPKISHA256: v.pins(), IssuedAt: v.TypedData.Message.IssuedAt, ExpiresAt: v.TypedData.Message.ExpiresAt,
	}
	body, err := statement.sign(key)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != v.ResponseBody {
		t.Fatalf("response body mismatch:\n got %s\nwant %s", body, v.ResponseBody)
	}

	var parsed AttestorTLSStatement
	if err := json.Unmarshal([]byte(v.ResponseBody), &parsed); err != nil {
		t.Fatal(err)
	}
	pinsHash, err := parsed.pinsHash()
	if err != nil {
		t.Fatal(err)
	}
	if pinsHash.Hex() != v.PinsHash {
		t.Fatalf("pins hash %s", pinsHash.Hex())
	}
	if got := parsed.domainSeparator().Hex(); got != v.DomainSeparator {
		t.Fatalf("domain separator %s", got)
	}
	structHash, err := parsed.structHash()
	if err != nil {
		t.Fatal(err)
	}
	if structHash.Hex() != v.StructHash {
		t.Fatalf("struct hash %s", structHash.Hex())
	}
	digest, err := parsed.signingDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest.Hex() != v.Digest || parsed.Signature != v.Signature {
		t.Fatalf("digest %s signature %s", digest.Hex(), parsed.Signature)
	}
	want := common.HexToAddress(v.RecoveredAddress)
	if want != crypto.PubkeyToAddress(key.PublicKey) || recoverDigestSigner(t, digest, parsed.Signature) != want {
		t.Fatal("statement does not recover to the provider key")
	}
	sig, _ := hex.DecodeString(strings.TrimPrefix(parsed.Signature, "0x"))
	sig[64] -= 27
	if recoverDigestSigner(t, digest, "0x"+hex.EncodeToString(sig)) != want {
		t.Fatal("0/1 recovery byte did not recover the provider key")
	}
	if out, _ := json.Marshal(parsed); string(out) != v.ResponseBody {
		t.Fatalf("re-marshalled statement differs: %s", out)
	}
}

func TestAttestorTLSStatementRejectsMalformedFields(t *testing.T) {
	v := loadAttestorTLSVector(t)
	valid := AttestorTLSStatement{
		Version: attestorTLSVersion, ChainID: v.TypedData.Domain.ChainID, RegistryAddress: v.TypedData.Domain.VerifyingContract,
		TLSSPKISHA256: v.pins(), IssuedAt: v.TypedData.Message.IssuedAt, ExpiresAt: v.TypedData.Message.ExpiresAt,
	}
	manyPins := make([]string, attestorTLSMaxPins+1)
	for i := range manyPins {
		manyPins[i] = strings.Repeat("0", 62) + hex.EncodeToString([]byte{byte(i)})
	}
	for _, tc := range []struct {
		name   string
		change func(*AttestorTLSStatement)
	}{
		{"version", func(s *AttestorTLSStatement) { s.Version = 2 }},
		{"chain", func(s *AttestorTLSStatement) { s.ChainID = 0 }},
		{"registry", func(s *AttestorTLSStatement) { s.RegistryAddress = common.Address{} }},
		{"no_pins", func(s *AttestorTLSStatement) { s.TLSSPKISHA256 = nil }},
		{"too_many_pins", func(s *AttestorTLSStatement) { s.TLSSPKISHA256 = manyPins }},
		{"unsorted", func(s *AttestorTLSStatement) { s.TLSSPKISHA256 = []string{s.TLSSPKISHA256[1], s.TLSSPKISHA256[0]} }},
		{"duplicate", func(s *AttestorTLSStatement) { s.TLSSPKISHA256 = []string{s.TLSSPKISHA256[0], s.TLSSPKISHA256[0]} }},
		{"uppercase", func(s *AttestorTLSStatement) { s.TLSSPKISHA256 = []string{strings.ToUpper(s.TLSSPKISHA256[0])} }},
		{"prefixed", func(s *AttestorTLSStatement) { s.TLSSPKISHA256 = []string{"0x" + s.TLSSPKISHA256[0]} }},
		{"short", func(s *AttestorTLSStatement) { s.TLSSPKISHA256 = []string{s.TLSSPKISHA256[0][:62]} }},
		{"empty_lease", func(s *AttestorTLSStatement) { s.ExpiresAt = s.IssuedAt }},
		{"long_lease", func(s *AttestorTLSStatement) { s.ExpiresAt = s.IssuedAt + attestorTLSMaxLease + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := valid
			changed.TLSSPKISHA256 = append([]string(nil), valid.TLSSPKISHA256...)
			tc.change(&changed)
			if _, err := changed.signingDigest(); err == nil {
				t.Fatal("accepted malformed statement")
			}
		})
	}
	valid.ExpiresAt = valid.IssuedAt + attestorTLSMaxLease
	if _, err := valid.signingDigest(); err != nil {
		t.Fatalf("rejected a %ds lease: %v", attestorTLSMaxLease, err)
	}
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// testPinSource answers with pins whose lease ends `lease` after the clock.
type testPinSource struct {
	mu       sync.Mutex
	clock    *testClock
	pins     []string
	lease    time.Duration
	err      error
	calls    int
	minLease time.Duration
}

func (s *testPinSource) get(_ context.Context, minLease time.Duration) ([]string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.minLease = minLease
	if s.err != nil {
		return nil, time.Time{}, s.err
	}
	return append([]string(nil), s.pins...), s.clock.Now().Add(s.lease), nil
}

func (s *testPinSource) set(pins []string, lease time.Duration, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pins, s.lease, s.err = pins, lease, err
}

func (s *testPinSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// newVectorAttestorTLS reproduces the vector's inputs: its key and scope, its
// pins in reverse order, and a clock at its issued_at.
func newVectorAttestorTLS(t *testing.T) (*AttestorTLS, *testPinSource, *testClock, attestorTLSVector) {
	t.Helper()
	v := loadAttestorTLSVector(t)
	clock := &testClock{now: time.Unix(int64(v.TypedData.Message.IssuedAt), 0)}
	pins := v.pins()
	source := &testPinSource{clock: clock, pins: []string{pins[1], pins[0]},
		lease: time.Duration(v.TypedData.Message.ExpiresAt-v.TypedData.Message.IssuedAt) * time.Second}
	a := NewAttestorTLS(v.ProviderKey.key(t), v.TypedData.Domain.ChainID, v.TypedData.Domain.VerifyingContract, source.get, nil)
	a.now = clock.Now
	return a, source, clock, v
}

func getAttestorTLS(a *AttestorTLS) *httptest.ResponseRecorder {
	r := gin.New()
	r.GET("/api/attestor-tls", a.Handle)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/attestor-tls", nil))
	return w
}

func requireUnavailable(t *testing.T, w *httptest.ResponseRecorder, retryAfter string) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable || w.Body.String() != `{"error":"attestor tls unavailable"}` {
		t.Fatalf("want 503, got %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") != retryAfter || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", w.Header())
	}
}

func TestAttestorTLSEndpointServesSignedStatement(t *testing.T) {
	a, source, _, v := newVectorAttestorTLS(t)
	w := getAttestorTLS(a)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", w.Header())
	}
	if w.Body.String() != v.ResponseBody {
		t.Fatalf("body mismatch:\n got %s\nwant %s", w.Body.String(), v.ResponseBody)
	}
	if source.minLease != attestorTLSRefreshAhead {
		t.Fatalf("identity asked for %s of lease", source.minLease)
	}
}

func TestAttestorTLSEndpointCapsLeaseAt300s(t *testing.T) {
	a, source, clock, _ := newVectorAttestorTLS(t)
	source.set(source.pins, 400*time.Second, nil)
	w := getAttestorTLS(a)
	var s AttestorTLSStatement
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || s.IssuedAt != uint64(clock.Now().Unix()) || s.ExpiresAt != s.IssuedAt+attestorTLSMaxLease {
		t.Fatalf("status %d, statement %+v", w.Code, s)
	}
	if recoverStatementSigner(t, s) != crypto.PubkeyToAddress(a.key.PublicKey) {
		t.Fatal("statement not signed by the provider key")
	}
}

func recoverStatementSigner(t *testing.T, s AttestorTLSStatement) common.Address {
	t.Helper()
	digest, err := s.signingDigest()
	if err != nil {
		t.Fatal(err)
	}
	return recoverDigestSigner(t, digest, s.Signature)
}

func TestAttestorTLSEndpointCachesAndRefreshesAhead(t *testing.T) {
	a, source, clock, v := newVectorAttestorTLS(t)
	first := getAttestorTLS(a).Body.String()
	expiresAt := time.Unix(int64(v.TypedData.Message.ExpiresAt), 0)

	clock.Set(expiresAt.Add(-attestorTLSRefreshAhead - time.Second))
	if got := getAttestorTLS(a).Body.String(); got != first || source.count() != 1 {
		t.Fatalf("statement re-signed inside its lease (%d lookups)", source.count())
	}

	// From refresh-ahead on, the cached statement is served while a shared
	// background refresh signs the next one.
	clock.Set(expiresAt.Add(-attestorTLSRefreshAhead))
	if got := getAttestorTLS(a).Body.String(); got != first {
		t.Fatal("refresh-ahead request did not get the cached statement")
	}
	deadline := time.Now().Add(5 * time.Second)
	var next string
	for {
		next = getAttestorTLS(a).Body.String()
		if next != first {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh never replaced the statement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if source.count() != 2 {
		t.Fatalf("%d identity lookups, want 2", source.count())
	}
	var s AttestorTLSStatement
	if err := json.Unmarshal([]byte(next), &s); err != nil {
		t.Fatal(err)
	}
	if s.IssuedAt != uint64(clock.Now().Unix()) || recoverStatementSigner(t, s) != common.HexToAddress(v.RecoveredAddress) {
		t.Fatalf("refreshed statement %+v", s)
	}
}

func TestAttestorTLSEndpointRefreshFailure(t *testing.T) {
	a, source, clock, v := newVectorAttestorTLS(t)
	first := getAttestorTLS(a).Body.String()
	expiresAt := time.Unix(int64(v.TypedData.Message.ExpiresAt), 0)
	pins := v.pins()
	source.set(nil, 0, errors.New("discovery unavailable"))

	// Past the serving margin a failed refresh keeps the cached statement.
	for _, before := range []time.Duration{attestorTLSServeMargin, attestorTLSFailMargin + time.Second} {
		clock.Set(expiresAt.Add(-before))
		if w := getAttestorTLS(a); w.Code != http.StatusOK || w.Body.String() != first {
			t.Fatalf("%s before expiry: %d %s", before, w.Code, w.Body.String())
		}
	}
	clock.Set(expiresAt.Add(-attestorTLSFailMargin))
	requireUnavailable(t, getAttestorTLS(a), "5")

	// Recovery takes effect once the retry interval has passed.
	source.set(pins, 288*time.Second, nil)
	calls := source.count()
	requireUnavailable(t, getAttestorTLS(a), "5")
	if source.count() != calls {
		t.Fatal("refresh retried inside the retry interval")
	}
	clock.Set(expiresAt.Add(-attestorTLSFailMargin + attestorTLSRetryInterval))
	if w := getAttestorTLS(a); w.Code != http.StatusOK || w.Body.String() == first {
		t.Fatalf("no recovery: %d %s", w.Code, w.Body.String())
	}
}

func TestAttestorTLSEndpointWithoutStatement(t *testing.T) {
	a, source, clock, _ := newVectorAttestorTLS(t)
	source.set(nil, 0, errors.New("discovery unavailable"))
	requireUnavailable(t, getAttestorTLS(a), "5")
	requireUnavailable(t, getAttestorTLS(a), "5")
	if source.count() != 1 {
		t.Fatalf("%d lookups inside the retry interval", source.count())
	}
	clock.Set(clock.Now().Add(attestorTLSRetryInterval))
	requireUnavailable(t, getAttestorTLS(a), "5")
	if source.count() != 2 {
		t.Fatalf("%d lookups after the retry interval", source.count())
	}

	manyPins := make([]string, attestorTLSMaxPins+1)
	for i := range manyPins {
		manyPins[i] = strings.Repeat("0", 62) + hex.EncodeToString([]byte{byte(i)})
	}
	for _, tc := range []struct {
		name  string
		pins  []string
		lease time.Duration
	}{
		{"no_pins", nil, time.Minute},
		{"too_many_pins", manyPins, time.Minute},
		{"invalid_pin", []string{"not-hex"}, time.Minute},
		{"ended_lease", []string{strings.Repeat("ab", 32)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, source, _, _ := newVectorAttestorTLS(t)
			source.set(tc.pins, tc.lease, nil)
			requireUnavailable(t, getAttestorTLS(a), "5")
		})
	}
}

func TestAttestorTLSEndpointUnconfigured(t *testing.T) {
	key, _ := crypto.GenerateKey()
	registry := common.HexToAddress("0x79d11d8d71784dfed781124e219ad3c62b3a17f6")
	pins := func(context.Context, time.Duration) ([]string, time.Time, error) {
		t.Fatal("unconfigured endpoint resolved pins")
		return nil, time.Time{}, nil
	}
	for name, a := range map[string]*AttestorTLS{
		"no_attestor_or_kms": NewAttestorTLS(key, 16602, registry, nil, nil),
		"no_key":             NewAttestorTLS(nil, 16602, registry, pins, nil),
		"no_chain":           NewAttestorTLS(key, 0, registry, pins, nil),
		"no_registry":        NewAttestorTLS(key, 16602, common.Address{}, pins, nil),
	} {
		t.Run(name, func(t *testing.T) {
			requireUnavailable(t, getAttestorTLS(a), "")
		})
	}
}

// blockingPins holds every pin lookup until the test releases it.
type blockingPins struct {
	inner   *testPinSource
	entered chan struct{}
	release chan struct{}
}

func newBlockingPins(inner *testPinSource) *blockingPins {
	return &blockingPins{inner: inner, entered: make(chan struct{}, 16), release: make(chan struct{})}
}

func (b *blockingPins) get(ctx context.Context, minLease time.Duration) ([]string, time.Time, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, time.Time{}, ctx.Err()
	}
	return b.inner.get(ctx, minLease)
}

// waitIdle waits for the running refresh, if any, to end. It starts none.
func waitIdle(t *testing.T, a *AttestorTLS) {
	t.Helper()
	a.mu.Lock()
	done := a.refreshing
	a.mu.Unlock()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not end")
	}
}

func TestAttestorTLSEndpointLimiterConfig(t *testing.T) {
	a := NewAttestorTLS(nil, 0, common.Address{}, nil, nil)
	if a.limiter.Limit() != attestorTLSRate || a.limiter.Burst() != attestorTLSBurst {
		t.Fatalf("limiter %v/%d", a.limiter.Limit(), a.limiter.Burst())
	}
}

// Serving a cached statement takes no token, so callers that exhaust the
// limiter cannot keep the statement from anyone.
func TestAttestorTLSEndpointServesCachedStatementWithoutToken(t *testing.T) {
	a, source, clock, v := newVectorAttestorTLS(t)
	first := getAttestorTLS(a).Body.String()
	a.limiter = rate.NewLimiter(0, 0) // every token refused
	expiresAt := time.Unix(int64(v.TypedData.Message.ExpiresAt), 0)

	for _, before := range []time.Duration{attestorTLSRefreshAhead + time.Second, attestorTLSServeMargin + time.Second} {
		clock.Set(expiresAt.Add(-before))
		for i := 0; i < 2*attestorTLSBurst; i++ {
			w := getAttestorTLS(a)
			if w.Code != http.StatusOK {
				t.Fatalf("%s before expiry, request %d: %d %s", before, i, w.Code, w.Body.String())
			}
			if before > attestorTLSRefreshAhead && w.Body.String() != first {
				t.Fatal("cached statement re-signed inside its lease")
			}
		}
		waitIdle(t, a)
	}
	// The refresh-ahead requests started exactly one background refresh.
	if source.count() != 2 || getAttestorTLS(a).Body.String() == first {
		t.Fatalf("%d lookups; background refresh did not replace the statement", source.count())
	}
}

// Requests that would wait for a running refresh are limited. A refused
// request never waits: it gets the cached statement while it is valid, and
// otherwise 429. All requests share one refresh.
func TestAttestorTLSEndpointLimitsOnlyRefreshWaits(t *testing.T) {
	a, source, clock, v := newVectorAttestorTLS(t)
	blocking := newBlockingPins(source)
	a.pins = blocking.get
	a.limiter = rate.NewLimiter(0, 0)

	// No statement yet and a refresh running: a refused request answers 429.
	for i := 0; i < 3; i++ {
		w := getAttestorTLS(a)
		if w.Code != http.StatusTooManyRequests || w.Body.String() != `{"error":"too many requests"}` {
			t.Fatalf("request %d: %d %s", i, w.Code, w.Body.String())
		}
		if w.Header().Get("Retry-After") != "1" || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("429 headers %v", w.Header())
		}
	}
	<-blocking.entered
	blocking.release <- struct{}{}
	waitIdle(t, a)
	first := getAttestorTLS(a)
	if first.Code != http.StatusOK || source.count() != 1 {
		t.Fatalf("after the shared refresh: %d %s, %d lookups", first.Code, first.Body.String(), source.count())
	}

	// Past the serving margin with a refresh running, a refused request gets
	// the still-valid cached statement at once.
	expiresAt := time.Unix(int64(v.TypedData.Message.ExpiresAt), 0)
	clock.Set(expiresAt.Add(-attestorTLSServeMargin))
	if w := getAttestorTLS(a); w.Code != http.StatusOK || w.Body.String() != first.Body.String() {
		t.Fatalf("refused request inside the fail margin: %d %s", w.Code, w.Body.String())
	}
	<-blocking.entered
	blocking.release <- struct{}{}
	waitIdle(t, a)
	if source.count() != 2 {
		t.Fatalf("%d lookups, want 2", source.count())
	}
}

// Waiting requests share the running refresh instead of each queueing a call.
func TestAttestorTLSEndpointWaitersShareOneRefresh(t *testing.T) {
	a, source, _, v := newVectorAttestorTLS(t)
	blocking := newBlockingPins(source)
	a.pins = blocking.get

	const waiters = 8
	a.limiter = rate.NewLimiter(0, waiters) // one token per waiter, no refill
	results := make(chan *httptest.ResponseRecorder, waiters)
	for i := 0; i < waiters; i++ {
		go func() { results <- getAttestorTLS(a) }()
	}
	<-blocking.entered
	// Each waiter takes its token before it waits.
	deadline := time.Now().Add(5 * time.Second)
	for a.limiter.Tokens() > 0.5 {
		if time.Now().After(deadline) {
			t.Fatalf("waiters did not arrive: %.1f tokens left", a.limiter.Tokens())
		}
		time.Sleep(time.Millisecond)
	}
	close(blocking.release)
	for i := 0; i < waiters; i++ {
		if w := <-results; w.Code != http.StatusOK || w.Body.String() != v.ResponseBody {
			t.Fatalf("waiter %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if source.count() != 1 {
		t.Fatalf("%d lookups for one shared refresh", source.count())
	}
}
