//go:build e2e

package main

// E2E tests exercise the complete billing pipeline against real external
// services: live Daytona, real Redis, and the 0G Galileo testnet.
//
// TestMain starts the full server (proxy + settler + generator) once and keeps
// it running across all TestE2E_* functions.  Each test only exercises one
// scenario; the shared infrastructure is never restarted between tests.
//
// Prerequisites:
//
//	MOCK_TEE=true
//	MOCK_APP_PRIVATE_KEY=0x<key>    TEE key = provider key = user wallet
//	MOCK_APP_ETH_ADDRESS=0x<addr>   (optional; derived from key if absent)
//	REDIS_ADDR                      (optional; default localhost:6379)
//	REDIS_PASSWORD                  (optional)
//	INTEGRATION_RPC_URL             (optional; default https://evmrpc-testnet.0g.ai)
//	INTEGRATION_CONTRACT            (optional; default 0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210)
//	INTEGRATION_DAYTONA_URL         (optional; default http://localhost:3000)
//	INTEGRATION_DAYTONA_KEY         (optional; default daytona_admin_key)
//	INTEGRATION_USER_KEY            (optional; defaults to MOCK_APP_PRIVATE_KEY)
//	INTEGRATION_VOUCHER_INTERVAL_SEC (optional; default 5 seconds)
//	INTEGRATION_CREATE_FEE           (optional; default 1 neuron)
//	INTEGRATION_COMPUTE_PRICE        (optional; default 1 neuron/sec)
//
// Before running:
//  1. The account must have deposited into the contract.
//  2. The account must have acknowledged itself as the TEE signer.
//
// Run with:
//
//	MOCK_TEE=true \
//	MOCK_APP_PRIVATE_KEY=0x<key> \
//	go test -v -tags e2e ./cmd/billing/ -run TestE2E -timeout 10m

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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
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

// ── Shared E2E environment ────────────────────────────────────────────────────

type e2eEnv struct {
	srv             *httptest.Server
	rdb             *redis.Client
	onchain         *chain.Client
	providerAddr    common.Address
	userKeyHex      string
	userAddr        common.Address
	queueKey        string
	voucherInterval time.Duration     // how often the generator fires
	cancel          context.CancelFunc // stops settler + generator
	cfg             *config.Config
	mainPrivKey     *ecdsa.PrivateKey // TEE/provider key; used to fund ephemeral accounts
	createFee       *big.Int
	computePrice    *big.Int
}

var globalE2E *e2eEnv

// TestMain starts the shared E2E environment once for all tests.
// If MOCK_APP_PRIVATE_KEY is absent or any dependency is unreachable,
// globalE2E is left nil and every TestE2E_* skips automatically.
// Component tests (which wire their own mock server) are unaffected.
func TestMain(m *testing.M) {
	if key := strings.TrimPrefix(os.Getenv("MOCK_APP_PRIVATE_KEY"), "0x"); key != "" {
		env, err := setupE2E(key)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[E2E] setup skipped: %v\n", err)
		} else {
			globalE2E = env
		}
	}
	code := m.Run()
	if globalE2E != nil {
		globalE2E.cancel()
		globalE2E.srv.Close()
	}
	os.Exit(code)
}

