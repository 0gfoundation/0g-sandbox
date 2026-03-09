package chain_test

// Integration test: deploys the SandboxServing beacon-proxy stack on an in-process
// simulated EVM, then exercises GetLastNonce, SettleFeesWithTEE and GetAccount via
// the real chain.Client code paths.
//
// No external process (Anvil, geth) is required — the go-ethereum simulated
// backend runs entirely in memory.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"

	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
	"github.com/0gfoundation/0g-sandbox-billing/internal/voucher"
)

// ── test keys (Anvil default accounts) ────────────────────────────────────────

var (
	providerKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	userKeyHex     = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	// The go-ethereum simulated backend always uses chainID 1337.
	simChainID = big.NewInt(1337)
)

// loadArtifact reads the Foundry-compiled JSON and returns (bytecode, parsedABI).
func loadArtifact(t *testing.T, relPath string, abiStr string) ([]byte, abi.ABI) {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	artifactPath := filepath.Join(filepath.Dir(thisFile), "..", "..", relPath)
	raw, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact %s: %v", relPath, err)
	}
	var artifact struct {
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatalf("parse artifact %s: %v", relPath, err)
	}
	hexStr := strings.TrimPrefix(artifact.Bytecode.Object, "0x")
	bytecode, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("decode bytecode %s: %v", relPath, err)
	}
	parsedABI, err := abi.JSON(strings.NewReader(abiStr))
	if err != nil {
		t.Fatalf("parse ABI %s: %v", relPath, err)
	}
	return bytecode, parsedABI
}

// ── helpers ───────────────────────────────────────────────────────────────────

// deployFixture deploys the full SandboxServing beacon-proxy stack on a fresh
// simulated chain and returns the SandboxServing binding (bound to the proxy
// address), the simulated backend, proxy/provider/user addresses, and signers.
func deployFixture(t *testing.T) (
	contract *chain.SandboxServing,
	backend *simulated.Backend,
	contractAddr common.Address,
	providerAddr common.Address,
	userAddr common.Address,
	providerAuth *bind.TransactOpts,
	userAuth *bind.TransactOpts,
) {
	t.Helper()

	providerKey, err := crypto.HexToECDSA(providerKeyHex)
	if err != nil {
		t.Fatalf("parse provider key: %v", err)
	}
	userKey, err := crypto.HexToECDSA(userKeyHex)
	if err != nil {
		t.Fatalf("parse user key: %v", err)
	}

	providerAddr = crypto.PubkeyToAddress(providerKey.PublicKey)
	userAddr = crypto.PubkeyToAddress(userKey.PublicKey)

	// Fund both accounts with 1000 ETH each.
	balance, _ := new(big.Int).SetString("1000000000000000000000", 10)
	alloc := types.GenesisAlloc{
		providerAddr: {Balance: balance},
		userAddr:     {Balance: balance},
	}
	backend = simulated.NewBackend(alloc, simulated.WithBlockGasLimit(30_000_000))
	client := backend.Client()

	providerAuth, err = bind.NewKeyedTransactorWithChainID(providerKey, simChainID)
	if err != nil {
		t.Fatalf("provider transactor: %v", err)
	}
	userAuth, err = bind.NewKeyedTransactorWithChainID(userKey, simChainID)
	if err != nil {
		t.Fatalf("user transactor: %v", err)
	}

	// ── Step 1: Deploy SandboxServing implementation (no constructor args) ────
	implBytecode, implABI := loadArtifact(t,
		"contracts/out/SandboxServing.sol/SandboxServing.json",
		chain.SandboxServingMetaData.ABI)

	providerAuth.GasLimit = 5_000_000
	implAddr, _, _, err := bind.DeployContract(providerAuth, implABI, implBytecode, client)
	if err != nil {
		t.Fatalf("deploy impl: %v", err)
	}
	providerAuth.GasLimit = 0
	backend.Commit()

	// ── Step 2: Deploy UpgradeableBeacon(impl, providerAddr) ─────────────────
	beaconBytecode, beaconABI := loadArtifact(t,
		"contracts/out/UpgradeableBeacon.sol/UpgradeableBeacon.json",
		chain.UpgradeableBeaconMetaData.ABI)

	providerAuth.GasLimit = 3_000_000
	beaconAddr, _, _, err := bind.DeployContract(providerAuth, beaconABI, beaconBytecode, client,
		implAddr, providerAddr)
	if err != nil {
		t.Fatalf("deploy beacon: %v", err)
	}
	providerAuth.GasLimit = 0
	backend.Commit()

	// ── Step 3: Deploy BeaconProxy(beacon, initialize(0)) ─────────────────────
	proxyBytecode, proxyConstructorABI := loadArtifact(t,
		"contracts/out/BeaconProxy.sol/BeaconProxy.json",
		`[{"type":"constructor","inputs":[{"name":"beacon","type":"address"},{"name":"data","type":"bytes"}],"stateMutability":"payable"}]`)

	initCalldata, err := implABI.Pack("initialize", big.NewInt(0))
	if err != nil {
		t.Fatalf("pack initialize calldata: %v", err)
	}

	providerAuth.GasLimit = 5_000_000
	contractAddr, _, _, err = bind.DeployContract(providerAuth, proxyConstructorABI, proxyBytecode, client,
		beaconAddr, initCalldata)
	if err != nil {
		t.Fatalf("deploy proxy: %v", err)
	}
	providerAuth.GasLimit = 0
	backend.Commit()

	// Bind SandboxServing interface to the proxy address.
	contract, err = chain.NewSandboxServing(contractAddr, client)
	if err != nil {
		t.Fatalf("bind contract: %v", err)
	}

	// Provider registers service.
	teeSigner := providerAddr
	pricePerMin := big.NewInt(100)
	createFee := big.NewInt(0)
	providerAuth.Value = big.NewInt(0)
	_, err = contract.AddOrUpdateService(providerAuth, "https://provider.test", teeSigner, pricePerMin, createFee, big.NewInt(0))
	if err != nil {
		t.Fatalf("addOrUpdateService: %v", err)
	}
	backend.Commit()

	// User deposits 10 ETH for the provider.
	userAuth.Value, _ = new(big.Int).SetString("10000000000000000000", 10)
	_, err = contract.Deposit(userAuth, userAddr, providerAddr)
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}
	backend.Commit()
	userAuth.Value = big.NewInt(0)

	// User acknowledges TEE signer.
	_, err = contract.AcknowledgeTEESigner(userAuth, providerAddr, true)
	if err != nil {
		t.Fatalf("acknowledgeTEESigner: %v", err)
	}
	backend.Commit()

	return contract, backend, contractAddr, providerAddr, userAddr, providerAuth, userAuth
}

