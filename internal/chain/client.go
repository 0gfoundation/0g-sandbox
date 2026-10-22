package chain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/0gfoundation/0g-sandbox/internal/config"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
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

// Client wraps go-ethereum and the generated SandboxServing + TappRegistry bindings.
type Client struct {
	eth          *ethclient.Client
	contract     *SandboxServing
	contractAddr common.Address
	tapp         *TappRegistry
	tappAddr     common.Address
	chainID      *big.Int
	nodeKey      *ecdsa.PrivateKey // the node's TEE signer: sealed attestations, KMS provider endorsement
	providerKey  *ecdsa.PrivateKey // signs vouchers (EIP-712, off-chain) and settlement txs
	providerAddr common.Address    // = providerKey's address
	kmsProvider  bool              // providerKey is the app's KMS key, not the node signer

	blockTimeMu  sync.Mutex
	blockTimeSec float64   // cached avg block time in seconds
	blockTimeAt  time.Time // when the cache was populated
}

func NewClient(cfg *config.Config) (*Client, error) {
	eth, err := ethclient.Dial(cfg.Chain.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial rpc: %w", err)
	}

	nodeKey, err := crypto.HexToECDSA(cfg.Chain.TEEPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse tee private key: %w", err)
	}

	// Provider identity: the provider address — voucher payee (EIP-712),
	// settler queue key, services key, balance bucket, settlement tx sender —
	// is the address of the key that signs vouchers. That key is the app's
	// KMS key when one was taken (PROVIDER_KEY_SOURCE=kms: the same on every
	// boot), else the node's TEE signer, which rotates with tapp-server. There
	// is no separately configured provider wallet either way; management
	// belongs to the appId owner.
	providerKey, kmsProvider := nodeKey, false
	if cfg.Chain.ProviderPrivateKey != "" {
		providerKey, err = crypto.HexToECDSA(cfg.Chain.ProviderPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("parse provider private key: %w", err)
		}
		kmsProvider = true
	}
	providerAddr := crypto.PubkeyToAddress(providerKey.PublicKey)

	addr := common.HexToAddress(cfg.Chain.ContractAddress)
	contract, err := NewSandboxServing(addr, eth)
	if err != nil {
		return nil, fmt.Errorf("bind contract: %w", err)
	}

	// TappRegistry binding — used by voucher verification (node membership)
	// and by user-ack lookups. Required because SandboxServing now delegates
	// signer identity + ack state to tapp.
	if cfg.Chain.TappRegistry == "" {
		return nil, fmt.Errorf("TAPP_REGISTRY is required")
	}
	tappAddr := common.HexToAddress(cfg.Chain.TappRegistry)
	tapp, err := NewTappRegistry(tappAddr, eth)
	if err != nil {
		return nil, fmt.Errorf("bind tapp: %w", err)
	}

	return &Client{
		eth:          eth,
		contract:     contract,
		contractAddr: addr,
		tapp:         tapp,
		tappAddr:     tappAddr,
		chainID:      big.NewInt(cfg.Chain.ChainID),
		nodeKey:      nodeKey,
		providerKey:  providerKey,
		providerAddr: providerAddr,
		kmsProvider:  kmsProvider,
	}, nil
}

// TappRegistryAddress returns the on-chain TappRegistry address this client
// is bound to. Useful for dashboard / CLI to display the trust root location.
func (c *Client) TappRegistryAddress() common.Address { return c.tappAddr }

// ProviderKey returns the key that signs vouchers and settlement txs, and
// whose address is the provider.
func (c *Client) ProviderKey() *ecdsa.PrivateKey { return c.providerKey }

// NodeKey returns the node's TEE signer — the address TappRegistry lists as
// a node of the app. It signs what must come from a node: sealed-container
// attestations, and the endorsement of a KMS provider. Without a KMS
// provider it is also the provider key.
func (c *Client) NodeKey() *ecdsa.PrivateKey { return c.nodeKey }

// NodeAddress returns the node's TEE signer address.
func (c *Client) NodeAddress() common.Address { return crypto.PubkeyToAddress(c.nodeKey.PublicKey) }

// KmsProvider reports whether the provider is the app's KMS key rather than
// the node signer.
func (c *Client) KmsProvider() bool { return c.kmsProvider }

// ChainID returns the configured chain ID.
func (c *Client) ChainID() *big.Int { return c.chainID }

// ContractAddress returns the settlement contract address.
func (c *Client) ContractAddress() common.Address { return c.contractAddr }

