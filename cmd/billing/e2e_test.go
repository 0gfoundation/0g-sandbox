package main

// Component tests for the billing pipeline.
//
// These tests wire up the full billing stack (auth → proxy → billing → settler)
// but replace external dependencies with lightweight in-process fakes:
//   - Chain:   go-ethereum simulated backend (deterministic, no network)
//   - Daytona: httptest.Server mock
//   - Redis:   miniredis (in-process)
//
// Tests skip gracefully when compiled contract artifacts are absent
// (run `make build-contracts` to produce contracts/out/).
//
//  1. TestComponent_HappyPath
//     Signed POST /sandbox → mock Daytona creates sandbox → billing enqueues
//     create-fee voucher → settler settles on simulated chain → lastNonce == 1.
//
//  2. TestComponent_InsufficientBalance
//     User has no balance → settler gets StatusInsufficientBalance →
//     runStopHandler stops sandbox → Redis stop key cleaned up.
//
//  3. TestComponent_OwnershipFiltering
//     Proxy injects daytona-owner label → list filtered by caller →
//     cross-owner GET returns 403.

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox-billing/internal/auth"
	"github.com/0gfoundation/0g-sandbox-billing/internal/billing"
	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
	"github.com/0gfoundation/0g-sandbox-billing/internal/config"
	"github.com/0gfoundation/0g-sandbox-billing/internal/daytona"
	"github.com/0gfoundation/0g-sandbox-billing/internal/proxy"
	"github.com/0gfoundation/0g-sandbox-billing/internal/settler"
	"github.com/0gfoundation/0g-sandbox-billing/internal/voucher"
)

// ── test keys (Anvil defaults) ────────────────────────────────────────────────

var (
	e2eProviderKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	e2eUserKeyHex     = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	e2eChainID        = big.NewInt(1337)
)

// ── e2eFixture: simulated chain with deployed SandboxServing beacon-proxy ─────

type e2eFixture struct {
	backend      *simulated.Backend
	simClient    simulated.Client
	contract     *chain.SandboxServing
	proxyAddr    common.Address
	providerAddr common.Address
	userAddr     common.Address
	providerKey  *ecdsa.PrivateKey
	userKey      *ecdsa.PrivateKey
	providerAuth *bind.TransactOpts
	userAuth     *bind.TransactOpts
}