func setupE2E(teeKeyHex string) (*e2eEnv, error) {
	const (
		defaultRPC      = "https://evmrpc-testnet.0g.ai"
		defaultContract = "0x24cD979DBd0Ae924a3f0c832a724CF4C58E5C210"
		defaultDaytona  = "http://localhost:3000"
		defaultDayKey   = "daytona_admin_key"
		defaultRedis    = "localhost:6379"
		chainIDVal      = int64(16602)
	)

	rpcURL := envOrDefault("INTEGRATION_RPC_URL", defaultRPC)
	contractAddr := envOrDefault("INTEGRATION_CONTRACT", defaultContract)
	daytonaURL := envOrDefault("INTEGRATION_DAYTONA_URL", defaultDaytona)
	daytonaKey := envOrDefault("INTEGRATION_DAYTONA_KEY", defaultDayKey)
	redisAddr := envOrDefault("REDIS_ADDR", defaultRedis)
	redisPassword := os.Getenv("REDIS_PASSWORD")
	userKeyHex := strings.TrimPrefix(envOrDefault("INTEGRATION_USER_KEY", teeKeyHex), "0x")
	createFeeStr := envOrDefault("INTEGRATION_CREATE_FEE", "1")
	computePriceStr := envOrDefault("INTEGRATION_COMPUTE_PRICE", "1")
	voucherIntervalSec, err := strconv.ParseInt(envOrDefault("INTEGRATION_VOUCHER_INTERVAL_SEC", "5"), 10, 64)
	if err != nil || voucherIntervalSec < 1 {
		return nil, fmt.Errorf("invalid INTEGRATION_VOUCHER_INTERVAL_SEC: must be a positive integer")
	}

	if !daytonaReachable(daytonaURL, daytonaKey) {
		return nil, fmt.Errorf("Daytona not reachable at %s", daytonaURL)
	}

	teePrivKey, err := crypto.HexToECDSA(teeKeyHex)
	if err != nil {
		return nil, fmt.Errorf("parse MOCK_APP_PRIVATE_KEY: %w", err)
	}
	providerAddr := crypto.PubkeyToAddress(teePrivKey.PublicKey)

	userPrivKey, err := crypto.HexToECDSA(userKeyHex)
	if err != nil {
		return nil, fmt.Errorf("parse user key: %w", err)
	}
	userAddr := crypto.PubkeyToAddress(userPrivKey.PublicKey)

	// Redis
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, Password: redisPassword})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("Redis not reachable at %s: %w", redisAddr, err)
	}

	// Flush stale billing sessions left by previous interrupted runs.
	// Without this, the generator keeps billing orphaned sessions indefinitely
	// (they never auto-stop because the main account always has sufficient balance).
	if keys, err := rdb.Keys(context.Background(), "billing:compute:*").Result(); err == nil && len(keys) > 0 {
		rdb.Del(context.Background(), keys...)
	}

	// Chain client
	cfg := &config.Config{
		Chain: config.ChainConfig{
			RPCURL:          rpcURL,
			ContractAddress: contractAddr,
			TEEPrivateKey:   teeKeyHex,
			ProviderAddress: providerAddr.Hex(),
			ChainID:         chainIDVal,
		},
		Billing: config.BillingConfig{VoucherIntervalSec: voucherIntervalSec, ComputePricePerSec: computePriceStr, CreateFee: createFeeStr},
		Daytona: config.DaytonaConfig{APIURL: daytonaURL, AdminKey: daytonaKey},
		Redis:   config.RedisConfig{Addr: redisAddr, Password: redisPassword},
	}
	onchain, err := chain.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("chain client: %w", err)
	}

	// Billing components
	createFee, ok := new(big.Int).SetString(createFeeStr, 10)
	if !ok {
		return nil, fmt.Errorf("invalid INTEGRATION_CREATE_FEE: %s", createFeeStr)
	}
	computePrice, ok := new(big.Int).SetString(computePriceStr, 10)
	if !ok {
		return nil, fmt.Errorf("invalid INTEGRATION_COMPUTE_PRICE: %s", computePriceStr)
	}
	signer := billing.NewSigner(
		onchain.PrivateKey(),
		big.NewInt(chainIDVal),
		common.HexToAddress(contractAddr),
		providerAddr,
		rdb,
		onchain,
		zap.NewNop(),
	)
	bh := billing.NewEventHandler(rdb, providerAddr.Hex(), computePrice, createFee, signer, zap.NewNop())

	// Proxy server
	gin.SetMode(gin.TestMode)
	r := gin.New()
	dtona := daytona.NewClient(daytonaURL, daytonaKey)
	minBalance := new(big.Int).Add(createFee, new(big.Int).Mul(computePrice, big.NewInt(cfg.Billing.VoucherIntervalSec)))
	proxy.NewHandler(dtona, bh, onchain, minBalance, zap.NewNop()).Register(r.Group("/api", auth.Middleware(rdb)))
	srv := httptest.NewServer(r)

	// Settler + generator (run for the lifetime of the test suite)
	bgCtx, cancel := context.WithCancel(context.Background())
	stopCh := make(chan settler.StopSignal, 100)
	go settler.Run(bgCtx, cfg, rdb, onchain, stopCh, zap.NewNop())
	go billing.RunGenerator(bgCtx, cfg, rdb, signer, zap.NewNop())
	go runStopHandler(bgCtx, stopCh, dtona, rdb, zap.NewNop())

	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, providerAddr.Hex())

	fmt.Printf("[E2E] provider:       %s\n", providerAddr.Hex())
	fmt.Printf("[E2E] user:           %s\n", userAddr.Hex())
	fmt.Printf("[E2E] proxy:          %s\n", srv.URL)
	fmt.Printf("[E2E] contract:       %s\n", contractAddr)
	fmt.Printf("[E2E] create fee:     %s neuron\n", createFeeStr)
	fmt.Printf("[E2E] compute price:  %s neuron/sec\n", computePriceStr)

	return &e2eEnv{
		srv:             srv,
		rdb:             rdb,
		onchain:         onchain,
		providerAddr:    providerAddr,
		userKeyHex:      userKeyHex,
		userAddr:        userAddr,
		queueKey:        queueKey,
		voucherInterval: time.Duration(cfg.Billing.VoucherIntervalSec) * time.Second,
		cancel:          cancel,
		cfg:             cfg,
		mainPrivKey:     teePrivKey,
		createFee:       createFee,
		computePrice:    computePrice,
	}, nil
}

