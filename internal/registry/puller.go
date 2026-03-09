package registry

import (
	"context"
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
)

// Copy pulls src image (with optional auth) and pushes it to the internal
// registry as registry:6000/daytona/<targetName>:<targetTag>.
// If username is empty, anonymous auth is used.
func Copy(ctx context.Context, src, targetName, targetTag, username, password string) (string, error) {
	dst := "registry:6000/daytona/" + targetName + ":" + targetTag

	// Validate target tag — Daytona rejects :latest.
	if targetTag == "latest" {
		return "", fmt.Errorf("tag 'latest' is not allowed — use a specific version tag")
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

	if err := crane.Copy(src, dst, opts...); err != nil {
		return "", fmt.Errorf("copy image: %w", err)
	}
	return dst, nil
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