// SettlerAddress is the EOA the settler uses to submit SettleFeesWithTEE txs:
// the provider key's address — the key that signs vouchers also pays for
// on-chain settlement.
func (c *Client) SettlerAddress() common.Address {
	return crypto.PubkeyToAddress(c.providerKey.PublicKey)
}

// ProviderAddress returns this deployment's on-chain provider identity: the
// provider key's address (see NewClient). It keys the voucher payee field,
// the settler queue, and all provider-bound chain lookups.
func (c *Client) ProviderAddress() common.Address { return c.providerAddr }

// BalanceAt returns the latest balance for addr. Wraps eth.BalanceAt(nil)
// so monitor code doesn't need direct access to the embedded ethclient.
func (c *Client) BalanceAt(ctx context.Context, addr common.Address) (*big.Int, error) {
	return c.eth.BalanceAt(ctx, addr, nil)
}

// SuggestGasPrice returns the RPC's gas price suggestion. Used by the balance
// monitor to derive a "1 settle tx worth of gas" threshold.
func (c *Client) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return c.eth.SuggestGasPrice(ctx)
}

// GetServiceAppId returns the appId bound to a provider's SandboxServing service.
// Empty string when no service is registered.
func (c *Client) GetServiceAppId(ctx context.Context, provider common.Address) (string, error) {
	opts := &bind.CallOpts{Context: ctx}
	svc, err := c.contract.Services(opts, provider)
	if err != nil {
		return "", fmt.Errorf("Services: %w", err)
	}
	return svc.AppId, nil
}

// GetAppOwner returns the current owner of appId in TappRegistry, or the zero
// address if the app is not registered. TappRegistry is the source of truth
// for who currently owns an app; the owner is the only wallet that can
// register/remove services and withdraw earnings in SandboxServing.
func (c *Client) GetAppOwner(ctx context.Context, appId string) (common.Address, error) {
	opts := &bind.CallOpts{Context: ctx}
	info, err := c.tapp.GetAppInfo(opts, appId)
	if err != nil {
		return common.Address{}, fmt.Errorf("tapp.GetAppInfo: %w", err)
	}
	return info.Owner, nil
}

// GetAppInstance reads the registration epoch and owner from one registry call.
func (c *Client) GetAppInstance(ctx context.Context, appId string) (uint64, common.Address, error) {
	info, err := c.tapp.GetAppInfo(&bind.CallOpts{Context: ctx}, appId)
	if err != nil {
		return 0, common.Address{}, fmt.Errorf("tapp.GetAppInfo: %w", err)
	}
	if info.RegisteredAt == nil || !info.RegisteredAt.IsUint64() {
		return 0, common.Address{}, fmt.Errorf("invalid app registration epoch")
	}
	return info.RegisteredAt.Uint64(), info.Owner, nil
}

// IsActiveNode returns true if `signer` is an active node for `appId` in
// TappRegistry. Equivalent to `tapp.getNode(appId, signer).addedAt != 0`,
// surfaced as a typed helper for downstream voucher/session verification.
func (c *Client) IsActiveNode(ctx context.Context, appId string, signer common.Address) (bool, error) {
	opts := &bind.CallOpts{Context: ctx}
	node, err := c.tapp.GetNode(opts, appId, signer)
	if err != nil {
		return false, fmt.Errorf("tapp.GetNode: %w", err)
	}
	return node.AddedAt.Sign() != 0, nil
}

// IsProviderActive reports whether this deployment's provider may settle
// right now, by the rule settlement applies: a KMS provider must have a
// service bound to its app's current instance (SandboxServing
// isActiveProvider); a node-signer provider must be an active node of the
// app its service is bound to. Returns (false, nil) when no service is bound
// yet — a legitimate "not yet ready" state.
func (c *Client) IsProviderActive(ctx context.Context) (bool, error) {
	if c.kmsProvider {
		return c.contract.IsActiveProvider(&bind.CallOpts{Context: ctx}, c.providerAddr)
	}
	// The node-signer rule, read from TappRegistry directly so it also holds
	// against a SandboxServing that predates isActiveProvider.
	appId, err := c.GetServiceAppId(ctx, c.providerAddr)
	if err != nil {
		return false, err
	}
	if appId == "" {
		return false, nil
	}
	return c.IsActiveNode(ctx, appId, c.providerAddr)
}

