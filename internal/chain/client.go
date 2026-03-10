package chain

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
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
	teeKey       *ecdsa.PrivateKey // signs vouchers (EIP-712, off-chain) and settlement txs
	providerAddr common.Address    // registered provider address (from PROVIDER_ADDRESS or TEE key)
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

	// Provider address: explicit config takes priority, otherwise derived from TEE key.
	var providerAddr common.Address
	if cfg.Chain.ProviderAddress != "" {
		providerAddr = common.HexToAddress(cfg.Chain.ProviderAddress)
	} else {
		providerAddr = crypto.PubkeyToAddress(teeKey.PublicKey)
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
		providerAddr: providerAddr,
	}, nil
}

// PrivateKey returns the TEE private key (for voucher signing).
func (c *Client) PrivateKey() *ecdsa.PrivateKey { return c.teeKey }

// ChainID returns the configured chain ID.
func (c *Client) ChainID() *big.Int { return c.chainID }

// ContractAddress returns the settlement contract address.
func (c *Client) ContractAddress() common.Address { return c.contractAddr }

// transactOpts builds a *bind.TransactOpts signed by the TEE key.
// The settlement contract no longer requires msg.sender == provider.
func (c *Client) transactOpts(ctx context.Context) (*bind.TransactOpts, error) {
	auth, err := bind.NewKeyedTransactorWithChainID(c.teeKey, c.chainID)
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
// without submitting a transaction.
func (c *Client) PreviewSettlementResults(ctx context.Context, vouchers []voucher.SandboxVoucher) ([]SettlementStatus, error) {
	opts := &bind.CallOpts{Context: ctx}
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

// VoucherEvent is a decoded VoucherSettled log from the settlement contract.
type VoucherEvent struct {
	User      common.Address
	Provider  common.Address
	TotalFee  *big.Int
	Nonce     *big.Int
	Status    SettlementStatus
	TxHash    string
	Block     uint64
	Timestamp uint64 // unix seconds (0 if unavailable)
}

// GetVoucherEvents queries VoucherSettled logs from the contract.
// lookback is the number of blocks to look back from the current latest block.
// lookback=0 means all history (from block 1).
// Returns the events, the current (latest) block number, and any error.
func (c *Client) GetVoucherEvents(ctx context.Context, lookback uint64) ([]VoucherEvent, uint64, error) {
	latest, err := c.eth.BlockNumber(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get block number: %w", err)
	}
	var fromBlock uint64 = 1
	if lookback > 0 && latest > lookback {
		fromBlock = latest - lookback
	}

	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		Addresses: []common.Address{c.contractAddr},
		Topics:    [][]common.Hash{{voucherSettledTopic}},
	}
	logs, err := c.eth.FilterLogs(ctx, query)
	if err != nil {
		return nil, latest, fmt.Errorf("FilterLogs: %w", err)
	}

	// Collect unique block numbers, then fetch their timestamps concurrently.
	blockNums := make(map[uint64]uint64) // block → timestamp
	for _, l := range logs {
		blockNums[l.BlockNumber] = 0
	}
	type tsResult struct {
		bn uint64
		ts uint64
	}
	// Use a detached context with timeout so header fetches are not cancelled
	// if the HTTP client disconnects mid-request.
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer fetchCancel()
	sem := make(chan struct{}, 20) // cap at 20 concurrent RPC calls
	ch := make(chan tsResult, len(blockNums))
	var wg sync.WaitGroup
	for bn := range blockNums {
		wg.Add(1)
		sem <- struct{}{}
		go func(bn uint64) {
			defer wg.Done()
			defer func() { <-sem }()
			hdr, err := c.eth.HeaderByNumber(fetchCtx, new(big.Int).SetUint64(bn))
			if err == nil {
				ch <- tsResult{bn, hdr.Time}
			} else {
				ch <- tsResult{bn, 0}
			}
		}(bn)
	}
	wg.Wait()
	close(ch)
	for r := range ch {
		blockNums[r.bn] = r.ts
	}

	events := make([]VoucherEvent, 0, len(logs))
	for _, l := range logs {
		ev, err := c.contract.ParseVoucherSettled(l)
		if err != nil {
			continue
		}
		events = append(events, VoucherEvent{
			User:      ev.User,
			Provider:  ev.Provider,
			TotalFee:  ev.TotalFee,
			Nonce:     ev.Nonce,
			Status:    SettlementStatus(ev.Status),
			TxHash:    l.TxHash.Hex(),
			Block:     l.BlockNumber,
			Timestamp: blockNums[l.BlockNumber],
		})
	}
	return events, latest, nil
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

// IsAcknowledged returns whether the user has acknowledged the TEE signer for
// this provider. Used by the proxy to reject start requests from users who
// have revoked acknowledgement.
func (c *Client) IsAcknowledged(ctx context.Context, user common.Address) (bool, error) {
	opts := &bind.CallOpts{Context: ctx}
	ok, err := c.contract.IsTEEAcknowledged(opts, user, c.providerAddr)
	if err != nil {
		return false, fmt.Errorf("IsTEEAcknowledged: %w", err)
	}
	return ok, nil
}

// GetBalance returns the on-chain balance for a user with a specific provider.
// Satisfies proxy.BalanceChecker.
func (c *Client) GetBalance(ctx context.Context, user, provider common.Address) (*big.Int, error) {
	balance, _, _, err := c.GetProviderBalance(ctx, user, provider)
	return balance, err
}

// GetServicePricing reads the provider's on-chain service registration and
// returns (pricePerCPUPerSec, pricePerMemGBPerSec, createFee).
// The contract stores prices per minute; this method converts to per-second.
// Returns (nil, nil, nil, nil) when the service is not yet registered.
func (c *Client) GetServicePricing(ctx context.Context, provider common.Address) (pricePerCPUPerSec, pricePerMemGBPerSec, createFee *big.Int, err error) {
	opts := &bind.CallOpts{Context: ctx}
	exists, err := c.contract.ServiceExists(opts, provider)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("ServiceExists: %w", err)
	}
	if !exists {
		return nil, nil, nil, nil
	}
	svc, err := c.contract.Services(opts, provider)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("Services: %w", err)
	}
	// Convert per-minute → per-second (integer division; truncation fine for
	// internal accounting — voucher amounts are summed over many seconds).
	cpuPerSec := new(big.Int).Div(svc.PricePerCPUPerMin, big.NewInt(60))
	memPerSec := new(big.Int).Div(svc.PricePerMemGBPerMin, big.NewInt(60))
	return cpuPerSec, memPerSec, svc.CreateFee, nil
}

