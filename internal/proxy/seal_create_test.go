package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// forwardedSeal is what a sealed create forwarded to Daytona.
type forwardedSeal struct {
	Attestation ImageAttestation
	Env         map[string]any
	Snapshot    any
	Labels      map[string]any
	Body        map[string]any
}

// testSealSnapshotID is the Daytona id of the fixture's "sealed" snapshot.
const testSealSnapshotID = "5b7c0f0e-1d2a-4c3b-8e4f-000000000001"

func newSealedCreateFixture(t *testing.T) (*Handler, *gin.Engine, <-chan forwardedSeal, *atomic.Int32) {
	t.Helper()
	return newSealedCreateFixtureWithSnapshot(t, nil)
}

// newSealedCreateFixtureWithSnapshot adds extra fields to the Daytona record
// of the "sealed" snapshot.
// testSealEntrypoint is the fixture image's own entrypoint.
var testSealEntrypoint = []string{"/usr/local/bin/sealed"}

func newSealedCreateFixtureWithSnapshot(t *testing.T, extra map[string]any) (*Handler, *gin.Engine, <-chan forwardedSeal, *atomic.Int32) {
	t.Helper()
	return newSealedCreateFixtureWithRecords(t, func(image string) []map[string]any {
		snapshot := map[string]any{"id": testSealSnapshotID, "name": "sealed", "cpu": 1, "mem": 1, "state": "active", "imageName": image}
		for k, v := range extra {
			snapshot[k] = v
		}
		return []map[string]any{snapshot}
	}, nil)
}

