package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/0gfoundation/0g-sandbox/internal/daytona"
)

const (
	ownerLabel  = "daytona-owner"
	sealedLabel = "0g-sealed" // immutable once set; blocks SSH and toolbox access
	imageLabel  = "0g-image"  // records image ref for TEE attestation
)

// CheckOwner fetches sandbox metadata and verifies the owner label matches walletAddr.
func CheckOwner(ctx context.Context, dtona *daytona.Client, sandboxID, walletAddr string) error {
	sb, err := dtona.GetSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("get sandbox: %w", err)
	}
	owner, ok := sb.Labels[ownerLabel]
	if !ok || !strings.EqualFold(owner, walletAddr) {
		return fmt.Errorf("forbidden")
	}
	return nil
}

// IsSealedSandbox reports whether a sandbox has the sealed label set.
func IsSealedSandbox(sb *daytona.Sandbox) bool {
	return sb.Labels[sealedLabel] == "true"
}

// InjectOwner sets labels["daytona-owner"] = walletAddr in the request body,
// forces autostop and autoarchive intervals to 0, and handles two additional
// fields that are interpreted by the proxy but not forwarded to Daytona:
//
//   - "sealed": true  → injects label "0g-sealed"="true", blocking SSH and
//     toolbox access for the lifetime of the sandbox.
//   - "image" / "snapshot" → recorded in label "0g-image" for TEE attestation.
func InjectOwner(body []byte, walletAddr string) ([]byte, error) {
	var m map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("unmarshal body: %w", err)
		}
	} else {
		m = make(map[string]any)
	}

	// Inject owner label
	labels, _ := m["labels"].(map[string]any)
	if labels == nil {
		labels = make(map[string]any)
	}
	labels[ownerLabel] = walletAddr

	// Handle sealed flag: convert to label, strip from body (Daytona doesn't know
	// this field). Detection MUST use the same canonical, case-insensitive parse as
	// extractSealed — the value that drives seal-key/attestation injection in
	// handleCreate. A case-sensitive map lookup here (m["sealed"]) previously let a
	// mixed-case {"Sealed":true} inject the seal key while leaving 0g-sealed unset,
	// so SSH and toolbox stayed open on a sealed workload.
	if extractSealed(body) {
		labels[sealedLabel] = "true"
	}
	// Strip every case variant so the field never reaches Daytona.
	for k := range m {
		if strings.EqualFold(k, "sealed") {
			delete(m, k)
		}
	}

	// Strip any caller-supplied volume mounts. Volumes are not a supported feature
	// in 0g-sandbox yet, and the proxy forwards to Daytona as admin, so an
	// unvalidated "volumes" array would let a caller mount another tenant's volume
	// into their own sandbox — no ownership check exists. Deny-by-default until
	// per-volume ownership validation is built (see the admin gate on
	// GET /api/volumes and tracking issue #81).
	for k := range m {
		if strings.EqualFold(k, "volumes") {
			delete(m, k)
		}
	}

	// Record image reference for TEE attestation.
	if img, _ := m["image"].(string); img != "" {
		labels[imageLabel] = img
	} else if snap, _ := m["snapshot"].(string); snap != "" {
		labels[imageLabel] = "snapshot:" + snap
	}

	m["labels"] = labels

	// Port exposure: private by default, public only on explicit opt-in.
	//
	// Marking every sandbox public — the previous behavior — exposed every
	// non-system port, so anyone who could enumerate a sandbox ID could reach an
	// unauthenticated service on any port (e.g. an OpenClaw gateway on :3284).
	// The public flag is now derived, and the caller's own "public" value is
	// ignored so a bare {"public": true} cannot reopen the hole:
	//
	//   - publicPorts given → public; the fork restricts exposure to those ports.
	//   - sealed, no publicPorts → expose only the attested agent proxy on :8080.
	//   - otherwise → private.
	//
	// Sealed detection uses extractSealed — the same case-insensitive parse that
	// drives seal-key injection in handleCreate — so a mixed-case {"Sealed":true}
	// still gets :8080 exposed rather than being treated as private.
	//
	// A first-class way to expose a port after create (opt-in UI / API) is a
	// known gap, tracked as follow-up; for now exposure is chosen at create via
	// publicPorts. System ports (22222/2280/33333) stay protected regardless.
	// Normalize case first: keep the exact-case publicPorts value (validated by
	// ValidatePublicPorts) and delete every other case variant of public /
	// publicPorts, so no caller-supplied casing survives to Daytona where a
	// case-insensitive consumer could resurrect the all-ports hole (#80 fixed
	// the same class for "sealed").
	var pp any
	hasPP := false
	for k, v := range m {
		if strings.EqualFold(k, "publicPorts") {
			if k == "publicPorts" && v != nil {
				pp, hasPP = v, true
			}
			delete(m, k)
		} else if strings.EqualFold(k, "public") {
			delete(m, k)
		}
	}
	if hasPP {
		m["public"] = true
		m["publicPorts"] = pp
	} else if extractSealed(body) {
		m["public"] = true
		m["publicPorts"] = []int{agentPort}
	} else {
		m["public"] = false
	}

	// autoStopInterval=0: disable Daytona's autostop; billing proxy owns shutdown.
	// autoArchiveInterval=60: fallback safety net — if billing proxy crashes and
	// fails to archive the sandbox, Daytona archives it after 60 minutes so it
	// does not occupy runner resources indefinitely.
	m["autoStopInterval"] = 0
	m["autoArchiveInterval"] = 60

	return json.Marshal(m)
}

// MergeProtectedLabels rewrites a PUT /labels payload so the protected labels
// (daytona-owner — ownership; 0g-sealed — sealed flag) always carry the
// sandbox's CURRENT values, regardless of what the caller sent.
//
// Two Daytona facts force this shape:
//   - the body nests the map under "labels" (SandboxLabelsDto:
//     {"labels": {"k": "v"}}), so a top-level strip never touches the real
//     payload;
//   - replaceLabels is a wholesale REPLACE, not a merge — so merely stripping
//     the protected keys from the payload would make every successful update
//     DELETE them on the sandbox: ownership gone (bricked management), sealed
//     flag gone (SSH/toolbox reopen on a sealed sandbox → SANDBOX_SEAL_KEY
//     exfiltration). The protected keys must be re-injected from the live
//     sandbox, not just removed from the payload.
//
// The caller keeps full replace semantics over every non-protected label
// (including deletion by omission). Case variants of the protected keys are
// dropped from the payload before re-injection.
func MergeProtectedLabels(body []byte, current map[string]string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	protected := func(k string) bool {
		return strings.EqualFold(k, ownerLabel) || strings.EqualFold(k, sealedLabel)
	}
	for k := range m {
		if protected(k) {
			delete(m, k)
		}
	}
	labels, _ := m["labels"].(map[string]any)
	if labels == nil {
		labels = make(map[string]any)
	}
	for k := range labels {
		if protected(k) {
			delete(labels, k)
		}
	}
	// Re-inject the live values so the upstream replace cannot drop them.
	if v, ok := current[ownerLabel]; ok {
		labels[ownerLabel] = v
	}
	if v, ok := current[sealedLabel]; ok {
		labels[sealedLabel] = v
	}
	m["labels"] = labels
	return json.Marshal(m)
}
