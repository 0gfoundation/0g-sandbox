package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/redis/go-redis/v9"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/config"
	"github.com/0gfoundation/0g-sandbox/internal/settler"
	"github.com/0gfoundation/0g-sandbox/internal/tee"
)

const cancellationGasLimit = 21_000

type settlementCancellation struct {
	chainID   *big.Int
	provider  common.Address
	hash      common.Hash
	nonce     uint64
	feeCap    *big.Int
	tip       *big.Int // nil: the RPC's suggested tip
	broadcast bool
}

type cancellationRPC interface {
	ChainID(context.Context) (*big.Int, error)
	TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error)
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
	NonceAt(context.Context, common.Address, *big.Int) (uint64, error)
	CodeAt(context.Context, common.Address, *big.Int) ([]byte, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	SuggestGasTipCap(context.Context) (*big.Int, error)
	BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error)
}

func parseSettlementCancellation(args []string) (settlementCancellation, error) {
	var req settlementCancellation
	flags := flag.NewFlagSet("cancel-pending-settlement", flag.ContinueOnError)
	chainID := flags.Int64("chain-id", 0, "expected chain ID")
	provider := flags.String("provider", "", "expected KMS provider address")
	hash := flags.String("tx-hash", "", "expected original settlement transaction hash")
	nonce := flags.String("nonce", "", "expected settlement sender nonce (decimal)")
	feeCap := flags.Uint64("max-fee-per-gas-wei", 0, "maximum gas fee in wei per gas")
	tip := flags.Uint64("tip-wei", 0, "priority fee in wei per gas (default: the RPC's suggestion)")
	flags.BoolVar(&req.broadcast, "broadcast", false, "sign in the TEE and broadcast the cancellation")
	if err := flags.Parse(args); err != nil {
		return req, err
	}
	if flags.NArg() != 0 || *chainID <= 0 || !common.IsHexAddress(*provider) || *feeCap == 0 {
		return req, errors.New("expected --chain-id, --provider, --tx-hash, --nonce and --max-fee-per-gas-wei")
	}
	if err := req.hash.UnmarshalText([]byte(*hash)); err != nil || req.hash == (common.Hash{}) {
		return req, errors.New("--tx-hash must be a nonzero 32-byte hash")
	}
	var err error
	req.nonce, err = strconv.ParseUint(*nonce, 10, 64)
	if err != nil {
		return req, errors.New("--nonce must be an unsigned decimal integer")
	}
	req.provider = common.HexToAddress(*provider)
	if req.provider == (common.Address{}) {
		return req, errors.New("--provider must be nonzero")
	}
	req.chainID = big.NewInt(*chainID)
	req.feeCap = new(big.Int).SetUint64(*feeCap)
	if *tip > 0 {
		if *tip > *feeCap {
			return req, errors.New("--tip-wei must not exceed --max-fee-per-gas-wei")
		}
		req.tip = new(big.Int).SetUint64(*tip)
	}
	return req, nil
}

func runCancelPendingSettlement(args []string) error {
	req, err := parseSettlementCancellation(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.Chain.ProviderKeySource != "kms" || os.Getenv("MOCK_TEE") != "" || os.Getenv("BACKEND_TAPP_SOCKET") == "" {
		return errors.New("cancellation requires the deployed KMS provider and local Tapp socket")
	}
	if cfg.Chain.ChainID != req.chainID.Int64() || !common.IsHexAddress(cfg.Chain.ContractAddress) {
		return errors.New("expected chain ID or settlement contract does not match deployment config")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password})
	defer rdb.Close()
	if err := settler.CheckPendingSettlement(ctx, rdb, req.provider, req.hash, req.nonce); err != nil {
		return err
	}
	eth, err := ethclient.DialContext(ctx, cfg.Chain.RPCURL)
	if err != nil {
		return fmt.Errorf("dial settlement RPC: %w", err)
	}
	defer eth.Close()
	tx, err := req.prepare(ctx, eth, common.HexToAddress(cfg.Chain.ContractAddress))
	if err != nil {
		return err
	}
	maxCost := new(big.Int).Mul(tx.GasFeeCap(), new(big.Int).SetUint64(tx.Gas()))
	fmt.Printf("Cancellation plan: chain=%s provider=%s original=%s nonce=%d value=0 gas=%d fee_cap_wei=%s tip_wei=%s max_cost_wei=%s\n",
		req.chainID, req.provider, req.hash, req.nonce, tx.Gas(), tx.GasFeeCap(), tx.GasTipCap(), maxCost)
	if !req.broadcast {
		fmt.Println("Dry run: cancellation not signed or broadcast.")
		return nil
	}
	key, err := tee.GetProviderKey(ctx)
	if err != nil {
		return fmt.Errorf("load KMS provider signer: %w", err)
	}
	if key.Address != req.provider || !key.InEvidence {
		return errors.New("attested KMS signer does not match the requested provider")
	}
	secret, err := crypto.HexToECDSA(key.PrivateKeyHex)
	if err != nil {
		return errors.New("invalid KMS signer key")
	}
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(req.chainID), secret)
	if err != nil {
		return fmt.Errorf("sign cancellation: %w", err)
	}
	// Recheck the Redis record and chain immediately before sending. If the old
	// tx wins the race after these checks, the chain rejects the same nonce.
	if err := settler.CheckPendingSettlement(ctx, rdb, req.provider, req.hash, req.nonce); err != nil {
		return err
	}
	if err := req.checkUnconfirmed(ctx, eth); err != nil {
		return err
	}
	if err := eth.SendTransaction(ctx, signed); err != nil {
		return fmt.Errorf("send cancellation %s, check its receipt before any retry: %w", signed.Hash(), err)
	}
	fmt.Printf("Broadcast cancellation %s; wait for its receipt before proceeding.\n", signed.Hash())
	return nil
}

