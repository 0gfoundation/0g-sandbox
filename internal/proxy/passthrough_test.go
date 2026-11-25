package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

// The listed passthrough actions reach Daytona unchanged for the owner of an
// unsealed sandbox.
func TestPassthrough_ListedActionsForward(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/api/sandbox/sb-slash/ports/3000/preview-url"},
		{http.MethodGet, "/api/sandbox/sb-slash/ports/65535/preview-url?x=1"},
		{http.MethodDelete, "/api/sandbox/sb-slash/ssh-access"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			srv, state := newSlashDaytona(t, false)
			r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.target, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
			}
			u, _ := url.Parse(tc.target)
			state.mu.Lock()
			defer state.mu.Unlock()
			if len(state.mutations) != 1 || state.mutations[0] != tc.method+" "+u.Path {
				t.Errorf("forwarded %v, want [%s %s]", state.mutations, tc.method, u.Path)
			}
		})
	}
}

// Every other action is refused without reaching Daytona, even for the owner
// of an unsealed sandbox: the forward runs as admin, and Daytona has actions
// billing does not account for. An action Daytona adds later is refused too.
func TestPassthrough_UnlistedActionsRefused(t *testing.T) {
	for _, tc := range []struct{ method, action string }{
		// Daytona sandbox actions not listed.
		{http.MethodPost, "fork"},
		{http.MethodPost, "snapshot"},
		{http.MethodPost, "backup"},
		{http.MethodPost, "recover"},
		{http.MethodPost, "resize"},
		{http.MethodPost, "public/true"},
		{http.MethodPost, "autodelete/0"},
		{http.MethodPost, "network-settings"},
		{http.MethodPost, "last-activity"},
		{http.MethodPut, "state"},
		{http.MethodGet, "forks"},
		{http.MethodGet, "parent"},
		{http.MethodGet, "ancestors"},
		{http.MethodGet, "build-logs"},
		{http.MethodGet, "build-logs-url"},
		{http.MethodGet, "toolbox-proxy-url"},
		{http.MethodGet, "organization"},
		{http.MethodGet, "region-quota"},
		{http.MethodGet, "ports/3000/signed-preview-url"},
		{http.MethodPost, "ports/3000/signed-preview-url/abc/expire"},
		{http.MethodPost, "anything-new-daytona-adds"},
		// Listed actions with another method.
		{http.MethodPost, "ports/3000/preview-url"},
		{http.MethodDelete, "ports/3000/preview-url"},
		{http.MethodHead, "ports/3000/preview-url"},
		{http.MethodGet, "ssh-access"},
		{http.MethodPatch, "ssh-access"},
		// Listed actions in another spelling.
		{http.MethodGet, "ports/abc/preview-url"},
		{http.MethodGet, "ports/0/preview-url"},
		{http.MethodGet, "ports/65536/preview-url"},
		{http.MethodGet, "ports/03000/preview-url"},
		{http.MethodGet, "ports/+3000/preview-url"},
		{http.MethodGet, "ports/3000/preview-url/"},
		{http.MethodGet, "ports/3000/preview-url/x"},
		{http.MethodGet, "ports/3000/Preview-Url"},
		{http.MethodGet, "Ports/3000/preview-url"},
		{http.MethodGet, "ports//preview-url"},
		{http.MethodDelete, "SSH-Access"},
		{http.MethodDelete, "ssh-access/"},
		// Listed actions percent-encoded: they decode to a listed action, but
		// the path is forwarded as sent.
		{http.MethodGet, "ports/3000/preview%2Durl"},
		{http.MethodGet, "%70orts/3000/preview-url"},
		{http.MethodGet, "ports/%33000/preview-url"},
		{http.MethodDelete, "ssh%2daccess"},
		// The sandbox ID percent-encoded: the owner check reads it decoded.
		{http.MethodGet, "/api/sandbox/sb%2Dslash/ports/3000/preview-url"},
	} {
		t.Run(tc.method+" "+tc.action, func(t *testing.T) {
			srv, state := newSlashDaytona(t, false)
			r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
			target := "/api/sandbox/sb-slash/" + tc.action
			if strings.HasPrefix(tc.action, "/") {
				target = tc.action
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, target, nil))
			if w.Code < 400 || w.Code >= 500 {
				t.Errorf("want a refusal, got %d: %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if len(state.mutations) != 0 {
				t.Errorf("reached Daytona: %v", state.mutations)
			}
		})
	}
}

func TestIsPassthroughAction(t *testing.T) {
	for _, tc := range []struct {
		method, action string
		want           bool
	}{
		{http.MethodGet, "/ports/1/preview-url", true},
		{http.MethodGet, "/ports/65535/preview-url", true},
		{http.MethodGet, "/ports/65535/signed-preview-url", false},
		{http.MethodDelete, "/ssh-access", true},
		{http.MethodGet, "/ports/-1/preview-url", false},
		{http.MethodGet, "/ports/1e3/preview-url", false},
		{http.MethodGet, "/ports/ 80/preview-url", false},
		{http.MethodGet, "/ports/80", false},
		{http.MethodGet, "/", false},
		{http.MethodGet, "", false},
	} {
		if got := isPassthroughAction(tc.method, tc.action); got != tc.want {
			t.Errorf("isPassthroughAction(%s, %q) = %v, want %v", tc.method, tc.action, got, tc.want)
		}
	}
}
