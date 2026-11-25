package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

// This mock models an upstream that accepts a trailing slash as the same
// operation. Authorization must hold regardless of upstream normalization.
type slashDaytona struct {
	mu        sync.Mutex
	labels    map[string]string
	mutations []string
}

func newSlashDaytona(t *testing.T, sealed bool) (*httptest.Server, *slashDaytona) {
	t.Helper()
	state := &slashDaytona{labels: map[string]string{ownerLabel: "0xOWNER", sealedLabel: "false"}}
	if sealed {
		state.labels[sealedLabel] = "true"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		p := strings.TrimRight(r.URL.Path, "/")
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || p != "/api/sandbox/sb-slash" {
			state.mutations = append(state.mutations, r.Method+" "+r.URL.Path)
		}
		if r.Method == http.MethodPut && p == "/api/sandbox/sb-slash/labels" {
			var body struct {
				Labels map[string]string `json:"labels"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			state.labels = body.Labels
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "sb-slash", "labels": state.labels,
			"env": map[string]string{sealKeyEnv: "mock-private-key"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func TestTrailingSlash_LabelsCannotBypassProtectedLabels(t *testing.T) {
	for _, suffix := range []string{"/labels", "/labels/"} {
		t.Run(suffix, func(t *testing.T) {
			srv, state := newSlashDaytona(t, false)
			r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
			w := httptest.NewRecorder()
			// Replacing labels must neither transfer ownership nor invent a seal
			// without passing through the sealed create/attestation path.
			body := `{"labels":{"daytona-owner":"0xOTHER","0g-sealed":"true","user-label":"ok"}}`
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/sandbox/sb-slash"+suffix, strings.NewReader(body)))
			if suffix == "/labels" && w.Code != http.StatusOK {
				t.Fatalf("canonical label update failed: %d %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if state.labels[ownerLabel] != "0xOWNER" || state.labels[sealedLabel] != "false" {
				t.Errorf("protected labels changed through %s: %v (HTTP %d)", suffix, state.labels, w.Code)
			}
			if suffix != "/labels" && len(state.mutations) != 0 {
				t.Errorf("noncanonical label mutation reached admin upstream: %v", state.mutations)
			}
		})
	}
}

func TestTrailingSlash_LifecycleCannotSkipBillingAndPolicy(t *testing.T) {
	for _, action := range []string{"start", "stop", "archive", "ensure-billing", "autostop", "autoarchive", "ssh-access", "force", "force-stop", "labels"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			t.Run(method+"/"+action, func(t *testing.T) {
				srv, state := newSlashDaytona(t, false)
				mb := &mockBilling{}
				r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), mb, "0xOWNER")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(method, "/api/sandbox/sb-slash/"+action+"/", nil))
				if w.Code != http.StatusBadRequest {
					t.Errorf("noncanonical control request must return 400, got %d: %s", w.Code, w.Body.String())
				}
				state.mu.Lock()
				defer state.mu.Unlock()
				if len(state.mutations) != 0 {
					t.Errorf("lifecycle operation bypassed proxy policy/hooks: %v", state.mutations)
				}
			})
		}
	}
}

// A slash after the sandbox ID also reaches the catch-all. In particular,
// DELETE must not bypass handleDelete and leave an orphan billing session.
func TestTrailingSlash_RootResourceCannotBypassDeleteHook(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			srv, state := newSlashDaytona(t, false)
			r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/api/sandbox/sb-slash/", nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("sandbox root alias: want 400, got %d: %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if len(state.mutations) != 0 {
				t.Errorf("sandbox root alias reached upstream: %v", state.mutations)
			}
		})
	}
}

// Trailing slashes can be meaningful to transparent upstream APIs. Limit the
// rejection to managed control paths, leaving toolbox paths intact. (The
// sandbox passthrough forwards only listed actions, as spelled:
// passthrough_test.go.)
func TestTrailingSlash_TransparentPathsStillForwardUnchanged(t *testing.T) {
	for _, target := range []string{
		"/api/toolbox/sb-slash/toolbox/files/",
	} {
		t.Run(target, func(t *testing.T) {
			srv, state := newSlashDaytona(t, false)
			r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("transparent path: want 200, got %d: %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if len(state.mutations) != 1 || state.mutations[0] != "GET "+target {
				t.Errorf("transparent path was changed or not forwarded: %v", state.mutations)
			}
		})
	}
}

// Daytona's router also ignores case, so a case variant is the same operation
// as the canonical action, with or without a trailing slash.
func TestCaseVariant_ControlCannotSkipBillingAndPolicy(t *testing.T) {
	for _, action := range []string{"START", "Start", "Start/", "Stop", "ARCHIVE/", "Ensure-Billing", "SSH-Access", "FORCE", "Force-Stop", "Labels", "LABELS/", "AutoStop", "AUTOARCHIVE/"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			t.Run(method+"/"+action, func(t *testing.T) {
				srv, state := newSlashDaytona(t, false)
				r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(method, "/api/sandbox/sb-slash/"+action, nil))
				if w.Code != http.StatusBadRequest {
					t.Errorf("case variant must return 400, got %d: %s", w.Code, w.Body.String())
				}
				state.mu.Lock()
				defer state.mu.Unlock()
				if len(state.mutations) != 0 {
					t.Errorf("case variant reached upstream: %v", state.mutations)
				}
			})
		}
	}
}

// Daytona takes the autostop and autoarchive values as a path segment
// (/autostop/:interval), so the block must cover what follows the action name.
func TestBlockedActions_IntervalPathsCannotReachUpstream(t *testing.T) {
	for _, action := range []string{"autostop/0", "autostop/30/", "AutoStop/15", "autoarchive/60", "AUTOARCHIVE/0"} {
		t.Run(action, func(t *testing.T) {
			srv, state := newSlashDaytona(t, false)
			r := newTestEngine(daytona.NewClient(srv.URL, "mock-admin-key"), &mockBilling{}, "0xOWNER")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/sandbox/sb-slash/"+action, nil))
			if w.Code != http.StatusForbidden {
				t.Errorf("want 403, got %d: %s", w.Code, w.Body.String())
			}
			state.mu.Lock()
			defer state.mu.Unlock()
			if len(state.mutations) != 0 {
				t.Errorf("blocked setting reached upstream: %v", state.mutations)
			}
		})
	}
}

func TestFirstSegment(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "/", "/autostop": "/autostop", "/autostop/30": "/autostop", "/a/b/c": "/a"} {
		if got := firstSegment(in); got != want {
			t.Errorf("firstSegment(%q) = %q, want %q", in, got, want)
		}
	}
}
