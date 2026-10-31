package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// customResourceFields are the create-request fields that let a caller pick a
// spec directly (image/buildInfo path in Daytona) instead of a fixed snapshot.
// They are rejected: see requireSnapshotCreate.
// Keys are compared with strings.EqualFold, the folding Go's struct decode uses.
// class and buildInfo are spec selectors too (class picks a Daytona size tier;
// buildInfo is the declarative-image path). Banning the full set — not five of
// seven — is what makes "billed spec == provisioned spec by construction" hold.
var customResourceFields = []string{"cpu", "memory", "disk", "gpu", "image", "class", "buildinfo"}

// requireSnapshotCreate enforces the snapshot-only create policy. A create must
// name a snapshot and MUST NOT carry any custom-resource field.
//
// Why: the billing gate derives the spec (cpu/memory) it charges for from the
// request, while Daytona provisions from the SAME request under different
// parsing rules — Go's case-insensitive last-wins struct decode vs Daytona's
// case-sensitive DTO. That divergence let {"cpu":2,"CPU":1} be billed as 1 core
// while Daytona ran 2 (#73), and a bare image create billed 0 compute while
// Daytona ran its default spec (#77). Restricting creates to named snapshots
// removes the custom-spec entry entirely: cpu/memory always come from the
// snapshot's own record (h.dtona.GetSnapshot), so the billed spec is exactly
// the provisioned spec by construction. Custom specs are not a supported
// feature yet; add per-field billing reconciliation before re-enabling them.
//
// Case-insensitive matching mirrors #80's lesson: a case-variant field
// ({"CPU":2}) is invisible to a lowercase-only check but honored downstream.
//
// Field names must also be unambiguous: see rejectFoldedKeys.
func requireSnapshotCreate(body []byte) error {
	var m map[string]json.RawMessage
	if len(body) > 0 {
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&m); err != nil {
			return fmt.Errorf("invalid request body")
		}
	}
	if err := rejectFoldedKeys(m); err != nil {
		return err
	}
	for k := range m {
		for _, banned := range customResourceFields {
			if strings.EqualFold(k, banned) {
				return fmt.Errorf("custom sandbox resources are not supported; create from a snapshot instead (offending field: %q)", k)
			}
		}
	}
	// snapshot must be present and a non-empty string.
	snap, ok := m["snapshot"]
	if !ok {
		return fmt.Errorf("a snapshot is required to create a sandbox")
	}
	var name string
	if err := json.Unmarshal(snap, &name); err != nil || strings.TrimSpace(name) == "" {
		return fmt.Errorf("a snapshot is required to create a sandbox")
	}
	return nil
}

// rejectFoldedKeys refuses a create body whose field names differ only by case.
//
// Go's struct decode matches a key by Unicode case folding ("Snapshot", and
// "ſnapshot" with U+017F) and keeps the last match, while Daytona reads only
// the exact key. A body with both "snapshot" and a folded variant therefore
// meant one snapshot to the proxy and another to Daytona: the certificate
// named one image while Daytona started a different one. The proxy now reads
// create fields by their exact names; this check also makes any remaining
// folding read see the same value Daytona does. A folded spelling of
// "snapshot" is refused even on its own, since Daytona would ignore it.
func rejectFoldedKeys(m map[string]json.RawMessage) error {
	seen := make(map[string]string, len(m))
	for k := range m {
		if strings.EqualFold(k, "snapshot") && k != "snapshot" {
			return fmt.Errorf("field %q must be spelled \"snapshot\"", k)
		}
		f := foldName(k)
		if other, dup := seen[f]; dup {
			pair := []string{other, k}
			sort.Strings(pair)
			return fmt.Errorf("field names %q and %q differ only by case", pair[0], pair[1])
		}
		seen[f] = k
	}
	return nil
}

// foldName maps each rune to the smallest rune of its case-folding orbit, so
// foldName(a) == foldName(b) exactly when strings.EqualFold(a, b).
func foldName(s string) string {
	return strings.Map(func(r rune) rune {
		for {
			f := unicode.SimpleFold(r)
			if f <= r {
				return f
			}
			r = f
		}
	}, s)
}
