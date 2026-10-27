package tee

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc"

	"github.com/0gfoundation/0g-sandbox/internal/tee/tapp_service"
)

func secretOf(last byte) []byte {
	s := make([]byte, 32)
	s[31] = last
	return s
}

// keyOneAddress is the address of private key 1 — the vector 0g-tapp's own
// eth_identity_of_secret test pins, so both sides read a secret the same way.
var keyOneAddress = common.HexToAddress("0x7e5f4552091a69125d5dfcb7b8c2659029395bdf")

func TestProviderMaterialIsHexOfProvider(t *testing.T) {
	if got := hex.EncodeToString([]byte("provider")); got != ProviderMaterial {
		t.Fatalf("ProviderMaterial = %s, hex(\"provider\") = %s", ProviderMaterial, got)
	}
}

func TestCanonicalCommitment(t *testing.T) {
	modulus, _ := hex.DecodeString("30644e72e131a029b85045b68181585d2833e84879b9709143e1f593f0000001")
	if canonicalCommitment(modulus) || canonicalCommitment(modulus[1:]) {
		t.Fatal("noncanonical field element accepted")
	}
	modulus[31]--
	if !canonicalCommitment(modulus) {
		t.Fatal("largest canonical field element rejected")
	}
}

func TestProviderKeyFromSecret(t *testing.T) {
	pk, err := providerKeyFromSecret(secretOf(1), keyOneAddress.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if pk.Address != keyOneAddress || pk.InEvidence {
		t.Fatalf("got %s in evidence=%v", pk.Address.Hex(), pk.InEvidence)
	}
	if pk.PrivateKeyHex != hex.EncodeToString(secretOf(1)) {
		t.Fatalf("private key hex %s", pk.PrivateKeyHex)
	}

	// An older tapp-server sends no address: usable, but not in the evidence.
	pk, err = providerKeyFromSecret(secretOf(1), nil)
	if err != nil || pk.InEvidence || pk.Address != keyOneAddress {
		t.Fatalf("no address: %+v, %v", pk, err)
	}

	// tapp-server computed another address from the same secret.
	if _, err := providerKeyFromSecret(secretOf(1), common.HexToAddress("0x01").Bytes()); err == nil {
		t.Fatal("mismatched address accepted")
	}

	order, _ := hex.DecodeString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	for name, secret := range map[string][]byte{
		"zero":        make([]byte, 32),
		"curve order": order,
		"31 bytes":    make([]byte, 31),
		"empty":       nil,
	} {
		if _, err := providerKeyFromSecret(secret, nil); err == nil {
			t.Errorf("%s: accepted as a private key", name)
		}
	}
}

type fakeTapp struct {
	tapp_service.UnimplementedTappServiceServer
	got      *tapp_service.GetAppSignerRequest
	measured bool
}

func (f *fakeTapp) GetAppSigner(_ context.Context, req *tapp_service.GetAppSignerRequest) (*tapp_service.GetAppSignerResponse, error) {
	f.got = req
	key, _ := crypto.ToECDSA(secretOf(1))
	return &tapp_service.GetAppSignerResponse{Success: true, Secret: secretOf(1), EthAddress: keyOneAddress.Bytes(),
		PublicKey: crypto.CompressPubkey(&key.PublicKey), KeySource: "kms", Derivation: "raw-scalar-v1",
		Commitment: make([]byte, 32), CommitmentScheme: "poseidon-bn254-4-v1", MeasuredInEvidence: f.measured}, nil
}

func TestGetProviderKeyAsksTheKMSForTheProviderMaterial(t *testing.T) {
	// Short path: a unix socket path is capped near 104 bytes on macOS.
	dir, err := os.MkdirTemp("", "tapp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	fake := &fakeTapp{measured: true}
	tapp_service.RegisterTappServiceServer(srv, fake)
	go srv.Serve(lis) //nolint:errcheck
	t.Cleanup(srv.Stop)

	t.Setenv("MOCK_TEE", "")
	t.Setenv("BACKEND_TAPP_SOCKET", sock)
	t.Setenv("BACKEND_APP_NAME", "sandbox-test")

	pk, err := GetProviderKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pk.Address != keyOneAddress || !pk.InEvidence {
		t.Fatalf("got %+v", pk)
	}
	if fake.got.AppId != "sandbox-test" {
		t.Fatalf("asked for app %q", fake.got.AppId)
	}
	fake.measured = false
	if _, err := GetProviderKey(context.Background()); err == nil {
		t.Fatal("nonempty address incorrectly treated as measured evidence")
	}

}

func TestGetProviderKeyMock(t *testing.T) {
	t.Setenv("MOCK_TEE", "true")
	t.Setenv("MOCK_PROVIDER_PRIVATE_KEY", "0x"+hex.EncodeToString(secretOf(1)))
	pk, err := GetProviderKey(context.Background())
	if err != nil || pk.Address != keyOneAddress {
		t.Fatalf("got %+v, %v", pk, err)
	}

	t.Setenv("MOCK_PROVIDER_PRIVATE_KEY", "")
	if _, err := GetProviderKey(context.Background()); err == nil || !strings.Contains(err.Error(), "MOCK_PROVIDER_PRIVATE_KEY") {
		t.Fatalf("missing mock key: %v", err)
	}
}
