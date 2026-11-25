//go:build !sealdebug

package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

func TestTrailingSlash_SealedAccessCannotReachUpstream(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/sandbox/sb-slash/ssh-access"},
		{http.MethodPost, "/api/sandbox/sb-slash/ssh-access/"},
		{http.MethodPut, "/api/sandbox/sb-slash/labels/"},
		{http.MethodPost, "/api/sandbox/sb-slash/toolbox/process/execute/"},
		{http.MethodPost, "/api/toolbox/sb-slash/process/execute"},
		{http.MethodPost, "/api/toolbox/sb-slash/process/execute/"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			srv, state := newSlashDaytona(t, true)
			r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"labels":{}}`)))
			if w.Code < 400 || w.Code >= 500 {
				t.Errorf("expected rejection before forwarding, got %d: %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if len(state.mutations) != 0 {
				t.Errorf("sealed operation reached admin upstream: %v", state.mutations)
			}
			if strings.Contains(w.Body.String(), "mock-private-key") {
				t.Error("sealed private key leaked")
			}
		})
	}
}
