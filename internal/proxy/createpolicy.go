package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// customResourceFields are the create-request fields that let a caller pick a
// spec directly (image/buildInfo path in Daytona) instead of a fixed snapshot.
// They are rejected: see requireSnapshotCreate.
// Values are lowercase — the caller's key is lowercased before comparison.
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
func requireSnapshotCreate(body []byte) error {
	var m map[string]json.RawMessage
	if len(body) > 0 {
		if err := json.NewDecoder(bytes.NewReader(body)).Decode(&m); err != nil {
			return fmt.Errorf("invalid request body")
		}
	}
	for k := range m {
		lk := strings.ToLower(k)
		for _, banned := range customResourceFields {
			if lk == banned {
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