func (r settlementCancellation) prepare(ctx context.Context, rpc cancellationRPC, contract common.Address) (*types.Transaction, error) {
	actualChainID, err := rpc.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("read chain ID: %w", err)
	}
	if actualChainID.Cmp(r.chainID) != 0 {
		return nil, fmt.Errorf("RPC chain ID is %s, expected %s", actualChainID, r.chainID)
	}
	if err := r.checkUnconfirmed(ctx, rpc); err != nil {
		return nil, err
	}
	code, err := rpc.CodeAt(ctx, r.provider, nil)
	if err != nil {
		return nil, fmt.Errorf("read provider code: %w", err)
	}
	if len(code) != 0 {
		return nil, errors.New("provider has code; self-transfer could execute code")
	}
	tip := r.tip
	if tip == nil {
		if tip, err = rpc.SuggestGasTipCap(ctx); err != nil {
			return nil, fmt.Errorf("read suggested gas tip: %w", err)
		}
	}
	header, err := rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("read latest block header: %w", err)
	}
	if header.BaseFee == nil || tip.Sign() <= 0 {
		return nil, errors.New("need an EIP-1559 base fee from the RPC and a positive gas tip")
	}
	if needed := new(big.Int).Add(header.BaseFee, tip); needed.Cmp(r.feeCap) > 0 {
		return nil, fmt.Errorf("base fee plus tip %s exceeds explicit fee cap %s", needed, r.feeCap)
	}
	if err := r.checkOriginal(ctx, rpc, contract, tip); err != nil {
		return nil, err
	}
	balance, err := rpc.BalanceAt(ctx, r.provider, nil)
	if err != nil {
		return nil, fmt.Errorf("read provider balance: %w", err)
	}
	if cost := new(big.Int).Mul(r.feeCap, big.NewInt(cancellationGasLimit)); balance.Cmp(cost) < 0 {
		return nil, fmt.Errorf("provider balance is below cancellation gas budget %s wei", cost)
	}
	return types.NewTx(&types.DynamicFeeTx{
		ChainID: r.chainID, Nonce: r.nonce, Gas: cancellationGasLimit, GasFeeCap: r.feeCap, GasTipCap: tip,
		To: &r.provider, Value: new(big.Int),
	}), nil
}

func (r settlementCancellation) checkUnconfirmed(ctx context.Context, rpc cancellationRPC) error {
	if _, err := rpc.TransactionReceipt(ctx, r.hash); !errors.Is(err, ethereum.NotFound) {
		if err == nil {
			return errors.New("original settlement already has a receipt")
		}
		return fmt.Errorf("read original settlement receipt: %w", err)
	}
	nonce, err := rpc.NonceAt(ctx, r.provider, nil)
	if err != nil {
		return fmt.Errorf("read confirmed provider nonce: %w", err)
	}
	if nonce != r.nonce {
		return fmt.Errorf("confirmed provider nonce is %d, expected %d", nonce, r.nonce)
	}
	return nil
}

func (r settlementCancellation) checkOriginal(ctx context.Context, rpc cancellationRPC, contract common.Address, tip *big.Int) error {
	tx, pending, err := rpc.TransactionByHash(ctx, r.hash)
	// An evicted tx is still cancellable. The matching Redis record and the
	// original receipt/nonce checks provide the identity in that case.
	if errors.Is(err, ethereum.NotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read original settlement transaction: %w", err)
	}
	abi, err := chain.SandboxServingMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("read settlement ABI: %w", err)
	}
	selector := abi.Methods["settleFeesWithTEE"].ID
	from, senderErr := types.Sender(types.LatestSignerForChainID(r.chainID), tx)
	if !pending || tx.Hash() != r.hash || senderErr != nil || from != r.provider || tx.Nonce() != r.nonce ||
		tx.To() == nil || *tx.To() != contract || tx.Value().Sign() != 0 || len(selector) != 4 || !bytes.HasPrefix(tx.Data(), selector) {
		return errors.New("original transaction does not match the pending provider settlement")
	}
	if tx.GasFeeCap().Cmp(r.feeCap) >= 0 || tx.GasTipCap().Cmp(tip) >= 0 {
		return errors.New("cancellation fee cap and tip must exceed the original transaction's")
	}
	return nil
}
