package voucher

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// SandboxVoucher is the signed billing proof submitted to the smart contract.
// SandboxID is metadata only (not part of the EIP-712 struct); it is carried
// in JSON so the settler knows which sandbox to stop on failure.
type SandboxVoucher struct {
	SandboxID string         `json:"sandbox_id"`
	User      common.Address `json:"user"`
	Provider  common.Address `json:"provider"`
	TotalFee  *big.Int       `json:"total_fee"`
	UsageHash [32]byte       `json:"usage_hash"`
	Nonce     *big.Int       `json:"nonce"`
	Signature []byte         `json:"signature"`
}

// Redis key templates
const (
	VoucherQueueKeyFmt = "voucher:queue:%s" // %s = provider address (checksummed)
	VoucherDLQKeyFmt   = "voucher:dlq:%s"
	NonceKeyFmt        = "billing:nonce:%s:%s" // %s = owner, provider
	// VoucherHeldKeyFmt holds the debt backlog for one (user, provider): vouchers
	// beyond what the user's on-chain balance currently covers, parked out of the
	// settle path so the settler never submits guaranteed-reject settlements for
	// them. They are moved back to the queue when the user tops up. %s = user
	// (lowercase hex), %s = provider (lowercase hex).
	VoucherHeldKeyFmt = "voucher:held:%s:%s"
)
