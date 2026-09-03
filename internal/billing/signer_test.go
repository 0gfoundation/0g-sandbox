package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// ── helpers ───────────────────────────────────────────────────────────────────

var (
	// Fixed deterministic test key (not used anywhere outside tests)
	testPrivKeyHex  = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	testChainID     = big.NewInt(31337)
	testContractHex = "0x5FbDB2315678afecb367f032d93F642f64180aa3"
	testProviderHex = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"
)

// mockNonceReader returns a fixed chain nonce or an error.
type mockNonceReader struct {
	nonce *big.Int
	err   error
}

func (m *mockNonceReader) GetLastNonce(_ context.Context, _, _ common.Address) (*big.Int, error) {
	if m.err != nil {
		return nil, m.err
	}
	return new(big.Int).Set(m.nonce), nil
}

func newTestSignerFull(t *testing.T) (*Signer, *redis.Client, common.Address) {
	t.Helper()
	return newTestSignerWithChainNonce(t, big.NewInt(0))
}

func newTestSignerWithChainNonce(t *testing.T, chainNonce *big.Int) (*Signer, *redis.Client, common.Address) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	privKey, err := crypto.HexToECDSA(testPrivKeyHex)
	if err != nil {
		t.Fatalf("load test private key: %v", err)
	}
	signerAddr := crypto.PubkeyToAddress(privKey.PublicKey)

	s := NewSigner(
		privKey,
		testChainID,
		common.HexToAddress(testContractHex),
		common.HexToAddress(testProviderHex),
		rdb,
		&mockNonceReader{nonce: chainNonce},
		zap.NewNop(),
	)
	return s, rdb, signerAddr
}

// ── IncrNonce: normal path ────────────────────────────────────────────────────

func TestIncrNonce_StartsAtOne(t *testing.T) {
	s, _, _ := newTestSignerFull(t)
	n, err := s.IncrNonce(context.Background(), testOwner, testProvider)
	if err != nil {
		t.Fatalf("IncrNonce: %v", err)
	}
	if n.Int64() != 1 {
		t.Errorf("first nonce: got %d want 1", n.Int64())
	}
}

func TestIncrNonce_Increments(t *testing.T) {
	s, _, _ := newTestSignerFull(t)
	ctx := context.Background()

	for i := int64(1); i <= 5; i++ {
		n, err := s.IncrNonce(ctx, testOwner, testProvider)
		if err != nil {
			t.Fatalf("IncrNonce [%d]: %v", i, err)
		}
		if n.Int64() != i {
			t.Errorf("nonce[%d]: got %d want %d", i, n.Int64(), i)
		}
	}
}

func TestIncrNonce_SeparateKeysPerPair(t *testing.T) {
	s, _, _ := newTestSignerFull(t)
	ctx := context.Background()

	ownerA := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	ownerB := "0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

	nA1, _ := s.IncrNonce(ctx, ownerA, testProvider)
	nB1, _ := s.IncrNonce(ctx, ownerB, testProvider)
	nA2, _ := s.IncrNonce(ctx, ownerA, testProvider)

	if nA1.Int64() != 1 {
		t.Errorf("ownerA first nonce: got %d want 1", nA1.Int64())
	}
	if nB1.Int64() != 1 {
		t.Errorf("ownerB first nonce: got %d want 1 (should have own counter)", nB1.Int64())
	}
	if nA2.Int64() != 2 {
		t.Errorf("ownerA second nonce: got %d want 2", nA2.Int64())
	}
}

func TestIncrNonce_CaseInsensitiveOwner(t *testing.T) {
	s, rdb, _ := newTestSignerFull(t)
	ctx := context.Background()

	upper := "0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	lower := strings.ToLower(upper)

	s.IncrNonce(ctx, upper, testProvider) //nolint:errcheck
	s.IncrNonce(ctx, lower, testProvider) //nolint:errcheck

	// Both calls must share the same Redis key → value = 2
	expectedKey := fmt.Sprintf(voucher.NonceKeyFmt,
		strings.ToLower(upper), strings.ToLower(testProvider))
	val, err := rdb.Get(ctx, expectedKey).Result()
	if err != nil {
		t.Fatalf("nonce key not found: %v", err)
	}
	if val != "2" {
		t.Errorf("nonce after 2 calls with same address (different case): got %q want 2", val)
	}
}

// ── IncrNonce: Redis-restart / chain-seeding path ─────────────────────────────

