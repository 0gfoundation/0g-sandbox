package daytona

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// ── Unit tests (httptest, no external deps) ───────────────────────────────────

func mockServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// ── GetSandbox ────────────────────────────────────────────────────────────────

func TestGetSandbox_OK(t *testing.T) {
	want := Sandbox{ID: "sb-unit-1", State: "running", Labels: map[string]string{"env": "test"}}
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want)
	})

	c := NewClient(srv.URL, "test-key")
	got, err := c.GetSandbox(context.Background(), "sb-unit-1")
	if err != nil {
		t.Fatalf("GetSandbox: %v", err)
	}
	if got.ID != want.ID {
		t.Errorf("ID: got %q want %q", got.ID, want.ID)
	}
	if got.State != want.State {
		t.Errorf("State: got %q want %q", got.State, want.State)
	}
	if got.Labels["env"] != "test" {
		t.Errorf("Labels[env]: got %q want %q", got.Labels["env"], "test")
	}
}

func TestGetSandbox_NotFound(t *testing.T) {
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c := NewClient(srv.URL, "key")
	_, err := c.GetSandbox(context.Background(), "sb-missing")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

func TestGetSandbox_SetsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(Sandbox{ID: "x"})
	})

	c := NewClient(srv.URL, "super-secret")
	c.GetSandbox(context.Background(), "x") //nolint:errcheck

	if gotAuth != "Bearer super-secret" {
		t.Errorf("Authorization: got %q want %q", gotAuth, "Bearer super-secret")
	}
}

func TestGetSandbox_URLPath(t *testing.T) {
	var gotPath string
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(Sandbox{ID: "sb-path"})
	})

	c := NewClient(srv.URL, "k")
	c.GetSandbox(context.Background(), "sb-path") //nolint:errcheck

	if gotPath != "/api/sandbox/sb-path" {
		t.Errorf("path: got %q want %q", gotPath, "/api/sandbox/sb-path")
	}
}

// ── ListSandboxes ─────────────────────────────────────────────────────────────

func TestListSandboxes_OK(t *testing.T) {
	sandboxes := []Sandbox{
		{ID: "sb-1", State: "running"},
		{ID: "sb-2", State: "stopped"},
	}
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sandboxes)
	})

	c := NewClient(srv.URL, "key")
	got, err := c.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("length: got %d want 2", len(got))
	}
	if got[0].ID != "sb-1" || got[1].ID != "sb-2" {
		t.Errorf("sandbox IDs: got %v", got)
	}
}

func TestListSandboxes_Empty(t *testing.T) {
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	})

	c := NewClient(srv.URL, "key")
	got, err := c.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d items", len(got))
	}
}

func TestListSandboxes_NonOK_ReturnsError(t *testing.T) {
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	c := NewClient(srv.URL, "key")
	_, err := c.ListSandboxes(context.Background())
	if err == nil {
		t.Fatal("expected error for non-200, got nil")
	}
}

func TestListSandboxes_SetsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte("[]"))
	})

	c := NewClient(srv.URL, "list-key")
	c.ListSandboxes(context.Background()) //nolint:errcheck

	if gotAuth != "Bearer list-key" {
		t.Errorf("Authorization: got %q want %q", gotAuth, "Bearer list-key")
	}
}

// ── ListSnapshots ─────────────────────────────────────────────────────────────

func TestListSnapshots_WalksAllPages(t *testing.T) {
	var gotPages []string
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPages = append(gotPages, r.URL.Query().Get("page"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			w.Write([]byte(`{"items":[{"id":"s1"},{"id":"s2"}],"total":3,"page":1,"totalPages":2}`))
		case "2":
			w.Write([]byte(`{"items":[{"id":"s3"}],"total":3,"page":2,"totalPages":2}`))
		default:
			t.Errorf("unexpected page request: %q", r.URL.Query().Get("page"))
		}
	})

	c := NewClient(srv.URL, "key")
	got, err := c.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("length: got %d want 3 (pagination truncated?)", len(got))
	}
	if got[0].ID != "s1" || got[2].ID != "s3" {
		t.Errorf("snapshot IDs: got %v", got)
	}
	if len(gotPages) != 2 || gotPages[0] != "1" || gotPages[1] != "2" {
		t.Errorf("expected page requests [1 2], got %v", gotPages)
	}
}

