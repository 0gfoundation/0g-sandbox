package proxy

import (
	"crypto/ecdsa"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Hand-rolled EIP-712 encoding, following internal/voucher/eip712.go, for the
// documents the provider key signs for sealed containers.

var eip712DomainTypeHash = crypto.Keccak256Hash([]byte(
	"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)",
))

func eip712DomainSeparator(name, version string, chainID uint64, verifyingContract common.Address) common.Hash {
	encoded := make([]byte, 5*32)
	copy(encoded[0:32], eip712DomainTypeHash[:])
	copy(encoded[32:64], crypto.Keccak256([]byte(name)))
	copy(encoded[64:96], crypto.Keccak256([]byte(version)))
	new(big.Int).SetUint64(chainID).FillBytes(encoded[96:128])
	copy(encoded[140:160], verifyingContract[:])
	return crypto.Keccak256Hash(encoded)
}

// eip712Digest is keccak256(0x1901 || domainSeparator || structHash).
func eip712Digest(domainSeparator, structHash common.Hash) common.Hash {
	return crypto.Keccak256Hash([]byte{0x19, 0x01}, domainSeparator[:], structHash[:])
}

// putUint64Word writes v as one left-padded 32-byte ABI word.
func putUint64Word(word []byte, v uint64) {
	binary.BigEndian.PutUint64(word[24:32], v)
}

// signDigest signs digest raw, without an EIP-191 prefix, and returns the
// 0x-prefixed r||s||v signature with V normalised to 27/28.
func signDigest(digest common.Hash, key *ecdsa.PrivateKey) (string, error) {
	sig, err := crypto.Sign(digest[:], key)
	if err != nil {
		return "", fmt.Errorf("sign digest: %w", err)
	}
	sig[64] += 27
	return "0x" + hex.EncodeToString(sig), nil
}