// deployE2EFixture deploys the full beacon-proxy stack on a simulated chain.
// It registers the provider service but does NOT deposit or acknowledge TEE
// signer for the user — tests control that to exercise different scenarios.
func deployE2EFixture(t *testing.T) *e2eFixture {
	t.Helper()

	providerKey, _ := crypto.HexToECDSA(e2eProviderKeyHex)
	userKey, _ := crypto.HexToECDSA(e2eUserKeyHex)
	providerAddr := crypto.PubkeyToAddress(providerKey.PublicKey)
	userAddr := crypto.PubkeyToAddress(userKey.PublicKey)

	balance, _ := new(big.Int).SetString("1000000000000000000000", 10) // 1000 0G
	alloc := types.GenesisAlloc{
		providerAddr: {Balance: balance},
		userAddr:     {Balance: balance},
	}
	backend := simulated.NewBackend(alloc, simulated.WithBlockGasLimit(30_000_000))
	t.Cleanup(func() { backend.Close() })
	simClient := backend.Client()

	providerAuth, _ := bind.NewKeyedTransactorWithChainID(providerKey, e2eChainID)
	userAuth, _ := bind.NewKeyedTransactorWithChainID(userKey, e2eChainID)

	// Deploy SandboxServing implementation
	implBytecode, implABI := e2eLoadArtifact(t,
		"contracts/out/SandboxServing.sol/SandboxServing.json",
		chain.SandboxServingMetaData.ABI)
	providerAuth.GasLimit = 5_000_000
	implAddr, _, _, err := bind.DeployContract(providerAuth, implABI, implBytecode, simClient)
	if err != nil {
		t.Fatalf("deploy impl: %v", err)
	}
	backend.Commit()

	// Deploy UpgradeableBeacon(impl, providerAddr)
	beaconBytecode, beaconABI := e2eLoadArtifact(t,
		"contracts/out/UpgradeableBeacon.sol/UpgradeableBeacon.json",
		chain.UpgradeableBeaconMetaData.ABI)
	providerAuth.GasLimit = 3_000_000
	beaconAddr, _, _, err := bind.DeployContract(providerAuth, beaconABI, beaconBytecode, simClient,
		implAddr, providerAddr)
	if err != nil {
		t.Fatalf("deploy beacon: %v", err)
	}
	backend.Commit()

	// Deploy BeaconProxy(beacon, initialize(0))
	proxyBytecode, proxyCtorABI := e2eLoadArtifact(t,
		"contracts/out/BeaconProxy.sol/BeaconProxy.json",
		`[{"type":"constructor","inputs":[{"name":"beacon","type":"address"},{"name":"data","type":"bytes"}],"stateMutability":"payable"}]`)
	initCalldata, _ := implABI.Pack("initialize", big.NewInt(0))
	providerAuth.GasLimit = 5_000_000
	proxyAddr, _, _, err := bind.DeployContract(providerAuth, proxyCtorABI, proxyBytecode, simClient,
		beaconAddr, initCalldata)
	if err != nil {
		t.Fatalf("deploy proxy: %v", err)
	}
	backend.Commit()
	providerAuth.GasLimit = 0

	contract, err := chain.NewSandboxServing(proxyAddr, simClient)
	if err != nil {
		t.Fatalf("bind contract: %v", err)
	}

	// Register service (TEE signer == providerAddr, 100 neuron/CPU/min, 0 mem fee, no create fee)
	_, err = contract.AddOrUpdateService(providerAuth, "https://provider.test",
		providerAddr, big.NewInt(100), big.NewInt(0), big.NewInt(0))
	if err != nil {
		t.Fatalf("addOrUpdateService: %v", err)
	}
	backend.Commit()

	return &e2eFixture{
		backend:      backend,
		simClient:    simClient,
		contract:     contract,
		proxyAddr:    proxyAddr,
		providerAddr: providerAddr,
		userAddr:     userAddr,
		providerKey:  providerKey,
		userKey:      userKey,
		providerAuth: providerAuth,
		userAuth:     userAuth,
	}
}

// ── simChainClient: settler.ChainClient backed by simulated EVM ───────────────

type simChainClient struct {
	contract     *chain.SandboxServing
	providerAuth *bind.TransactOpts
	simClient    simulated.Client
	backend      *simulated.Backend
}

// SettleFeesWithTEE submits the vouchers to the simulated chain.
// Statuses are read via PreviewSettlementResults BEFORE the tx so they are
// accurate for all outcomes (success, insufficient balance, etc.).
func (c *simChainClient) SettleFeesWithTEE(ctx context.Context, vs []voucher.SandboxVoucher) ([]chain.SettlementStatus, error) {
	cvs := make([]chain.SandboxServingSandboxVoucher, len(vs))
	for i, v := range vs {
		cvs[i] = chain.SandboxServingSandboxVoucher{
			User: v.User, Provider: v.Provider,
			TotalFee: v.TotalFee, UsageHash: v.UsageHash,
			Nonce: v.Nonce, Signature: v.Signature,
		}
	}

	// Preview statuses before the tx; From must equal voucher.Provider.
	previewOpts := &bind.CallOpts{Context: ctx, From: c.providerAuth.From}
	rawStatuses, err := c.contract.PreviewSettlementResults(previewOpts, cvs)
	if err != nil {
		return nil, fmt.Errorf("preview statuses: %w", err)
	}

	// Submit tx and mine a block.
	opts := *c.providerAuth
	opts.Context = ctx
	tx, err := c.contract.SettleFeesWithTEE(&opts, cvs)
	if err != nil {
		return nil, fmt.Errorf("SettleFeesWithTEE tx: %w", err)
	}
	c.backend.Commit()

	receipt, err := c.simClient.TransactionReceipt(ctx, tx.Hash())
	if err != nil {
		return nil, fmt.Errorf("get receipt: %w", err)
	}
	if receipt.Status == 0 {
		return nil, fmt.Errorf("settlement tx reverted")
	}

	statuses := make([]chain.SettlementStatus, len(rawStatuses))
	for i, s := range rawStatuses {
		statuses[i] = chain.SettlementStatus(s)
	}
	return statuses, nil
}

