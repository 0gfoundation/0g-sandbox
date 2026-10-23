package tee

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/0gfoundation/0g-sandbox/internal/tee/tapp_service"
)

// ProviderMaterial is the KMS derivation material of the provider key:
// hex("provider"). The KMS derives the key as DPRF(master, app_id ‖
// material), and its address is the provider every balance, nonce and
// service is keyed by — changing this constant changes the provider.
const ProviderMaterial = "70726f7669646572"

// ProviderKey is the provider key the app takes from the KMS: the same on
// every boot and every node of the app, unlike the node signer (AppKey),
// which tapp makes anew whenever tapp-server restarts.
type ProviderKey struct {
	// PrivateKeyHex is the secp256k1 private key, lowercase hex without "0x".
	PrivateKeyHex string
	Address       common.Address
	// InEvidence is true only when GetAppSigner explicitly confirms that the
	// typed identity and commitment were recorded. A returned address alone
	// never establishes that a key appears in evidence.
	InEvidence bool
}

// GetProviderKey takes the provider key from the KMS through the tapp-daemon
// (GetAppSigner under ProviderMaterial), or from
// MOCK_PROVIDER_PRIVATE_KEY when MOCK_TEE is set.
//
// tapp restores this boot's measured identities after process restarts; no
// periodic secret fetch is required to keep the identity in evidence.
func GetProviderKey(ctx context.Context) (*ProviderKey, error) {
	if os.Getenv("MOCK_TEE") != "" {
		return providerKeyMock()
	}
	return providerKeyGRPC(ctx)
}

func providerKeyMock() (*ProviderKey, error) {
	raw := strings.TrimPrefix(os.Getenv("MOCK_PROVIDER_PRIVATE_KEY"), "0x")
	if raw == "" {
		return nil, fmt.Errorf("tee: MOCK_TEE is set but MOCK_PROVIDER_PRIVATE_KEY is empty")
	}
	secret, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("tee: MOCK_PROVIDER_PRIVATE_KEY: %w", err)
	}
	return providerKeyFromSecret(secret, nil)
}

func providerKeyGRPC(ctx context.Context) (*ProviderKey, error) {
	appID := os.Getenv("BACKEND_APP_NAME")
	if appID == "" {
		return nil, fmt.Errorf("tee: BACKEND_APP_NAME is required for the KMS provider key")
	}
	conn, err := dialTapp()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	resp, err := tapp_service.NewTappServiceClient(conn).GetAppSigner(ctx, &tapp_service.GetAppSignerRequest{
		AppId: appID,
	})
	if err != nil {
		return nil, fmt.Errorf("tee: GetAppSigner: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("tee: GetAppSigner failed: %s", resp.Message)
	}
	if resp.KeySource != "kms" || resp.Derivation != "raw-scalar-v1" || len(resp.EthAddress) != common.AddressLength {
		return nil, fmt.Errorf("tee: unexpected app signer source or derivation")
	}
	pk, err := providerKeyFromSecret(resp.Secret, resp.EthAddress)
	if err != nil {
		return nil, err
	}
	publicKey, err := crypto.DecompressPubkey(resp.PublicKey)
	if err != nil || crypto.PubkeyToAddress(*publicKey) != pk.Address {
		return nil, fmt.Errorf("tee: app signer public key does not match the secret/address")
	}
	if !resp.MeasuredInEvidence || !canonicalCommitment(resp.Commitment) || resp.CommitmentScheme != "poseidon-bn254-4-v1" {
		return nil, fmt.Errorf("tee: app signer was not recorded in attestation evidence")
	}
	pk.InEvidence = true
	return pk, nil
}

func canonicalCommitment(commitment []byte) bool {
	// Poseidon output is a canonical BN254 scalar, encoded as 32-byte big endian.
	modulus, _ := hex.DecodeString("30644e72e131a029b85045b68181585d2833e84879b9709143e1f593f0000001")
	return len(commitment) == 32 && bytes.Compare(commitment, modulus) < 0
}

// providerKeyFromSecret reads a KMS secret as a secp256k1 private key — the
// dedicated application-signer convention — and checks it against the address
// tapp-server computed from the same secret, when it sent one.
func providerKeyFromSecret(secret, ethAddress []byte) (*ProviderKey, error) {
	if len(secret) != 32 {
		return nil, fmt.Errorf("tee: KMS secret is %d bytes, want 32", len(secret))
	}
	key, err := crypto.ToECDSA(secret)
	if err != nil {
		return nil, fmt.Errorf("tee: KMS secret is not a secp256k1 private key: %w", err)
	}
	pk := &ProviderKey{
		PrivateKeyHex: hex.EncodeToString(secret),
		Address:       crypto.PubkeyToAddress(key.PublicKey),
	}
	if len(ethAddress) > 0 {
		if len(ethAddress) != common.AddressLength || common.BytesToAddress(ethAddress) != pk.Address {
			return nil, fmt.Errorf("tee: tapp-server lists the KMS key as 0x%x, but the secret is %s's",
				ethAddress, pk.Address.Hex())
		}
	}
	return pk, nil
}

func dialTapp() (*grpc.ClientConn, error) {
	target := tappTarget()
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("tee: grpc dial %s: %w", target, err)
	}
	return conn, nil
}
