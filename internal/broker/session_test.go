package broker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/auth"
	"github.com/0gfoundation/0g-sandbox/internal/indexer"
)

// ── Mocks ─────────────────────────────────────────────────────────────────────

type mockProviderLookup struct {
	mu      sync.Mutex
	records map[string]indexer.ProviderRecord
}

func (m *mockProviderLookup) Get(addr string) (indexer.ProviderRecord, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.records[strings.ToLower(addr)]
	return r, ok
}

type mockSessionChain struct {
	mu                  sync.Mutex
	cpuPerSec           *big.Int
	memPerSec           *big.Int
	createFee           *big.Int
	pricingErr          error
	balance             *big.Int
	balanceAfterDeposit *big.Int // if set, returned on 2nd+ GetProviderBalance calls
	balCallCount        int
	balErr              error
	expectedTEEAddr     common.Address // signer must match for IsActiveNode → true
	nodeErr             error
}

func (m *mockSessionChain) GetServicePricing(_ context.Context, _ common.Address) (*big.Int, *big.Int, *big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cpuPerSec, m.memPerSec, m.createFee, m.pricingErr
}

func (m *mockSessionChain) GetProviderBalance(_ context.Context, _, _ common.Address) (*big.Int, *big.Int, *big.Int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balCallCount++
	bal := m.balance
	if m.balanceAfterDeposit != nil && m.balCallCount > 1 {
		bal = m.balanceAfterDeposit
	}
	return bal, big.NewInt(0), big.NewInt(0), m.balErr
}

func (m *mockSessionChain) IsActiveNode(_ context.Context, _ string, signer common.Address) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.nodeErr != nil {
		return false, m.nodeErr
	}
	if m.expectedTEEAddr == (common.Address{}) {
		// Unconfigured — treat as "any signer accepted" (most tests don't care).
		return true, nil
	}
	return signer == m.expectedTEEAddr, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// teeSign mirrors the signing logic in internal/proxy/broker.go.
// It pre-applies auth.HashMessage because auth.Recover applies it again internally.
func teeSign(key *ecdsa.PrivateKey, msgHash []byte) string {
	prefixed := auth.HashMessage(msgHash)
	sig, err := crypto.Sign(prefixed, key)
	if err != nil {
		panic(err)
	}
	sig[64] += 27 // normalize V to 27/28 (Ethereum convention)
	return "0x" + hex.EncodeToString(sig)
}

// newSessionSetup creates a SessionHandler wired to a provider whose TEE signer
// is teeKey. Returns the handler, provider lookup mock, and a fresh Redis client.
func newSessionSetup(t *testing.T, teeKey *ecdsa.PrivateKey, chain sessionChainClient, payment PaymentLayer) (*SessionHandler, *mockProviderLookup) {
	t.Helper()
	// v2: the provider IS its TEE signer, and the handlers now enforce
	// signer == provider — so the record under test is keyed by the teeKey's
	// own address. provider1 stays registered for lookup-shape tests.
	teeAddr := crypto.PubkeyToAddress(teeKey.PublicKey)
	providers := &mockProviderLookup{
		records: map[string]indexer.ProviderRecord{
			strings.ToLower(teeAddr.Hex()): {
				Address: teeAddr.Hex(),
				AppId:   "test-app",
			},
			strings.ToLower(provider1.Hex()): {
				Address: provider1.Hex(),
				AppId:   "test-app",
			},
		},
	}
	rdb := newTestRedis(t)
	h := NewSessionHandler(providers, chain, payment, rdb, zap.NewNop(), 3, 90)
	return h, providers
}

// newSessionRouter mounts the session endpoints onto a test gin router.
func newSessionRouter(h *SessionHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/session", h.HandlePost)
	r.DELETE("/api/session/:id", h.HandleDelete)
	return r
}

// defaultChain returns a mock chain with cpu=10/sec, mem=5/sec, given balance.
// expectedTEEAddr left zero so IsActiveNode accepts any signer (the
// signature-validity tests configure it explicitly when needed).
func defaultChain(balance int64) *mockSessionChain {
	return &mockSessionChain{
		cpuPerSec: big.NewInt(10),
		memPerSec: big.NewInt(5),
		createFee: big.NewInt(0),
		balance:   big.NewInt(balance),
	}
}

