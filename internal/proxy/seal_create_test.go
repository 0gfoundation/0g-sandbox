package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func newSealedCreateFixture(t *testing.T) (*Handler, *gin.Engine, <-chan ImageAttestation, *atomic.Int32) {
	t.Helper()
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", testSealImageHash)
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		w.Header().Set("Content-Length", "2")
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(registry.Close)
	image := strings.TrimPrefix(strings.Replace(registry.URL, "127.0.0.1", "localhost", 1), "http://") + "/sealed:test"
	created := &atomic.Int32{}
	envelopes := make(chan ImageAttestation, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snapshot := map[string]any{"name": "sealed", "cpu": 1, "mem": 1, "state": "active", "imageName": image}
		if r.Method == http.MethodGet && r.URL.Path == "/api/snapshots" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{snapshot}, "totalPages": 1})
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/snapshots/") {
			_ = json.NewEncoder(w).Encode(snapshot)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/sandbox" {
			http.NotFound(w, r)
			return
		}
		created.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if env, ok := body["env"].(map[string]any); ok {
			if raw, ok := env["SANDBOX_SEAL_ATTESTATION"].(string); ok {
				var a ImageAttestation
				if err := json.Unmarshal([]byte(raw), &a); err != nil {
					t.Error(err)
				}
				envelopes <- a
			}
		}
		body["id"] = "new-sandbox"
		body["state"] = "started"
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(upstream.Close)
	appKey, _ := crypto.GenerateKey()
	h := NewHandler(daytona.NewClient(upstream.URL, "test-admin"), &mockBilling{}, nil, nil, nil, nil, nil, nil, nil,
		crypto.PubkeyToAddress(appKey.PublicKey).Hex(), nil, "", nil, zap.NewNop(), "", appKey, appKey, 60)
	r := gin.New()
	h.Register(r.Group("/api", func(c *gin.Context) { c.Set("wallet_address", "0xOWNER"); c.Next() }))
	return h, r, envelopes, created
}

func TestSealedCreateIssuerFailureNeverCreates(t *testing.T) {
	for _, name := range []string{"missing_callback", "chain_error", "invalid_scope", "deadline"} {
		t.Run(name, func(t *testing.T) {
			h, r, _, created := newSealedCreateFixture(t)
			switch name {
			case "chain_error":
				h.SealIssuer = func(context.Context) (SealIssuer, error) { return SealIssuer{}, errors.New("binding mismatch") }
			case "invalid_scope":
				h.SealIssuer = func(context.Context) (SealIssuer, error) { return SealIssuer{}, nil }
			case "deadline":
				h.SealIssuer = func(ctx context.Context) (SealIssuer, error) { <-ctx.Done(); return SealIssuer{}, ctx.Err() }
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			request := httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true}`)).WithContext(ctx)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, request)
			if w.Code < 500 {
				t.Fatalf("expected issuer refusal, got %d: %s", w.Code, w.Body.String())
			}
			if created.Load() != 0 {
				t.Fatal("created workload after issuer authentication failed")
			}
		})
	}
}

func TestSealedCreateIssuerRejectionReleasesOnlyThisReservation(t *testing.T) {
	for _, name := range []string{"missing_callback", "chain_error", "invalid_scope", "request_cancelled", "deadline"} {
		for _, held := range []int64{0, 42} {
			t.Run(fmt.Sprintf("%s/existing_%d", name, held), func(t *testing.T) {
				h, r, _, created := newSealedCreateFixture(t)
				mr := miniredis.RunT(t)
				rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = rdb.Close() })
				h.rdb = rdb
				h.balCheck = &mockBalChecker{bal: big.NewInt(1000)}
				h.createFee = big.NewInt(11)
				h.computePricePerSec = new(big.Int)
				const wallet = "0xOWNER"
				if held > 0 {
					if _, err := billing.Reserve(context.Background(), rdb, wallet, h.providerAddress, big.NewInt(held), time.Minute); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if name != "missing_callback" {
					h.SealIssuer = func(ctx context.Context) (SealIssuer, error) {
						if got := billing.GetReserved(context.Background(), rdb, wallet, h.providerAddress); got.Int64() != held+11 {
							t.Fatalf("create did not actually reserve: %s", got)
						}
						switch name {
						case "invalid_scope":
							return SealIssuer{}, nil
						case "request_cancelled":
							cancel()
							return SealIssuer{}, context.Canceled
						case "deadline":
							<-ctx.Done()
							return SealIssuer{}, ctx.Err()
						default:
							return SealIssuer{}, errors.New("chain unavailable")
						}
					}
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true}`)).WithContext(ctx))
				if w.Code < 500 {
					t.Fatalf("expected refusal, got %d: %s", w.Code, w.Body.String())
				}
				if created.Load() != 0 {
					t.Fatal("created sandbox after issuer refusal")
				}
				if got := billing.GetReserved(context.Background(), rdb, wallet, h.providerAddress); got.Int64() != held {
					t.Fatalf("reservation after refusal %s; want existing %d only", got, held)
				}
			})
		}
	}
}

func TestSealedCreateRefreshesIssuerAndUsesProviderKey(t *testing.T) {
	h, r, envelopes, created := newSealedCreateFixture(t)
	var lookups uint64
	h.SealIssuer = func(ctx context.Context) (SealIssuer, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("issuer callback has no deadline")
		}
		lookups++
		issuer := testSealIssuer()
		issuer.RegisteredAt += lookups
		return issuer, nil
	}
	for i := uint64(1); i <= 2; i++ {
		request := httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true,"registered_at":999,"env":{"SANDBOX_SEAL_ATTESTATION":"caller"}}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, request)
		if w.Code != http.StatusOK {
			t.Fatalf("create %d: %d %s", i, w.Code, w.Body.String())
		}
		a := <-envelopes
		if a.Version != 2 || a.RegisteredAt != testSealIssuer().RegisteredAt+i {
			t.Fatalf("stale/caller issuer: %+v", a)
		}
		if recoverSealSigner(t, a).Hex() != h.providerAddress {
			t.Fatal("attestation did not use provider app signer")
		}
		if strings.Contains(w.Body.String(), sealKeyEnv) {
			t.Fatal("returned container secret")
		}
	}
	if lookups != 2 || created.Load() != 2 {
		t.Fatalf("lookups %d, creates %d", lookups, created.Load())
	}
}

func TestNonsealedCreateNeedsNoIssuer(t *testing.T) {
	h, r, _, created := newSealedCreateFixture(t)
	h.sealKey = nil
	h.SealIssuer = func(context.Context) (SealIssuer, error) {
		t.Fatal("nonsealed request queried issuer")
		return SealIssuer{}, nil
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed"}`)))
	if w.Code != http.StatusOK || created.Load() != 1 {
		t.Fatalf("nonsealed path changed: %d %s", w.Code, w.Body.String())
	}
}
