package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/auth"
	"github.com/0gfoundation/0g-sandbox/internal/broker"
)

func monitorEngine(t *testing.T, admins []string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	r := gin.New()
	sessions := func(context.Context) []broker.SessionEntry {
		return []broker.SessionEntry{{SandboxID: "sb-1"}}
	}
	registerMonitor(r, rdb, admins, sessions, zap.NewNop())
	return r
}

func signedMonitorGet(t *testing.T, key *ecdsa.PrivateKey, nonce string) *http.Request {
	t.Helper()
	msg, _ := json.Marshal(auth.SignedRequest{
		Action:    "GET:/api/monitor",
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
		Nonce:     nonce,
		Payload:   json.RawMessage(`{}`),
	})
	sig, _ := crypto.Sign(auth.HashMessage(msg), key)
	sig[64] += 27
	req := httptest.NewRequest(http.MethodGet, "/api/monitor", nil)
	req.Header.Set("X-Wallet-Address", crypto.PubkeyToAddress(key.PublicKey).Hex())
	req.Header.Set("X-Signed-Message", base64.StdEncoding.EncodeToString(msg))
	req.Header.Set("X-Wallet-Signature", "0x"+hex.EncodeToString(sig))
	return req
}

func TestMonitorRoute(t *testing.T) {
	admin, _ := crypto.GenerateKey()
	other, _ := crypto.GenerateKey()
	admins := []string{strings.ToLower(crypto.PubkeyToAddress(admin.PublicKey).Hex())}

	t.Run("no admins configured: route stays off", func(t *testing.T) {
		w := httptest.NewRecorder()
		monitorEngine(t, nil).ServeHTTP(w, signedMonitorGet(t, admin, "m-1"))
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", w.Code)
		}
	})

	r := monitorEngine(t, admins)

	t.Run("unsigned request rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/monitor", nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("non-admin signer rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, signedMonitorGet(t, other, "m-2"))
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d", w.Code)
		}
	})

	t.Run("admin signer gets the registry", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, signedMonitorGet(t, admin, "m-3"))
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), `"sb-1"`) {
			t.Fatalf("expected the session list, got %s", w.Body.String())
		}
	})
}