// newSealedCreateFixtureWithRecords serves the given snapshot records, built
// around the fixture image. GET /api/snapshots/<ref> resolves ids before names,
// as Daytona does, unless get overrides it.
func newSealedCreateFixtureWithRecords(t *testing.T, build func(image string) []map[string]any, get func(ref string) map[string]any) (*Handler, *gin.Engine, <-chan forwardedSeal, *atomic.Int32) {
	t.Helper()
	entrypoint, _ := json.Marshal(testSealEntrypoint)
	config := []byte(fmt.Sprintf(`{"architecture":"amd64","os":"linux","config":{"Entrypoint":%s},"rootfs":{"type":"layers","diff_ids":[]}}`, entrypoint))
	configDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(config))
	manifest := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`, configDigest, len(config)))
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := manifest
		if strings.HasSuffix(r.URL.Path, "/blobs/"+configDigest) {
			body = config
		} else {
			w.Header().Set("Docker-Content-Digest", fmt.Sprintf("sha256:%x", sha256.Sum256(manifest)))
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	}))
	t.Cleanup(registry.Close)
	image := strings.TrimPrefix(strings.Replace(registry.URL, "127.0.0.1", "localhost", 1), "http://") + "/sealed:test"
	created := &atomic.Int32{}
	envelopes := make(chan forwardedSeal, 4)
	records := build(image)
	if get == nil {
		get = func(ref string) map[string]any {
			for _, field := range []string{"id", "name"} {
				for _, rec := range records {
					if rec[field] == ref {
						return rec
					}
				}
			}
			return nil
		}
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/snapshots" {
			_ = json.NewEncoder(w).Encode(map[string]any{"items": records, "totalPages": 1})
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/snapshots/") {
			rec := get(strings.TrimPrefix(r.URL.Path, "/api/snapshots/"))
			if rec == nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(rec)
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
				labels, _ := body["labels"].(map[string]any)
				envelopes <- forwardedSeal{Attestation: a, Env: env, Snapshot: body["snapshot"], Labels: labels, Body: body}
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
		request := httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true,"env":{"SANDBOX_SEAL_ATTESTATION":"caller","ATTESTOR_TLS_DESCRIPTOR":"caller","ATTESTOR_URL":"https://attestor.example"}}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, request)
		if w.Code != http.StatusOK {
			t.Fatalf("create %d: %d %s", i, w.Code, w.Body.String())
		}
		forwarded := <-envelopes
		a := forwarded.Attestation
		if a.Version != 3 || a.RegisteredAt != testSealIssuer().RegisteredAt+i {
			t.Fatalf("stale/caller issuer: %+v", a)
		}
		if recoverSealSigner(t, a).Hex() != h.providerAddress {
			t.Fatal("attestation did not use provider app signer")
		}
		if _, ok := forwarded.Env["ATTESTOR_TLS_DESCRIPTOR"]; ok || forwarded.Env["ATTESTOR_URL"] != "https://attestor.example" {
			t.Fatalf("forwarded env %v", forwarded.Env)
		}
		if strings.Contains(w.Body.String(), sealKeyEnv) {
			t.Fatal("returned container secret")
		}
	}
	if lookups != 2 || created.Load() != 2 {
		t.Fatalf("lookups %d, creates %d", lookups, created.Load())
	}
}

// A create cannot name a sandbox to link to. Daytona resolves linkedSandbox in
// the organization every tenant shares, puts both sandboxes on one network,
// and gives the new sandbox's name to it as a DNS alias there.
func TestSealedCreateNeverForwardsSandboxLinks(t *testing.T) {
	refused := []string{
		`{"snapshot":"sealed","sealed":true,"linkedSandbox":"other-sandbox-id","autoDeleteInterval":0}`,
		`{"snapshot":"sealed","sealed":true,"LinkedSandbox":"other-sandbox-id","autoDeleteInterval":0}`,
		`{"snapshot":"sealed","sealed":true,"linkedſandbox":"other-sandbox-id","autoDeleteInterval":0}`,
		`{"snapshot":"sealed","sealed":true,"name":"EVMRPC-testnet.0g.ai"}`,
		`{"snapshot":"sealed","sealed":true,"Name":"evmrpc-testnet.0g.ai"}`,
	}
	for _, body := range refused {
		h, r, _, created := newSealedCreateFixture(t)
		h.SealIssuer = func(context.Context) (SealIssuer, error) { return testSealIssuer(), nil }
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d %s; want 400", body, w.Code, w.Body.String())
		}
		if created.Load() != 0 {
			t.Errorf("%s: reached Daytona", body)
		}
	}

	h, r, envelopes, _ := newSealedCreateFixture(t)
	h.SealIssuer = func(context.Context) (SealIssuer, error) { return testSealIssuer(), nil }
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true,"name":"my-agent","AutoDeleteInterval":0,"autoDeleteInterval":0}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("case-variant pair: got %d %s; want 400", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true,"name":"my-agent","autoDeleteInterval":0}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	forwarded := (<-envelopes).Body
	if forwarded["autoDeleteInterval"] != float64(-1) || forwarded["name"] != "my-agent" {
		t.Fatalf("forwarded autoDeleteInterval %v name %v; want -1 and my-agent", forwarded["autoDeleteInterval"], forwarded["name"])
	}
	for k := range forwarded {
		if strings.EqualFold(k, "linkedSandbox") {
			t.Fatalf("forwarded %q", k)
		}
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

// The certificate attests only the image digest, so a sealed create refuses a
// snapshot that runs anything but its image's own entrypoint, and releases its
// reservation.
func TestSealedCreateRefusesSnapshotOverrides(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"entrypoint": {"entrypoint": []string{"sh", "-c", "exec /entry"}},
		"build_info": {"buildInfo": map[string]any{"snapshotRef": "built", "dockerfileContent": "FROM scratch"}},
	} {
		t.Run(name, func(t *testing.T) {
			h, r, _, created := newSealedCreateFixtureWithSnapshot(t, extra)
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			h.rdb = rdb
			h.balCheck = &mockBalChecker{bal: big.NewInt(1000)}
			h.createFee = big.NewInt(11)
			h.computePricePerSec = new(big.Int)
			h.SealIssuer = func(context.Context) (SealIssuer, error) {
				t.Fatal("refused snapshot reached the issuer")
				return SealIssuer{}, nil
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true}`)))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), errSnapshotOverridesImage.Error()) {
				t.Fatalf("want refusal, got %d %s", w.Code, w.Body.String())
			}
			if created.Load() != 0 {
				t.Fatal("created a sealed sandbox from an overriding snapshot")
			}
			if got := billing.GetReserved(context.Background(), rdb, "0xOWNER", h.providerAddress); got.Sign() != 0 {
				t.Fatalf("reservation after refusal %s", got)
			}

			// Nonsealed creates are unaffected.
			h.sealKey = nil
			w = httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed"}`)))
			if w.Code != http.StatusOK || created.Load() != 1 {
				t.Fatalf("nonsealed create: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

// Daytona copies the image's own entrypoint into every snapshot record, so a
// recorded entrypoint alone must not refuse a sealed create.
func TestSealedCreateAcceptsSnapshotWithImageEntrypoint(t *testing.T) {
	h, r, _, created := newSealedCreateFixtureWithSnapshot(t, map[string]any{"entrypoint": testSealEntrypoint})
	h.SealIssuer = func(context.Context) (SealIssuer, error) { return testSealIssuer(), nil }
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"sealed","sealed":true}`)))
	if w.Code != http.StatusOK || created.Load() != 1 {
		t.Fatalf("sealed create from a snapshot with its image entrypoint: %d %s", w.Code, w.Body.String())
	}
}

// The proxy forwards the certified snapshot record by id, so Daytona starts
// the record whose image the certificate names.
func TestSealedCreateForwardsCertifiedSnapshotID(t *testing.T) {
	for _, ref := range []string{"sealed", testSealSnapshotID} {
		t.Run(ref, func(t *testing.T) {
			h, r, envelopes, created := newSealedCreateFixture(t)
			h.SealIssuer = func(context.Context) (SealIssuer, error) { return testSealIssuer(), nil }
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"`+ref+`","sealed":true}`)))
			if w.Code != http.StatusOK || created.Load() != 1 {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			forwarded := <-envelopes
			if forwarded.Snapshot != testSealSnapshotID {
				t.Fatalf("forwarded snapshot %v, want the certified record id", forwarded.Snapshot)
			}
			if forwarded.Labels[imageLabel] != "snapshot:"+ref {
				t.Fatalf("image label %v", forwarded.Labels[imageLabel])
			}
		})
	}
}