// ── e2eMockDaytona: records creates and stops ─────────────────────────────────

type e2eMockDaytona struct {
	mu      sync.Mutex
	created []string // sandbox IDs returned by POST /api/sandbox
	stopped []string // sandbox IDs stopped by POST /api/sandbox/:id/stop
	srv     *httptest.Server
}

func newE2EMockDaytona(t *testing.T) *e2eMockDaytona {
	t.Helper()
	m := &e2eMockDaytona{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, method := r.URL.Path, r.Method

		// POST /api/sandbox → create sandbox, return new ID
		if method == http.MethodPost && path == "/api/sandbox" {
			m.mu.Lock()
			id := fmt.Sprintf("sb-e2e-%d", len(m.created)+1)
			m.created = append(m.created, id)
			m.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":%q}`, id)
			return
		}

		// POST /api/sandbox/:id/stop → stop sandbox
		if method == http.MethodPost && strings.HasSuffix(path, "/stop") {
			// path = /api/sandbox/:id/stop → split gives ["api","sandbox",id,"stop"]
			parts := strings.Split(strings.Trim(path, "/"), "/")
			if len(parts) == 4 && parts[0] == "api" && parts[1] == "sandbox" {
				m.mu.Lock()
				m.stopped = append(m.stopped, parts[2])
				m.mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *e2eMockDaytona) createdIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.created))
	copy(out, m.created)
	return out
}

func (m *e2eMockDaytona) stoppedIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.stopped))
	copy(out, m.stopped)
	return out
}

// ── shared helpers ────────────────────────────────────────────────────────────

// e2eLoadArtifact reads a Foundry JSON artifact. Skips the test if not found.
func e2eLoadArtifact(t *testing.T, relPath, abiStr string) ([]byte, abi.ABI) {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	fullPath := filepath.Join(filepath.Dir(thisFile), "..", "..", relPath)
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		t.Skipf("artifact not found (run `make build-contracts`): %v", err)
	}
	var artifact struct {
		Bytecode struct{ Object string `json:"object"` } `json:"bytecode"`
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

// e2eSignedHeaders builds EIP-191 auth headers for the given private key.
func e2eSignedHeaders(t *testing.T, privKeyHex string) (walletAddr, msgB64, sigHex string) {
	t.Helper()
	privKey, err := crypto.HexToECDSA(privKeyHex)
	if err != nil {
		t.Fatalf("parse private key: %v", err)
	}
	walletAddr = crypto.PubkeyToAddress(privKey.PublicKey).Hex()
	req := auth.SignedRequest{
		Action:    "create",
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		Nonce:     fmt.Sprintf("e2e-%d", time.Now().UnixNano()),
	}
	msgBytes, _ := json.Marshal(req)
	hash := auth.HashMessage(msgBytes)
	sig, err := crypto.Sign(hash, privKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig[64] += 27 // normalize V to Ethereum convention (27/28)
	return walletAddr,
		base64.StdEncoding.EncodeToString(msgBytes),
		"0x" + hex.EncodeToString(sig)
}

// waitFor polls f() until it returns true or timeout elapses.
func waitFor(t *testing.T, desc string, timeout time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", desc)
}

// buildServer wires up the full gin HTTP server (auth + proxy handler).
func buildServer(t *testing.T, dtona *daytona.Client, bh proxy.BillingHooks, rdb *redis.Client) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := r.Group("/api", auth.Middleware(rdb))
	proxy.NewHandler(dtona, bh, nil, nil, nil, big.NewInt(0), big.NewInt(0), "", rdb, zap.NewNop()).Register(api)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// postSandbox sends an authenticated POST /api/sandbox to srv and asserts 201.
func postSandbox(t *testing.T, ctx context.Context, srvURL, privKeyHex string) {
	t.Helper()
	walletAddr, msgB64, sigHex := e2eSignedHeaders(t, privKeyHex)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		srvURL+"/api/sandbox", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msgB64)
	req.Header.Set("X-Wallet-Signature", sigHex)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sandbox: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sandbox: got HTTP %d, want 201", resp.StatusCode)
	}
}

