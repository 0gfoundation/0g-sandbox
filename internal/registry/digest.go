package registry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
)

// ErrTagSharesManifest signals that a DeleteTag candidate cannot be removed
// without collateral damage: another tag in the same repo points at the same
// manifest, and Docker Registry v2 only deletes manifests (taking every tag
// pointing at them with it). Callers should treat this as a soft skip, not a
// failure.
var ErrTagSharesManifest = errors.New("tag shares manifest with another tag")

// PinRef rewrites imageRef to its digest-pinned form "repo@<digest>" —
// content-addressed, so the reference cannot be re-pointed after attestation.
// The tag (mutable) is dropped; a ref that is already digest-pinned rewrites
// to the same value. Sealed creates forward THIS form to Daytona so the image
// the runner pulls is byte-identical to the one the TEE attested.
func PinRef(imageRef, digest string) (string, error) {
	ref, err := name.ParseReference(imageRef, name.Insecure)
	if err != nil {
		return "", fmt.Errorf("parse image ref %q: %w", imageRef, err)
	}
	return ref.Context().Name() + "@" + digest, nil
}

// GetDigest fetches the content digest (e.g. "sha256:abc...") for imageRef.
//
// Registries whose host starts with "registry:" or "localhost:" are contacted
// over plain HTTP (matching the internal registry:6000 setup). All others use
// the default HTTPS transport.
//
// For sealed containers this must succeed before sandbox creation proceeds;
// callers should treat an error as a hard failure.
func GetDigest(ctx context.Context, imageRef string) (string, error) {
	opts := []crane.Option{
		crane.WithContext(ctx),
		crane.WithAuth(authn.Anonymous),
	}

	// name.Insecure is needed for ParseReference when the registry host
	// does not have a TLD (e.g. "registry:6000/...").
	ref, err := name.ParseReference(imageRef, name.Insecure)
	if err != nil {
		return "", fmt.Errorf("parse image ref %q: %w", imageRef, err)
	}

	if isInternalRegistry(ref.Context().RegistryStr()) {
		opts = append(opts, crane.Insecure)
	}

	digest, err := crane.Digest(imageRef, opts...)
	if err != nil {
		return "", fmt.Errorf("get digest for %q: %w", imageRef, err)
	}
	return digest, nil
}

// TagByDigest resolves imageRef to its content digest and creates a derived
// tag "<repo>:d-<first16hex-of-digest>" in the same registry pointing at the
// same manifest. Returns the full derived-tag reference.
//
// Snapshot registration uses this to give Daytona a stable tag-form imageName
// whose tag rotates whenever the source content changes: re-pushing the same
// base tag with new content yields a new digest, therefore a new derived tag,
// therefore a fresh Daytona-side cache key on the runner — avoiding stale
// wrapped-image cache hits.
//
// Digest-form refs ("<repo>@sha256:...") would be more natural but Daytona
// rejects them ("invalid reference format"), so we stay in tag-space.
func TagByDigest(ctx context.Context, imageRef string) (string, error) {
	digest, err := GetDigest(ctx, imageRef)
	if err != nil {
		return "", err
	}
	ref, err := name.ParseReference(imageRef, name.Insecure)
	if err != nil {
		return "", fmt.Errorf("parse image ref %q: %w", imageRef, err)
	}
	short := strings.TrimPrefix(digest, "sha256:")
	if len(short) < 16 {
		return "", fmt.Errorf("digest too short: %q", digest)
	}
	newTag := "d-" + short[:16]
	newRef := ref.Context().Name() + ":" + newTag

	opts := []crane.Option{crane.WithContext(ctx), crane.WithAuth(authn.Anonymous)}
	if isInternalRegistry(ref.Context().RegistryStr()) {
		opts = append(opts, crane.Insecure)
	}
	if err := crane.Tag(imageRef, newTag, opts...); err != nil {
		return "", fmt.Errorf("tag %q as %q: %w", imageRef, newRef, err)
	}
	return newRef, nil
}

func isInternalRegistry(host string) bool {
	return strings.HasPrefix(host, "registry:") || strings.HasPrefix(host, "localhost:")
}

// DeleteTag removes a tag from the registry. The underlying blobs remain
// until the registry's garbage collector runs.
//
// Docker Registry v2 only supports DELETE by digest, not by tag — sending the
// tag form returns DIGEST_INVALID. We resolve the tag to a digest first, then
// DELETE by digest. Because that operation removes the whole manifest, every
// tag pointing at it disappears, so we first verify no sibling tag in the
// same repo shares the digest. If one does, returns ErrTagSharesManifest.
//
// Used by snapshot deletion to clean up the derived "<repo>:d-<shortdigest>"
// tag that handleSnapshotCreate planted via TagByDigest.
func DeleteTag(ctx context.Context, imageRef string) error {
	ref, err := name.ParseReference(imageRef, name.Insecure)
	if err != nil {
		return fmt.Errorf("parse image ref %q: %w", imageRef, err)
	}
	tagged, ok := ref.(name.Tag)
	if !ok {
		return fmt.Errorf("ref %q is not a tag form", imageRef)
	}
	opts := []crane.Option{crane.WithContext(ctx), crane.WithAuth(authn.Anonymous)}
	if isInternalRegistry(ref.Context().RegistryStr()) {
		opts = append(opts, crane.Insecure)
	}

	targetDigest, err := crane.Digest(imageRef, opts...)
	if err != nil {
		return fmt.Errorf("get digest for %q: %w", imageRef, err)
	}

	repoName := tagged.Context().Name()
	tags, err := crane.ListTags(repoName, opts...)
	if err != nil {
		return fmt.Errorf("list tags for %q: %w", repoName, err)
	}
	for _, sibling := range tags {
		if sibling == tagged.TagStr() {
			continue
		}
		siblingRef := repoName + ":" + sibling
		siblingDigest, err := crane.Digest(siblingRef, opts...)
		if err != nil {
			// Conservative: if we can't probe a sibling, refuse the delete
			// rather than risk taking down a tag we couldn't inspect.
			return fmt.Errorf("probe sibling %q: %w", siblingRef, err)
		}
		if siblingDigest == targetDigest {
			return fmt.Errorf("%w: %q == %q at %s", ErrTagSharesManifest, imageRef, siblingRef, targetDigest)
		}
	}

	digestRef := repoName + "@" + targetDigest
	if err := crane.Delete(digestRef, opts...); err != nil {
		return fmt.Errorf("delete %q: %w", digestRef, err)
	}
	return nil
}

// IsDerivedTag reports whether imageRef ends in a ":d-<hex>" tag produced by
// TagByDigest. Snapshot cleanup only removes derived tags — caller-supplied
// tags are left intact in case other workloads still reference them.
func IsDerivedTag(imageRef string) bool {
	ref, err := name.ParseReference(imageRef, name.Insecure)
	if err != nil {
		return false
	}
	tagged, ok := ref.(name.Tag)
	if !ok {
		return false
	}
	return strings.HasPrefix(tagged.TagStr(), "d-")
}
