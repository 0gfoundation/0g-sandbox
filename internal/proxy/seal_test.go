package proxy

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

var testSealImageHash = "sha256:" + strings.Repeat("ab", 32)

func testSealIssuer() SealIssuer {
	return SealIssuer{ChainID: 16602, RegistryAddress: common.HexToAddress("0x1111111111111111111111111111111111111111"), AppID: "sandbox-test", RegisteredAt: 1234}
}

func loadSealVector(t *testing.T) (ImageAttestation, string, string, string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/sealed-attestation-v2-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Envelope  ImageAttestation `json:"envelope"`
		Words     string           `json:"encoded_words"`
		Digest    string           `json:"digest"`
		AppSigner string           `json:"app_signer"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Envelope, v.Words, v.Digest, v.AppSigner
}

func recoverSealSigner(t *testing.T, a ImageAttestation) common.Address {
	t.Helper()
	hash, err := a.signingDigest()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(a.Signature, "0x"))
	if err != nil || len(sig) != 65 {
		t.Fatal("invalid signature")
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatal("signature recovery byte must be 27/28")
	}
	sig[64] -= 27
	pub, err := crypto.SigToPub(hash[:], sig)
	if err != nil {
		t.Fatal(err)
	}
	return crypto.PubkeyToAddress(*pub)
}

func TestImageAttestationV2KnownAnswer(t *testing.T) {
	a, wantWords, wantDigest, wantSigner := loadSealVector(t)
	words, err := a.signingWords()
	if err != nil {
		t.Fatal(err)
	}
	if got := "0x" + hex.EncodeToString(words); got != wantWords {
		t.Fatalf("ABI words mismatch: %s", got)
	}
	hash, err := a.signingDigest()
	if err != nil {
		t.Fatal(err)
	}
	if hash.Hex() != wantDigest || hash.Hex() != "0x89f5a12fefe1868ddbc9a3f31091db032edb737c0782c876b3a455a493744b38" {
		t.Fatalf("digest mismatch: %s", hash.Hex())
	}
	if got := recoverSealSigner(t, a); got != common.HexToAddress(wantSigner) {
		t.Fatalf("signer: %s", got)
	}
	key, err := crypto.HexToECDSA(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := crypto.Sign(hash[:], key)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27
	if "0x"+hex.EncodeToString(sig) != a.Signature {
		t.Fatal("signature differs from independent vector")
	}
}

func TestImageAttestationV2ScopeTampering(t *testing.T) {
	a, _, _, wantSigner := loadSealVector(t)
	for _, tc := range []struct {
		name   string
		change func(*ImageAttestation)
	}{
		{"chain", func(a *ImageAttestation) { a.ChainID++ }},
		{"registry", func(a *ImageAttestation) { a.RegistryAddress[0]++ }},
		{"exact_app_id", func(a *ImageAttestation) { a.AppID += " " }},
		{"epoch", func(a *ImageAttestation) { a.RegisteredAt++ }},
		{"seal", func(a *ImageAttestation) { a.SealID = strings.Repeat("44", 32) }},
		{"image", func(a *ImageAttestation) { a.ImageHash = testSealImageHash }},
		{"issued_at", func(a *ImageAttestation) { a.Timestamp++ }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := a
			tc.change(&changed)
			if recoverSealSigner(t, changed) == common.HexToAddress(wantSigner) {
				t.Fatal("tampered scope retained app signer")
			}
		})
	}
	for _, version := range []uint64{0, 1, 3} {
		a.Version = version
		if _, err := a.signingDigest(); err == nil {
			t.Fatalf("accepted version %d", version)
		}
	}
}

func TestInjectSealV2UsesAppSignerAndServerScope(t *testing.T) {
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
	if a.Version != 2 || a.SealIssuer != issuer {
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

func TestInjectSealV2RejectsInvalidIssuer(t *testing.T) {
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
