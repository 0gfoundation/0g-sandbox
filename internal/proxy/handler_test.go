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
	"sync/atomic"
	"testing"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

func init() { gin.SetMode(gin.TestMode) }

// ── Mock billing hooks ────────────────────────────────────────────────────────

type mockBilling struct {
	mu        sync.Mutex
	creates   []string
	starts    []string
	stops     []string
	deletes   []string
	archives  []string
	createCPU int // cpu of the last OnCreate — pins the billed spec (#118 nit)
	createMem int
}

func (m *mockBilling) OnCreate(_ context.Context, sandboxID, _ string, cpu, memGB int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creates = append(m.creates, sandboxID)
	m.createCPU, m.createMem = cpu, memGB
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

	// GET /api/snapshots/{name} — return a default spec so create-path spec
	// lookups (snapshot-only policy, #118) resolve. Tests needing a specific
	// spec use their own mux.
	mux.HandleFunc("GET /api/snapshots/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[len("/api/snapshots/"):]
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": name, "name": name, "cpu": 1, "mem": 1, "state": "active"}) //nolint:errcheck
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

// ── GET /api/volumes: admin-only (deny-by-default) ─────────────────────────────

func TestVolumesList_NonAdmin_Forbidden(t *testing.T) {
	srv, _ := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	// newTestEngine passes nil adminAddresses + no app-owner fn, so no wallet is admin.
	r := newTestEngine(dtona, &mockBilling{}, "0xNOTADMIN")

	req := httptest.NewRequest(http.MethodGet, "/api/volumes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("non-admin GET /api/volumes: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

// ── Create: owner injection ───────────────────────────────────────────────────

func TestHandleCreate_InjectsOwnerLabel(t *testing.T) {
	srv, captured := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	mb := &mockBilling{}
	r := newTestEngine(dtona, mb, "0xMYWALLET")

	body := []byte(`{"name":"test-sandbox","snapshot":"snap-x"}`)
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
	body := []byte(`{"autostopInterval":3600,"snapshot":"snap-x"}`)
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

	req := httptest.NewRequest(http.MethodPost, "/api/sandbox", bytes.NewReader([]byte(`{"snapshot":"snap-x"}`)))
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

// End-to-end merge semantics for PUT /labels: Daytona's replaceLabels is a
// wholesale replace, so the forwarded body must DELIBERATELY carry the
// protected labels with their LIVE values (re-injected from the sandbox), with
// caller-supplied values for them discarded — not merely have them stripped,
// which would make the replace delete them (unseal + brick).
func TestHandleLabels_MergesLiveProtectedLabels(t *testing.T) {
	sb := daytona.Sandbox{
		ID:     "sb-mine",
		Labels: map[string]string{ownerLabel: "0xOWNER", sealedLabel: "true"},
	}
	srv, captured := mockDaytona(t, []daytona.Sandbox{sb})
	dtona := daytona.NewClient(srv.URL, "key")
	r := newTestEngine(dtona, &mockBilling{}, "0xOWNER")

	// The real #90 exfil shape: nested labels, owner echoed, 0g-sealed omitted
	// (replace would unseal) — plus a rewrite attempt for good measure.
	payload := []byte(`{"labels":{"daytona-owner":"0xATTACKER","env":"staging"}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/sandbox/sb-mine/labels", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Find the forwarded PUT body (the one carrying a labels object).
	var labels map[string]any
	for _, b := range *captured {
		var m map[string]any
		if json.Unmarshal(b, &m) == nil {
			if l, ok := m["labels"].(map[string]any); ok {
				labels = l
			}
		}
	}
	if labels == nil {
		t.Fatal("forwarded labels body not captured")
	}
	if labels[ownerLabel] != "0xOWNER" {
		t.Errorf("owner must be forwarded with the LIVE value, got %v", labels[ownerLabel])
	}
	if labels[sealedLabel] != "true" {
		t.Errorf("0g-sealed must be re-injected (omission would unseal via replace), got %v", labels[sealedLabel])
	}
	if labels["env"] != "staging" {
		t.Errorf("caller's label must survive, got %v", labels["env"])
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

	body := []byte(`{"snapshot":"snap-x"}`) // no sealed flag → must be rejected
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

	body := []byte(`{"snapshot":"snap-x","sealed":true}`)
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

// Review F2 (#82): the other half of the admin gate — an admin's GET
// /api/volumes must be forwarded, guarding against the gate ever being
// "fixed" into 403-for-everyone.
func TestVolumesList_Admin_Forwarded(t *testing.T) {
	srv, _ := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		c.Set("wallet_address", "0xADmin")
		c.Next()
	})
	NewHandler(dtona, &mockBilling{}, nil, nil, nil, nil, nil, nil, nil, "",
		[]string{"0xadmin"}, "", nil, zap.NewNop(), "", nil, 0).Register(api)

	req := httptest.NewRequest(http.MethodGet, "/api/volumes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Errorf("admin GET /api/volumes must be forwarded, got 403: %s", w.Body.String())
	}
}

// Review F1 (#82): the transparent catch-all must strip caller-supplied
// volumes (any case) before forwarding as admin — if the backend ever accepts
// volume attach through a sandbox-scoped action, an unvalidated array would
// reopen the cross-tenant mount closed at create.
func TestCatchAll_StripsVolumesFromBody(t *testing.T) {
	sandboxes := []daytona.Sandbox{{ID: "sb-1", Labels: map[string]string{"daytona-owner": "0xOWNER"}}}
	srv, captured := mockDaytona(t, sandboxes)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := newTestEngine(dtona, &mockBilling{}, "0xOWNER")

	body := `{"Volumes":[{"volumeId":"other-tenant"}],"volumes":[{"volumeId":"x"}],"keep":"me"}`
	req := httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-1/update", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var forwarded []byte
	for _, b := range *captured {
		if len(b) > 0 && strings.Contains(string(b), "keep") {
			forwarded = b
		}
	}
	if forwarded == nil {
		t.Fatalf("forwarded body not captured; status=%d", w.Code)
	}
	s := strings.ToLower(string(forwarded))
	if strings.Contains(s, "volumes") {
		t.Errorf("volumes must be stripped from catch-all forward, got: %s", forwarded)
	}
	if !strings.Contains(string(forwarded), "keep") {
		t.Errorf("unrelated fields must be preserved, got: %s", forwarded)
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

// Finding #26: caller-controlled routing/method-override headers must never
// reach Daytona next to the injected admin bearer — an upstream that honors
// them would reinterpret the request (other path, other method) with admin
// rights on a route the proxy's gates never saw.
func TestForward_StripsOverrideHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`[]`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) {
		c.Set("wallet_address", "0xADmin")
		c.Next()
	})
	NewHandler(dtona, &mockBilling{}, nil, nil, nil, nil, nil, nil, nil, "",
		[]string{"0xadmin"}, "", nil, zap.NewNop(), "", nil, 0).Register(api)

	req := httptest.NewRequest(http.MethodGet, "/api/volumes", nil)
	req.Header.Set("X-Original-URL", "/api/sandbox/victim/start")
	req.Header.Set("X-HTTP-Method-Override", "DELETE")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("Cookie", "session=fake")
	req.Header.Set("X-Wallet-Address", "0xADmin")
	req.Header.Set("X-Signed-Message", "c2lnbmVk")
	req.Header.Set("X-Wallet-Signature", "0xdeadbeef")
	req.Header.Set("X-Custom-App", "keep-me")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got == nil {
		t.Fatalf("request not forwarded; status=%d", w.Code)
	}
	// X-Forwarded-For: the caller's spoofed value must be gone; the transport
	// re-adds one containing only the TRUE client IP (ReverseProxy appends it
	// after the Director) — which is exactly the wanted outcome.
	if xff := got.Get("X-Forwarded-For"); strings.Contains(xff, "1.2.3.4") {
		t.Errorf("caller-spoofed X-Forwarded-For must be stripped, got %q", xff)
	}
	for _, h := range []string{
		"X-Original-Url", "X-Http-Method-Override", "X-Forwarded-Host",
		"Cookie",
		// signature material must never reach the upstream operator
		"X-Wallet-Address", "X-Signed-Message", "X-Wallet-Signature",
	} {
		if got.Get(h) != "" {
			t.Errorf("override header %s must be stripped, got %q", h, got.Get(h))
		}
	}
	if got.Get("X-Custom-App") != "keep-me" {
		t.Error("unrelated headers must pass through")
	}
	if got.Get("Authorization") != "Bearer test-key" {
		t.Errorf("admin bearer expected, got %q", got.Get("Authorization"))
	}
}

// Finding #71 helpers: the sealed create must pin the FORWARDED image to the
// attested digest — a mutable tag can be re-pointed between attestation and
// the runner's pull, running code the attestation never covered.
func TestRewriteImage_PinsForwardedRef(t *testing.T) {
	body := []byte(`{"image":"registry:6000/daytona/app:latest","sealed":true,"env":{"A":"b"}}`)
	if !hasDirectImage(body) {
		t.Fatal("hasDirectImage must detect a direct ref")
	}
	out, err := rewriteImage(body, "registry:6000/daytona/app@sha256:abc")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck
	if m["image"] != "registry:6000/daytona/app@sha256:abc" {
		t.Errorf("image not pinned: %v", m["image"])
	}
	if m["env"].(map[string]any)["A"] != "b" {
		t.Error("other fields must survive")
	}
	if hasDirectImage([]byte(`{"snapshot":"snap-1"}`)) {
		t.Error("snapshot-only body must not count as direct image")
	}
}

// ── GET /api/events default window ───────────────────────────────────────────

type capturingEventFetcher struct{ gotSince uint64 }

func (f *capturingEventFetcher) GetVoucherEvents(_ context.Context, sinceTimestamp uint64, _, _ int) ([]chain.VoucherEvent, int, uint64, error) {
	f.gotSince = sinceTimestamp
	return nil, 0, 0, nil
}

// An omitted/zero since must NOT mean "all history": on any contract with real
// history an unbounded event query blows past RPC response limits and 502s
// every time (observed live at nonce 514k). The default is a 7-day window.
func TestEvents_DefaultSinceIsBounded(t *testing.T) {
	fetcher := &capturingEventFetcher{}
	srv, _ := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) { c.Set("wallet_address", "0xWALLET"); c.Next() })
	NewHandler(dtona, &mockBilling{}, nil, nil, fetcher, nil, nil, nil, nil, "",
		nil, "", nil, zap.NewNop(), "", nil, 0).RegisterPublic(api)

	for _, q := range []string{"", "?since=0"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events"+q, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%q: status %d body %s", q, w.Code, w.Body.String())
		}
		weekAgo := uint64(time.Now().Add(-7 * 24 * time.Hour).Unix())
		if fetcher.gotSince < weekAgo-60 || fetcher.gotSince > weekAgo+60 {
			t.Errorf("%q: since=%d, want ~%d (7-day window)", q, fetcher.gotSince, weekAgo)
		}
	}

	// Explicit since is honored untouched.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events?since=1700000000", nil))
	if fetcher.gotSince != 1700000000 {
		t.Errorf("explicit since not honored: %d", fetcher.gotSince)
	}
}

// ── #74 balance-gate TOCTOU ──────────────────────────────────────────────────

// N concurrent creates against a balance that covers exactly ONE must admit
// exactly one. Pre-fix, all N read the pre-reservation total and all passed;
// reserve-first serializes on INCRBY, so this holds under ANY interleaving.
func TestCreateGate_ConcurrentCreates_OnlyOneAdmitted(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	var daytonaCreates int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			atomic.AddInt32(&daytonaCreates, 1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"sb-new","state":"started","labels":{"daytona-owner":"0xW"}}`)) //nolint:errcheck
	}))
	defer srv.Close()
	dtona := daytona.NewClient(srv.URL, "test-key")

	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) { c.Set("wallet_address", "0xW"); c.Next() })
	// createFee=100, no per-resource pricing → createRequired = 100.
	// Balance 150 covers exactly one create.
	NewHandler(dtona, &mockBilling{}, &mockBalChecker{bal: big.NewInt(150)}, nil, nil,
		big.NewInt(100), new(big.Int), new(big.Int), new(big.Int), "0xPROV",
		nil, "", rdb, zap.NewNop(), "", nil, 60).Register(api)

	const n = 8
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"snap-x"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)

	ok, denied := 0, 0
	for c := range codes {
		switch c {
		case http.StatusOK, http.StatusCreated:
			ok++
		case http.StatusPaymentRequired:
			denied++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 1 || denied != n-1 {
		t.Fatalf("want exactly 1 admitted / %d denied, got %d / %d (daytona creates: %d)",
			n-1, ok, denied, atomic.LoadInt32(&daytonaCreates))
	}
	if got := atomic.LoadInt32(&daytonaCreates); got != 1 {
		t.Fatalf("daytona must see exactly 1 create, got %d", got)
	}
}

// A rejected create must roll back its reservation — otherwise rejections
// permanently eat into available balance until the TTL expires.
func TestCreateGate_RejectionRollsBackReservation(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	srv, _ := mockDaytona(t, nil)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) { c.Set("wallet_address", "0xW"); c.Next() })
	NewHandler(dtona, &mockBilling{}, &mockBalChecker{bal: big.NewInt(10)}, nil, nil,
		big.NewInt(100), new(big.Int), new(big.Int), new(big.Int), "0xPROV",
		nil, "", rdb, zap.NewNop(), "", nil, 60).Register(api)

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"snap-x"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusPaymentRequired {
			t.Fatalf("attempt %d: status %d", i, w.Code)
		}
	}
	if got := billing.GetReserved(context.Background(), rdb, "0xW", "0xPROV"); got.Sign() != 0 {
		t.Fatalf("rejected creates must leave zero reservation, got %s", got)
	}
}

