package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

func init() { gin.SetMode(gin.TestMode) }

// ── Mock billing hooks ────────────────────────────────────────────────────────

type mockBilling struct {
	mu       sync.Mutex
	creates  []string
	starts   []string
	stops    []string
	deletes  []string
	archives []string
}

func (m *mockBilling) OnCreate(_ context.Context, sandboxID, _ string, _, _ int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates = append(m.creates, sandboxID)
}
func (m *mockBilling) OnStart(_ context.Context, sandboxID, _ string, _, _ int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.starts = append(m.starts, sandboxID)
}
func (m *mockBilling) OnStop(_ context.Context, sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stops = append(m.stops, sandboxID)
}
func (m *mockBilling) OnDelete(_ context.Context, sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletes = append(m.deletes, sandboxID)
}
func (m *mockBilling) OnArchive(_ context.Context, sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.archives = append(m.archives, sandboxID)
}
func (m *mockBilling) EnsureSession(_ context.Context, _, _ string) {}

// ── Mock Daytona server helpers ───────────────────────────────────────────────

// mockDaytona returns an httptest.Server that simulates the Daytona API.
// sandboxes is the initial set of sandboxes the server knows about.
// capturedBodies records request bodies received at each path (for assertion).
func mockDaytona(t *testing.T, sandboxes []daytona.Sandbox) (*httptest.Server, *[][]byte) {
	t.Helper()
	captured := &[][]byte{}
	var mu sync.Mutex

	mux := http.NewServeMux()

	// GET /api/sandbox — list all
	mux.HandleFunc("GET /api/sandbox", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sandboxes)
	})

	// GET /api/sandbox/{id} — get one
	mux.HandleFunc("GET /api/sandbox/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/api/sandbox/"):]
		for _, s := range sandboxes {
			if s.ID == id {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(s)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	// POST /api/sandbox — create; return {"id":"sb-new"}
	mux.HandleFunc("POST /api/sandbox", func(w http.ResponseWriter, r *http.Request) {
		buf := &bytes.Buffer{}
		buf.ReadFrom(r.Body)
		mu.Lock()
		*captured = append(*captured, buf.Bytes())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"sb-new"}`)
	})

	// POST/DELETE /api/sandbox/{id}/* — lifecycle ops
	mux.HandleFunc("/api/sandbox/", func(w http.ResponseWriter, r *http.Request) {
		buf := &bytes.Buffer{}
		buf.ReadFrom(r.Body)
		mu.Lock()
		*captured = append(*captured, buf.Bytes())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, captured
}

// newTestEngine builds a Gin engine with the proxy handler mounted,
// with a middleware that pre-sets wallet_address in the context.
func newTestEngine(dtona *daytona.Client, bh BillingHooks, wallet string) *gin.Engine {
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		c.Set("wallet_address", wallet)
		c.Next()
	})
	NewHandler(dtona, bh, nil, nil, nil, nil, nil, nil, nil, "", nil, "", nil, zap.NewNop(), "", nil, 0).Register(api)
	return r
}

// ── Blocked endpoints ─────────────────────────────────────────────────────────

func TestBlockedEndpoints(t *testing.T) {
	srv, _ := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := newTestEngine(dtona, &mockBilling{}, "0xWALLET")

	for _, path := range []string{
		"/api/sandbox/sb-1/autostop",
		"/api/sandbox/sb-1/autoarchive",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
			req := httptest.NewRequest(method, path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s: expected 403, got %d", method, path, w.Code)
			}
		}
	}
}

// ── Create: owner injection ───────────────────────────────────────────────────

func TestHandleCreate_InjectsOwnerLabel(t *testing.T) {
	srv, captured := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	mb := &mockBilling{}
	r := newTestEngine(dtona, mb, "0xMYWALLET")

	body := []byte(`{"name":"test-sandbox"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sandbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// The body forwarded to Daytona must contain the owner label
	if len(*captured) == 0 {
		t.Fatal("no request body captured by mock Daytona")
	}
	var fwdBody map[string]any
	json.Unmarshal((*captured)[0], &fwdBody)
	labels, _ := fwdBody["labels"].(map[string]any)
	if labels == nil || labels[ownerLabel] != "0xMYWALLET" {
		t.Errorf("daytona-owner not injected: labels=%v", labels)
	}
}

func TestHandleCreate_ForcesAutostopZero(t *testing.T) {
	srv, captured := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := newTestEngine(dtona, &mockBilling{}, "0xWALLET")

	// Client tries to set autostop
	body := []byte(`{"autostopInterval":3600}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sandbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var fwdBody map[string]any
	json.Unmarshal((*captured)[0], &fwdBody)
	if fwdBody["autoStopInterval"] != float64(0) {
		t.Errorf("autoStopInterval should be forced to 0, got %v", fwdBody["autoStopInterval"])
	}
	if fwdBody["autoArchiveInterval"] != float64(60) {
		t.Errorf("autoArchiveInterval should be 60, got %v", fwdBody["autoArchiveInterval"])
	}
}

func TestHandleCreate_AdminKeyForwarded(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"sb-x"}`)
	}))
	t.Cleanup(srv.Close)

	dtona := daytona.NewClient(srv.URL, "super-secret-admin-key")
	r := newTestEngine(dtona, &mockBilling{}, "0xWALLET")

	req := httptest.NewRequest(http.MethodPost, "/api/sandbox", bytes.NewReader([]byte(`{}`)))
	r.ServeHTTP(httptest.NewRecorder(), req)

	if receivedAuth != "Bearer super-secret-admin-key" {
		t.Errorf("Authorization header: got %q want %q", receivedAuth, "Bearer super-secret-admin-key")
	}
}