// e2eNonceReader satisfies billing.NonceReader via the simulated chain.
type e2eNonceReader struct{ contract *chain.SandboxServing }

func (r *e2eNonceReader) GetLastNonce(ctx context.Context, user, provider common.Address) (*big.Int, error) {
	return r.contract.GetLastNonce(&bind.CallOpts{Context: ctx}, user, provider)
}

// ── Test 1: happy path ────────────────────────────────────────────────────────

// TestComponent_HappyPath exercises the full happy-path flow on a simulated chain:
//
//  1. POST /api/sandbox → Daytona mock receives create request, returns sandbox ID.
//  2. billing.OnCreate enqueues a create-fee voucher into Redis.
//  3. settler.Run settles the voucher on-chain.
//  4. On-chain lastNonce advances to 1.
func TestComponent_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fix := deployE2EFixture(t)

	// User deposits 10 0G for the provider and acknowledges the TEE signer.
	fix.userAuth.Value, _ = new(big.Int).SetString("10000000000000000000", 10)
	if _, err := fix.contract.Deposit(fix.userAuth, fix.userAddr, fix.providerAddr); err != nil {
		t.Fatalf("deposit: %v", err)
	}
	fix.backend.Commit()
	fix.userAuth.Value = big.NewInt(0)

	if _, err := fix.contract.AcknowledgeTEESigner(fix.userAuth, fix.providerAddr, true); err != nil {
		t.Fatalf("acknowledgeTEESigner: %v", err)
	}
	fix.backend.Commit()

	// Redis + Daytona mock
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mock := newE2EMockDaytona(t)
	dtona := daytona.NewClient(mock.srv.URL, "test-key")

	// Billing: createFee=100 neuron so OnCreate enqueues a non-trivial voucher.
	signer := billing.NewSigner(fix.providerKey, e2eChainID, fix.proxyAddr, fix.providerAddr,
		rdb, &e2eNonceReader{fix.contract}, zap.NewNop())
	bh := billing.NewEventHandler(rdb, fix.providerAddr.Hex(),
		big.NewInt(0), big.NewInt(100), new(big.Int), new(big.Int), signer, zap.NewNop())

	srv := buildServer(t, dtona, bh, rdb)

	// ── 1. POST /sandbox ──────────────────────────────────────────────────────
	postSandbox(t, ctx, srv.URL, e2eUserKeyHex)

	// ── Assert: Daytona received the create request ───────────────────────────
	waitFor(t, "Daytona create request", 3*time.Second, func() bool {
		return len(mock.createdIDs()) == 1
	})
	createdID := mock.createdIDs()[0]
	t.Logf("Daytona create confirmed: sandbox ID = %q", createdID)

	// ── 2. Wait for OnCreate to enqueue the voucher ───────────────────────────
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, fix.providerAddr.Hex())
	waitFor(t, "voucher in Redis queue", 3*time.Second, func() bool {
		n, _ := rdb.LLen(ctx, queueKey).Result()
		return n >= 1
	})

	// ── 3. Settler processes the voucher ──────────────────────────────────────
	onchain := &simChainClient{
		contract:     fix.contract,
		providerAuth: fix.providerAuth,
		simClient:    fix.simClient,
		backend:      fix.backend,
	}
	stopCh := make(chan settler.StopSignal, 4)
	cfg := &config.Config{
		Chain:   config.ChainConfig{ProviderAddress: fix.providerAddr.Hex()},
		Billing: config.BillingConfig{VoucherIntervalSec: 1},
	}
	settlerCtx, settlerCancel := context.WithCancel(ctx)
	defer settlerCancel()
	go settler.Run(settlerCtx, cfg, rdb, onchain, stopCh, zap.NewNop())

	// ── 4. Assert: on-chain lastNonce == 1 ────────────────────────────────────
	waitFor(t, "on-chain lastNonce == 1", 10*time.Second, func() bool {
		n, err := fix.contract.GetLastNonce(&bind.CallOpts{}, fix.userAddr, fix.providerAddr)
		if err != nil {
			return false
		}
		return n.Int64() == 1
	})
	settlerCancel()
	t.Logf("Settlement confirmed: lastNonce(user=%s, provider=%s) = 1",
		fix.userAddr.Hex(), fix.providerAddr.Hex())
}