// buildPostReq constructs and signs a postSessionReq.
func buildPostReq(t *testing.T, key *ecdsa.PrivateKey, sandboxID string, cpu, memGB int64) postSessionReq {
	t.Helper()
	req := postSessionReq{
		SandboxID:          sandboxID,
		ProviderAddr:       crypto.PubkeyToAddress(key.PublicKey).Hex(), // v2: provider == signer
		UserAddr:           userA.Hex(),
		CPU:                cpu,
		MemGB:              memGB,
		StartTime:          time.Now().Unix(),
		VoucherIntervalSec: 60,
	}
	req.Signature = teeSign(key, sessionMsgHash(req))
	return req
}

func doPost(t *testing.T, router *gin.Engine, req postSessionReq) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/api/session", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func doDelete(t *testing.T, router *gin.Engine, sandboxID string, key *ecdsa.PrivateKey) *httptest.ResponseRecorder {
	t.Helper()
	ts := time.Now().Unix()
	req := struct {
		Timestamp int64  `json:"timestamp"`
		Signature string `json:"signature"`
	}{
		Timestamp: ts,
		Signature: teeSign(key, deregisterMsgHash(sandboxID, ts)),
	}
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodDelete, "/api/session/"+sandboxID, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

// ── HandlePost tests ──────────────────────────────────────────────────────────

