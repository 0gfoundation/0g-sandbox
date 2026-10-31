package proxy

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	imageAttestationVersion = 3

	imageAttestationDomainName    = "0G Sandbox ImageAttestation"
	imageAttestationDomainVersion = "3"
)

var imageAttestationTypeHash = crypto.Keccak256Hash([]byte(
	"ImageAttestation(string appId,uint64 registeredAt,bytes32 sealId,bytes containerPubkey,bytes32 imageHash,uint64 issuedAt)",
))

// Caller env keys removed from every sealed create, in addition to every
// SANDBOX_* key, which only the provider writes. The sealed runtime treats
// ATTESTOR_ALLOW_UNPINNED_DEV as a dev-only bypass and no longer reads the
// other two; none of them may come from the caller.
var strippedSealEnv = []string{"ATTESTOR_TLS_DESCRIPTOR", "ATTESTOR_ALLOW_UNPINNED_DEV", "TEE_SIGNER_ADDRESS"}

// ErrCallerEnvKey refuses a sealed create whose env has a key that is not a
// variable name. The runtime joins each entry as KEY=VALUE, so a key such as
// "CHAIN_RPC_URL=https://x/" would become a second CHAIN_RPC_URL entry beside
// the one the caller's attestor sets, and either may win.
var ErrCallerEnvKey = errors.New("env keys must match [A-Za-z_][A-Za-z0-9_]*")