// signVoucher signs a voucher using the provider key (TEE signer == provider).
func signVoucher(t *testing.T, v *voucher.SandboxVoucher, contractAddr common.Address) {
	t.Helper()
	providerKey, _ := crypto.HexToECDSA(providerKeyHex)
	if err := voucher.Sign(v, providerKey, simChainID, contractAddr); err != nil {
		t.Fatalf("sign voucher: %v", err)
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestGetLastNonce_InitiallyZero verifies that a fresh (user, provider) pair
// returns 0 from the contract.
func TestGetLastNonce_InitiallyZero(t *testing.T) {
	contract, backend, _, providerAddr, userAddr, _, _ := deployFixture(t)
	_ = backend

	ctx := context.Background()
	opts := &bind.CallOpts{Context: ctx}
	n, err := contract.GetLastNonce(opts, userAddr, providerAddr)
	if err != nil {
		t.Fatalf("GetLastNonce: %v", err)
	}
	if n.Int64() != 0 {
		t.Errorf("initial lastNonce: got %d want 0", n.Int64())
	}
}

// TestSettleFeesWithTEE_Success settles one voucher and verifies lastNonce advances.
func TestSettleFeesWithTEE_Success(t *testing.T) {
	contract, backend, contractAddr, providerAddr, userAddr, providerAuth, _ := deployFixture(t)
	ctx := context.Background()

	v := &voucher.SandboxVoucher{
		SandboxID: "sb-e2e-1",
		User:      userAddr,
		Provider:  providerAddr,
		TotalFee:  big.NewInt(500),
		Nonce:     big.NewInt(1),
		UsageHash: voucher.BuildUsageHash("sb-e2e-1", 1000, 1060, 1),
	}
	signVoucher(t, v, contractAddr)

	cv := chain.SandboxServingSandboxVoucher{
		User: v.User, Provider: v.Provider,
		TotalFee: v.TotalFee, UsageHash: v.UsageHash,
		Nonce: v.Nonce, Signature: v.Signature,
	}
	tx, err := contract.SettleFeesWithTEE(providerAuth, []chain.SandboxServingSandboxVoucher{cv})
	if err != nil {
		t.Fatalf("SettleFeesWithTEE: %v", err)
	}
	backend.Commit()

	receipt, err := backend.Client().TransactionReceipt(ctx, tx.Hash())
	if err != nil {
		t.Fatalf("get receipt: %v", err)
	}
	if receipt.Status != 1 {
		t.Fatalf("tx reverted")
	}

	opts := &bind.CallOpts{Context: ctx}
	n, err := contract.GetLastNonce(opts, userAddr, providerAddr)
	if err != nil {
		t.Fatalf("GetLastNonce after settle: %v", err)
	}
	if n.Int64() != 1 {
		t.Errorf("lastNonce after settle: got %d want 1", n.Int64())
	}
}

// TestSettleFeesWithTEE_InvalidNonce verifies nonce=0 gives StatusInvalidNonce.
// previewSettlementResults checks msg.sender == voucher.provider, so we must
// set CallOpts.From to providerAddr.
func TestSettleFeesWithTEE_InvalidNonce(t *testing.T) {
	contract, backend, contractAddr, providerAddr, userAddr, _, _ := deployFixture(t)
	_ = backend
	ctx := context.Background()

	v := &voucher.SandboxVoucher{
		SandboxID: "sb-e2e-bad",
		User:      userAddr,
		Provider:  providerAddr,
		TotalFee:  big.NewInt(100),
		Nonce:     big.NewInt(0), // nonce=0 must be rejected (contract requires nonce > lastNonce=0)
		UsageHash: voucher.BuildUsageHash("sb-e2e-bad", 0, 60, 1),
	}
	signVoucher(t, v, contractAddr)

	cv := chain.SandboxServingSandboxVoucher{
		User: v.User, Provider: v.Provider,
		TotalFee: v.TotalFee, UsageHash: v.UsageHash,
		Nonce: v.Nonce, Signature: v.Signature,
	}
	// From must equal voucher.Provider: the contract uses msg.sender for the
	// provider match check even in view calls.
	opts := &bind.CallOpts{Context: ctx, From: providerAddr}
	statuses, err := contract.PreviewSettlementResults(opts, []chain.SandboxServingSandboxVoucher{cv})
	if err != nil {
		t.Fatalf("PreviewSettlementResults: %v", err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	// 4 = StatusInvalidNonce (Solidity enum ordinal)
	if statuses[0] != 4 {
		t.Errorf("status: got %d want 4 (InvalidNonce), status string: %s",
			statuses[0], chain.SettlementStatus(statuses[0]).String())
	}
}

// TestGetBalance_BalanceAfterDeposit verifies the deposited balance is readable per-provider.
func TestGetBalance_BalanceAfterDeposit(t *testing.T) {
	contract, backend, _, providerAddr, userAddr, _, _ := deployFixture(t)
	_ = backend
	ctx := context.Background()

	opts := &bind.CallOpts{Context: ctx}
	result, err := contract.GetBalance(opts, userAddr, providerAddr)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	expected, _ := new(big.Int).SetString("10000000000000000000", 10) // 10 ETH
	if result.Balance.Cmp(expected) != 0 {
		t.Errorf("balance: got %s want %s", result.Balance, expected)
	}
}

// TestNonceIncreasesMonotonically settles 3 vouchers and checks lastNonce = 3.
func TestNonceIncreasesMonotonically(t *testing.T) {
	contract, backend, contractAddr, providerAddr, userAddr, providerAuth, _ := deployFixture(t)
	ctx := context.Background()

	for i := int64(1); i <= 3; i++ {
		v := &voucher.SandboxVoucher{
			SandboxID: "sb-mono",
			User:      userAddr,
			Provider:  providerAddr,
			TotalFee:  big.NewInt(100 * i),
			Nonce:     big.NewInt(i),
			UsageHash: voucher.BuildUsageHash("sb-mono", i*1000, i*1000+60, i),
		}
		signVoucher(t, v, contractAddr)

		cv := chain.SandboxServingSandboxVoucher{
			User: v.User, Provider: v.Provider,
			TotalFee: v.TotalFee, UsageHash: v.UsageHash,
			Nonce: v.Nonce, Signature: v.Signature,
		}
		_, err := contract.SettleFeesWithTEE(providerAuth, []chain.SandboxServingSandboxVoucher{cv})
		if err != nil {
			t.Fatalf("SettleFeesWithTEE [%d]: %v", i, err)
		}
		backend.Commit()
	}

	opts := &bind.CallOpts{Context: ctx}
	n, err := contract.GetLastNonce(opts, userAddr, providerAddr)
	if err != nil {
		t.Fatalf("GetLastNonce: %v", err)
	}
	if n.Int64() != 3 {
		t.Errorf("lastNonce after 3 settlements: got %d want 3", n.Int64())
	}
}