// ── Test 2: insufficient balance → auto-stop ─────────────────────────────────

// TestComponent_InsufficientBalance exercises the sad-path flow on a simulated chain:
//
//  1. POST /api/sandbox → Daytona mock creates sandbox, billing enqueues voucher.
//  2. settler.Run settles → StatusInsufficientBalance (user never deposited).
//  3. settler calls persistStop → runStopHandler calls Daytona stop endpoint.
//  4. Daytona mock confirms it received the stop request for the correct sandbox.
//  5. Redis stop:sandbox key is cleaned up.
func TestComponent_InsufficientBalance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fix := deployE2EFixture(t)

	// User acknowledges TEE signer but does NOT deposit → balance == 0.
	// This produces StatusInsufficientBalance (not StatusNotAcknowledged).
	if _, err := fix.contract.AcknowledgeTEESigner(fix.userAuth, fix.providerAddr, true); err != nil {
		t.Fatalf("acknowledgeTEESigner: %v", err)
	}
	fix.backend.Commit()

	// Redis + Daytona mock
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mock := newE2EMockDaytona(t)
	dtona := daytona.NewClient(mock.srv.URL, "test-key")

	// createFee=100 neuron: OnCreate enqueues a voucher that will fail due to
	// the user's zero balance.
	signer := billing.NewSigner(fix.providerKey, e2eChainID, fix.proxyAddr, fix.providerAddr,
		rdb, &e2eNonceReader{fix.contract}, zap.NewNop())
	bh := billing.NewEventHandler(rdb, fix.providerAddr.Hex(),
		big.NewInt(0), big.NewInt(100), new(big.Int), new(big.Int), signer, zap.NewNop())

	srv := buildServer(t, dtona, bh, rdb)

	// ── 1. POST /sandbox ──────────────────────────────────────────────────────
	postSandbox(t, ctx, srv.URL, e2eUserKeyHex)

	// Wait for Daytona to receive the create request.
	waitFor(t, "Daytona create request", 3*time.Second, func() bool {
		return len(mock.createdIDs()) == 1
	})
	sandboxID := mock.createdIDs()[0]
	t.Logf("Daytona create confirmed: sandbox ID = %q", sandboxID)

	// Wait for the create-fee voucher to land in the queue.
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, fix.providerAddr.Hex())
	waitFor(t, "voucher in Redis queue", 3*time.Second, func() bool {
		n, _ := rdb.LLen(ctx, queueKey).Result()
		return n >= 1
	})

	// ── 2. Settler + stop handler ─────────────────────────────────────────────
	onchain := &simChainClient{
		contract:     fix.contract,
		providerAuth: fix.providerAuth,
		simClient:    fix.simClient,
		backend:      fix.backend,
	}
	stopCh := make(chan settler.StopSignal, 4)
	cfg := &config.Config{
		Chain:   config.ChainConfig{ProviderAddress: fix.providerAddr.Hex()},
		Billing: config.BillingConfig{VoucherIntervalSec: 1},
	}
	settlerCtx, settlerCancel := context.WithCancel(ctx)
	defer settlerCancel()
	go settler.Run(settlerCtx, cfg, rdb, onchain, stopCh, zap.NewNop())
	go runStopHandler(ctx, stopCh, dtona, rdb, zap.NewNop())

	// ── 3. Assert: Daytona received stop for the correct sandbox ──────────────
	waitFor(t, fmt.Sprintf("Daytona stop for %q", sandboxID), 10*time.Second, func() bool {
		for _, id := range mock.stoppedIDs() {
			if id == sandboxID {
				return true
			}
		}
		return false
	})
	t.Logf("Daytona stop confirmed: sandbox %q stopped", sandboxID)

	// ── 4. Assert: Redis stop key cleaned up ──────────────────────────────────
	stopKey := "stop:sandbox:" + sandboxID
	waitFor(t, fmt.Sprintf("Redis key %q deleted", stopKey), 3*time.Second, func() bool {
		n, _ := rdb.Exists(ctx, stopKey).Result()
		return n == 0
	})
	t.Logf("Redis cleanup confirmed: %q deleted", stopKey)
}