func TestListSnapshots_SinglePage(t *testing.T) {
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":[{"id":"only"}],"total":1,"page":1,"totalPages":1}`))
	})

	c := NewClient(srv.URL, "key")
	got, err := c.ListSnapshots(context.Background())
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(got) != 1 || got[0].ID != "only" {
		t.Errorf("got %v want single snapshot 'only'", got)
	}
}

func TestListSnapshots_NonOK_ReturnsError(t *testing.T) {
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	c := NewClient(srv.URL, "key")
	if _, err := c.ListSnapshots(context.Background()); err == nil {
		t.Fatal("expected error for non-200, got nil")
	}
}

// ── StopSandbox ───────────────────────────────────────────────────────────────

func TestStopSandbox_OK(t *testing.T) {
	var gotMethod, gotPath string
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})

	c := NewClient(srv.URL, "key")
	if err := c.StopSandbox(context.Background(), "sb-stop"); err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q want POST", gotMethod)
	}
	if gotPath != "/api/sandbox/sb-stop/stop" {
		t.Errorf("path: got %q want /api/sandbox/sb-stop/stop", gotPath)
	}
}

func TestStopSandbox_Error(t *testing.T) {
	srv := mockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	c := NewClient(srv.URL, "key")
	if err := c.StopSandbox(context.Background(), "sb-gone"); err == nil {
		t.Fatal("expected error for 404, got nil")
	}
}

// ── Accessors ─────────────────────────────────────────────────────────────────

func TestBaseURL(t *testing.T) {
	c := NewClient("http://example.com", "k")
	if c.BaseURL() != "http://example.com" {
		t.Errorf("BaseURL: got %q", c.BaseURL())
	}
}

func TestAdminKey(t *testing.T) {
	c := NewClient("http://x", "my-admin-key")
	if c.AdminKey() != "my-admin-key" {
		t.Errorf("AdminKey: got %q", c.AdminKey())
	}
}

// ── Integration tests (real Daytona at localhost:3000) ────────────────────────
//
// These run only when Daytona is reachable. They never mutate state (no create/delete).

func realClient(t *testing.T) *Client {
	t.Helper()
	baseURL := os.Getenv("DAYTONA_API_URL")
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}
	adminKey := os.Getenv("DAYTONA_ADMIN_KEY")
	if adminKey == "" {
		adminKey = "daytona_admin_key"
	}

	// Check reachability
	resp, err := http.Get(baseURL + "/api/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skipf("Daytona not reachable at %s, skipping integration test", baseURL)
	}
	resp.Body.Close()

	return NewClient(baseURL, adminKey)
}

func TestIntegration_ListSandboxes_AuthAccepted(t *testing.T) {
	c := realClient(t)
	sandboxes, err := c.ListSandboxes(context.Background())
	if err != nil {
		t.Fatalf("ListSandboxes against real Daytona: %v", err)
	}
	t.Logf("real Daytona returned %d sandbox(es)", len(sandboxes))
}

func TestIntegration_GetSandbox_NotFound(t *testing.T) {
	c := realClient(t)
	_, err := c.GetSandbox(context.Background(), "sb-definitely-does-not-exist-xyzxyz")
	if err == nil {
		t.Fatal("expected error for non-existent sandbox, got nil")
	}
	t.Logf("got expected error: %v", err)
}

func TestIntegration_WrongKey_Unauthorized(t *testing.T) {
	baseURL := "http://localhost:3000"
	resp, err := http.Get(baseURL + "/api/health")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Skipf("Daytona not reachable, skipping")
	}
	resp.Body.Close()

	c := NewClient(baseURL, "wrong-key-intentionally")
	_, err = c.ListSandboxes(context.Background())
	if err == nil {
		t.Fatal("expected error for wrong API key, got nil")
	}
	t.Logf("got expected auth error: %v", err)
}