// TestIncrNonce_SeedsFromChainOnFirstCall simulates a Redis restart where the
// key is absent but the contract already has lastNonce = 5.
// The first emitted nonce must be 6 (strictly greater than on-chain value).
func TestIncrNonce_SeedsFromChainOnFirstCall(t *testing.T) {
	s, _, _ := newTestSignerWithChainNonce(t, big.NewInt(5))
	ctx := context.Background()

	n, err := s.IncrNonce(ctx, testOwner, testProvider)
	if err != nil {
		t.Fatalf("IncrNonce: %v", err)
	}
	if n.Int64() != 6 {
		t.Errorf("first nonce after restart: got %d want 6 (chain lastNonce=5)", n.Int64())
	}
}

// TestIncrNonce_SubsequentCallsAfterSeed verifies that after the chain-seeded
// first call, subsequent calls continue from the correct value.
func TestIncrNonce_SubsequentCallsAfterSeed(t *testing.T) {
	s, _, _ := newTestSignerWithChainNonce(t, big.NewInt(10))
	ctx := context.Background()

	n1, _ := s.IncrNonce(ctx, testOwner, testProvider) // seeds to 10, then INCR → 11
	n2, _ := s.IncrNonce(ctx, testOwner, testProvider) // key exists, plain INCR → 12
	n3, _ := s.IncrNonce(ctx, testOwner, testProvider) // → 13

	for i, n := range []*big.Int{n1, n2, n3} {
		want := int64(11 + i)
		if n.Int64() != want {
			t.Errorf("nonce[%d]: got %d want %d", i, n.Int64(), want)
		}
	}
}

// Finding #30 (#79): when the chain is unreachable the signer must FAIL, not
// guess 0 — nonce=1 is rejected INVALID_NONCE whenever the pair has settled
// before, and the settler discards those: unbilled compute with no recovery.
// Failing skips this period's voucher; the generator's backlog catch-up bills
// it once the chain read recovers.
func TestIncrNonce_ChainUnavailable_FailsClosed(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	privKey, _ := crypto.HexToECDSA(testPrivKeyHex)
	s := NewSigner(
		privKey, testChainID,
		common.HexToAddress(testContractHex),
		common.HexToAddress(testProviderHex),
		rdb,
		&mockNonceReader{err: errors.New("chain unreachable")},
		zap.NewNop(),
	)

	if _, err := s.IncrNonce(context.Background(), testOwner, testProvider); err == nil {
		t.Fatal("IncrNonce must fail closed when the seed read fails — guessing 0 emits permanently-rejected vouchers")
	}

	// No poisoned counter left behind: once the chain recovers, seeding works
	// and the first emitted nonce continues from on-chain state.
	if mr.Exists("billing:nonce:" + testOwner + ":" + testProvider) {
		t.Error("failed seed must not leave a counter key")
	}
}

// TestIncrNonce_ConcurrentSeed verifies that two goroutines racing on a missing
// key each receive a unique nonce (no duplicates even without a lock).
func TestIncrNonce_ConcurrentSeed(t *testing.T) {
	s, _, _ := newTestSignerWithChainNonce(t, big.NewInt(100))
	ctx := context.Background()

	results := make(chan int64, 2)
	for range 2 {
		go func() {
			n, err := s.IncrNonce(ctx, testOwner, testProvider)
			if err != nil {
				t.Errorf("IncrNonce goroutine: %v", err)
				results <- -1
				return
			}
			results <- n.Int64()
		}()
	}

	n1, n2 := <-results, <-results
	if n1 == n2 {
		t.Errorf("concurrent IncrNonce returned duplicate nonce %d", n1)
	}
	// Both must be > 100 (seeded from chain)
	for _, n := range []int64{n1, n2} {
		if n <= 100 {
			t.Errorf("nonce %d should be > chain lastNonce 100", n)
		}
	}
}

// ── Enqueue ───────────────────────────────────────────────────────────────────

func TestEnqueue_PushesToQueue(t *testing.T) {
	s, rdb, _ := newTestSignerFull(t)
	ctx := context.Background()

	v := &voucher.SandboxVoucher{
		SandboxID: "sb-sign-1",
		User:      common.HexToAddress(testOwner),
		Provider:  common.HexToAddress(testProviderHex),
		TotalFee:  big.NewInt(500),
		UsageHash: voucher.BuildUsageHash("sb-sign-1", 1000, 1060, 1),
	}

	if err := s.Enqueue(ctx, v); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, common.HexToAddress(testProviderHex).Hex())
	n, err := rdb.LLen(ctx, queueKey).Result()
	if err != nil {
		t.Fatalf("LLEN: %v", err)
	}
	if n != 1 {
		t.Errorf("queue length: got %d want 1", n)
	}
}

