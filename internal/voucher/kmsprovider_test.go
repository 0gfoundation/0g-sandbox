package voucher

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// TestKmsProviderDigestMatchesEIP712 checks the hand-rolled encoding against
// go-ethereum's typed-data hasher, from the type string alone.
func TestKmsProviderDigestMatchesEIP712(t *testing.T) {
	chainID := big.NewInt(16602)
	contract := common.HexToAddress("0x24fdd3d6a5f1f5b1b0a0f6e3d6bba4d0e5a4c0a1")
	provider := common.HexToAddress("0x7e5f4552091a69125d5dfcb7b8c2659029395bdf")
	appId := "0g-sandbox-provider"

	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"KmsProvider": {
				{Name: "provider", Type: "address"},
				{Name: "appId", Type: "string"},
			},
		},
		PrimaryType: "KmsProvider",
		Domain: apitypes.TypedDataDomain{
			Name:              "0G Sandbox Serving",
			Version:           "1",
			ChainId:           (*math.HexOrDecimal256)(chainID),
			VerifyingContract: contract.Hex(),
		},
		Message: apitypes.TypedDataMessage{
			"provider": provider.Hex(),
			"appId":    appId,
		},
	}
	want, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		t.Fatal(err)
	}
	got := KmsProviderDigest(chainID, contract, provider, appId)
	if !bytes.Equal(got[:], want) {
		t.Fatalf("digest %x, EIP-712 says %x", got, want)
	}
}

const (
	kmsVectorContract = "0x5991A2dF15A8F6A256D3Ec51E99254Cd3fb576A9"
	kmsVectorProvider = "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"
	kmsVectorDigest   = "0xf1a05d887d3bfa532429d95d0e9e270da85bc96e28217f12c54518beee1e3cb7"
)

// TestKmsProviderDigestVector pins the digest SandboxServing.t.sol's
// test_Kms_DigestVector asserts on chain (forge's chain 31337, the test
// proxy's address, the test KMS key, the test appId), so Go and Solidity
// cannot drift apart.
func TestKmsProviderDigestVector(t *testing.T) {
	got := KmsProviderDigest(
		big.NewInt(31337),
		common.HexToAddress(kmsVectorContract),
		common.HexToAddress(kmsVectorProvider),
		"sandbox-test-app",
	)
	if common.Hash(got).Hex() != kmsVectorDigest {
		t.Fatalf("digest %s, the contract's is %s", common.Hash(got).Hex(), kmsVectorDigest)
	}
}

func TestEndorseKmsProviderRoundTrip(t *testing.T) {
	node, _ := crypto.GenerateKey()
	chainID := big.NewInt(16602)
	contract := common.HexToAddress("0x01")
	provider := common.HexToAddress("0x02")

	sig, err := EndorseKmsProvider(node, chainID, contract, provider, "app")
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] != 27 && sig[64] != 28 {
		t.Fatalf("v = %d, want 27/28", sig[64])
	}
	got, err := KmsProviderEndorser(sig, chainID, contract, provider, "app")
	if err != nil || got != crypto.PubkeyToAddress(node.PublicKey) {
		t.Fatalf("recovered %s, %v", got.Hex(), err)
	}
	// Any other provider or app recovers someone else.
	if other, _ := KmsProviderEndorser(sig, chainID, contract, common.HexToAddress("0x03"), "app"); other == got {
		t.Fatal("endorsement valid for another provider")
	}
	if other, _ := KmsProviderEndorser(sig, chainID, contract, provider, "other"); other == got {
		t.Fatal("endorsement valid for another app")
	}
}
