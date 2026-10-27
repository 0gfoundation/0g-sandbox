package proxy

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	sealIDLabel             = "0g-seal-id"
	sealIDBytes             = 32 // hex-encoded length = 64
	imageAttestationVersion = 2
)

// SealIssuer is authenticated by the server's live chain lookup, never by a
// create request. RegisteredAt identifies the current registry app instance.
type SealIssuer struct {
	ChainID         uint64         `json:"chain_id"`
	RegistryAddress common.Address `json:"registry_address"`
	AppID           string         `json:"app_id"`
	RegisteredAt    uint64         `json:"registered_at"`
}

func (s SealIssuer) validate() error {
	if s.ChainID == 0 || s.RegistryAddress == (common.Address{}) || s.RegisteredAt == 0 {
		return fmt.Errorf("invalid sealed attestation issuer scope")
	}
	if len(s.AppID) == 0 || len(s.AppID) > 256 || !utf8.ValidString(s.AppID) {
		return fmt.Errorf("sealed issuer app_id must be 1..256 UTF-8 bytes")
	}
	return nil
}

// ImageAttestation is the v2 envelope given to the sealed runtime. Its digest
// is nine fixed ABI words, not a JSON hash or a concatenated text message.
type ImageAttestation struct {
	Version uint64 `json:"version"`
	SealIssuer
	SealID    string `json:"seal_id"`
	PublicKey string `json:"pubkey"`
	ImageHash string `json:"image_hash"`
	Signature string `json:"signature"`
	Timestamp uint64 `json:"ts"`
}

func (a ImageAttestation) signingWords() ([]byte, error) {
	if a.Version != imageAttestationVersion {
		return nil, fmt.Errorf("unsupported sealed attestation version")
	}
	if err := a.SealIssuer.validate(); err != nil {
		return nil, err
	}
	sealID, err := hex.DecodeString(a.SealID)
	if err != nil || len(a.SealID) != 64 || len(sealID) != 32 {
		return nil, fmt.Errorf("seal_id must be 64 hex chars")
	}
	pub, err := hex.DecodeString(strings.TrimPrefix(a.PublicKey, "0x"))
	if err != nil || !strings.HasPrefix(a.PublicKey, "0x") || len(pub) != 33 {
		return nil, fmt.Errorf("pubkey must be a 0x-prefixed compressed public key")
	}
	if _, err := crypto.DecompressPubkey(pub); err != nil {
		return nil, fmt.Errorf("invalid container public key: %w", err)
	}
	image, err := hex.DecodeString(strings.TrimPrefix(a.ImageHash, "sha256:"))
	if err != nil || !strings.HasPrefix(a.ImageHash, "sha256:") || len(image) != 32 {
		return nil, fmt.Errorf("image_hash must be sha256: followed by 64 hex chars")
	}
	if a.Timestamp == 0 {
		return nil, fmt.Errorf("attestation timestamp must be positive")
	}
	words := make([]byte, 9*32)
	copy(words[0:32], crypto.Keccak256([]byte("0g.sandbox.ImageAttestation.v2")))
	new(big.Int).SetUint64(a.ChainID).FillBytes(words[32:64])
	copy(words[64+12:96], a.RegistryAddress[:])
	copy(words[96:128], crypto.Keccak256([]byte(a.AppID)))
	new(big.Int).SetUint64(a.RegisteredAt).FillBytes(words[128:160])
	copy(words[160:192], sealID)
	copy(words[192:224], crypto.Keccak256(pub))
	copy(words[224:256], image)
	new(big.Int).SetUint64(a.Timestamp).FillBytes(words[256:288])
	return words, nil
}

func (a ImageAttestation) signingDigest() (common.Hash, error) {
	words, err := a.signingWords()
	if err != nil {
		return common.Hash{}, err
	}
	return crypto.Keccak256Hash(words), nil
}

// InjectSeal wires the sealed-container identity material into a sandbox create
// request body that has already been processed by InjectOwner.
//
// It generates an independent ephemeral secp256k1 keypair, builds a v2
// app-signed attestation binding the current issuer scope, and injects two env vars into the
// container started by Daytona:
//
//   - SANDBOX_SEAL_KEY         — hex private key; the container's signing identity.
//     Never logged, never returned through the API, never stored outside the enclave.
//   - SANDBOX_SEAL_ATTESTATION — JSON the container presents to other services
//     to prove which image it runs and which key it holds.
//
// The sealId is also written to label "0g-seal-id" so operators can correlate
// a running sandbox with the attestation that was issued for it.
//
// appKey is the stable KMS app signer (ProviderKey), never the node signer.
func InjectSeal(body []byte, appKey *ecdsa.PrivateKey, imageHash string, issuer SealIssuer) ([]byte, error) {
	if appKey == nil {
		return nil, fmt.Errorf("KMS app signer not configured")
	}
	if err := issuer.validate(); err != nil {
		return nil, err
	}
	// Parse body once up front so we can both read a caller-provided seal_id
	// and patch env/labels on the same map before re-marshalling.
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("unmarshal body: %w", err)
	}

	// sealId: accept a caller-provided value (64 hex chars = 32 bytes) so
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
	// Issuer scope always comes from the authenticated server callback.
	for _, field := range []string{"version", "chain_id", "registry_address", "app_id", "registered_at"} {
		delete(m, field)
	}

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
	attestation := ImageAttestation{
		Version: imageAttestationVersion, SealIssuer: issuer,
		SealID: sealID, PublicKey: pubKey, ImageHash: imageHash,
		Timestamp: uint64(time.Now().Unix()),
	}
	hash, err := attestation.signingDigest()
	if err != nil {
		return nil, err
	}
	sig, err := crypto.Sign(hash[:], appKey)
	if err != nil {
		return nil, fmt.Errorf("sign attestation: %w", err)
	}
	sig[64] += 27 // normalise V to 27/28 for Solidity ecrecover compatibility

	attestation.Signature = "0x" + hex.EncodeToString(sig)
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