// Go's struct decode matches "Snapshot" and "ſnapshot" (U+017F) to the
// snapshot field and keeps the last one, while Daytona reads only the exact
// key. A create that names a snapshot under two spellings is refused, so the
// certificate cannot name one image while Daytona starts another.
func TestCreateRefusesFoldedSnapshotKeys(t *testing.T) {
	build := func(image string) []map[string]any {
		return []map[string]any{
			{"id": testSealSnapshotID, "name": "sealed", "cpu": 1, "mem": 1, "state": "active", "imageName": image},
			{"id": "5b7c0f0e-1d2a-4c3b-8e4f-000000000002", "name": "decoy", "cpu": 1, "mem": 1, "state": "active", "imageName": "decoy:latest"},
		}
	}
	bodies := []string{
		// serde_json without preserve_order sorts keys bytewise, so the attestor
		// relays the U+017F variant after the exact key.
		`{"sealed":true,"snapshot":"decoy","ſnapshot":"sealed"}`,
		`{"Snapshot":"sealed","sealed":true,"snapshot":"decoy"}`,
		`{"sealed":true,"snapshot":"decoy","Snapshot":"sealed"}`,
		`{"sealed":true,"snapshot":"sealed","SNAPSHOT":"sealed"}`,
		`{"ſnapshot":"sealed","sealed":true}`,
		`{"Snapshot":"sealed","sealed":true}`,
	}
	for _, sealed := range []bool{true, false} {
		for _, body := range bodies {
			t.Run(fmt.Sprintf("sealed=%v/%s", sealed, body), func(t *testing.T) {
				h, r, _, created := newSealedCreateFixtureWithRecords(t, build, nil)
				if !sealed {
					h.sealKey = nil
					body = strings.Replace(body, `"sealed":true`, `"sealed":false`, 1)
				}
				h.SealIssuer = func(context.Context) (SealIssuer, error) {
					t.Fatal("refused create reached the issuer")
					return SealIssuer{}, nil
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(body)))
				if w.Code != http.StatusBadRequest {
					t.Fatalf("want 400, got %d %s", w.Code, w.Body.String())
				}
				if created.Load() != 0 {
					t.Fatal("created a sandbox from an ambiguous snapshot field")
				}
			})
		}
	}
}

