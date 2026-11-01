package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// testRegistry serves an in-memory registry and returns its host.
func testRegistry(t *testing.T, wrap func(http.Handler) http.Handler) string {
	t.Helper()
	h := ggcrregistry.New()
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return strings.Replace(strings.TrimPrefix(srv.URL, "http://"), "127.0.0.1", "localhost", 1)
}

func pushRandom(t *testing.T, ref string) string {
	t.Helper()
	img, err := random.Image(64, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := crane.Push(img, ref, crane.Insecure); err != nil {
		t.Fatal(err)
	}
	d, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d.String()
}

// An import never re-points an existing tag: the same manifest again is a
// no-op, any other is refused and the tag keeps its manifest.
func TestCopyNeverRepointsATag(t *testing.T) {
	ctx := context.Background()
	host := testRegistry(t, nil)
	prefix := host + "/daytona/"
	genuine := pushRandom(t, host+"/src/genuine:1")
	pushRandom(t, host+"/src/other:1")

	dst, err := copyTo(ctx, prefix, host+"/src/genuine:1", "0g-sealed", "v1", "", "")
	if err != nil || dst != prefix+"0g-sealed:v1" {
		t.Fatalf("first import: %q %v", dst, err)
	}
	if _, err := copyTo(ctx, prefix, host+"/src/genuine:1", "0g-sealed", "v1", "", ""); err != nil {
		t.Fatalf("same manifest again: %v", err)
	}
	if _, err := copyTo(ctx, prefix, host+"/src/other:1", "0g-sealed", "v1", "", ""); !errors.Is(err, ErrTagExists) {
		t.Fatalf("re-point: got %v, want ErrTagExists", err)
	}
	if got, _ := crane.Digest(prefix+"0g-sealed:v1", crane.Insecure); got != genuine {
		t.Fatalf("tag now %s, want %s", got, genuine)
	}
}

// Daytona's snapshot runner images (daytona-* repositories) and the derived
// d-* tags of snapshot registration are never written by an import.
func TestCopyRefusesReservedTargets(t *testing.T) {
	ctx := context.Background()
	host := testRegistry(t, nil)
	pushRandom(t, host+"/src/genuine:1")
	for _, tc := range []struct{ name, tag string }{
		{"0g-sealed", "d-0123456789abcdef"},
		{"daytona-" + strings.Repeat("a", 64), "daytona"},
		{"team/daytona-x", "v1"},
	} {
		if _, err := copyTo(ctx, host+"/daytona/", host+"/src/genuine:1", tc.name, tc.tag, "", ""); !errors.Is(err, ErrReservedTarget) {
			t.Errorf("%s:%s: got %v, want ErrReservedTarget", tc.name, tc.tag, err)
		}
	}
	for _, tc := range []struct{ name, tag string }{{"0g-sealed", "latest"}, {"Bad", "v1"}, {"x", "a:b"}} {
		if _, err := copyTo(ctx, host+"/daytona/", host+"/src/genuine:1", tc.name, tc.tag, "", ""); err == nil {
			t.Errorf("%s:%s: accepted", tc.name, tc.tag)
		}
	}
	if tags, err := crane.ListTags(host+"/daytona/0g-sealed", crane.Insecure); err == nil && len(tags) > 0 {
		t.Fatalf("refused imports wrote %v", tags)
	}
}

// When the registry cannot say whether the tag exists, nothing is written.
func TestCopyFailsClosedWhenTargetUnknown(t *testing.T) {
	var puts int
	host := testRegistry(t, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/v2/daytona/") {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if r.Method != http.MethodGet {
					puts++
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	pushRandom(t, host+"/src/genuine:1")
	if _, err := copyTo(context.Background(), host+"/daytona/", host+"/src/genuine:1", "0g-sealed", "v1", "", ""); err == nil {
		t.Fatal("import proceeded without knowing the target")
	}
	if puts != 0 {
		t.Fatalf("%d writes to the target", puts)
	}
}

// GetConfig names the image config a runner starts: for an index, the
// linux/amd64 image's. A runner's re-push of that image (a new manifest
// digest) keeps the config digest.
func TestGetConfig(t *testing.T) {
	ctx := context.Background()
	host := testRegistry(t, nil)
	amd64, _ := random.Image(64, 1)
	arm64, _ := random.Image(64, 1)
	idx := mutate.AppendManifests(empty.Index,
		mutate.IndexAddendum{Add: arm64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
		mutate.IndexAddendum{Add: amd64, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
	)
	if err := crane.Push(amd64, host+"/runner/daytona-x:daytona", crane.Insecure); err != nil {
		t.Fatal(err)
	}
	ref := host + "/sealed/app:v1"
	if err := pushIndex(idx, ref); err != nil {
		t.Fatal(err)
	}
	want, _ := amd64.ConfigName()
	for _, r := range []string{ref, host + "/runner/daytona-x:daytona"} {
		got, err := GetConfig(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if got.Digest != want.String() {
			t.Errorf("%s: config %s, want %s", r, got.Digest, want)
		}
	}
	indexDigest, _ := idx.Digest()
	if d, _ := GetDigest(ctx, ref); d != indexDigest.String() {
		t.Fatalf("GetDigest %s, want the index digest %s", d, indexDigest)
	}
}

func pushIndex(idx v1.ImageIndex, ref string) error {
	r, err := name.ParseReference(ref, name.Insecure)
	if err != nil {
		return err
	}
	return remote.WriteIndex(r, idx)
}
