package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/0gfoundation/0g-sandbox/internal/auth"
)

const testProvider = "0x47a8C0Ca5cCEC440b17fd859Dee6a72438aCc31e"

// TestSignRequestPassesRequestBinding checks the CLI's signatures against the
// server middleware with request binding required — the two must agree on
// the action format and the body hash, or every CLI call fails in production.
func TestSignRequestPassesRequestBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := gin.New()
	api := r.Group("/api", auth.MiddlewareWithOptions(rdb, auth.Options{
		ProviderAddress:       testProvider,
		Strict:                true,
		RequireRequestBinding: true,
	}))
	echo := func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(b))
	}
	api.POST("/sandbox", echo)
	api.GET("/sandbox", echo)
	api.Any("/toolbox/:id/*action", echo)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, method, path, resourceID string
		body                           []byte
	}{
		{"create", http.MethodPost, "/api/sandbox", "", []byte(`{"snapshot":"0g-ubuntu22"}`)},
		{"list", http.MethodGet, "/api/sandbox", "", nil},
		{"exec", http.MethodPost, "/api/toolbox/sb-1/toolbox/process/execute", "sb-1", []byte(`{"command":"ls","timeout":10}`)},
		{"toolbox-query", http.MethodGet, "/api/toolbox/sb-1/toolbox/files?path=/home", "sb-1", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg, sig, addr := signRequest(key, testProvider, tc.method, tc.path, tc.resourceID, tc.body)
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.body))
			req.Header.Set("X-Wallet-Address", addr)
			req.Header.Set("X-Signed-Message", msg)
			req.Header.Set("X-Wallet-Signature", sig)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("CLI-signed %s %s rejected: %d %s", tc.method, tc.path, w.Code, w.Body.String())
			}
			if w.Body.String() != string(tc.body) {
				t.Fatalf("handler saw body %q, want %q", w.Body.String(), tc.body)
			}
		})
	}
}
