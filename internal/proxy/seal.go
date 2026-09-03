package proxy

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

const (
	sealIDLabel = "0g-seal-id"
	sealIDBytes = 32 // hex-encoded length = 64
)

// InjectSeal wires the sealed-container identity material into a sandbox create
// request body that has already been processed by InjectOwner.
//
// It generates an ephemeral secp256k1 keypair, builds a TEE-signed attestation
// over {sealId, pubkey, imageHash, ts}, and injects two env vars into the
// container started by Daytona:
//
//   - SANDBOX_SEAL_KEY         — hex private key; the container's signing identity.
//     Never logged, never returned through the API, never stored outside the enclave.
//   - SANDBOX_SEAL_ATTESTATION — JSON the container presents to other services
//     to prove which image it runs and which key it holds.
//
// The sealId is also written to label "0g-seal-id" so operators can correlate
// a running sandbox with the attestation that was issued for it.
func InjectSeal(body []byte, teeKey *ecdsa.PrivateKey, imageHash string) ([]byte, error) {
	// Parse body once up front so we can both read a caller-provided seal_id
	// and patch env/labels on the same map before re-marshalling.
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("unmarshal body: %w", err)
	}

	// sealId: accept a caller-provided value (32 hex chars = 16 bytes) so
	// verifiers can pre-register an expected id; fall back to a fresh random
	// value. Reject malformed input rather than silently overwriting it.
	var sealID string
	if v, ok := m["seal_id"].(string); ok && v != "" {
		if _, err := hex.DecodeString(v); err != nil || len(v) != sealIDBytes*2 {
			return nil, fmt.Errorf("seal_id must be %d hex chars", sealIDBytes*2)
		}
		sealID = v
	} else {
		var raw [sealIDBytes]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, fmt.Errorf("generate seal id: %w", err)
		}
		sealID = hex.EncodeToString(raw[:])
	}
	delete(m, "seal_id") // not a Daytona field; strip before forwarding

	// Generate ephemeral container identity keypair.
	privKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate seal keypair: %w", err)
	}

	// pubKey is the 33-byte compressed secp256k1 public key, hex-encoded with
	// 0x prefix. Compressed form so consumers can do ECIES/ECDH directly without
	// reconstructing the point from an address.
	pubKey := "0x" + hex.EncodeToString(crypto.CompressPubkey(&privKey.PublicKey))
	privHex := "0x" + hex.EncodeToString(crypto.FromECDSA(privKey))
	ts := time.Now().Unix()

	// Build TEE-signed attestation.
	// Message: keccak256("ImageAttestation:" || sealId || ":" || pubkey || ":" || imageHash || ":" || ts)
	msg := fmt.Sprintf("ImageAttestation:%s:%s:%s:%d", sealID, pubKey, imageHash, ts)
	hash := crypto.Keccak256Hash([]byte(msg))
	sig, err := crypto.Sign(hash[:], teeKey)
	if err != nil {
		return nil, fmt.Errorf("sign attestation: %w", err)
	}
	sig[64] += 27 // normalise V to 27/28 for Solidity ecrecover compatibility

	attestation := map[string]any{
		"seal_id":    sealID,
		"pubkey":     pubKey,
		"image_hash": imageHash,
		"signature":  "0x" + hex.EncodeToString(sig),
		"ts":         ts,
	}
	attestationJSON, err := json.Marshal(attestation)
	if err != nil {
		return nil, fmt.Errorf("marshal attestation: %w", err)
	}

	// Write sealId to label so operators can correlate sandbox ↔ attestation.
	// pubkey is intentionally omitted: the verifier obtains it from the attestation
	// presented by the container itself, not from the proxy.
	labels, _ := m["labels"].(map[string]any)
	if labels == nil {
		labels = make(map[string]any)
	}
	labels[sealIDLabel] = sealID
	m["labels"] = labels

	// Env vars forwarded into the container runtime by Daytona.
	// The entire docker-compose stack runs inside the same TDX enclave, so
	// this intra-enclave call is not visible to the operator.
	env, _ := m["env"].(map[string]any)
	if env == nil {
		env = make(map[string]any)
	}
	env[sealKeyEnv] = privHex
	env["SANDBOX_SEAL_ATTESTATION"] = string(attestationJSON)

	// SANDBOX_PROXY_DOMAIN lets bootstrap inside the sealed container
	// compose its own public URL by combining this host:port with the
	// Daytona-injected DAYTONA_SANDBOX_ID. We don't know the UUID here
	// (Daytona assigns it after this create call returns) but the proxy
	// domain is static and known at this point — see EVOLUTION_DESIGN
	// section 15.5 for the full URL assembly contract.
	if pd := os.Getenv("PROXY_DOMAIN"); pd != "" {
		env["SANDBOX_PROXY_DOMAIN"] = pd
	}
	m["env"] = env

	return json.Marshal(m)
}

// stripSealKey removes SANDBOX_SEAL_KEY from the env map in a sandbox JSON
// response body. The private key must never be returned to the caller.
func stripSealKey(body []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if env, ok := m["env"].(map[string]any); ok {
		delete(env, sealKeyEnv)
	}
	return json.Marshal(m)
}
