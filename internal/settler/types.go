package settler

import (
	"context"

	"github.com/ethereum/go-ethereum/common"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// StopSignal carries the reason a sandbox should be stopped.
type StopSignal struct {
	SandboxID string
	Reason    string // "insufficient_balance" | "not_acknowledged"
}

// ChainClient submits signed vouchers to the settlement contract.
// Satisfied by *chain.Client; decoupled here so the settler can be tested
// without a live RPC connection.
type ChainClient interface {
	SettleFeesWithTEE(ctx context.Context, vouchers []voucher.SandboxVoucher) ([]chain.SettlementStatus, error)
	// ProviderAddress is this deployment's provider identity (= the TEE
	// signer address); it keys the voucher queue the settler drains.
	ProviderAddress() common.Address
	// IsLocalTEEActiveNode reports whether our signer is currently a
	// registered node of the app in TappRegistry. While false, the settler
	// holds the queue instead of submitting: after a machine rebuild the
	// new signer's vouchers would all settle INVALID_SIGNATURE until the
	// operator runs add-node-onchain — holding them avoids burning gas and
	// dead-lettering real revenue during that window.
	IsLocalTEEActiveNode(ctx context.Context) (bool, error)
}

// NonceSigner assigns a monotone nonce and cryptographically signs a voucher
// in place. Satisfied by *billing.Signer; decoupled here to avoid import cycles.
// The settler is single-threaded, so calling Sign sequentially guarantees
// strict nonce ordering even under concurrent OnCreate goroutines.
type NonceSigner interface {
	Sign(ctx context.Context, v *voucher.SandboxVoucher) error
}