// ── List: owner filtering ─────────────────────────────────────────────────────

func TestHandleList_FiltersByOwner(t *testing.T) {
	allSandboxes := []daytona.Sandbox{
		{ID: "sb-mine-1", Labels: map[string]string{ownerLabel: "0xMYWALLET"}},
		{ID: "sb-mine-2", Labels: map[string]string{ownerLabel: "0xmywallet"}}, // case-insensitive
		{ID: "sb-others", Labels: map[string]string{ownerLabel: "0xOTHER"}},
		{ID: "sb-nolabel", Labels: map[string]string{}},
	}
	srv, _ := mockDaytona(t, allSandboxes)
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xMYWALLET")

	req := httptest.NewRequest(http.MethodGet, "/api/sandbox", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result []daytona.Sandbox
	json.Unmarshal(w.Body.Bytes(), &result)

	if len(result) != 2 {
		t.Fatalf("expected 2 sandboxes, got %d: %+v", len(result), result)
	}
	for _, s := range result {
		if s.ID == "sb-others" || s.ID == "sb-nolabel" {
			t.Errorf("sandbox %q should not appear in filtered list", s.ID)
		}
	}
}

// ── Owner check: 403 on mismatch ──────────────────────────────────────────────

func TestHandleStop_OwnerCheck_Pass(t *testing.T) {
	sb := daytona.Sandbox{
		ID:     "sb-mine",
		Labels: map[string]string{ownerLabel: "0xOWNER"},
	}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sb})
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xOWNER")

	req := httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-mine/stop", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleStop_OwnerCheck_Fail(t *testing.T) {
	sb := daytona.Sandbox{
		ID:     "sb-others",
		Labels: map[string]string{ownerLabel: "0xRIGHTFULOWNER"},
	}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sb})
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xATTACKER")

	req := httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-others/stop", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleDelete_OwnerCheck_Fail(t *testing.T) {
	sb := daytona.Sandbox{
		ID:     "sb-victim",
		Labels: map[string]string{ownerLabel: "0xVICTIM"},
	}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sb})
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xATTACKER")

	req := httptest.NewRequest(http.MethodDelete, "/api/sandbox/sb-victim", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// ── Labels: strip daytona-owner ───────────────────────────────────────────────

func TestHandleLabels_StripsOwnerLabel(t *testing.T) {
	sb := daytona.Sandbox{
		ID:     "sb-mine",
		Labels: map[string]string{ownerLabel: "0xOWNER"},
	}
	srv, captured := mockDaytona(t, []daytona.Sandbox{sb})
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xOWNER")

	// Attacker tries to hijack the sandbox via label update
	payload := []byte(`{"daytona-owner":"0xATTACKER","env":"staging"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/sandbox/sb-mine/labels", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// The body forwarded to Daytona must NOT contain daytona-owner
	if len(*captured) == 0 {
		t.Fatal("no body captured")
	}
	// captured[0] = GET sandbox (owner check), captured[1] = PUT labels
	var fwdBody map[string]any
	for _, b := range *captured {
		if err := json.Unmarshal(b, &fwdBody); err == nil {
			if _, has := fwdBody[ownerLabel]; has {
				t.Errorf("daytona-owner must not be forwarded to Daytona: %v", fwdBody)
			}
		}
	}
}

// ── Sealed container ──────────────────────────────────────────────────────────

// mockDaytonaWithSSH extends mockDaytona to also handle the ssh-access endpoint.
func mockDaytonaWithSSH(t *testing.T, sandboxes []daytona.Sandbox) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/sandbox/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/api/sandbox/"):]
		for _, s := range sandboxes {
			if s.ID == id {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(s)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	mux.HandleFunc("/api/sandbox/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestSealedOnly_RejectsUnsealedCreate exercises the SEALED_ONLY config gate.
// When the provider runs with sealed_only=true, every create that doesn't carry
// `"sealed": true` must fail at 400 before the body ever reaches Daytona.
func TestSealedOnly_RejectsUnsealedCreate(t *testing.T) {
	srv, captured := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")

	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		c.Set("wallet_address", "0xWALLET")
		c.Next()
	})
	h := NewHandler(dtona, &mockBilling{}, nil, nil, nil, nil, nil, nil, nil, "", nil, "", nil, zap.NewNop(), "", nil, 0)
	h.SealedOnly = true
	h.Register(api)

	body := []byte(`{"image":"alpine:3.20"}`) // no sealed flag → must be rejected
	req := httptest.NewRequest(http.MethodPost, "/api/sandbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("sealed sandboxes")) {
		t.Errorf("expected error mentioning sealed sandboxes, got: %s", w.Body.String())
	}
	if n := len(*captured); n != 0 {
		t.Errorf("expected request to be rejected before reaching Daytona, but Daytona received %d bodies", n)
	}
}

// TestSealedOnly_AcceptsSealedCreate confirms the gate doesn't accidentally
// block sealed creates when sealed_only=true. Sealed: true must pass the
// gate (and then hit the existing "TEE key not configured" path because the
// test handler has teeKey=nil).
func TestSealedOnly_AcceptsSealedCreate(t *testing.T) {
	srv, _ := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")

	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		c.Set("wallet_address", "0xWALLET")
		c.Next()
	})
	h := NewHandler(dtona, &mockBilling{}, nil, nil, nil, nil, nil, nil, nil, "", nil, "", nil, zap.NewNop(), "", nil, 0)
	h.SealedOnly = true
	h.Register(api)

	body := []byte(`{"image":"alpine:3.20","sealed":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sandbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// teeKey is nil in the test setup, so we expect 501 (not 400 from the
	// SealedOnly gate). That confirms the gate let the request through.
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 (TEE key not configured) after passing sealed gate, got %d: %s",
			w.Code, w.Body.String())
	}
}

func TestSealedSandbox_StopAllowed(t *testing.T) {
	sealedSB := daytona.Sandbox{
		ID:     "sb-sealed",
		Labels: map[string]string{ownerLabel: "0xOWNER", sealedLabel: "true"},
	}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sealedSB})
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xOWNER")

	req := httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-sealed/stop", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("sealed sandbox stop: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUnsealedSandbox_SSHAllowed(t *testing.T) {
	normalSB := daytona.Sandbox{
		ID:     "sb-normal",
		Labels: map[string]string{ownerLabel: "0xOWNER"},
	}
	srv := mockDaytonaWithSSH(t, []daytona.Sandbox{normalSB})
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xOWNER")

	req := httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-normal/ssh-access", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Mock returns 200 for all /api/sandbox/* requests, so not 403 means sealed check passed
	if w.Code == http.StatusForbidden {
		t.Errorf("normal sandbox SSH should not be blocked: got 403")
	}
}