// IsActiveProvider reports whether `provider` has a service whose own key may
// sign vouchers right now (SandboxServing isActiveProvider): the check the
// broker applies to the providers it indexes and the requests they sign.
// Against a SandboxServing that predates isActiveProvider (the call reverts)
// it falls back to the node-signer rule — the only kind of provider such a
// contract has.
func (c *Client) IsActiveProvider(ctx context.Context, provider common.Address) (bool, error) {
	ok, err := c.contract.IsActiveProvider(&bind.CallOpts{Context: ctx}, provider)
	if err == nil {
		return ok, nil
	}
	if !strings.Contains(err.Error(), "execution reverted") {
		return false, fmt.Errorf("isActiveProvider: %w", err)
	}
	appId, aerr := c.GetServiceAppId(ctx, provider)
	if aerr != nil {
		return false, aerr
	}
	if appId == "" {
		return false, nil
	}
	return c.IsActiveNode(ctx, appId, provider)
}

// KmsBinding returns the app instance a KMS provider is bound to — the
// TappRegistry registeredAt it was pinned to (0 when `provider` is not a KMS
// provider) and keccak256 of its appId.
func (c *Client) KmsBinding(ctx context.Context, provider common.Address) (*big.Int, [32]byte, error) {
	b, err := c.contract.KmsBindings(&bind.CallOpts{Context: ctx}, provider)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("kmsBindings: %w", err)
	}
	return b.Epoch, b.AppIdHash, nil
}

// KmsProviderEndorsement is this node's endorsement of the provider key as a
// KMS provider of `appId`: the signature addOrUpdateKmsService takes.
// Deterministic (RFC 6979), so callers may compute it once.
func (c *Client) KmsProviderEndorsement(appId string) ([]byte, error) {
	return voucher.EndorseKmsProvider(c.nodeKey, c.chainID, c.contractAddr, c.providerAddr, appId)
}

// transactOpts builds a *bind.TransactOpts signed by the provider key.
// The settlement contract no longer requires msg.sender == provider.
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
//
// SubmitSettleFees broadcasts the settlement tx and returns it WITHOUT
// waiting for it to mine. Split from SettleFeesWithTEE so the settler can
// persist the in-flight tx before its fate is known: re-signing the same
// vouchers with fresh nonces while the original tx may still mine is a
// double-charge (the contract dedupes only by strictly-increasing nonce, not
// by usageHash).
func (c *Client) SubmitSettleFees(ctx context.Context, vouchers []voucher.SandboxVoucher) (*types.Transaction, error) {
	opts, err := c.transactOpts(ctx)
	if err != nil {
		return nil, fmt.Errorf("build tx opts: %w", err)
	}
	tx, err := c.contract.SettleFeesWithTEE(opts, toContractVouchers(vouchers))
	if err != nil {
		return nil, fmt.Errorf("SettleFeesWithTEE tx: %w", err)
	}
	return tx, nil
}

// TxFate resolution values for a broadcast tx whose receipt is not yet known.
type TxFate int

const (
	TxPending TxFate = iota // still in flight — keep waiting
	TxMined                 // receipt available
	TxDropped               // provably gone: account nonce advanced past it with no receipt
)

// ResolveTxFate reports the definitive state of a broadcast tx. The settler
// never issues replacement txs, so "confirmed account nonce > tx nonce and no
// receipt" is proof the tx was dropped and can never mine.
func (c *Client) ResolveTxFate(ctx context.Context, txHash common.Hash, accountNonce uint64) (TxFate, *types.Receipt, error) {
	receipt, err := c.eth.TransactionReceipt(ctx, txHash)
	if err == nil && receipt != nil {
		return TxMined, receipt, nil
	}
	confirmed, nerr := c.eth.NonceAt(ctx, c.SettlerAddress(), nil)
	if nerr != nil {
		return TxPending, nil, nerr
	}
	if confirmed > accountNonce {
		// The account nonce advanced past this tx with no receipt. That is
		// usually a drop — but receipt lookup and NonceAt are two RPCs, and an
		// LB-fronted or mid-import node can serve a nonce that reflects a block
		// whose receipt the first call missed. Re-fetch the receipt once,
		// fresh, before declaring dropped: if the tx actually mined, this
		// catches it and avoids re-signing settled usage (double charge). The
		// caller additionally requires two consecutive dropped observations.
		if r2, e2 := c.eth.TransactionReceipt(ctx, txHash); e2 == nil && r2 != nil {
			return TxMined, r2, nil
		}
		return TxDropped, nil, nil
	}
	return TxPending, nil, nil
}

