package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

// ── scrubSealKeyFromBody ─────────────────────────────────────────────────────

func TestScrubSealKeyFromBody_Object(t *testing.T) {
	in := []byte(`{"id":"sb-1","env":{"SANDBOX_SEAL_KEY":"0xdeadbeef","SANDBOX_SEAL_ATTESTATION":"{\"pubkey\":\"0xabc\"}","PATH":"/usr/bin"},"state":"started"}`)
	out := scrubSealKeyFromBody(in)

	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("scrubbed body is not JSON: %s", out)
	}
	env, _ := m["env"].(map[string]any)
	if env == nil {
		t.Fatal("env map missing after scrub")
	}
	if _, still := env[sealKeyEnv]; still {
		t.Error("SANDBOX_SEAL_KEY survived the scrub")
	}
	if env["SANDBOX_SEAL_ATTESTATION"] == nil {
		t.Error("SANDBOX_SEAL_ATTESTATION must be kept — it is the public half")
	}
	if env["PATH"] != "/usr/bin" {
		t.Errorf("unrelated env vars must be kept, PATH=%v", env["PATH"])
	}
}

func TestScrubSealKeyFromBody_Array(t *testing.T) {
	in := []byte(`[{"id":"a","env":{"SANDBOX_SEAL_KEY":"0x1"}},{"id":"b","env":{"SANDBOX_SEAL_KEY":"0x2"}},{"id":"c","env":{}}]`)
	out := scrubSealKeyFromBody(in)

	var arr []map[string]any
	if err := json.Unmarshal(out, &arr); err != nil {
		t.Fatalf("scrubbed body is not JSON: %s", out)
	}
	if len(arr) != 3 {
		t.Fatalf("array length changed: %d", len(arr))
	}
	for i, e := range arr {
		env, _ := e["env"].(map[string]any)
		if env == nil {
			t.Fatalf("element %d lost its env map", i)
		}
		if _, still := env[sealKeyEnv]; still {
			t.Errorf("element %d: SANDBOX_SEAL_KEY survived", i)
		}
	}
}

func TestScrubSealKeyFromBody_NoKeyIsByteIdentical(t *testing.T) {
	in := []byte(`{"id":"sb-1","env":{"PATH":"/usr/bin"},"cpu":2,"memory":4}`)
	out := scrubSealKeyFromBody(in)
	if !bytes.Equal(in, out) {
		t.Errorf("body without the key must pass through unchanged\nin:  %s\nout: %s", in, out)
	}
}

func TestScrubSealKeyFromBody_NotJSONPassesThrough(t *testing.T) {
	in := []byte(`SANDBOX_SEAL_KEY mentioned in a plain-text error body`)
	out := scrubSealKeyFromBody(in)
	if !bytes.Equal(in, out) {
		t.Error("non-JSON body must pass through unchanged (cannot parse, cannot scrub)")
	}
}

// Regression: numbers must survive the scrub byte-exact. Wei-scale integers
// exceed float64's exact range (2^53); a naive Unmarshal into any would
// silently corrupt them on re-marshal.
func TestScrubSealKeyFromBody_NumbersRoundTripExactly(t *testing.T) {
	big := "123456789012345678901234567890" // 30 digits, far beyond 2^53
	in := []byte(`{"env":{"SANDBOX_SEAL_KEY":"0x1","amount":` + big + `},"f":1.5,"n":null}`)
	out := scrubSealKeyFromBody(in)

	// Assert on the raw bytes: re-parsing through a plain Unmarshal (no
	// UseNumber) would itself lose precision.
	if !bytes.Contains(out, []byte(`"amount":`+big)) {
		t.Errorf("amount corrupted by scrub:\nwant …%q…\ngot  %s", `"amount":`+big, out)
	}
	if !bytes.Contains(out, []byte(`"f":1.5`)) || !bytes.Contains(out, []byte(`"n":null`)) {
		t.Errorf("other values corrupted: %s", out)
	}
	if bytes.Contains(out, []byte(sealKeyEnv)) {
		t.Errorf("key survived: %s", out)
	}
}

// Deeper "env" keys (not a sandbox object's own env) must be left alone — a
// user service echoing its own JSON through the toolbox owns its bytes. The
// top-level env here carries only innocuous vars, so nothing is a sandbox
// object from the scrubber's perspective and the body must be byte-identical.
func TestScrubSealKeyFromBody_NestedEnvUntouched(t *testing.T) {
	in := []byte(`{"tool":"echo","env":{"PATH":"/x"},"outer":{"env":{"SANDBOX_SEAL_KEY":"0x2"}},"list":[{"env":{"SANDBOX_SEAL_KEY":"0x3"}}]}`)
	out := scrubSealKeyFromBody(in)
	if !bytes.Equal(in, out) {
		t.Errorf("nested env keys must not be touched\nin:  %s\nout: %s", in, out)
	}
}

// ── End-to-end through the reverse proxy ─────────────────────────────────────

// mockDaytonaRawSealed returns a Daytona stand-in that answers EVERY request
// with the raw sealed-sandbox JSON — including env.SANDBOX_SEAL_KEY, exactly
// as Daytona stores it (and as the proxy's typed daytona.Sandbox cannot see).
func mockDaytonaRawSealed(t *testing.T) *httptest.Server {
	t.Helper()
	sealed := `{"id":"sb-sealed","name":"sb-sealed","state":"started",` +
		`"labels":{"daytona-owner":"0xWALLET","0g-sealed":"true"},` +
		`"env":{"SANDBOX_SEAL_KEY":"0x8675309deadbeef","SANDBOX_SEAL_ATTESTATION":"{\"seal_id\":\"abc\"}","PATH":"/usr/bin"},` +
		`"cpu":2,"memory":4}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sealed))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Regression (found live, 2026-09-03): GET /api/sandbox/:id forwarded Daytona's
// sandbox object verbatim, so the owner of a sealed sandbox read its private
// signing key out of env.SANDBOX_SEAL_KEY with one authenticated GET.
func TestForwardedGetSandbox_ScrubsSealKey(t *testing.T) {
	srv := mockDaytonaRawSealed(t)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := newTestEngine(dtona, &mockBilling{}, "0xWALLET")

	req := httptest.NewRequest(http.MethodGet, "/api/sandbox/sb-sealed", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if bytes.Contains(w.Body.Bytes(), []byte(sealKeyEnv)) {
		t.Errorf("SANDBOX_SEAL_KEY leaked through GET /api/sandbox/:id:\n%s", body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("SANDBOX_SEAL_ATTESTATION")) {
		t.Error("SANDBOX_SEAL_ATTESTATION (public) must still be present")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"PATH":"/usr/bin"`)) {
		t.Error("unrelated env vars must survive the scrub")
	}
}

// Same leak on the second path: PUT /api/sandbox/:id/labels echoes the updated
// sandbox object.
func TestForwardedLabelsUpdate_ScrubsSealKey(t *testing.T) {
	srv := mockDaytonaRawSealed(t)
	dtona := daytona.NewClient(srv.URL, "test-key")
	r := newTestEngine(dtona, &mockBilling{}, "0xWALLET")

	req := httptest.NewRequest(http.MethodPut, "/api/sandbox/sb-sealed/labels", bytes.NewReader([]byte(`{"labels":{"probe":"x"}}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte(sealKeyEnv)) {
		t.Errorf("SANDBOX_SEAL_KEY leaked through PUT /api/sandbox/:id/labels:\n%s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("SANDBOX_SEAL_ATTESTATION")) {
		t.Error("SANDBOX_SEAL_ATTESTATION (public) must still be present")
	}
}
