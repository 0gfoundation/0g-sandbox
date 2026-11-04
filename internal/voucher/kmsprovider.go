package voucher

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var kmsProviderTypeHash = crypto.Keccak256Hash([]byte("KmsProvider(address provider,string appId)"))

// KmsProviderDigest is the EIP-712 digest a node signs to endorse `provider`
// as a KMS provider of `appId` — SandboxServing.kmsProviderDigest, under the
// same domain as vouchers.
func KmsProviderDigest(chainID *big.Int, contractAddr, provider common.Address, appId string) [32]byte {
	encoded := make([]byte, 3*32)
	copy(encoded[0:32], kmsProviderTypeHash[:])
	copy(encoded[44:64], provider.Bytes())
	appIdHash := crypto.Keccak256Hash([]byte(appId))
	copy(encoded[64:96], appIdHash[:])
	structHash := crypto.Keccak256Hash(encoded)

	sep := domainSeparator(chainID, contractAddr)
	msg := make([]byte, 2+32+32)
	msg[0] = 0x19
	msg[1] = 0x01
	copy(msg[2:34], sep[:])
	copy(msg[34:66], structHash[:])
	return crypto.Keccak256Hash(msg)
}

// EndorseKmsProvider signs the endorsement with the node's TEE key; the
// signature is what addOrUpdateKmsService takes.
func EndorseKmsProvider(nodeKey *ecdsa.PrivateKey, chainID *big.Int, contractAddr, provider common.Address, appId string) ([]byte, error) {
	digest := KmsProviderDigest(chainID, contractAddr, provider, appId)
	sig, err := crypto.Sign(digest[:], nodeKey)
	if err != nil {
		return nil, err
	}
	sig[64] += 27 // Solidity ecrecover convention
	return sig, nil
}

// KmsProviderEndorser recovers the node that signed an endorsement.
func KmsProviderEndorser(sig []byte, chainID *big.Int, contractAddr, provider common.Address, appId string) (common.Address, error) {
	if len(sig) != 65 {
		return common.Address{}, fmt.Errorf("endorsement is %d bytes, want 65", len(sig))
	}
	digest := KmsProviderDigest(chainID, contractAddr, provider, appId)
	s := make([]byte, 65)
	copy(s, sig)
	if s[64] >= 27 {
		s[64] -= 27
	}
	pub, err := crypto.SigToPub(digest[:], s)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(*pub), nil
}
