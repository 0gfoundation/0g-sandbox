package proxy

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var testSealImageHash = "sha256:" + strings.Repeat("ab", 32)

func testSealIssuer() SealIssuer {
	return SealIssuer{ChainID: 16602, RegistryAddress: common.HexToAddress("0x1111111111111111111111111111111111111111"), AppID: "sandbox-test", RegisteredAt: 1234}
}

type vectorKey struct {
	PrivateKey string `json:"private_key"`
	Address    string `json:"address"`
}

func (k vectorKey) key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.HexToECDSA(strings.TrimPrefix(k.PrivateKey, "0x"))
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(key.PublicKey) != common.HexToAddress(k.Address) {
		t.Fatal("vector key does not match its address")
	}
	return key
}

type vectorDomain struct {
	Name              string         `json:"name"`
	Version           string         `json:"version"`
	ChainID           uint64         `json:"chainId"`
	VerifyingContract common.Address `json:"verifyingContract"`
}

// imageAttestationVector is testdata/image-attestation-v3.json, shared
// byte-identical with the attestor and the sealed runtime.
type imageAttestationVector struct {
	ProviderKey  vectorKey `json:"test_only_provider_key"`
	ContainerKey vectorKey `json:"test_only_container_key"`
	TypedData    struct {
		Domain  vectorDomain `json:"domain"`
		Message struct {
			AppID           string `json:"appId"`
			RegisteredAt    uint64 `json:"registeredAt"`
			SealID          string `json:"sealId"`
			ContainerPubkey string `json:"containerPubkey"`
			ImageHash       string `json:"imageHash"`
			IssuedAt        uint64 `json:"issuedAt"`
		} `json:"message"`
	} `json:"typed_data"`
	TypeHash               string `json:"type_hash"`
	DomainSeparator        string `json:"domain_separator"`
	StructHash             string `json:"struct_hash"`
	Digest                 string `json:"digest"`
	Signature              string `json:"signature"`
	RecoveredAddress       string `json:"recovered_address"`
	SandboxSealAttestation string `json:"sandbox_seal_attestation"`
}

