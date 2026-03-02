package chain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox-billing/internal/config"
	"github.com/0gfoundation/0g-sandbox-billing/internal/voucher"
)

// SettlementStatus mirrors the Solidity enum (same ordinal values).
type SettlementStatus uint8

const (
	StatusSuccess SettlementStatus = iota
	StatusInsufficientBalance
	StatusProviderMismatch
	StatusNotAcknowledged
	StatusInvalidNonce
	StatusInvalidSignature
)

func (s SettlementStatus) String() string {
	switch s {
	case StatusSuccess:
		return "SUCCESS"
	case StatusInsufficientBalance:
		return "INSUFFICIENT_BALANCE"
	case StatusProviderMismatch:
		return "PROVIDER_MISMATCH"
	case StatusNotAcknowledged:
		return "NOT_ACKNOWLEDGED"
	case StatusInvalidNonce:
		return "INVALID_NONCE"
	case StatusInvalidSignature:
		return "INVALID_SIGNATURE"
	default:
		return "UNKNOWN"
	}
}

// Client wraps go-ethereum and the generated SandboxServing binding.
type Client struct {
	eth          *ethclient.Client
	contract     *SandboxServing
	contractAddr common.Address
	chainID      *big.Int
	teeKey       *ecdsa.PrivateKey // signs vouchers (EIP-712, off-chain)
	providerKey  *ecdsa.PrivateKey // sends settlement txs (msg.sender == provider)
}

func NewClient(cfg *config.Config) (*Client, error) {
	eth, err := ethclient.Dial(cfg.Chain.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}

	teeKey, err := crypto.HexToECDSA(cfg.Chain.TEEPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse tee private key: %w", err)
	}

	// Provider key signs settlement txs (msg.sender must equal v.provider).
	// Falls back to TEE key when not set (e.g. single-key setups where TEE == provider).
	providerKey := teeKey
	if cfg.Chain.ProviderPrivateKey != "" {
		providerKey, err = crypto.HexToECDSA(cfg.Chain.ProviderPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("parse provider private key: %w", err)
		}
	}

	addr := common.HexToAddress(cfg.Chain.ContractAddress)
	contract, err := NewSandboxServing(addr, eth)
	if err != nil {
		return nil, fmt.Errorf("bind contract: %w", err)
	}

	return &Client{
		eth:          eth,
		contract:     contract,
		contractAddr: addr,
		chainID:      big.NewInt(cfg.Chain.ChainID),
		teeKey:       teeKey,
		providerKey:  providerKey,
	}, nil
}

// PrivateKey returns the TEE private key (for voucher signing).
func (c *Client) PrivateKey() *ecdsa.PrivateKey { return c.teeKey }

// ChainID returns the configured chain ID.
func (c *Client) ChainID() *big.Int { return c.chainID }

// ContractAddress returns the settlement contract address.
func (c *Client) ContractAddress() common.Address { return c.contractAddr }

// transactOpts builds a *bind.TransactOpts signed by the provider key.
// The provider key's address must equal v.provider in the settlement contract.
func (c *Client) transactOpts(ctx context.Context) (*bind.TransactOpts, error) {
	auth, err := bind.NewKeyedTransactorWithChainID(c.providerKey, c.chainID)
	if err != nil {
		return nil, err
	}
	auth.Context = ctx
	return auth, nil
}

// toContractVouchers converts internal vouchers to the ABI-generated struct.
func toContractVouchers(vs []voucher.SandboxVoucher) []SandboxServingSandboxVoucher {
	out := make([]SandboxServingSandboxVoucher, len(vs))
	for i, v := range vs {
		out[i] = SandboxServingSandboxVoucher{
			User:      v.User,
			Provider:  v.Provider,
			TotalFee:  v.TotalFee,
			UsageHash: v.UsageHash,
			Nonce:     v.Nonce,
			Signature: v.Signature,
		}
	}
	return out
}

// voucherSettledTopic is keccak256("VoucherSettled(address,address,uint256,bytes32,uint256,uint8)").
// Used to identify VoucherSettled logs in a tx receipt.
var voucherSettledTopic = crypto.Keccak256Hash([]byte("VoucherSettled(address,address,uint256,bytes32,uint256,uint8)"))