// A sealed create refuses a reference that Daytona could resolve to a record
// other than the certified one, and releases its reservation.
func TestSealedCreateRefusesAmbiguousSnapshot(t *testing.T) {
	const other = "5b7c0f0e-1d2a-4c3b-8e4f-000000000002"
	record := func(id, name, image string) map[string]any {
		return map[string]any{"id": id, "name": name, "cpu": 1, "mem": 1, "state": "active", "imageName": image}
	}
	cases := map[string]struct {
		ref   string
		build func(image string) []map[string]any
		get   func(ref string) map[string]any
	}{
		"duplicate_name": {"sealed", func(image string) []map[string]any {
			return []map[string]any{record(testSealSnapshotID, "sealed", image), record(other, "sealed", "decoy:latest")}
		}, nil},
		"id_named_by_another_record": {"sealed", func(image string) []map[string]any {
			return []map[string]any{record(testSealSnapshotID, "sealed", image), record(other, testSealSnapshotID, "decoy:latest")}
		}, nil},
		"ref_is_name_and_id": {other, func(image string) []map[string]any {
			return []map[string]any{record(testSealSnapshotID, other, image), record(other, "decoy", "decoy:latest")}
		}, nil},
		"billing_record_differs": {"sealed", func(image string) []map[string]any {
			return []map[string]any{record(testSealSnapshotID, "sealed", image)}
		}, func(string) map[string]any { return record(other, "sealed", "decoy:latest") }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, r, _, created := newSealedCreateFixtureWithRecords(t, tc.build, tc.get)
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			h.rdb = rdb
			h.balCheck = &mockBalChecker{bal: big.NewInt(1000)}
			h.createFee = big.NewInt(11)
			h.computePricePerSec = new(big.Int)
			h.SealIssuer = func(context.Context) (SealIssuer, error) {
				t.Fatal("ambiguous snapshot reached the issuer")
				return SealIssuer{}, nil
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox", strings.NewReader(`{"snapshot":"`+tc.ref+`","sealed":true}`)))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "more than one snapshot record") {
				t.Fatalf("want ambiguity refusal, got %d %s", w.Code, w.Body.String())
			}
			if created.Load() != 0 {
				t.Fatal("created a sealed sandbox from an ambiguous snapshot")
			}
			if got := billing.GetReserved(context.Background(), rdb, "0xOWNER", h.providerAddress); got.Sign() != 0 {
				t.Fatalf("reservation after refusal %s", got)
			}
		})
	}
}

func TestResolveSnapshot(t *testing.T) {
	const a, b = "5b7c0f0e-1d2a-4c3b-8e4f-00000000000a", "5b7c0f0e-1d2a-4c3b-8e4f-00000000000b"
	snaps := []daytona.Snapshot{{ID: a, Name: "one"}, {ID: b, Name: "two"}}
	for _, ref := range []string{"one", a} {
		if s, err := resolveSnapshot(snaps, ref); err != nil || s.ID != a {
			t.Fatalf("resolve %q: %v %v", ref, s, err)
		}
	}
	// A record listed twice (pagination shifting) is still one record.
	if s, err := resolveSnapshot(append(snaps, daytona.Snapshot{ID: a, Name: "one"}), "one"); err != nil || s.ID != a {
		t.Fatalf("duplicate listing: %v %v", s, err)
	}
	for name, tc := range map[string]struct {
		snaps []daytona.Snapshot
		ref   string
	}{
		"not_found":      {snaps, "three"},
		"no_id":          {[]daytona.Snapshot{{Name: "one"}}, "one"},
		"same_name":      {[]daytona.Snapshot{{ID: a, Name: "one"}, {ID: b, Name: "one"}}, "one"},
		"name_is_id":     {[]daytona.Snapshot{{ID: a, Name: "one"}, {ID: b, Name: a}}, a},
		"id_is_a_name":   {[]daytona.Snapshot{{ID: a, Name: "one"}, {ID: b, Name: a}}, "one"},
		"empty_snapshot": {nil, "one"},
	} {
		if s, err := resolveSnapshot(tc.snaps, tc.ref); err == nil {
			t.Errorf("%s: resolved %+v", name, s)
		}
	}
}

func TestSnapshotCreateRefusesUUIDShapedName(t *testing.T) {
	h, r, _, _ := newSealedCreateFixture(t)
	h.adminAddresses = []string{"0xowner"}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/snapshots", strings.NewReader(`{"name":"5B7C0F0E-1D2A-4C3B-8E4F-000000000001","imageName":"x:1"}`)))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "UUID-shaped") {
		t.Fatalf("want refusal, got %d %s", w.Code, w.Body.String())
	}
}