func loadImageAttestationVector(t *testing.T) imageAttestationVector {
	t.Helper()
	raw, err := os.ReadFile("testdata/image-attestation-v3.json")
	if err != nil {
		t.Fatal(err)
	}
	var v imageAttestationVector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// recoverDigestSigner accepts V as 27/28 or 0/1, as verifiers do.
func recoverDigestSigner(t *testing.T, digest common.Hash, signature string) common.Address {
	t.Helper()
	sig, err := hex.DecodeString(strings.TrimPrefix(signature, "0x"))
	if err != nil || len(sig) != 65 {
		t.Fatal("invalid signature")
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(digest[:], sig)
	if err != nil {
		t.Fatal(err)
	}
	return crypto.PubkeyToAddress(*pub)
}

func recoverSealSigner(t *testing.T, a ImageAttestation) common.Address {
	t.Helper()
	digest, err := a.signingDigest()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(a.Signature, "1b") && !strings.HasSuffix(a.Signature, "1c") {
		t.Fatal("signature recovery byte must be 27/28")
	}
	return recoverDigestSigner(t, digest, a.Signature)
}

func TestImageAttestationV3KnownAnswer(t *testing.T) {
	v := loadImageAttestationVector(t)
	providerKey := v.ProviderKey.key(t)
	containerKey := v.ContainerKey.key(t)
	if imageAttestationTypeHash.Hex() != v.TypeHash {
		t.Fatalf("type hash %s", imageAttestationTypeHash.Hex())
	}
	if v.TypedData.Domain.Name != imageAttestationDomainName || v.TypedData.Domain.Version != imageAttestationDomainVersion {
		t.Fatalf("domain %q/%q", v.TypedData.Domain.Name, v.TypedData.Domain.Version)
	}
	msg := v.TypedData.Message
	if "0x"+hex.EncodeToString(crypto.CompressPubkey(&containerKey.PublicKey)) != msg.ContainerPubkey {
		t.Fatal("container key does not match containerPubkey")
	}

	// Issuing from the typed-data inputs reproduces the wire string exactly.
	issuer := SealIssuer{ChainID: v.TypedData.Domain.ChainID, RegistryAddress: v.TypedData.Domain.VerifyingContract, AppID: msg.AppID, RegisteredAt: msg.RegisteredAt}
	wire, err := issueImageAttestation(issuer, strings.TrimPrefix(msg.SealID, "0x"), msg.ContainerPubkey,
		"sha256:"+strings.TrimPrefix(msg.ImageHash, "0x"), msg.IssuedAt, providerKey)
	if err != nil {
		t.Fatal(err)
	}
	if wire != v.SandboxSealAttestation {
		t.Fatalf("wire string mismatch:\n got %s\nwant %s", wire, v.SandboxSealAttestation)
	}

	var a ImageAttestation
	if err := json.Unmarshal([]byte(v.SandboxSealAttestation), &a); err != nil {
		t.Fatal(err)
	}
	if got := a.domainSeparator().Hex(); got != v.DomainSeparator {
		t.Fatalf("domain separator %s", got)
	}
	structHash, err := a.structHash()
	if err != nil {
		t.Fatal(err)
	}
	if structHash.Hex() != v.StructHash {
		t.Fatalf("struct hash %s", structHash.Hex())
	}
	digest, err := a.signingDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest.Hex() != v.Digest {
		t.Fatalf("digest %s", digest.Hex())
	}
	if a.Signature != v.Signature {
		t.Fatalf("signature %s", a.Signature)
	}
	want := common.HexToAddress(v.RecoveredAddress)
	if want != crypto.PubkeyToAddress(providerKey.PublicKey) || recoverSealSigner(t, a) != want {
		t.Fatal("certificate does not recover to the provider key")
	}
	sig, _ := hex.DecodeString(strings.TrimPrefix(a.Signature, "0x"))
	sig[64] -= 27
	if recoverDigestSigner(t, digest, "0x"+hex.EncodeToString(sig)) != want {
		t.Fatal("0/1 recovery byte did not recover the provider key")
	}
	if out, _ := json.Marshal(a); string(out) != v.SandboxSealAttestation {
		t.Fatalf("re-marshalled certificate differs: %s", out)
	}
}

func TestImageAttestationV3ScopeTampering(t *testing.T) {
	v := loadImageAttestationVector(t)
	var a ImageAttestation
	if err := json.Unmarshal([]byte(v.SandboxSealAttestation), &a); err != nil {
		t.Fatal(err)
	}
	signer := common.HexToAddress(v.RecoveredAddress)
	otherKey, _ := crypto.GenerateKey()
	otherPub := "0x" + hex.EncodeToString(crypto.CompressPubkey(&otherKey.PublicKey))
	for _, tc := range []struct {
		name   string
		change func(*ImageAttestation)
	}{
		{"chain", func(a *ImageAttestation) { a.ChainID++ }},
		{"registry", func(a *ImageAttestation) { a.RegistryAddress[0]++ }},
		{"exact_app_id", func(a *ImageAttestation) { a.AppID += " " }},
		{"epoch", func(a *ImageAttestation) { a.RegisteredAt++ }},
		{"seal", func(a *ImageAttestation) { a.SealID = strings.Repeat("44", 32) }},
		{"pubkey", func(a *ImageAttestation) { a.PublicKey = otherPub }},
		{"image", func(a *ImageAttestation) { a.ImageHash = testSealImageHash }},
		{"issued_at", func(a *ImageAttestation) { a.Timestamp++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := a
			tc.change(&changed)
			if recoverSealSigner(t, changed) == signer {
				t.Fatal("tampered certificate retained the provider signer")
			}
		})
	}
	for _, version := range []uint64{0, 1, 2, 4} {
		changed := a
		changed.Version = version
		if _, err := changed.signingDigest(); err == nil {
			t.Fatalf("accepted version %d", version)
		}
	}
	upper := a
	upper.SealID = strings.ToUpper(a.SealID)
	if _, err := upper.signingDigest(); err == nil {
		t.Fatal("accepted an uppercase seal_id")
	}
	upper = a
	upper.ImageHash = "sha256:" + strings.ToUpper(strings.TrimPrefix(a.ImageHash, "sha256:"))
	if _, err := upper.signingDigest(); err == nil {
		t.Fatal("accepted an uppercase image_hash")
	}
}

func TestInjectSealUsesAppSignerAndServerScope(t *testing.T) {
	appKey, _ := crypto.GenerateKey()
	nodeKey, _ := crypto.GenerateKey()
	issuer := testSealIssuer()
	body := []byte(`{"version":1,"chain_id":1,"app_id":"caller-app","registered_at":1,"registry_address":"caller-registry","env":{"SANDBOX_SEAL_ATTESTATION":"caller-envelope"}}`)
	out, err := InjectSeal(body, appKey, testSealImageHash, issuer)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	var a ImageAttestation
	if err := json.Unmarshal([]byte(m["env"].(map[string]any)["SANDBOX_SEAL_ATTESTATION"].(string)), &a); err != nil {
		t.Fatal(err)
	}
	if a.Version != 3 || a.SealIssuer != issuer {
		t.Fatalf("caller scope escaped overwrite: %+v", a.SealIssuer)
	}
	for _, field := range []string{"version", "chain_id", "app_id", "registered_at", "registry_address"} {
		if _, ok := m[field]; ok {
			t.Fatalf("forwarded caller issuer field %s", field)
		}
	}
	got := recoverSealSigner(t, a)
	if got != crypto.PubkeyToAddress(appKey.PublicKey) || got == crypto.PubkeyToAddress(nodeKey.PublicKey) {
		t.Fatalf("wrong attestation signer %s", got)
	}
	sealKey, err := crypto.HexToECDSA(strings.TrimPrefix(m["env"].(map[string]any)[sealKeyEnv].(string), "0x"))
	if err != nil {
		t.Fatal(err)
	}
	if crypto.PubkeyToAddress(sealKey.PublicKey) == got {
		t.Fatal("container reused app signer secret")
	}
}

func TestInjectSealRejectsInvalidIssuer(t *testing.T) {
	key, _ := crypto.GenerateKey()
	for _, change := range []func(*SealIssuer){
		func(s *SealIssuer) { s.ChainID = 0 },
		func(s *SealIssuer) { s.RegistryAddress = common.Address{} },
		func(s *SealIssuer) { s.AppID = "" },
		func(s *SealIssuer) { s.AppID = strings.Repeat("界", 86) },
		func(s *SealIssuer) { s.AppID = string([]byte{0xff}) },
		func(s *SealIssuer) { s.RegisteredAt = 0 },
	} {
		scope := testSealIssuer()
		change(&scope)
		if _, err := InjectSeal([]byte(`{}`), key, testSealImageHash, scope); err == nil {
			t.Fatalf("accepted invalid scope %+v", scope)
		}
	}
	if _, err := InjectSeal([]byte(`{}`), nil, testSealImageHash, testSealIssuer()); err == nil {
		t.Fatal("accepted missing app signer")
	}
}

func TestInjectSeal_EnvVarsInjected(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	body := []byte(`{"labels":{"daytona-owner":"0xOWNER","0g-sealed":"true"}}`)

	out, err := InjectSeal(body, teeKey, testSealImageHash, testSealIssuer())
	if err != nil {
		t.Fatalf("InjectSeal error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	env, _ := m["env"].(map[string]any)
	if env == nil {
		t.Fatal("env map missing")
	}

	// Private key must be present in env
	sealKey, _ := env["SANDBOX_SEAL_KEY"].(string)
	if len(sealKey) == 0 {
		t.Fatal("SANDBOX_SEAL_KEY missing from env")
	}
	if sealKey[:2] != "0x" {
		t.Errorf("SANDBOX_SEAL_KEY must be 0x-prefixed, got %s", sealKey[:10])
	}

	// Attestation must be present and parseable
	attestRaw, _ := env["SANDBOX_SEAL_ATTESTATION"].(string)
	if attestRaw == "" {
		t.Fatal("SANDBOX_SEAL_ATTESTATION missing from env")
	}
	var attest map[string]any
	if err := json.Unmarshal([]byte(attestRaw), &attest); err != nil {
		t.Fatalf("SANDBOX_SEAL_ATTESTATION is not valid JSON: %v", err)
	}
}

func TestInjectSeal_CallerProvidedSealID(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	want := "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	body := []byte(fmt.Sprintf(`{"seal_id":%q,"labels":{}}`, want))

	out, err := InjectSeal(body, teeKey, testSealImageHash, testSealIssuer())
	if err != nil {
		t.Fatalf("InjectSeal error: %v", err)
	}
	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels := m["labels"].(map[string]any)
	if got := labels["0g-seal-id"]; got != want {
		t.Errorf("label 0g-seal-id = %v, want %s", got, want)
	}
	if _, ok := m["seal_id"]; ok {
		t.Error("seal_id should be stripped from body")
	}

	env := m["env"].(map[string]any)
	var attest map[string]any
	json.Unmarshal([]byte(env["SANDBOX_SEAL_ATTESTATION"].(string)), &attest) //nolint:errcheck
	if attest["seal_id"] != want {
		t.Errorf("attestation seal_id = %v, want %s", attest["seal_id"], want)
	}
}

func TestInjectSeal_InvalidCallerSealID(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	cases := []string{
		`"not-hex-!!"`,
		`"deadbeef"`,                         // too short
		`"aabbccddeeff00112233445566778899"`, // 32 chars (old size), too short now
		`"aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899ff"`, // too long
	}
	for _, v := range cases {
		body := []byte(fmt.Sprintf(`{"seal_id":%s,"labels":{}}`, v))
		if _, err := InjectSeal(body, teeKey, testSealImageHash, testSealIssuer()); err == nil {
			t.Errorf("expected error for seal_id=%s", v)
		}
	}
}

func TestInjectSeal_AttestationFields(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	imageHash := testSealImageHash
	body := []byte(`{"labels":{"daytona-owner":"0xOWNER","0g-sealed":"true"}}`)

	out, err := InjectSeal(body, teeKey, imageHash, testSealIssuer())
	if err != nil {
		t.Fatalf("InjectSeal error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	env := m["env"].(map[string]any)
	var attest map[string]any
	json.Unmarshal([]byte(env["SANDBOX_SEAL_ATTESTATION"].(string)), &attest) //nolint:errcheck

	if attest["image_hash"] != imageHash {
		t.Errorf("image_hash: got %v want %v", attest["image_hash"], imageHash)
	}
	if attest["seal_id"] == "" {
		t.Error("seal_id must not be empty")
	}
	if attest["pubkey"] == "" {
		t.Error("pubkey must not be empty")
	}
	sigStr, _ := attest["signature"].(string)
	if len(sigStr) < 4 {
		t.Error("signature must not be empty")
	}
	if attest["ts"] == nil {
		t.Error("ts must be present")
	}
}

func TestInjectSeal_SealIDInLabel(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	body := []byte(`{"labels":{"daytona-owner":"0xOWNER","0g-sealed":"true"}}`)

	out, err := InjectSeal(body, teeKey, testSealImageHash, testSealIssuer())
	if err != nil {
		t.Fatalf("InjectSeal error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels := m["labels"].(map[string]any)
	sealID, _ := labels[sealIDLabel].(string)
	if len(sealID) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("0g-seal-id label: got len %d want 64, value=%q", len(sealID), sealID)
	}
	// Must be valid hex
	if _, err := hex.DecodeString(sealID); err != nil {
		t.Errorf("0g-seal-id is not valid hex: %v", err)
	}
}

func TestInjectSeal_PrivkeyNotInLabels(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	body := []byte(`{"labels":{"daytona-owner":"0xOWNER","0g-sealed":"true"}}`)

	out, err := InjectSeal(body, teeKey, testSealImageHash, testSealIssuer())
	if err != nil {
		t.Fatalf("InjectSeal error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	labels := m["labels"].(map[string]any)
	for k, v := range labels {
		vs, _ := v.(string)
		if len(vs) >= 64 && k != sealIDLabel {
			t.Errorf("label %q looks like it might contain a private key: %s", k, vs[:16])
		}
	}
}

func TestInjectSeal_SignatureVerifiable(t *testing.T) {
	teeKey, _ := crypto.GenerateKey()
	teeAddr := crypto.PubkeyToAddress(teeKey.PublicKey)
	imageHash := testSealImageHash
	body := []byte(`{"labels":{"daytona-owner":"0xOWNER","0g-sealed":"true"}}`)

	out, err := InjectSeal(body, teeKey, imageHash, testSealIssuer())
	if err != nil {
		t.Fatalf("InjectSeal error: %v", err)
	}

	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck

	env := m["env"].(map[string]any)
	var attest map[string]any
	json.Unmarshal([]byte(env["SANDBOX_SEAL_ATTESTATION"].(string)), &attest) //nolint:errcheck

	var envelope ImageAttestation
	if err := json.Unmarshal([]byte(env["SANDBOX_SEAL_ATTESTATION"].(string)), &envelope); err != nil {
		t.Fatal(err)
	}
	sigHex := envelope.Signature[2:]
	hash, err := envelope.signingDigest()
	if err != nil {
		t.Fatal(err)
	}

	// Recover signer
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	sigBytes[64] -= 27 // convert back from 27/28 to 0/1
	recovered, err := crypto.SigToPub(hash[:], sigBytes)
	if err != nil {
		t.Fatalf("SigToPub: %v", err)
	}
	recoveredAddr := crypto.PubkeyToAddress(*recovered)

	if recoveredAddr != teeAddr {
		t.Errorf("signature signer: got %s want %s", recoveredAddr.Hex(), teeAddr.Hex())
	}
}

func TestStripSealKey_RemovesPrivkey(t *testing.T) {
	body := []byte(`{"id":"abc","env":{"SANDBOX_SEAL_KEY":"0xdeadbeef","SANDBOX_SEAL_ATTESTATION":"{}"},"labels":{}}`)
	out, err := stripSealKey(body)
	if err != nil {
		t.Fatalf("stripSealKey: %v", err)
	}
	var m map[string]any
	json.Unmarshal(out, &m) //nolint:errcheck
	env := m["env"].(map[string]any)
	if _, ok := env["SANDBOX_SEAL_KEY"]; ok {
		t.Error("SANDBOX_SEAL_KEY must be removed from response")
	}
	if env["SANDBOX_SEAL_ATTESTATION"] == nil {
		t.Error("SANDBOX_SEAL_ATTESTATION must be preserved")
	}
}

func TestInjectSealLowercasesCallerSealID(t *testing.T) {
	key, _ := crypto.GenerateKey()
	want := strings.Repeat("ab", 32)
	out, err := InjectSeal([]byte(fmt.Sprintf(`{"seal_id":%q}`, strings.ToUpper(want))), key, testSealImageHash, testSealIssuer())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	var a ImageAttestation
	if err := json.Unmarshal([]byte(m["env"].(map[string]any)["SANDBOX_SEAL_ATTESTATION"].(string)), &a); err != nil {
		t.Fatal(err)
	}
	if a.SealID != want || m["labels"].(map[string]any)[sealIDLabel] != want {
		t.Fatalf("seal_id not canonical: certificate %q, label %v", a.SealID, m["labels"].(map[string]any)[sealIDLabel])
	}
}

func TestInjectSealStripsCallerTrustEnv(t *testing.T) {
	kept := map[string]any{
		"ATTESTOR_URL": "https://attestor.example", "SEAL_BOOT_TICKET": strings.Repeat("11", 32),
		"SEAL_SECRET_ENV": "0xciphertext", "API_KEY": "k", "CHAIN_RPC_URL": "https://rpc.example",
		"AGENTIC_ID_ADDR": "0x01", "VERIFIED_FEEDBACK_ADDR": "0x02", "INDEXER_URL": "https://indexer.example",
		"AGENT_FRAMEWORK": "openclaw",
	}
	callerEnv := map[string]any{
		"ATTESTOR_TLS_DESCRIPTOR":     "caller",
		"ATTESTOR_ALLOW_UNPINNED_DEV": "1",
		"TEE_SIGNER_ADDRESS":          "0x0000000000000000000000000000000000000001",
		"SANDBOX_SEAL_KEY":            "0x" + strings.Repeat("22", 32),
		"SANDBOX_SEAL_ATTESTATION":    "caller",
		"SANDBOX_PROXY_DOMAIN":        "caller.example",
		"SANDBOX_ATTESTOR_IDENTITY":   "caller",
		"SANDBOX_API_URL":             "https://caller.example",
	}
	for k, v := range kept {
		callerEnv[k] = v
	}
	for _, proxyDomain := range []string{"", "proxy.example:4000"} {
		t.Run("proxy_domain="+proxyDomain, func(t *testing.T) {
			t.Setenv("PROXY_DOMAIN", proxyDomain)
			key, _ := crypto.GenerateKey()
			body, _ := json.Marshal(map[string]any{"env": callerEnv})
			out, err := InjectSeal(body, key, testSealImageHash, testSealIssuer())
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(out, &m); err != nil {
				t.Fatal(err)
			}
			env := m["env"].(map[string]any)
			want := len(kept) + 2
			if proxyDomain != "" {
				want++
				if env["SANDBOX_PROXY_DOMAIN"] != proxyDomain {
					t.Fatalf("SANDBOX_PROXY_DOMAIN = %v", env["SANDBOX_PROXY_DOMAIN"])
				}
			} else if _, ok := env["SANDBOX_PROXY_DOMAIN"]; ok {
				t.Fatal("caller SANDBOX_PROXY_DOMAIN survived")
			}
			if len(env) != want {
				t.Fatalf("env keys %v", env)
			}
			for k, v := range kept {
				if env[k] != v {
					t.Fatalf("%s = %v, want caller value", k, env[k])
				}
			}
			sealKey, err := crypto.HexToECDSA(strings.TrimPrefix(env[sealKeyEnv].(string), "0x"))
			if err != nil {
				t.Fatal(err)
			}
			var a ImageAttestation
			if err := json.Unmarshal([]byte(env["SANDBOX_SEAL_ATTESTATION"].(string)), &a); err != nil {
				t.Fatal(err)
			}
			if a.PublicKey != "0x"+hex.EncodeToString(crypto.CompressPubkey(&sealKey.PublicKey)) {
				t.Fatal("SANDBOX_SEAL_KEY does not match the certificate pubkey")
			}
			if recoverSealSigner(t, a) != crypto.PubkeyToAddress(key.PublicKey) {
				t.Fatal("certificate not signed by the provider key")
			}
		})
	}
}

func TestInjectSealRefusesEnvKeysThatAreNotVariableNames(t *testing.T) {
	key, _ := crypto.GenerateKey()
	for _, name := range []string{"CHAIN_RPC_URL=https://rpc.example/?x", "", "1A", "A-B", "A B", "A\nB", "Ä"} {
		body, _ := json.Marshal(map[string]any{"env": map[string]any{"CHAIN_RPC_URL": "https://rpc.example", name: ""}})
		if _, err := InjectSeal(body, key, testSealImageHash, testSealIssuer()); !errors.Is(err, ErrCallerEnvKey) {
			t.Fatalf("%q: got %v", name, err)
		}
	}
	body, _ := json.Marshal(map[string]any{"env": map[string]any{"_A1": "x", "a": "y", "SEAL_SECRET_ENV": "z"}})
	if _, err := InjectSeal(body, key, testSealImageHash, testSealIssuer()); err != nil {
		t.Fatal(err)
	}
}