// ── E2E helpers ───────────────────────────────────────────────────────────────

// e2eSkip skips t if the E2E environment is not available.
func e2eSkip(t *testing.T) {
	t.Helper()
	if globalE2E == nil {
		t.Skip("E2E environment not set up — set MOCK_APP_PRIVATE_KEY to enable")
	}
}

// e2eRequest builds an authenticated request to the proxy server.
func (e *e2eEnv) e2eRequest(ctx context.Context, method, path string, body io.Reader) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, method, e.srv.URL+path, body)
	wa, mb, sh := e2eSignedHeadersRaw(e.userKeyHex)
	req.Header.Set("X-Wallet-Address", wa)
	req.Header.Set("X-Signed-Message", mb)
	req.Header.Set("X-Wallet-Signature", sh)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// e2eSignedHeadersRaw builds EIP-191 auth headers without requiring *testing.T.
func e2eSignedHeadersRaw(privKeyHex string) (walletAddr, msgB64, sigHex string) {
	privKey, _ := crypto.HexToECDSA(privKeyHex)
	walletAddr = crypto.PubkeyToAddress(privKey.PublicKey).Hex()
	req := auth.SignedRequest{
		Action:    "create",
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
		Nonce:     fmt.Sprintf("e2e-%d", time.Now().UnixNano()),
	}
	msgBytes, _ := json.Marshal(req)
	hash := auth.HashMessage(msgBytes)
	sig, _ := crypto.Sign(hash, privKey)
	sig[64] += 27
	return walletAddr,
		base64.StdEncoding.EncodeToString(msgBytes),
		"0x" + hex.EncodeToString(sig)
}

