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
	sealed, _ := m["sealed"].(bool)
	if sealed {
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
	// A first-class way to expose a port after create (opt-in UI / API) is a
	// known gap, tracked as follow-up; for now exposure is chosen at create via
	// publicPorts. System ports (22222/2280/33333) stay protected regardless.
	if pp, ok := m["publicPorts"]; ok && pp != nil {
		m["public"] = true
	} else if sealed {
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

// StripOwnerLabel removes protected labels from a label-update payload.
// Users may not overwrite daytona-owner (ownership) or 0g-sealed (sealed flag).
func StripOwnerLabel(body []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	delete(m, ownerLabel)
	delete(m, sealedLabel) // sealed is immutable once set
	return json.Marshal(m)
}