func TestHandlePost_providerNotFound(t *testing.T) {
	key, _ := crypto.GenerateKey()
	h, _ := newSessionSetup(t, key, defaultChain(0), &mockPaymentLayer{})
	router := newSessionRouter(h)

	req := buildPostReq(t, key, "sb-1", 2, 4)
	req.ProviderAddr = "0x9999999999999999999999999999999999999999" // not in indexer
	req.Signature = teeSign(key, sessionMsgHash(req))

	w := doPost(t, router, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestHandlePost_invalidSignature(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	wrongKey, _ := crypto.GenerateKey()
	chain := defaultChain(0)
	chain.expectedTEEAddr = crypto.PubkeyToAddress(teeKey.PublicKey) // only teeKey recovers to a valid node
	h, _ := newSessionSetup(t, teeKey, chain, &mockPaymentLayer{})
	router := newSessionRouter(h)

	// Request names the REGISTERED provider (teeKey's address) but is signed
	// by a different key: recovered signer != provider → 401.
	req := buildPostReq(t, teeKey, "sb-1", 2, 4)
	req.Signature = teeSign(wrongKey, sessionMsgHash(req))
	w := doPost(t, router, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandlePost_pricingError(t *testing.T) {
	key, _ := crypto.GenerateKey()
	chain := &mockSessionChain{pricingErr: errors.New("rpc down")}
	h, _ := newSessionSetup(t, key, chain, &mockPaymentLayer{})
	router := newSessionRouter(h)

	w := doPost(t, router, buildPostReq(t, key, "sb-1", 2, 4))
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestHandlePost_balanceError(t *testing.T) {
	key, _ := crypto.GenerateKey()
	chain := &mockSessionChain{
		cpuPerSec: big.NewInt(10),
		memPerSec: big.NewInt(5),
		createFee: big.NewInt(0),
		balErr:    errors.New("rpc down"),
	}
	h, _ := newSessionSetup(t, key, chain, &mockPaymentLayer{})
	router := newSessionRouter(h)

	w := doPost(t, router, buildPostReq(t, key, "sb-1", 2, 4))
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestHandlePost_noDeficit_noPayment(t *testing.T) {
	key, _ := crypto.GenerateKey()
	// cpu=2, mem=4, interval=60, topupIntervals=3 → needed = (2×10+4×5)×60×3 = 10800
	// balance=50000 → no deficit
	mp := &mockPaymentLayer{}
	h, _ := newSessionSetup(t, key, defaultChain(50_000), mp)
	router := newSessionRouter(h)

	w := doPost(t, router, buildPostReq(t, key, "sb-1", 2, 4))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if mp.depositCount() != 0 {
		t.Errorf("RequestDeposit called %d times, want 0 (no deficit)", mp.depositCount())
	}
}

func TestHandlePost_deficit_triggersPayment(t *testing.T) {
	key, _ := crypto.GenerateKey()
	// cpu=2, mem=4, interval=60, topupIntervals=3
	// pricePerSec = 2×10 + 4×5 = 40
	// needed = 40×60×3 = 7200
	// balance = 100 → deficit = 7200-100 = 7100
	mp := &mockPaymentLayer{}
	chain := defaultChain(100)
	chain.balanceAfterDeposit = big.NewInt(10_000) // simulate deposit landing on-chain
	h, _ := newSessionSetup(t, key, chain, mp)
	router := newSessionRouter(h)

	w := doPost(t, router, buildPostReq(t, key, "sb-1", 2, 4))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if mp.depositCount() != 1 {
		t.Fatalf("RequestDeposit called %d times, want 1", mp.depositCount())
	}
	want := big.NewInt(7100)
	if mp.calls[0].amount.Cmp(want) != 0 {
		t.Errorf("deposit amount = %s, want %s", mp.calls[0].amount, want)
	}
	if mp.calls[0].user != userA {
		t.Errorf("user = %s, want %s", mp.calls[0].user.Hex(), userA.Hex())
	}
}

func TestHandlePost_paymentError(t *testing.T) {
	key, _ := crypto.GenerateKey()
	mp := &mockPaymentLayer{failFor: map[string]bool{userA.Hex(): true}}
	h, _ := newSessionSetup(t, key, defaultChain(0), mp)
	router := newSessionRouter(h)

	w := doPost(t, router, buildPostReq(t, key, "sb-1", 2, 4))
	if w.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", w.Code)
	}
}

func TestHandlePost_fundingOnly_noSessionWritten(t *testing.T) {
	key, _ := crypto.GenerateKey()
	mp := &mockPaymentLayer{}
	chain := defaultChain(0)
	chain.balanceAfterDeposit = big.NewInt(10_000) // simulate deposit landing on-chain
	h, _ := newSessionSetup(t, key, chain, mp)
	router := newSessionRouter(h)

	// sandbox_id="" → funding-only, no session entry should be written
	req := buildPostReq(t, key, "", 2, 4)
	w := doPost(t, router, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Verify no session key exists in Redis
	sessions := h.rdb.Keys(context.Background(), sessionPrefix+"*").Val()
	if len(sessions) != 0 {
		t.Errorf("sessions in Redis = %d, want 0 (funding-only call)", len(sessions))
	}
}

func TestHandlePost_withSandboxID_sessionWritten(t *testing.T) {
	key, _ := crypto.GenerateKey()
	mp := &mockPaymentLayer{}
	h, _ := newSessionSetup(t, key, defaultChain(50_000), mp)
	router := newSessionRouter(h)

	w := doPost(t, router, buildPostReq(t, key, "sb-42", 2, 4))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Session entry must exist in Redis
	data, err := h.rdb.Get(context.Background(), sessionPrefix+"sb-42").Bytes()
	if err != nil {
		t.Fatalf("session not found in Redis: %v", err)
	}
	var entry SessionEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	if entry.SandboxID != "sb-42" {
		t.Errorf("SandboxID = %q, want %q", entry.SandboxID, "sb-42")
	}
	if entry.CPU != 2 || entry.MemGB != 4 {
		t.Errorf("resources = cpu:%d mem:%d, want cpu:2 mem:4", entry.CPU, entry.MemGB)
	}
	// pricePerSec = 2×10 + 4×5 = 40
	if entry.PricePerSec != "40" {
		t.Errorf("PricePerSec = %q, want %q", entry.PricePerSec, "40")
	}
}

func TestHandlePost_antiReplay(t *testing.T) {
	key, _ := crypto.GenerateKey()
	h, _ := newSessionSetup(t, key, defaultChain(50_000), &mockPaymentLayer{})
	router := newSessionRouter(h)

	req := buildPostReq(t, key, "sb-dup", 1, 1)

	// First call: should succeed
	w1 := doPost(t, router, req)
	if w1.Code != http.StatusOK {
		t.Fatalf("first call status = %d, want 200", w1.Code)
	}

	// Second call with same sandbox_id and start_time: must be rejected
	w2 := doPost(t, router, req)
	if w2.Code != http.StatusConflict {
		t.Errorf("replay status = %d, want 409", w2.Code)
	}
}

// ── HandleDelete tests ────────────────────────────────────────────────────────

func TestHandleDelete_sessionNotFound(t *testing.T) {
	key, _ := crypto.GenerateKey()
	h, _ := newSessionSetup(t, key, defaultChain(0), &mockPaymentLayer{})
	router := newSessionRouter(h)

	w := doDelete(t, router, "no-such-sandbox", key)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleDelete_providerNotFound(t *testing.T) {
	key, _ := crypto.GenerateKey()
	h, providers := newSessionSetup(t, key, defaultChain(0), &mockPaymentLayer{})
	router := newSessionRouter(h)

	// Seed a session, then remove the provider from the indexer
	seedSession(t, h.rdb, SessionEntry{
		SandboxID: "sb-del", User: userA.Hex(), Provider: crypto.PubkeyToAddress(key.PublicKey).Hex(),
		PricePerSec: "40", VoucherIntervalSec: 60,
	})
	providers.mu.Lock()
	delete(providers.records, strings.ToLower(crypto.PubkeyToAddress(key.PublicKey).Hex()))
	providers.mu.Unlock()

	w := doDelete(t, router, "sb-del", key)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestHandleDelete_invalidSignature(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	wrongKey, _ := crypto.GenerateKey()
	chain := defaultChain(0)
	chain.expectedTEEAddr = crypto.PubkeyToAddress(teeKey.PublicKey)
	h, _ := newSessionSetup(t, teeKey, chain, &mockPaymentLayer{})
	router := newSessionRouter(h)

	seedSession(t, h.rdb, SessionEntry{
		SandboxID: "sb-del", User: userA.Hex(), Provider: crypto.PubkeyToAddress(teeKey.PublicKey).Hex(),
		PricePerSec: "40", VoucherIntervalSec: 60,
	})

	w := doDelete(t, router, "sb-del", wrongKey) // signed with wrong key
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestHandleDelete_timestampExpired(t *testing.T) {
	key, _ := crypto.GenerateKey()
	h, _ := newSessionSetup(t, key, defaultChain(0), &mockPaymentLayer{})

	seedSession(t, h.rdb, SessionEntry{
		SandboxID: "sb-del", User: userA.Hex(), Provider: crypto.PubkeyToAddress(key.PublicKey).Hex(),
		PricePerSec: "40", VoucherIntervalSec: 60,
	})

	// Build a delete request with an expired timestamp (> 300s ago)
	ts := time.Now().Unix() - 400
	req := struct {
		Timestamp int64  `json:"timestamp"`
		Signature string `json:"signature"`
	}{
		Timestamp: ts,
		Signature: teeSign(key, deregisterMsgHash("sb-del", ts)),
	}
	body, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodDelete, "/api/session/sb-del", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newSessionRouter(h).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (expired timestamp)", w.Code)
	}
}

func TestHandleDelete_success(t *testing.T) {
	key, _ := crypto.GenerateKey()
	h, _ := newSessionSetup(t, key, defaultChain(0), &mockPaymentLayer{})
	router := newSessionRouter(h)

	seedSession(t, h.rdb, SessionEntry{
		SandboxID: "sb-del", User: userA.Hex(), Provider: crypto.PubkeyToAddress(key.PublicKey).Hex(),
		PricePerSec: "40", VoucherIntervalSec: 60,
	})

	w := doDelete(t, router, "sb-del", key)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Session must be gone from Redis
	exists, _ := h.rdb.Exists(context.Background(), sessionPrefix+"sb-del").Result()
	if exists != 0 {
		t.Errorf("session still in Redis after delete")
	}
}

// Finding #16: a SIBLING node of the same app passes IsActiveNode but must not
// act on another provider's (user, provider) bucket — deposits or session
// deletes for a provider you are not are refused.
func TestHandlePost_SiblingNodeCannotActForOtherProvider(t *testing.T) {
	providerKey, _ := crypto.GenerateKey()
	siblingKey, _ := crypto.GenerateKey()
	chain := defaultChain(0) // expectedTEEAddr zero → IsActiveNode accepts ANY signer
	h, _ := newSessionSetup(t, providerKey, chain, &mockPaymentLayer{})
	router := newSessionRouter(h)

	// Sibling signs a funding request naming the OTHER provider.
	req := buildPostReq(t, providerKey, "", 2, 4) // provider = providerKey's addr
	req.Signature = teeSign(siblingKey, sessionMsgHash(req))
	w := doPost(t, router, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("sibling-node funding for another provider: status = %d, want 401", w.Code)
	}
}

func TestHandleDelete_SiblingNodeCannotDeleteOtherProvidersSession(t *testing.T) {
	providerKey, _ := crypto.GenerateKey()
	siblingKey, _ := crypto.GenerateKey()
	h, _ := newSessionSetup(t, providerKey, defaultChain(0), &mockPaymentLayer{})
	router := newSessionRouter(h)

	seedSession(t, h.rdb, SessionEntry{
		SandboxID: "sb-x", User: userA.Hex(), Provider: crypto.PubkeyToAddress(providerKey.PublicKey).Hex(),
		PricePerSec: "40", VoucherIntervalSec: 60,
	})
	w := doDelete(t, router, "sb-x", siblingKey)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("sibling-node delete of another provider's session: status = %d, want 401", w.Code)
	}
}