// e2eCreate creates a sandbox via the proxy and returns its ID.
func (e *e2eEnv) e2eCreate(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := e.e2eRequest(ctx, http.MethodPost, "/api/sandbox", strings.NewReader(`{}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sandbox: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST /api/sandbox: got %d; body: %s", resp.StatusCode, body)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.ID == "" {
		t.Fatalf("cannot extract sandbox ID from %q", body)
	}
	t.Logf("created sandbox: %s", result.ID)
	return result.ID
}

// e2eStop stops a sandbox via the proxy.
func (e *e2eEnv) e2eStop(t *testing.T, ctx context.Context, sandboxID string) {
	t.Helper()
	req := e.e2eRequest(ctx, http.MethodPost, "/api/sandbox/"+sandboxID+"/stop", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("stop %s: %v (may already be stopped)", sandboxID, err)
		return
	}
	resp.Body.Close()
	t.Logf("stopped sandbox %s (status %d)", sandboxID, resp.StatusCode)
}

// e2eNonce returns the current on-chain lastNonce for the test account pair.
func (e *e2eEnv) e2eNonce(ctx context.Context) (*big.Int, error) {
	return e.onchain.GetLastNonce(ctx, e.userAddr, e.providerAddr)
}

// ── E2E tests ─────────────────────────────────────────────────────────────────

// TestE2E_CreateFeeSettled verifies that creating a sandbox triggers a
// create-fee settlement on chain (nonce advances by 1).
func TestE2E_CreateFeeSettled(t *testing.T) {
	e2eSkip(t)
	env := globalE2E
	ctx := context.Background()

	// Pre-flight: user must have deposited.
	balance, _, _, err := env.onchain.GetProviderBalance(ctx, env.userAddr, env.providerAddr)
	if err != nil {
		t.Fatalf("GetProviderBalance: %v", err)
	}
	if balance.Sign() == 0 {
		t.Skip("user balance is 0 — run cmd/setup first")
	}
	t.Logf("balance before: %s neuron", balance)

	nonceBefore, err := env.e2eNonce(ctx)
	if err != nil {
		t.Fatalf("nonce before: %v", err)
	}
	t.Logf("nonce before: %s", nonceBefore)

	// Flush stale vouchers.
	env.rdb.Del(ctx, env.queueKey) //nolint:errcheck

	sandboxID := env.e2eCreate(t, ctx)
	t.Cleanup(func() { env.e2eStop(t, context.Background(), sandboxID) })

	// Wait for create-fee settlement.
	expected := new(big.Int).Add(nonceBefore, big.NewInt(1))
	var nonceAfter *big.Int
	waitFor(t, fmt.Sprintf("nonce >= %s", expected), 5*time.Minute, func() bool {
		n, err := env.e2eNonce(ctx)
		if err == nil && n.Cmp(expected) >= 0 {
			nonceAfter = n
			return true
		}
		return false
	})
	t.Logf("create-fee settled: nonce = %s (delta = +%s)", nonceAfter, new(big.Int).Sub(nonceAfter, nonceBefore))
}

// TestE2E_ComputeFeeSettled verifies that stopping a sandbox after it has been
// running triggers a compute-fee settlement on chain (nonce advances by 1 more).
func TestE2E_ComputeFeeSettled(t *testing.T) {
	e2eSkip(t)
	env := globalE2E
	ctx := context.Background()

	balance, _, _, err := env.onchain.GetProviderBalance(ctx, env.userAddr, env.providerAddr)
	if err != nil {
		t.Fatalf("GetProviderBalance: %v", err)
	}
	if balance.Sign() == 0 {
		t.Skip("user balance is 0 — run cmd/setup first")
	}

	// Flush stale vouchers.
	env.rdb.Del(ctx, env.queueKey) //nolint:errcheck

	nonceBefore, err := env.e2eNonce(ctx)
	if err != nil {
		t.Fatalf("nonce before: %v", err)
	}
	t.Logf("nonce before: %s", nonceBefore)

	sandboxID := env.e2eCreate(t, ctx)

	// Wait for create-fee to settle (nonce+1).
	afterCreate := new(big.Int).Add(nonceBefore, big.NewInt(1))
	waitFor(t, fmt.Sprintf("create-fee nonce >= %s", afterCreate), 5*time.Minute, func() bool {
		n, err := env.e2eNonce(ctx)
		return err == nil && n.Cmp(afterCreate) >= 0
	})
	t.Logf("create-fee settled: nonce >= %s", afterCreate)

	// Let the sandbox run for 6 voucher intervals so the periodic generator
	// fires several times before the final stop voucher.
	runDuration := 6 * env.voucherInterval
	t.Logf("sandbox running for %s (6 × voucher interval)…", runDuration)
	time.Sleep(runDuration)

	// Stop via proxy → OnStop → generateFinalVoucher → compute-fee settlement.
	env.e2eStop(t, ctx, sandboxID)

	// Wait for compute-fee to settle (nonce+2 minimum; actual may be higher if
	// the periodic generator fired multiple times during the run duration).
	afterStop := new(big.Int).Add(nonceBefore, big.NewInt(2))
	var nonceAfterStop *big.Int
	waitFor(t, fmt.Sprintf("compute-fee nonce >= %s", afterStop), 5*time.Minute, func() bool {
		n, err := env.e2eNonce(ctx)
		if err == nil && n.Cmp(afterStop) >= 0 {
			nonceAfterStop = n
			return true
		}
		return false
	})
	t.Logf("compute-fee settled: nonce = %s (expected >= %s, delta = +%s)",
		nonceAfterStop, afterStop, new(big.Int).Sub(nonceAfterStop, nonceBefore))

	balance2, _, _, _ := env.onchain.GetProviderBalance(ctx, env.userAddr, env.providerAddr)
	t.Logf("balance after: %s neuron", balance2)
}

// TestE2E_InsufficientBalance verifies that a wallet with no on-chain deposit
// is rejected with HTTP 402 before Daytona is ever called.
func TestE2E_InsufficientBalance(t *testing.T) {
	e2eSkip(t)
	env := globalE2E
	ctx := context.Background()

	// Generate a fresh ephemeral key — never deposited, balance = 0.
	ephemeralKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate ephemeral key: %v", err)
	}
	ephemeralHex := hex.EncodeToString(crypto.FromECDSA(ephemeralKey))

	wa, mb, sh := e2eSignedHeadersRaw(ephemeralHex)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		env.srv.URL+"/api/sandbox", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Wallet-Address", wa)
	req.Header.Set("X-Signed-Message", mb)
	req.Header.Set("X-Wallet-Signature", sh)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sandbox: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPaymentRequired {
		t.Fatalf("want 402 Payment Required, got %d; body: %s", resp.StatusCode, body)
	}
	t.Logf("correctly rejected with 402: %s", body)
}

// TestE2E_AutoStopInsufficientBalance verifies that a sandbox is automatically
// stopped by the billing system when the user's on-chain balance is exhausted.
//
// An ephemeral wallet is funded with exactly createFee + 1 compute period of
// tokens.  After the create-fee voucher settles the balance drops to 1 neuron.
// After the first periodic compute voucher settles it reaches 0.  The next
// compute voucher returns StatusInsufficientBalance, causing the settler to
// write a stop:sandbox:<id> key and the runStopHandler to call Daytona stop.
func TestE2E_AutoStopInsufficientBalance(t *testing.T) {
	e2eSkip(t)
	env := globalE2E
	ctx := context.Background()

	// Deposit exactly createFee + 1 compute period.
	// After create-fee settles: balance = 1 period of compute.
	// After 1st periodic compute voucher settles: balance = 0.
	// 2nd periodic voucher → StatusInsufficientBalance → auto-stop.
	voucherIntervalSec := int64(env.voucherInterval / time.Second)
	oneComputePeriod := new(big.Int).Mul(env.computePrice, big.NewInt(voucherIntervalSec))
	depositWei := new(big.Int).Add(env.createFee, oneComputePeriod)
	t.Logf("ephemeral deposit: %s neuron (createFee=%s + %ds×computePrice=%s)",
		depositWei, env.createFee, voucherIntervalSec, env.computePrice)

	ephKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate ephemeral key: %v", err)
	}
	ephKeyHex := hex.EncodeToString(crypto.FromECDSA(ephKey))
	t.Logf("ephemeral address: %s", crypto.PubkeyToAddress(ephKey.PublicKey).Hex())

	env.e2eSetupEphemeralAccount(t, ctx, ephKey, depositWei)

	sandboxID := env.e2eCreateAs(t, ctx, ephKeyHex)
	t.Logf("sandbox created: %s", sandboxID)

	stopKey := "stop:sandbox:" + sandboxID

	// Wait for the settler to detect InsufficientBalance and schedule a stop.
	waitFor(t, fmt.Sprintf("stop key %q set", stopKey), 5*time.Minute, func() bool {
		n, _ := env.rdb.Exists(ctx, stopKey).Result()
		return n == 1
	})
	t.Logf("auto-stop scheduled: stop key set for sandbox %s", sandboxID)

	// Wait for runStopHandler to call Daytona stop and delete the key.
	waitFor(t, fmt.Sprintf("stop key %q deleted", stopKey), 2*time.Minute, func() bool {
		n, _ := env.rdb.Exists(ctx, stopKey).Result()
		return n == 0
	})
	t.Logf("auto-stop completed: sandbox %s stopped and key cleaned up", sandboxID)
}

// e2eCreateAs creates a sandbox via the proxy using the given private key hex.
func (e *e2eEnv) e2eCreateAs(t *testing.T, ctx context.Context, privKeyHex string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, e.srv.URL+"/api/sandbox", strings.NewReader(`{}`))
	wa, mb, sh := e2eSignedHeadersRaw(privKeyHex)
	req.Header.Set("X-Wallet-Address", wa)
	req.Header.Set("X-Signed-Message", mb)
	req.Header.Set("X-Wallet-Signature", sh)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sandbox as %s: %v", wa, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST /api/sandbox as %s: got %d; body: %s", wa, resp.StatusCode, body)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.ID == "" {
		t.Fatalf("cannot extract sandbox ID from %q", body)
	}
	t.Logf("created sandbox as %s: %s", wa, result.ID)
	return result.ID
}

// e2eSetupEphemeralAccount prepares a fresh key for billing tests:
//  1. Transfers 0.01 ETH from the main key to the ephemeral address for gas.
//  2. Calls contract.Deposit(provider) with msg.value=depositWei from the ephemeral key.
//  3. Calls contract.AcknowledgeTEESigner(provider, true) from the ephemeral key.
func (e *e2eEnv) e2eSetupEphemeralAccount(t *testing.T, ctx context.Context, ephKey *ecdsa.PrivateKey, depositWei *big.Int) {
	t.Helper()

	ethClient, err := ethclient.Dial(e.cfg.Chain.RPCURL)
	if err != nil {
		t.Fatalf("dial rpc: %v", err)
	}
	defer ethClient.Close()

	chainID := big.NewInt(e.cfg.Chain.ChainID)
	contractAddr := common.HexToAddress(e.cfg.Chain.ContractAddress)
	ephAddr := crypto.PubkeyToAddress(ephKey.PublicKey)

	// Step 1: Fund the ephemeral address with enough 0G to cover gas for
	// Deposit + AcknowledgeTEESigner. Use gasPrice×300k as an upper bound
	// (Deposit ~150k gas + AcknowledgeTEESigner ~50k gas, 2× safety margin).
	gasPrice, err := ethClient.SuggestGasPrice(ctx)
	if err != nil {
		t.Fatalf("suggest gas price: %v", err)
	}
	gasFund := new(big.Int).Mul(gasPrice, big.NewInt(300_000))

	// Pre-flight: make sure main account can cover gasFund + cost of this transfer.
	mainBalance, err := ethClient.BalanceAt(ctx, e.userAddr, nil)
	if err != nil {
		t.Fatalf("get main balance: %v", err)
	}
	needed := new(big.Int).Add(gasFund, new(big.Int).Mul(gasPrice, big.NewInt(21_000)))
	if mainBalance.Cmp(needed) < 0 {
		t.Skipf("main account %s has insufficient 0G: balance %s neuron, need ~%s neuron — fund with testnet 0G first",
			e.userAddr.Hex(), mainBalance, needed)
	}

	mainNonce, err := ethClient.PendingNonceAt(ctx, e.userAddr)
	if err != nil {
		t.Fatalf("pending nonce for main: %v", err)
	}
	to := ephAddr
	fundTx, err := types.SignNewTx(e.mainPrivKey, types.NewEIP155Signer(chainID), &types.LegacyTx{
		Nonce:    mainNonce,
		To:       &to,
		Value:    gasFund,
		Gas:      21000,
		GasPrice: gasPrice,
	})
	if err != nil {
		t.Fatalf("sign fund tx: %v", err)
	}
	if err := ethClient.SendTransaction(ctx, fundTx); err != nil {
		t.Fatalf("send fund tx: %v", err)
	}
	if _, err := bind.WaitMined(ctx, ethClient, fundTx); err != nil {
		t.Fatalf("wait fund tx: %v", err)
	}
	t.Logf("funded ephemeral %s with %s neuron for gas", ephAddr.Hex(), gasFund)

	// Bind the contract for the ephemeral transactor.
	contract, err := chain.NewSandboxServing(contractAddr, ethClient)
	if err != nil {
		t.Fatalf("bind contract: %v", err)
	}
	ephAuth, err := bind.NewKeyedTransactorWithChainID(ephKey, chainID)
	if err != nil {
		t.Fatalf("ephemeral transactor: %v", err)
	}

	// Step 2: Deposit into the contract from the ephemeral account.
	// recipient = ephAddr so the balance is credited to the ephemeral user.
	ephAuth.Value = depositWei
	providerAddr := common.HexToAddress(e.cfg.Chain.ProviderAddress)
	depositTx, err := contract.Deposit(ephAuth, ephAddr, providerAddr)
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}
	if _, err := bind.WaitMined(ctx, ethClient, depositTx); err != nil {
		t.Fatalf("wait deposit: %v", err)
	}
	ephAuth.Value = big.NewInt(0)
	t.Logf("deposited %s neuron for ephemeral %s", depositWei, ephAddr.Hex())

	// Step 3: Acknowledge TEE signer from the ephemeral account.
	ackTx, err := contract.AcknowledgeTEESigner(ephAuth, e.providerAddr, true)
	if err != nil {
		t.Fatalf("acknowledgeTEESigner: %v", err)
	}
	if _, err := bind.WaitMined(ctx, ethClient, ackTx); err != nil {
		t.Fatalf("wait ack tx: %v", err)
	}
	t.Logf("ephemeral %s acknowledged TEE signer for provider %s",
		ephAddr.Hex(), e.providerAddr.Hex())
}

// ── Shared helpers ────────────────────────────────────────────────────────────

// envOrDefault returns the value of key if set, otherwise dflt.
func envOrDefault(key, dflt string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return dflt
}

// daytonaReachable returns true if Daytona responds with HTTP < 500.
func daytonaReachable(baseURL, adminKey string) bool {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/sandbox", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+adminKey)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}
