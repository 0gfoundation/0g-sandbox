//go:build !sealdebug

package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

// The transparent catch-all forwards the listed /sandbox/:id/* actions to
// Daytona as admin. On a sealed sandbox even those are a way into any port,
// not just the attested :8080 — outside what sealing promises the owner — so
// sealed denies the catch-all entirely; the lifecycle actions an owner
// legitimately needs have explicit routes and keep working.
func TestSealedSandbox_CatchAllDenied(t *testing.T) {
	sealedSB := daytona.Sandbox{
		ID:     "sb-sealed",
		Labels: map[string]string{ownerLabel: "0xOWNER", sealedLabel: "true"},
	}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sealedSB})
	r := newTestEngine(daytona.NewClient(srv.URL, "key"), &mockBilling{}, "0xOWNER")

	for _, tc := range []struct {
		method, path string
		listed       bool // refused by the sealed gate, not by the allowlist
	}{
		{http.MethodGet, "/api/sandbox/sb-sealed/ports/3000/preview-url", true},
		{http.MethodDelete, "/api/sandbox/sb-sealed/ssh-access", true},
		{http.MethodPost, "/api/sandbox/sb-sealed/public/true", false},
		{http.MethodGet, "/api/sandbox/sb-sealed/build-logs", false},
		{http.MethodPost, "/api/sandbox/sb-sealed/anything-new-daytona-adds", false},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s on a sealed sandbox: got %d, want 403 — the owner "+
				"has a channel into the workload beyond :8080", tc.method, tc.path, w.Code)
		}
		if tc.listed && !strings.Contains(w.Body.String(), "sealed sandbox") {
			t.Errorf("%s %s: refused by %s, want the sealed gate", tc.method, tc.path, w.Body.String())
		}
	}
}

// Lifecycle control stays with the owner of a sealed sandbox: these have
// explicit routes and must not be caught by the sealed catch-all denial.
func TestSealedSandbox_LifecycleStillAllowed(t *testing.T) {
	sealedSB := daytona.Sandbox{
		ID:     "sb-sealed",
		Labels: map[string]string{ownerLabel: "0xOWNER", sealedLabel: "true"},
	}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sealedSB})
	r := newTestEngine(daytona.NewClient(srv.URL, "key"), &mockBilling{}, "0xOWNER")

	for _, path := range []string{"/stop", "/archive"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-sealed"+path, nil))
		if w.Code == http.StatusForbidden {
			t.Errorf("POST %s on own sealed sandbox was denied; lifecycle must stay available", path)
		}
	}
}

// Unsealed sandboxes keep the listed passthrough actions (passthroughActions).
func TestUnsealedSandbox_CatchAllStillForwards(t *testing.T) {
	sb := daytona.Sandbox{ID: "sb-normal", Labels: map[string]string{ownerLabel: "0xOWNER"}}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sb})
	r := newTestEngine(daytona.NewClient(srv.URL, "key"), &mockBilling{}, "0xOWNER")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sandbox/sb-normal/ports/3000/preview-url", nil))
	// The mock answers unknown GET subpaths with 404, so "forwarded" shows up
	// as anything other than the proxy's own 403.
	if w.Code == http.StatusForbidden {
		t.Errorf("unsealed catch-all was denied (403); it must still forward")
	}
}

// Non-owners are still refused on the catch-all, sealed or not.
func TestCatchAll_NonOwnerDenied(t *testing.T) {
	sb := daytona.Sandbox{ID: "sb-normal", Labels: map[string]string{ownerLabel: "0xOWNER"}}
	srv, _ := mockDaytona(t, []daytona.Sandbox{sb})
	r := newTestEngine(daytona.NewClient(srv.URL, "key"), &mockBilling{}, "0xSOMEONEELSE")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sandbox/sb-normal/ports/3000/preview-url", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("non-owner catch-all: got %d, want 403", w.Code)
	}
}
