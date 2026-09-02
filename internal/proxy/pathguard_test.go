package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func guardEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(PathTraversalGuard())
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.Any("/api/sandbox/:id/*action", ok)
	r.GET("/api/sandbox", ok)
	r.GET("/healthz", ok)
	return r
}

// The finding-#13 shape: :id binds to the attacker's sandbox while the raw
// forwarded path traverses to the victim's. Must die at the boundary — both
// literal and percent-encoded.
func TestPathGuard_RejectsDotSegments(t *testing.T) {
	r := guardEngine()
	for _, target := range []string{
		"/api/sandbox/mine/../victim/ssh-access",
		"/api/sandbox/mine/%2e%2e/victim/ssh-access",
		"/api/sandbox/mine/%2E%2E/victim/toolbox/process/execute",
		"/api/sandbox/./mine/labels",
		"/api/sandbox//mine/ssh-access",
	} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", target, w.Code)
		}
	}
}

func TestPathGuard_AllowsCanonicalPaths(t *testing.T) {
	r := guardEngine()
	for _, target := range []string{
		"/api/sandbox/550e8400-e29b-41d4-a716-446655440000/ssh-access",
		"/api/sandbox/my.app-v1.2/labels", // interior dots in a name are fine
		"/api/sandbox",
		"/healthz",
	} {
		req := httptest.NewRequest(http.MethodPost, target, nil)
		w := httptest.NewRecorder()
		if target == "/api/sandbox" || target == "/healthz" {
			req = httptest.NewRequest(http.MethodGet, target, nil)
		}
		r.ServeHTTP(w, req)
		if w.Code == http.StatusBadRequest {
			t.Errorf("%s: canonical path wrongly rejected", target)
		}
	}
}
