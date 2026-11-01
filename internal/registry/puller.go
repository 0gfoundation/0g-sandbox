package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// internalImportPrefix is where Copy writes imports.
const internalImportPrefix = "registry:6000/daytona/"

// ErrTagExists refuses an import that would re-point an existing tag.
var ErrTagExists = errors.New("target tag already exists with different content; import under a new tag")

// ErrReservedTarget refuses an import into a reference that Daytona or
// snapshot registration writes.
var ErrReservedTarget = errors.New("target is reserved: repositories named daytona-* hold Daytona's snapshot images and d-* tags are planted by snapshot registration")

// copyMu serialises imports, so two of them cannot both find a tag missing.
var copyMu sync.Mutex

// Copy pulls src image (with optional auth) and pushes it to the internal
// registry as registry:6000/daytona/<targetName>:<targetTag>.
// If username is empty, anonymous auth is used.
//
// Imported tags are immutable. A sealed certificate names the image a snapshot
// resolves to and Daytona starts the copy it made at snapshot creation, so an
// import must not re-point either one: an existing tag is accepted only when
// it already holds the source manifest (the import is then a no-op), and the
// forms Daytona and snapshot registration write (daytona-* repositories, d-*
// tags) are refused.
func Copy(ctx context.Context, src, targetName, targetTag, username, password string) (string, error) {
	return copyTo(ctx, internalImportPrefix, src, targetName, targetTag, username, password)
}

func copyTo(ctx context.Context, prefix, src, targetName, targetTag, username, password string) (string, error) {
	dst := prefix + targetName + ":" + targetTag

	// Validate target tag — Daytona rejects :latest.
	if targetTag == "latest" {
		return "", fmt.Errorf("tag 'latest' is not allowed — use a specific version tag")
	}
	if _, err := name.NewTag(dst, name.Insecure, name.StrictValidation); err != nil {
		return "", fmt.Errorf("invalid target: %w", err)
	}
	if reservedTarget(targetName, targetTag) {
		return "", ErrReservedTarget
	}

	// Validate source reference.
	if _, err := name.ParseReference(src); err != nil {
		return "", fmt.Errorf("invalid source image: %w", err)
	}

	// Use a split keychain: src registry gets provided credentials (or anonymous),
	// dst (internal registry:6000) always gets anonymous auth.
	var srcAuth authn.Authenticator
	if username != "" {
		srcAuth = authn.FromConfig(authn.AuthConfig{Username: username, Password: password})
	} else {
		srcAuth = authn.Anonymous
	}

	opts := []crane.Option{
		crane.WithContext(ctx),
		crane.Insecure, // internal registry:6000 serves plain HTTP
		crane.WithAuthFromKeychain(&splitKeychain{
			srcRef:  src,
			srcAuth: srcAuth,
			dstAnon: authn.Anonymous,
		}),
	}

	copyMu.Lock()
	defer copyMu.Unlock()
	existing, err := crane.Head(dst, opts...)
	if err == nil {
		srcDigest, err := crane.Digest(src, opts...)
		if err != nil {
			return "", fmt.Errorf("get source digest: %w", err)
		}
		if srcDigest != existing.Digest.String() {
			return "", fmt.Errorf("%w: %s", ErrTagExists, dst)
		}
		return dst, nil
	}
	var terr *transport.Error
	if !errors.As(err, &terr) || terr.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("check target %s: %w", dst, err)
	}

	if err := crane.Copy(src, dst, opts...); err != nil {
		return "", fmt.Errorf("copy image: %w", err)
	}
	return dst, nil
}

// reservedTarget reports whether an import would write a reference that
// Daytona (a snapshot's runner image, <project>/daytona-<hash>:daytona) or
// snapshot registration (a <repo>:d-<hex> tag) owns.
func reservedTarget(targetName, targetTag string) bool {
	if strings.HasPrefix(targetTag, "d-") {
		return true
	}
	for _, part := range strings.Split(targetName, "/") {
		if strings.HasPrefix(part, "daytona-") {
			return true
		}
	}
	return false
}

// splitKeychain uses provided credentials for the source registry and
// anonymous auth for the destination (internal registry:6000).
type splitKeychain struct {
	srcRef  string
	srcAuth authn.Authenticator
	dstAnon authn.Authenticator
}

func (k *splitKeychain) Resolve(res authn.Resource) (authn.Authenticator, error) {
	srcRef, err := name.ParseReference(k.srcRef)
	if err != nil {
		return authn.Anonymous, nil
	}
	if res.RegistryStr() == srcRef.Context().RegistryStr() {
		if k.srcRef != "" {
			return k.srcAuth, nil
		}
	}
	return authn.Anonymous, nil
}