// ServiceInfo holds the full on-chain service registration for a provider.
type ServiceInfo struct {
	URL                 string
	TEESignerAddress    common.Address
	PricePerCPUPerMin   *big.Int
	PricePerMemGBPerMin *big.Int
	CreateFee           *big.Int
	SignerVersion       *big.Int
}

// GetServiceInfo returns the full on-chain service data for a provider.
// Returns (nil, nil) when the service is not registered.
func (c *Client) GetServiceInfo(ctx context.Context, provider common.Address) (*ServiceInfo, error) {
	opts := &bind.CallOpts{Context: ctx}
	exists, err := c.contract.ServiceExists(opts, provider)
	if err != nil {
		return nil, fmt.Errorf("ServiceExists: %w", err)
	}
	if !exists {
		return nil, nil
	}
	svc, err := c.contract.Services(opts, provider)
	if err != nil {
		return nil, fmt.Errorf("Services: %w", err)
	}
	return &ServiceInfo{
		URL:                 svc.Url,
		TEESignerAddress:    svc.TeeSignerAddress,
		PricePerCPUPerMin:   svc.PricePerCPUPerMin,
		PricePerMemGBPerMin: svc.PricePerMemGBPerMin,
		CreateFee:           svc.CreateFee,
		SignerVersion:       svc.SignerVersion,
	}, nil
}

// GetProviderBalance returns a user's balance, pendingRefund, and refundUnlockAt
// for a specific provider.
func (c *Client) GetProviderBalance(ctx context.Context, user, provider common.Address) (balance, pendingRefund, refundUnlockAt *big.Int, err error) {
	opts := &bind.CallOpts{Context: ctx}
	result, err := c.contract.GetBalance(opts, user, provider)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("GetBalance: %w", err)
	}
	return result.Balance, result.PendingRefund, result.RefundUnlockAt, nil
}
