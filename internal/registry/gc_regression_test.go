package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression for bug-report #3: a corrupted /tags/list response must surface as
// an error, not be silently treated as an empty tag list (which would skip every
// orphan "d-" tag in that repo while GC reports success).
func TestListDerivedTags_CorruptTagsList_ReturnsError(t *testing.T) {
	// Healthy baseline: the repo has a real orphan "d-" tag and it is found.
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			w.Write([]byte(`{"repositories":["myrepo"]}`))
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			w.Write([]byte(`{"tags":["d-deadbeef"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer healthy.Close()

	refs, err := ListDerivedTags(context.Background(), healthy.URL)
	if err != nil {
		t.Fatalf("healthy: unexpected error: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("healthy: expected 1 orphan tag, got %v", refs)
	}

	// Corrupt: same repo, truncated JSON. Must return an error so GC fails loudly
	// rather than silently retaining the orphan.
	corrupt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/_catalog":
			w.Write([]byte(`{"repositories":["myrepo"]}`))
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			w.Write([]byte(`{"tags":["d-deadbe`)) // truncated / corrupt
		default:
			http.NotFound(w, r)
		}
	}))
	defer corrupt.Close()

	refs, err = ListDerivedTags(context.Background(), corrupt.URL)
	if err == nil {
		t.Fatalf("corrupt tag list must return an error, got err=nil refs=%v", refs)
	}
	if !strings.Contains(err.Error(), "myrepo") {
		t.Errorf("error should name the failing repository, got: %v", err)
	}
}