// isEnvName reports whether key matches [A-Za-z_][A-Za-z0-9_]*.
func isEnvName(key string) bool {
	if key == "" {
		return false
	}
	for i, c := range key {
		letter := c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

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

// ImageAttestation is the v3 container certificate given to the sealed runtime
// as SANDBOX_SEAL_ATTESTATION. Field order is the wire order. The signature is
// EIP-712 over ImageAttestation in the domain of ChainID and RegistryAddress.
type ImageAttestation struct {
	Version uint64 `json:"version"`
	SealIssuer
	SealID    string `json:"seal_id"`
	PublicKey string `json:"pubkey"`
	ImageHash string `json:"image_hash"`
	Timestamp uint64 `json:"ts"`
	Signature string `json:"signature"`
}

func (a ImageAttestation) domainSeparator() common.Hash {
	return eip712DomainSeparator(imageAttestationDomainName, imageAttestationDomainVersion, a.ChainID, a.RegistryAddress)
}

func (a ImageAttestation) structHash() (common.Hash, error) {
	if a.Version != imageAttestationVersion {
		return common.Hash{}, fmt.Errorf("unsupported sealed attestation version")
	}
	if err := a.SealIssuer.validate(); err != nil {
		return common.Hash{}, err
	}
	sealID, err := hex.DecodeString(a.SealID)
	if err != nil || len(sealID) != sealIDBytes || a.SealID != strings.ToLower(a.SealID) {
		return common.Hash{}, fmt.Errorf("seal_id must be 64 lowercase hex chars")
	}
	pub, err := hex.DecodeString(strings.TrimPrefix(a.PublicKey, "0x"))
	if err != nil || !strings.HasPrefix(a.PublicKey, "0x") || len(pub) != 33 {
		return common.Hash{}, fmt.Errorf("pubkey must be a 0x-prefixed compressed public key")
	}
	if _, err := crypto.DecompressPubkey(pub); err != nil {
		return common.Hash{}, fmt.Errorf("invalid container public key: %w", err)
	}
	image, err := hex.DecodeString(strings.TrimPrefix(a.ImageHash, "sha256:"))
	if err != nil || !strings.HasPrefix(a.ImageHash, "sha256:") || len(image) != 32 || a.ImageHash != strings.ToLower(a.ImageHash) {
		return common.Hash{}, fmt.Errorf("image_hash must be sha256: followed by 64 lowercase hex chars")
	}
	if a.Timestamp == 0 {
		return common.Hash{}, fmt.Errorf("attestation timestamp must be positive")
	}
	encoded := make([]byte, 7*32)
	copy(encoded[0:32], imageAttestationTypeHash[:])
	copy(encoded[32:64], crypto.Keccak256([]byte(a.AppID)))
	putUint64Word(encoded[64:96], a.RegisteredAt)
	copy(encoded[96:128], sealID)
	copy(encoded[128:160], crypto.Keccak256(pub))
	copy(encoded[160:192], image)
	putUint64Word(encoded[192:224], a.Timestamp)
	return crypto.Keccak256Hash(encoded), nil
}

func (a ImageAttestation) signingDigest() (common.Hash, error) {
	structHash, err := a.structHash()
	if err != nil {
		return common.Hash{}, err
	}
	return eip712Digest(a.domainSeparator(), structHash), nil
}

// issueImageAttestation signs a v3 certificate with the provider key and
// returns its SANDBOX_SEAL_ATTESTATION JSON.
func issueImageAttestation(issuer SealIssuer, sealID, pubKey, imageHash string, issuedAt uint64, key *ecdsa.PrivateKey) (string, error) {
	attestation := ImageAttestation{
		Version: imageAttestationVersion, SealIssuer: issuer,
		SealID: sealID, PublicKey: pubKey, ImageHash: imageHash, Timestamp: issuedAt,
	}
	digest, err := attestation.signingDigest()
	if err != nil {
		return "", err
	}
	if attestation.Signature, err = signDigest(digest, key); err != nil {
		return "", err
	}
	out, err := json.Marshal(attestation)
	if err != nil {
		return "", fmt.Errorf("marshal attestation: %w", err)
	}
	return string(out), nil
}

// InjectSeal wires the sealed-container identity material into a sandbox create
// request body that has already been processed by InjectOwner.
//
// It generates an independent ephemeral secp256k1 keypair, builds a v3
// certificate binding the current issuer scope, and injects two env vars into
// the container started by Daytona:
//
//   - SANDBOX_SEAL_KEY         — hex private key; the container's signing identity.
//     Never logged, never returned through the API, never stored outside the enclave.
//   - SANDBOX_SEAL_ATTESTATION — the certificate the container presents to other
//     services to prove which image it runs and which key it holds.
//
// Every caller-supplied SANDBOX_* key and the strippedSealEnv keys are removed
// first, so the container sees only the values written here.
//
// The sealId is also written to label "0g-seal-id" so operators can correlate
// a running sandbox with the certificate that was issued for it.
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
	// value. Reject malformed input rather than silently overwriting it. The
	// certificate carries the lowercase form.
	var sealID string
	if v, ok := m["seal_id"].(string); ok && v != "" {
		if _, err := hex.DecodeString(v); err != nil || len(v) != sealIDBytes*2 {
			return nil, fmt.Errorf("seal_id must be %d hex chars", sealIDBytes*2)
		}
		sealID = strings.ToLower(v)
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
	attestation, err := issueImageAttestation(issuer, sealID, pubKey, imageHash, uint64(time.Now().Unix()), appKey)
	if err != nil {
		return nil, err
	}

	// Write sealId to label so operators can correlate sandbox ↔ certificate.
	// pubkey is intentionally omitted: the verifier obtains it from the
	// certificate presented by the container itself, not from the proxy.
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
	for key := range env {
		if !isEnvName(key) {
			return nil, ErrCallerEnvKey
		}
	}
	stripCallerSealEnv(env)
	env[sealKeyEnv] = privHex
	env["SANDBOX_SEAL_ATTESTATION"] = attestation

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

func stripCallerSealEnv(env map[string]any) {
	for key := range env {
		if strings.HasPrefix(key, "SANDBOX_") {
			delete(env, key)
		}
	}
	for _, key := range strippedSealEnv {
		delete(env, key)
	}
}

// stripSealKey removes SANDBOX_SEAL_KEY and SEAL_BOOT_TICKET (see
// secretResponseEnv) from the env map in a sandbox JSON response body. The
// private key and the boot ticket must never be returned to the caller.
func stripSealKey(body []byte) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	if env, ok := m["env"].(map[string]any); ok {
		deleteSecretEnv(env)
	}
	return json.Marshal(m)
}
