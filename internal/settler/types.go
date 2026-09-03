package settler

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

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
	// SubmitSettleFees broadcasts without waiting; the settler persists the
	// returned tx before its fate is known (see PendingTxKeyFmt) so a
	// WaitMined failure or crash can never lead to re-signing usage that the
	// original tx later settles (double charge).
	SubmitSettleFees(ctx context.Context, vouchers []voucher.SandboxVoucher) (*types.Transaction, error)
	fateResolver
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
	// GetBalanceBatch is the read-only balance call the pre-settle sweep uses
	// to split a backlog into an affordable aggregate + held debt (no gas).
	GetBalanceBatch(ctx context.Context, users []common.Address, provider common.Address) ([]*big.Int, error)
}

// NonceSigner assigns a monotone nonce and cryptographically signs a voucher
// in place. Satisfied by *billing.Signer; decoupled here to avoid import cycles.
// The settler is single-threaded, so calling Sign sequentially guarantees
// strict nonce ordering even under concurrent OnCreate goroutines.
type NonceSigner interface {
	Sign(ctx context.Context, v *voucher.SandboxVoucher) error
}
