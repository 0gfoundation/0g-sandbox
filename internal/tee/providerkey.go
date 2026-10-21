package tee

import (
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
	// InEvidence reports whether tapp-server lists the key in the app's
	// attestation evidence (runtime_data.kms_keys). Servers ≥0.11.0 do, and
	// say so by returning the key's address; with an older one, nothing ties
	// the address to the TEE but the node's on-chain endorsement.
	InEvidence bool
}

// GetProviderKey takes the provider key from the KMS through the tapp-daemon
// (GetSecretResource under ProviderMaterial), or from
// MOCK_PROVIDER_PRIVATE_KEY when MOCK_TEE is set.
//
// Not cached: a tapp-server process lists in the evidence only the keys it
// has itself handed out, so after a tapp-server restart the key is listed
// again only once the app asks again — callers repeat the call periodically.
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

	resp, err := tapp_service.NewTappServiceClient(conn).GetSecretResource(ctx, &tapp_service.GetSecretResourceRequest{
		AppId:    appID,
		Material: ProviderMaterial,
	})
	if err != nil {
		return nil, fmt.Errorf("tee: GetSecretResource: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("tee: GetSecretResource failed: %s", resp.Message)
	}
	return providerKeyFromSecret(resp.Secret, resp.EthAddress)
}

// providerKeyFromSecret reads a KMS secret as a secp256k1 private key — the
// convention tapp's evidence relies on — and checks it against the address
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
		pk.InEvidence = true
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