// SettleStatusesFromReceipt extracts per-voucher settlement statuses from a
// mined settlement tx's receipt (VoucherSettled events + preview fallback for
// vouchers that emitted none). Receipt with status 0 (whole tx reverted) means
// NO voucher was consumed — callers may safely re-sign and resubmit.
// ErrTxReverted marks a settlement tx that mined with status 0 — the whole tx
// reverted, so NO voucher was consumed on-chain and re-signing is safe. It is
// deliberately distinct from a status-extraction (preview) failure on a MINED
// tx, where events already settled and re-signing would double-charge.
var ErrTxReverted = errors.New("settlement tx reverted")

func (c *Client) SettleStatusesFromReceipt(ctx context.Context, receipt *types.Receipt, vouchers []voucher.SandboxVoucher) ([]SettlementStatus, error) {
	if receipt.Status == 0 {
		return nil, fmt.Errorf("%w: %s", ErrTxReverted, receipt.TxHash.Hex())
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

// SettleFeesWithTEE submits a settlement batch and waits for it to mine —
// the compose of SubmitSettleFees + WaitMined + SettleStatusesFromReceipt.
// Callers that must survive a WaitMined failure without double-charging
// (the settler) use the split pieces and persist the in-flight tx instead.
func (c *Client) SettleFeesWithTEE(ctx context.Context, vouchers []voucher.SandboxVoucher) ([]SettlementStatus, error) {
	tx, err := c.SubmitSettleFees(ctx, vouchers)
	if err != nil {
		return nil, err
	}
	receipt, err := bind.WaitMined(ctx, c.eth, tx)
	if err != nil {
		return nil, fmt.Errorf("wait mined: %w", err)
	}
	return c.SettleStatusesFromReceipt(ctx, receipt, vouchers)
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

const (
	blockTimeCacheTTL = 5 * time.Minute
	blockTimeSample   = 100
	blockTimeFallback = 1.0 // seconds, used if RPC fails
)

// getAvgBlockTime returns the cached avg block time, refreshing if stale.
func (c *Client) getAvgBlockTime(ctx context.Context, latest uint64) float64 {
	c.blockTimeMu.Lock()
	defer c.blockTimeMu.Unlock()

	if c.blockTimeSec > 0 && time.Since(c.blockTimeAt) < blockTimeCacheTTL {
		return c.blockTimeSec
	}

	avg := blockTimeFallback
	if latest > blockTimeSample {
		hNew, err1 := c.eth.HeaderByNumber(ctx, new(big.Int).SetUint64(latest))
		hOld, err2 := c.eth.HeaderByNumber(ctx, new(big.Int).SetUint64(latest-blockTimeSample))
		if err1 == nil && err2 == nil && hNew.Time > hOld.Time {
			avg = float64(hNew.Time-hOld.Time) / float64(blockTimeSample)
		}
	}

	c.blockTimeSec = avg
	c.blockTimeAt = time.Now()
	return avg
}

// GetVoucherEvents queries VoucherSettled logs from the contract.
// sinceTimestamp is a Unix timestamp (seconds); only events with block.timestamp >= sinceTimestamp
// are returned. sinceTimestamp=0 means all history (from block 1).
// page/pageSize control which slice to return (page is 0-indexed, newest-first).
// pageSize=0 returns all events without pagination.
// Returns the page of events, the total count, the current (latest) block number, and any error.
func (c *Client) GetVoucherEvents(ctx context.Context, sinceTimestamp uint64, page, pageSize int) ([]VoucherEvent, int, uint64, error) {
	latest, err := c.eth.BlockNumber(ctx)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("get block number: %w", err)
	}

	var fromBlock uint64 = 1
	if sinceTimestamp > 0 {
		now := uint64(time.Now().Unix())
		if sinceTimestamp < now {
			duration := now - sinceTimestamp

			avgBlockTimeSec := c.getAvgBlockTime(ctx, latest)
			lookback := uint64(float64(duration) / avgBlockTimeSec)
			if lookback < latest {
				fromBlock = latest - lookback
			}
		}
	}

	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		Addresses: []common.Address{c.contractAddr},
		Topics:    [][]common.Hash{{voucherSettledTopic}},
	}
	logs, err := c.eth.FilterLogs(ctx, query)
	if err != nil {
		return nil, 0, latest, fmt.Errorf("FilterLogs: %w", err)
	}

	type tsResult struct {
		bn uint64
		ts uint64
	}
	fetchTimestamps := func(blockSet map[uint64]uint64) {
		fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer fetchCancel()
		sem := make(chan struct{}, 5)
		ch := make(chan tsResult, len(blockSet))
		var wg sync.WaitGroup
		for bn := range blockSet {
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
			blockSet[r.bn] = r.ts
		}
	}

	// When filtering by time, fetch timestamps for all logs so we can filter before paginating.
	var blockNums map[uint64]uint64
	if sinceTimestamp > 0 {
		blockNums = make(map[uint64]uint64, len(logs))
		for _, l := range logs {
			blockNums[l.BlockNumber] = 0
		}
		fetchTimestamps(blockNums)

		filtered := logs[:0]
		for _, l := range logs {
			ts := blockNums[l.BlockNumber]
			// Include events whose timestamp is unavailable (ts==0 means the
			// HeaderByNumber call failed); exclude only confirmed-older events.
			if ts == 0 || ts >= sinceTimestamp {
				filtered = append(filtered, l)
			}
		}
		logs = filtered
	}

	total := len(logs)

	// Determine the slice of logs for this page (logs are ascending; we want newest-first).
	pageLogs := logs
	if pageSize > 0 {
		start := page * pageSize
		end := start + pageSize
		if start >= total {
			return []VoucherEvent{}, total, latest, nil
		}
		if end > total {
			end = total
		}
		// Slice from the end (newest logs are at the tail).
		pageLogs = logs[total-end : total-start]
	}

	// Fetch timestamps for page blocks (reuse already-fetched map when available).
	if blockNums == nil {
		blockNums = make(map[uint64]uint64)
		for _, l := range pageLogs {
			blockNums[l.BlockNumber] = 0
		}
		fetchTimestamps(blockNums)
	}

	events := make([]VoucherEvent, 0, len(pageLogs))
	for i := len(pageLogs) - 1; i >= 0; i-- {
		l := pageLogs[i]
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
	return events, total, latest, nil
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
// Note: TEE signer identity now lives in TappRegistry (per-node cluster);
// AppId is the link from sandbox commercial state to tapp trust state.
type ServiceInfo struct {
	URL                 string
	AppId               string
	PricePerCPUPerMin   *big.Int
	PricePerMemGBPerMin *big.Int
	CreateFee           *big.Int
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
		AppId:               svc.AppId,
		PricePerCPUPerMin:   svc.PricePerCPUPerMin,
		PricePerMemGBPerMin: svc.PricePerMemGBPerMin,
		CreateFee:           svc.CreateFee,
	}, nil
}

// ProviderEvent holds a decoded ServiceUpdated event from the contract.
type ProviderEvent struct {
	Provider common.Address
	URL      string
	AppId    string
	Block    uint64
	TxHash   string
}

// GetServiceUpdatedEvents queries ServiceUpdated logs starting at fromBlock.
// fromBlock=0 scans from block 1. Returns events, the current latest block, and any error.
func (c *Client) GetServiceUpdatedEvents(ctx context.Context, fromBlock uint64) ([]ProviderEvent, uint64, error) {
	latest, err := c.eth.BlockNumber(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("get block number: %w", err)
	}
	start := fromBlock
	if start == 0 {
		start = 1
	}
	opts := &bind.FilterOpts{
		Start:   start,
		End:     &latest,
		Context: ctx,
	}
	iter, err := c.contract.FilterServiceUpdated(opts, nil)
	if err != nil {
		return nil, latest, fmt.Errorf("FilterServiceUpdated: %w", err)
	}
	defer iter.Close()

	var events []ProviderEvent
	for iter.Next() {
		e := iter.Event
		events = append(events, ProviderEvent{
			Provider: e.Provider,
			URL:      e.Url,
			AppId:    e.AppId,
			Block:    e.Raw.BlockNumber,
			TxHash:   e.Raw.TxHash.Hex(),
		})
	}
	if err := iter.Error(); err != nil {
		return nil, latest, fmt.Errorf("iterate ServiceUpdated: %w", err)
	}
	return events, latest, nil
}

// GetBalanceBatch returns the on-chain balances for a list of users with a
// specific provider in a single view call.
func (c *Client) GetBalanceBatch(ctx context.Context, users []common.Address, provider common.Address) ([]*big.Int, error) {
	opts := &bind.CallOpts{Context: ctx}
	balances, err := c.contract.BalanceOfBatch(opts, users, provider)
	if err != nil {
		return nil, fmt.Errorf("BalanceOfBatch: %w", err)
	}
	return balances, nil
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