// ── extractID ─────────────────────────────────────────────────────────────────

func TestExtractID(t *testing.T) {
	cases := []struct {
		body []byte
		want string
	}{
		{[]byte(`{"id":"sb-abc"}`), "sb-abc"},
		{[]byte(`{"id":""}`), ""},
		{[]byte(`{}`), ""},
		{[]byte(`not json`), ""},
		{nil, ""},
	}
	for _, tc := range cases {
		got := extractID(tc.body)
		if got != tc.want {
			t.Errorf("extractID(%q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// ── GET /api/balance ─────────────────────────────────────────────────────────

type mockBalChecker struct{ bal *big.Int }

func (m *mockBalChecker) GetBalance(context.Context, common.Address, common.Address) (*big.Int, error) {
	return m.bal, nil
}

// The balance endpoint must report exactly what the create/start gates compute:
// on-chain balance, reservations, held debt, and the spendable remainder.
func TestBalanceEndpoint_SubtractsDebtAndReserved(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	wallet := "0x00000000000000000000000000000000000a11ce"
	provider := "0x0000000000000000000000000000000000000bbb"

	// Park 300 wei of debt in the held list the gates read.
	heldKey := fmt.Sprintf(voucher.VoucherHeldKeyFmt,
		strings.ToLower(common.HexToAddress(wallet).Hex()),
		strings.ToLower(common.HexToAddress(provider).Hex()))
	hv := voucher.SandboxVoucher{
		SandboxID: "sb-1",
		User:      common.HexToAddress(wallet),
		Provider:  common.HexToAddress(provider),
		TotalFee:  big.NewInt(300),
	}
	raw, _ := json.Marshal(hv)
	rdb.RPush(context.Background(), heldKey, string(raw))

	// And 200 wei still queued for settlement (accrued, will be charged).
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, common.HexToAddress(provider).Hex())
	qv := hv
	qv.TotalFee = big.NewInt(200)
	rawQ, _ := json.Marshal(qv)
	rdb.RPush(context.Background(), queueKey, string(rawQ))

	srv, _ := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		c.Set("wallet_address", wallet)
		c.Next()
	})
	NewHandler(dtona, &mockBilling{}, &mockBalChecker{bal: big.NewInt(1000)}, nil, nil,
		nil, nil, nil, nil, provider, nil, "", rdb, zap.NewNop(), "", nil, 0).Register(api)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/balance", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]string{
		"balance":            "1000",
		"reserved":           "0",
		"outstanding_debt":   "300",
		"pending_settlement": "200",
		"available":          "500",
	}
	for k, v := range want {
		if resp[k] != v {
			t.Errorf("%s: got %q want %q (body %s)", k, resp[k], v, w.Body.String())
		}
	}
}