// Review #116 F1: a start against an already-open billing session is a billing
// no-op (OnStart returns early), so it must NOT take a reservation — that would
// leak for the TTL. Assert the reservation counter stays at whatever it was.
func TestStartGate_OpenSession_NoReservationTaken(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	// Seed an open billing session for the sandbox.
	billing.CreateSession(context.Background(), rdb, billing.Session{ //nolint:errcheck
		SandboxID: "sb-open", Owner: "0xW", Provider: "0xPROV", PricePerSec: "100",
	})

	srv, _ := mockDaytona(t, []daytona.Sandbox{{ID: "sb-open", CPU: 2, Memory: 4, State: "stopped", Labels: map[string]string{"daytona-owner": "0xW"}}})
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) { c.Set("wallet_address", "0xW"); c.Next() })
	NewHandler(dtona, &mockBilling{}, &mockBalChecker{bal: big.NewInt(1000)}, nil, nil,
		big.NewInt(100), big.NewInt(1), new(big.Int), new(big.Int), "0xPROV",
		nil, "", rdb, zap.NewNop(), "", nil, 60).Register(api)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-open/start", nil)
	r.ServeHTTP(w, req)

	if got := billing.GetReserved(context.Background(), rdb, "0xW", "0xPROV"); got.Sign() != 0 {
		t.Fatalf("start on an open session must take no reservation, got %s", got)
	}
}

