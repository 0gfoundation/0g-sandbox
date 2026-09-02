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

	// Handle sealed flag: convert to label, strip from body (Daytona doesn't know this field).
	if sealed, _ := m["sealed"].(bool); sealed {
		labels[sealedLabel] = "true"
	}
	delete(m, "sealed")

	// Record image reference for TEE attestation.
	if img, _ := m["image"].(string); img != "" {
		labels[imageLabel] = img
	} else if snap, _ := m["snapshot"].(string); snap != "" {
		labels[imageLabel] = "snapshot:" + snap
	}

	m["labels"] = labels

	// public=true: Daytona OIDC is not used in 0G; all sandbox management is
	// controlled via EIP-191 (billing proxy layer). Setting public=true makes
	// user-defined service ports (e.g. 8080, 9090) reachable via the proxy URL
	// without an OIDC session. System ports (22222/TERMINAL, 2280/TOOLBOX,
	// 33333/RECORDING) remain protected by Daytona regardless of this flag.
	m["public"] = true

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
