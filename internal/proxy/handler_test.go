package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox-billing/internal/daytona"
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

func (m *mockBilling) OnCreate(_ context.Context, sandboxID, _ string) {
	m.mu.Lock(); defer m.mu.Unlock()
	m.creates = append(m.creates, sandboxID)
}
func (m *mockBilling) OnStart(_ context.Context, sandboxID, _ string) {
	m.mu.Lock(); defer m.mu.Unlock()
	m.starts = append(m.starts, sandboxID)
}
func (m *mockBilling) OnStop(_ context.Context, sandboxID string) {
	m.mu.Lock(); defer m.mu.Unlock()
	m.stops = append(m.stops, sandboxID)
}
func (m *mockBilling) OnDelete(_ context.Context, sandboxID string) {
	m.mu.Lock(); defer m.mu.Unlock()
	m.deletes = append(m.deletes, sandboxID)
}
func (m *mockBilling) OnArchive(_ context.Context, sandboxID string) {
	m.mu.Lock(); defer m.mu.Unlock()
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
	NewHandler(dtona, bh, nil, nil, nil, nil, nil, "", nil, zap.NewNop()).Register(api)
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
	if fwdBody["autostopInterval"] != float64(0) {
		t.Errorf("autostopInterval should be forced to 0, got %v", fwdBody["autostopInterval"])
	}
	if fwdBody["autoarchiveInterval"] != float64(0) {
		t.Errorf("autoarchiveInterval should be forced to 0, got %v", fwdBody["autoarchiveInterval"])
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
