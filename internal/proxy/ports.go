package proxy

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
)

// reservedPreviewPorts are Daytona system ports that always require auth at
// the preview proxy; publicPorts may never include them.
var reservedPreviewPorts = map[int]bool{22222: true, 2280: true, 33333: true}

const maxPublicPorts = 16

// agentPort is the agent-fronting proxy port sealed containers serve on; a
// sealed sandbox restricted with publicPorts must keep it reachable.
const agentPort = 8080

// ValidatePublicPorts checks the optional publicPorts field of a create
// request. Full normalization (dedupe/sort) happens in the patched Daytona
// API; this boundary check exists to return clear 400s and to enforce the
// sealed rule, which Daytona knows nothing about. Returns nil when the field
// is absent.
func ValidatePublicPorts(body []byte, sealed bool) error {
	ports, present, err := parsePublicPorts(body)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if len(ports) == 0 {
		return fmt.Errorf("publicPorts must not be empty; omit it to keep every user port private")
	}
	if len(ports) > maxPublicPorts {
		return fmt.Errorf("publicPorts allows at most %d entries", maxPublicPorts)
	}
	hasAgentPort := false
	for _, p := range ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("publicPorts entry %d out of range 1-65535", p)
		}
		if reservedPreviewPorts[p] {
			return fmt.Errorf("publicPorts entry %d is a reserved system port", p)
		}
		if p == agentPort {
			hasAgentPort = true
		}
	}
	if sealed && !hasAgentPort {
		return fmt.Errorf("sealed sandboxes with publicPorts must include port %d (the agent-fronting proxy)", agentPort)
	}
	return nil
}

// parsePublicPorts extracts publicPorts from a JSON body. present is false
// when the field is absent or null. Rejects non-array values and non-integer
// elements.
func parsePublicPorts(body []byte) (ports []int, present bool, err error) {
	if len(body) == 0 {
		return nil, false, nil
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, false, fmt.Errorf("invalid request body")
	}
	raw, ok := m["publicPorts"]
	if !ok || raw == nil {
		return nil, false, nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, false, fmt.Errorf("publicPorts must be an array of integers")
	}
	for _, v := range arr {
		f, ok := v.(float64)
		if !ok || f != math.Trunc(f) {
			return nil, false, fmt.Errorf("publicPorts must be an array of integers")
		}
		ports = append(ports, int(f))
	}
	return ports, true, nil
}

// decoratePublicPorts post-processes a 2xx create response for a request
// that asked for publicPorts. supported is false when the response carries
// no publicPorts field — the tell that the Daytona backend silently dropped
// the restriction (stock image, whitelist validation), in which case the
// caller must fail the create rather than hand out an unrestricted sandbox.
// When supported, a preview_urls map is attached so callers get ready-to-use
// URLs for each opened port (skipped when PROXY_DOMAIN is unset).
func decoratePublicPorts(respBytes []byte) (out []byte, supported bool, err error) {
	var m map[string]any
	if err := json.Unmarshal(respBytes, &m); err != nil {
		return nil, false, err
	}
	rawPorts, ok := m["publicPorts"].([]any)
	if !ok || len(rawPorts) == 0 {
		return respBytes, false, nil
	}

	domain := os.Getenv("PROXY_DOMAIN")
	id, _ := m["id"].(string)
	if domain != "" && id != "" {
		scheme := os.Getenv("PROXY_PROTOCOL")
		if scheme == "" {
			scheme = "http"
		}
		urls := make(map[string]string, len(rawPorts))
		for _, v := range rawPorts {
			f, ok := v.(float64)
			if !ok {
				continue
			}
			port := strconv.Itoa(int(f))
			urls[port] = fmt.Sprintf("%s://%s-%s.%s", scheme, port, id, domain)
		}
		m["preview_urls"] = urls
	}

	out, err = json.Marshal(m)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