// Review #118 nit: pin that a snapshot create bills the SNAPSHOT's spec, not 0.
// The gate resolves cpu/mem from GetSnapshot; OnCreate must receive them (the
// response-echo behavior this PR's correctness leans on). A Daytona that stops
// echoing spec would silently reopen #77 — this test fires if it does.
func TestSnapshotCreate_BillsSnapshotSpec(t *testing.T) {
	// mock Daytona: snapshot lookup returns 4c/8g; create returns the sandbox.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/snapshots/big-4c", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"big-4c","name":"big-4c","cpu":4,"mem":8,"state":"active"}`)) //nolint:errcheck
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"sb-x","state":"started","labels":{"daytona-owner":"0xW"}}`)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dtona := daytona.NewClient(srv.URL, "test-key")
	mb := &mockBilling{}
	r := gin.New()
	api := r.Group("/api", func(c *gin.Context) { c.Set("wallet_address", "0xW"); c.Next() })
	NewHandler(dtona, mb, nil, nil, nil, big.NewInt(1), new(big.Int), new(big.Int), new(big.Int),
		"0xPROV", nil, "", nil, zap.NewNop(), "", nil, 60).Register(api)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"big-4c"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", w.Code, w.Body.String())
	}
	// OnCreate runs in a goroutine — wait for it.
	var cpu, mem int
	for i := 0; i < 100; i++ {
		mb.mu.Lock()
		cpu, mem = mb.createCPU, mb.createMem
		done := len(mb.creates) > 0
		mb.mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if cpu != 4 || mem != 8 {
		t.Fatalf("OnCreate billed spec = %dc/%dg, want the snapshot's 4c/8g", cpu, mem)
	}
}
