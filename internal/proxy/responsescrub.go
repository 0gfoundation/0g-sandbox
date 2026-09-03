package proxy

import (
	"bytes"
	"encoding/json"
)

// sealKeyEnv is the env var InjectSeal writes into a sealed container: the
// hex secp256k1 private key that is the container's signing identity. It must
// never appear in any response body returned to a caller.
const sealKeyEnv = "SANDBOX_SEAL_KEY"

// scrubSealKeyFromBody removes the sealKeyEnv entry from the env map of a
// JSON sandbox object — or from every element of a JSON array of sandbox
// objects. Bodies that do not contain the key, are not JSON, or use any other
// shape are returned byte-for-byte unchanged, so the scrub is safe to apply
// to every JSON response that passes through the reverse proxy.
//
// Numbers are decoded with UseNumber so every value re-marshals exactly as
// received — wei amounts and other integers beyond 2^53 must not round-trip
// through float64.
func scrubSealKeyFromBody(body []byte) []byte {
	if !bytes.Contains(body, []byte(`"`+sealKeyEnv+`"`)) {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return body
	}
	if !scrubSealKeyInValue(v) {
		return body
	}
	out, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return out
}

// scrubSealKeyInValue deletes sealKeyEnv from v's env map, or from the env
// map of each object element when v is an array. It reports whether anything
// was deleted. Only these two shapes are touched: a sandbox object is flat
// (its env sits at the top level) and list responses are arrays of sandbox
// objects, so deeper "env" keys — a user service echoing its own JSON through
// the toolbox, for example — are left alone.
func scrubSealKeyInValue(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		env, ok := t["env"].(map[string]any)
		if !ok {
			return false
		}
		if _, had := env[sealKeyEnv]; had {
			delete(env, sealKeyEnv)
			return true
		}
		return false
	case []any:
		changed := false
		for _, e := range t {
			if scrubSealKeyInValue(e) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}