// ── noopBillingHooks ─────────────────────────────────────────────────────────

// noopBillingHooks satisfies proxy.BillingHooks with no-op methods.
// Used by tests that only care about proxy/ownership behavior, not billing.
type noopBillingHooks struct{}

func (n *noopBillingHooks) OnCreate(_ context.Context, _, _ string, _, _ int) {}
func (n *noopBillingHooks) OnStart(_ context.Context, _, _ string, _, _ int)  {}
func (n *noopBillingHooks) OnStop(_ context.Context, _ string)            {}
func (n *noopBillingHooks) OnDelete(_ context.Context, _ string)          {}
func (n *noopBillingHooks) OnArchive(_ context.Context, _ string)         {}
func (n *noopBillingHooks) EnsureSession(_ context.Context, _, _ string)  {}

// ── ownerMockDaytona ─────────────────────────────────────────────────────────

// ownerMockDaytona is a stateful mock Daytona server that stores sandboxes with
// their labels so the proxy's ownership-filter and ownership-check logic can be
// exercised without a real Daytona instance.
type ownerMockDaytona struct {
	mu        sync.Mutex
	sandboxes map[string]daytona.Sandbox
	nextID    int
	srv       *httptest.Server
}

func newOwnerMockDaytona(t *testing.T) *ownerMockDaytona {
	t.Helper()
	m := &ownerMockDaytona{sandboxes: make(map[string]daytona.Sandbox)}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, method := r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")

		// POST /api/sandbox → create sandbox, extract injected labels
		if method == http.MethodPost && path == "/api/sandbox" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			labels := map[string]string{}
			if ls, ok := body["labels"].(map[string]any); ok {
				for k, v := range ls {
					if s, ok := v.(string); ok {
						labels[k] = s
					}
				}
			}
			m.mu.Lock()
			m.nextID++
			id := fmt.Sprintf("sb-owner-%d", m.nextID)
			sb := daytona.Sandbox{ID: id, State: "running", Labels: labels}
			m.sandboxes[id] = sb
			m.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(sb)
			return
		}

		// GET /api/sandbox → list all (proxy does the owner-label filtering)
		if method == http.MethodGet && path == "/api/sandbox" {
			m.mu.Lock()
			list := make([]daytona.Sandbox, 0, len(m.sandboxes))
			for _, s := range m.sandboxes {
				list = append(list, s)
			}
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(list)
			return
		}

		// GET /api/sandbox/:id → single sandbox lookup
		// path = "/api/sandbox/<id>" → trim leading "/" → split into 3 parts
		parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 4)
		if method == http.MethodGet && len(parts) == 3 &&
			parts[0] == "api" && parts[1] == "sandbox" {
			id := parts[2]
			m.mu.Lock()
			sb, ok := m.sandboxes[id]
			m.mu.Unlock()
			if !ok {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(sb)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// ── ownership helpers ─────────────────────────────────────────────────────────

// postSandboxGetID sends an authenticated POST /api/sandbox and returns the
// created sandbox ID extracted from the JSON response body.
func postSandboxGetID(t *testing.T, ctx context.Context, srvURL, privKeyHex string) string {
	t.Helper()
	walletAddr, msgB64, sigHex := e2eSignedHeaders(t, privKeyHex)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		srvURL+"/api/sandbox", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msgB64)
	req.Header.Set("X-Wallet-Signature", sigHex)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sandbox: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sandbox: got HTTP %d, want 201; body: %s", resp.StatusCode, body)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.ID == "" {
		t.Fatalf("POST /api/sandbox: cannot extract ID from %q", body)
	}
	return result.ID
}