func TestEnqueue_QueueItemIsValidJSON(t *testing.T) {
	s, rdb, _ := newTestSignerFull(t)
	ctx := context.Background()

	v := &voucher.SandboxVoucher{
		SandboxID: "sb-json",
		User:      common.HexToAddress(testOwner),
		Provider:  common.HexToAddress(testProviderHex),
		TotalFee:  big.NewInt(200),
		UsageHash: voucher.BuildUsageHash("sb-json", 2000, 2060, 1),
	}
	s.Enqueue(ctx, v) //nolint:errcheck

	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, common.HexToAddress(testProviderHex).Hex())
	raw, _ := rdb.LPop(ctx, queueKey).Result()

	var got voucher.SandboxVoucher
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("queue item is not valid JSON: %v", err)
	}
	if got.SandboxID != "sb-json" {
		t.Errorf("SandboxID: got %q want %q", got.SandboxID, "sb-json")
	}
}

// ── Sign + Enqueue ────────────────────────────────────────────────────────────

func TestSign_SignatureVerifiable(t *testing.T) {
	s, rdb, signerAddr := newTestSignerFull(t)
	ctx := context.Background()

	contractAddr := common.HexToAddress(testContractHex)

	v := &voucher.SandboxVoucher{
		SandboxID: "sb-verify",
		User:      common.HexToAddress(testOwner),
		Provider:  common.HexToAddress(testProviderHex),
		TotalFee:  big.NewInt(300),
		UsageHash: voucher.BuildUsageHash("sb-verify", 3000, 3060, 1),
	}
	if err := s.Sign(ctx, v); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	s.Enqueue(ctx, v) //nolint:errcheck

	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, common.HexToAddress(testProviderHex).Hex())
	raw, _ := rdb.LPop(ctx, queueKey).Result()
	var got voucher.SandboxVoucher
	json.Unmarshal([]byte(raw), &got) //nolint:errcheck

	recovered, err := voucher.Verify(&got, testChainID, contractAddr)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if recovered != signerAddr {
		t.Errorf("recovered signer: got %s want %s", recovered.Hex(), signerAddr.Hex())
	}
}

func TestEnqueue_QueueKeyUsesProviderAddress(t *testing.T) {
	s, rdb, _ := newTestSignerFull(t)
	ctx := context.Background()

	v := &voucher.SandboxVoucher{
		SandboxID: "sb-qkey",
		User:      common.HexToAddress(testOwner),
		Provider:  common.HexToAddress(testProviderHex),
		TotalFee:  big.NewInt(100),
	}
	s.Enqueue(ctx, v) //nolint:errcheck

	correctKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, common.HexToAddress(testProviderHex).Hex())
	wrongKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, "0x0000000000000000000000000000000000000000")

	n, _ := rdb.LLen(ctx, correctKey).Result()
	if n != 1 {
		t.Errorf("correct queue length: got %d want 1", n)
	}
	nWrong, _ := rdb.LLen(ctx, wrongKey).Result()
	if nWrong != 0 {
		t.Errorf("wrong queue should be empty, got %d", nWrong)
	}
}

func TestEnqueue_MultipleVouchers_FIFOOrder(t *testing.T) {
	s, rdb, _ := newTestSignerFull(t)
	ctx := context.Background()
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, common.HexToAddress(testProviderHex).Hex())

	for i := int64(1); i <= 3; i++ {
		v := &voucher.SandboxVoucher{
			SandboxID: fmt.Sprintf("sb-%d", i),
			User:      common.HexToAddress(testOwner),
			Provider:  common.HexToAddress(testProviderHex),
			TotalFee:  big.NewInt(i * 100),
		}
		s.Enqueue(ctx, v) //nolint:errcheck
	}

	for i := int64(1); i <= 3; i++ {
		raw, err := rdb.LPop(ctx, queueKey).Result()
		if err != nil {
			t.Fatalf("LPop [%d]: %v", i, err)
		}
		var got voucher.SandboxVoucher
		json.Unmarshal([]byte(raw), &got) //nolint:errcheck
		want := fmt.Sprintf("sb-%d", i)
		if got.SandboxID != want {
			t.Errorf("FIFO order[%d]: got %q want %q", i, got.SandboxID, want)
		}
	}
}