// SettleFeesWithTEE submits a batch of signed vouchers to the contract and
// returns per-voucher settlement statuses.
//
// Statuses are recovered in two steps:
//  1. Parse VoucherSettled events from the receipt — the contract emits these
//     for SUCCESS and INSUFFICIENT_BALANCE (after the nonce is committed).
//  2. For vouchers that emitted no event (PROVIDER_MISMATCH, NOT_ACKNOWLEDGED,
//     INVALID_NONCE, INVALID_SIGNATURE — all return before the nonce commit),
//     call PreviewSettlementResults with the original vouchers.  Because the
//     nonce was never committed, the view function still evaluates correctly.
func (c *Client) SettleFeesWithTEE(ctx context.Context, vouchers []voucher.SandboxVoucher) ([]SettlementStatus, error) {
	opts, err := c.transactOpts(ctx)
	if err != nil {
		return nil, fmt.Errorf("build tx opts: %w", err)
	}

	tx, err := c.contract.SettleFeesWithTEE(opts, toContractVouchers(vouchers))
	if err != nil {
		return nil, fmt.Errorf("SettleFeesWithTEE tx: %w", err)
	}

	receipt, err := bind.WaitMined(ctx, c.eth, tx)
	if err != nil {
		return nil, fmt.Errorf("wait mined: %w", err)
	}
	if receipt.Status == 0 {
		return nil, fmt.Errorf("tx reverted: %s", tx.Hash().Hex())
	}

	// Step 1: parse VoucherSettled events → (user, nonce) → status.
	type voucherKey struct{ user, nonce string }
	fromEvent := make(map[voucherKey]SettlementStatus)
	for _, log := range receipt.Logs {
		if log.Address != c.contractAddr {
			continue
		}
		if len(log.Topics) == 0 || log.Topics[0] != voucherSettledTopic {
			continue
		}
		ev, err := c.contract.ParseVoucherSettled(*log)
		if err != nil {
			continue
		}
		fromEvent[voucherKey{ev.User.Hex(), ev.Nonce.String()}] = SettlementStatus(ev.Status)
	}

	// Step 2: assign statuses; collect vouchers that emitted no event.
	statuses := make([]SettlementStatus, len(vouchers))
	var missingIdx []int
	var missingVouchers []voucher.SandboxVoucher
	for i, v := range vouchers {
		key := voucherKey{v.User.Hex(), v.Nonce.String()}
		if s, ok := fromEvent[key]; ok {
			statuses[i] = s
		} else {
			missingIdx = append(missingIdx, i)
			missingVouchers = append(missingVouchers, v)
		}
	}

	// Step 3: preview the no-event vouchers to get the specific failure reason.
	if len(missingVouchers) > 0 {
		fallback, err := c.PreviewSettlementResults(ctx, missingVouchers)
		if err != nil {
			return nil, fmt.Errorf("preview no-event vouchers: %w", err)
		}
		for j, i := range missingIdx {
			statuses[i] = fallback[j]
		}
	}

	return statuses, nil
}

// PreviewSettlementResults calls the view function to check expected statuses
// without submitting a transaction.  From must be set to the provider address
// so msg.sender passes the provider check inside _previewOne.
func (c *Client) PreviewSettlementResults(ctx context.Context, vouchers []voucher.SandboxVoucher) ([]SettlementStatus, error) {
	providerAddr := crypto.PubkeyToAddress(c.providerKey.PublicKey) // provider key's address
	opts := &bind.CallOpts{Context: ctx, From: providerAddr}
	raw, err := c.contract.PreviewSettlementResults(opts, toContractVouchers(vouchers))
	if err != nil {
		return nil, fmt.Errorf("PreviewSettlementResults: %w", err)
	}
	statuses := make([]SettlementStatus, len(raw))
	for i, s := range raw {
		statuses[i] = SettlementStatus(s)
	}
	return statuses, nil
}

// GetLastNonce returns the last settled nonce for a (user, provider) pair from the contract.
func (c *Client) GetLastNonce(ctx context.Context, user, provider common.Address) (*big.Int, error) {
	opts := &bind.CallOpts{Context: ctx}
	n, err := c.contract.GetLastNonce(opts, user, provider)
	if err != nil {
		return nil, fmt.Errorf("GetLastNonce: %w", err)
	}
	return n, nil
}

// GetBalance returns only the on-chain balance for a user (satisfies proxy.BalanceChecker).
func (c *Client) GetBalance(ctx context.Context, user common.Address) (*big.Int, error) {
	balance, _, _, err := c.GetAccount(ctx, user)
	return balance, err
}

// GetAccount returns a user's balance, pendingRefund, and refundUnlockAt.
func (c *Client) GetAccount(ctx context.Context, user common.Address) (balance, pendingRefund, refundUnlockAt *big.Int, err error) {
	opts := &bind.CallOpts{Context: ctx}
	result, err := c.contract.GetAccount(opts, user)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("GetAccount: %w", err)
	}
	return result.Balance, result.PendingRefund, result.RefundUnlockAt, nil
}
