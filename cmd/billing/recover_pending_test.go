package main

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
)

type cancellationTestRPC struct {
	chainID    *big.Int
	original   *types.Transaction
	receipt    *types.Receipt
	receiptErr error
	nonce      uint64
	code       []byte
}

func (f cancellationTestRPC) ChainID(context.Context) (*big.Int, error) { return f.chainID, nil }
func (f cancellationTestRPC) NonceAt(context.Context, common.Address, *big.Int) (uint64, error) {
	return f.nonce, nil
}
func (f cancellationTestRPC) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	if f.original == nil {
		return nil, false, ethereum.NotFound
	}
	return f.original, true, nil
}
func (f cancellationTestRPC) TransactionReceipt(context.Context, common.Hash) (*types.Receipt, error) {
	if f.receipt == nil && f.receiptErr == nil {
		return nil, ethereum.NotFound
	}
	return f.receipt, f.receiptErr
}
func (f cancellationTestRPC) CodeAt(context.Context, common.Address, *big.Int) ([]byte, error) {
	return f.code, nil
}
func (f cancellationTestRPC) HeaderByNumber(context.Context, *big.Int) (*types.Header, error) {
	return &types.Header{BaseFee: big.NewInt(7)}, nil
}
func (f cancellationTestRPC) SuggestGasTipCap(context.Context) (*big.Int, error) {
	return big.NewInt(2_000_000_000_000), nil
}
func (f cancellationTestRPC) BalanceAt(context.Context, common.Address, *big.Int) (*big.Int, error) {
	return big.NewInt(1_000_000_000_000_000_000), nil
}

func TestPrepareSettlementCancellation(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	contract := common.HexToAddress("0xabc")
	r := settlementCancellation{
		chainID: big.NewInt(16602), provider: crypto.PubkeyToAddress(key.PublicKey), nonce: 1073,
		feeCap: big.NewInt(2_200_000_000_000),
	}
	abi, err := chain.SandboxServingMetaData.GetAbi()
	if err != nil {
		t.Fatal(err)
	}
	original, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID: r.chainID, Nonce: r.nonce, To: &contract, Gas: 366855,
		GasFeeCap: big.NewInt(5_000_000_007), GasTipCap: big.NewInt(5_000_000_000),
		Data: abi.Methods["settleFeesWithTEE"].ID,
	}), types.LatestSignerForChainID(r.chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	r.hash = original.Hash()
	f := cancellationTestRPC{chainID: r.chainID, original: original, nonce: r.nonce}
	plan, err := r.prepare(context.Background(), f, contract)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ChainId().Cmp(r.chainID) != 0 || plan.Nonce() != r.nonce || plan.To() == nil ||
		*plan.To() != r.provider || plan.Value().Sign() != 0 || len(plan.Data()) != 0 ||
		plan.Gas() != 21_000 || plan.GasFeeCap().Cmp(r.feeCap) != 0 {
		t.Fatalf("unsafe cancellation plan: %v", plan)
	}

	t.Run("explicit tip replaces the RPC suggestion", func(t *testing.T) {
		req := r
		req.tip = big.NewInt(2_100_000_000_000)
		plan, err := req.prepare(context.Background(), f, contract)
		if err != nil {
			t.Fatal(err)
		}
		if plan.GasTipCap().Cmp(req.tip) != 0 {
			t.Fatalf("tip = %s, want %s", plan.GasTipCap(), req.tip)
		}
		req.tip = big.NewInt(4_000_000_000) // below the original's tip
		if _, err := req.prepare(context.Background(), f, contract); err == nil || !strings.Contains(err.Error(), "must exceed the original") {
			t.Fatalf("want a refusal below the original's tip, got %v", err)
		}
	})
	t.Run("evicted transaction can be cancelled", func(t *testing.T) {
		state := f
		state.original = nil
		if _, err := r.prepare(context.Background(), state, contract); err != nil {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name string
		want string
		edit func(*settlementCancellation, *cancellationTestRPC, *common.Address)
	}{
		{"confirmed nonce advanced", "confirmed provider nonce", func(_ *settlementCancellation, rpc *cancellationTestRPC, _ *common.Address) { rpc.nonce++ }},
		{"old receipt exists", "already has a receipt", func(_ *settlementCancellation, rpc *cancellationTestRPC, _ *common.Address) {
			rpc.receipt = &types.Receipt{}
		}},
		{"receipt RPC unavailable", "read original settlement receipt", func(_ *settlementCancellation, rpc *cancellationTestRPC, _ *common.Address) {
			rpc.receiptErr = errors.New("RPC down")
		}},
		{"wrong chain", "RPC chain ID", func(_ *settlementCancellation, rpc *cancellationTestRPC, _ *common.Address) {
			rpc.chainID = big.NewInt(1)
		}},
		{"wrong settlement contract", "does not match the pending provider settlement", func(_ *settlementCancellation, _ *cancellationTestRPC, c *common.Address) {
			*c = common.HexToAddress("0x123")
		}},
		{"unsafe self-transfer", "provider has code", func(_ *settlementCancellation, rpc *cancellationTestRPC, _ *common.Address) { rpc.code = []byte{1} }},
		{"gas cap below quote", "exceeds explicit fee cap", func(req *settlementCancellation, _ *cancellationTestRPC, _ *common.Address) {
			req.feeCap = big.NewInt(2_000_000_000_000)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, rpc, address := r, f, contract
			tc.edit(&req, &rpc, &address)
			if _, err := req.prepare(context.Background(), rpc, address); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestParseSettlementCancellationDefaultsToDryRun(t *testing.T) {
	args := []string{
		"--chain-id", "16602", "--provider", "0x0000000000000000000000000000000000000001",
		"--tx-hash", "0x0000000000000000000000000000000000000000000000000000000000000001",
		"--nonce", "0", "--max-fee-per-gas-wei", "2200000000000",
	}
	req, err := parseSettlementCancellation(args)
	if err != nil || req.broadcast || req.nonce != 0 {
		t.Fatalf("want an explicit nonce zero dry run, got %+v and %v", req, err)
	}
	if req, err := parseSettlementCancellation(append(args, "--broadcast")); err != nil || !req.broadcast {
		t.Fatalf("explicit broadcast option not accepted: %v", err)
	}
	if req, err := parseSettlementCancellation(append(args, "--tip-wei", "1000000000000")); err != nil || req.tip == nil || req.tip.Cmp(big.NewInt(1_000_000_000_000)) != 0 {
		t.Fatalf("explicit tip not accepted: %+v, %v", req, err)
	}
	if _, err := parseSettlementCancellation(append(args, "--tip-wei", "3000000000000")); err == nil {
		t.Fatal("accepted a tip above the fee cap")
	}
	if req, _ := parseSettlementCancellation(args); req.tip != nil {
		t.Fatal("tip must default to the RPC suggestion")
	}
	args[7] = ""
	if _, err := parseSettlementCancellation(args); err == nil {
		t.Fatal("accepted missing nonce")
	}
}