// getSandboxList sends GET /api/sandbox with auth headers and returns the
// (already owner-filtered) list returned by the proxy.
func getSandboxList(t *testing.T, ctx context.Context, srvURL, privKeyHex string) []daytona.Sandbox {
	t.Helper()
	walletAddr, msgB64, sigHex := e2eSignedHeaders(t, privKeyHex)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srvURL+"/api/sandbox", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msgB64)
	req.Header.Set("X-Wallet-Signature", sigHex)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sandbox: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/sandbox: got HTTP %d, want 200", resp.StatusCode)
	}
	var list []daytona.Sandbox
	_ = json.NewDecoder(resp.Body).Decode(&list)
	return list
}

// getSandboxStatus sends GET /api/sandbox/:id with auth headers and returns
// the HTTP status code.  Used to verify 200 (owner) or 403 (non-owner).
func getSandboxStatus(t *testing.T, ctx context.Context, srvURL, sandboxID, privKeyHex string) int {
	t.Helper()
	walletAddr, msgB64, sigHex := e2eSignedHeaders(t, privKeyHex)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		srvURL+"/api/sandbox/"+sandboxID, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Wallet-Address", walletAddr)
	req.Header.Set("X-Signed-Message", msgB64)
	req.Header.Set("X-Wallet-Signature", sigHex)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/sandbox/%s: %v", sandboxID, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// ── Test 3: ownership filtering ───────────────────────────────────────────────

// TestComponent_OwnershipFiltering verifies the proxy's owner-isolation guarantees:
//
//  1. POST /api/sandbox injects the caller's wallet address into labels["daytona-owner"].
//  2. GET /api/sandbox returns only the requesting user's sandboxes.
//  3. GET /api/sandbox/:id returns 403 when accessed by a non-owner.
//  4. GET /api/sandbox/:id returns 200 for the owner.
func TestComponent_OwnershipFiltering(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	mock := newOwnerMockDaytona(t)
	dtona := daytona.NewClient(mock.srv.URL, "test-key")
	srv := buildServer(t, dtona, &noopBillingHooks{}, rdb)

	// Two distinct Anvil test wallets.
	const (
		userAKey = "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
		userBKey = "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
	)

	// ── 1. Each user creates one sandbox ─────────────────────────────────────
	sbAID := postSandboxGetID(t, ctx, srv.URL, userAKey)
	sbBID := postSandboxGetID(t, ctx, srv.URL, userBKey)
	t.Logf("userA sandbox: %s | userB sandbox: %s", sbAID, sbBID)

	// ── 2. List: each user sees only their own sandbox ────────────────────────
	listA := getSandboxList(t, ctx, srv.URL, userAKey)
	if len(listA) != 1 || listA[0].ID != sbAID {
		t.Fatalf("userA GET /sandbox: expected [{%s}], got %+v", sbAID, listA)
	}
	listB := getSandboxList(t, ctx, srv.URL, userBKey)
	if len(listB) != 1 || listB[0].ID != sbBID {
		t.Fatalf("userB GET /sandbox: expected [{%s}], got %+v", sbBID, listB)
	}
	t.Log("list filtering: PASS")

	// ── 3. Cross-owner GET returns 403 ───────────────────────────────────────
	if got := getSandboxStatus(t, ctx, srv.URL, sbBID, userAKey); got != http.StatusForbidden {
		t.Fatalf("GET /sandbox/%s as userA: expected 403, got %d", sbBID, got)
	}
	if got := getSandboxStatus(t, ctx, srv.URL, sbAID, userBKey); got != http.StatusForbidden {
		t.Fatalf("GET /sandbox/%s as userB: expected 403, got %d", sbAID, got)
	}
	t.Log("cross-owner access control: PASS")

	// ── 4. Owner GET returns 200 ──────────────────────────────────────────────
	if got := getSandboxStatus(t, ctx, srv.URL, sbAID, userAKey); got != http.StatusOK {
		t.Fatalf("GET /sandbox/%s as userA: expected 200, got %d", sbAID, got)
	}
	if got := getSandboxStatus(t, ctx, srv.URL, sbBID, userBKey); got != http.StatusOK {
		t.Fatalf("GET /sandbox/%s as userB: expected 200, got %d", sbBID, got)
	}
	t.Log("owner access: PASS")
}
