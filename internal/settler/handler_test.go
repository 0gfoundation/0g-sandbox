package settler

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

var (
	testUser     = common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	testProvider = common.HexToAddress("0x1111111111111111111111111111111111111111")
)

const testQueueKey = "voucher:queue:test"

func makeVoucher(sandboxID string) voucher.SandboxVoucher {
	return voucher.SandboxVoucher{
		SandboxID: sandboxID,
		User:      testUser,
		Provider:  testProvider,
		TotalFee:  big.NewInt(100),
		Nonce:     big.NewInt(1),
	}
}

// pushRemaining pushes vouchers[1:] (the items NOT yet BLPOP'd) into the queue.
func pushRemaining(t *testing.T, rdb *redis.Client, key string, vs []voucher.SandboxVoucher) {
	t.Helper()
	ctx := context.Background()
	for _, v := range vs[1:] {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal voucher: %v", err)
		}
		rdb.RPush(ctx, key, string(raw)) //nolint:errcheck
	}
}

// queueLen returns the current queue length.
func queueLen(t *testing.T, rdb *redis.Client, key string) int64 {
	t.Helper()
	n, err := rdb.LLen(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("LLEN %s: %v", key, err)
	}
	return n
}

// stopKey returns the Redis key that persistStop writes.
func stopKey(sandboxID string) string { return "stop:sandbox:" + sandboxID }

// dlqKey returns the DLQ key for a provider address.
func dlqKey(addr common.Address) string {
	return fmt.Sprintf(voucher.VoucherDLQKeyFmt, addr.Hex())
}

// ── StatusSuccess ─────────────────────────────────────────────────────────────

func TestHandleStatuses_Success_NoSideEffects(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{makeVoucher("sb-ok")}
	sts := []chain.SettlementStatus{chain.StatusSuccess}

	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	// No stop key written
	exists, _ := rdb.Exists(ctx, stopKey("sb-ok")).Result()
	if exists != 0 {
		t.Error("stop key must not exist for StatusSuccess")
	}
	// No stop signal sent
	if len(stopCh) != 0 {
		t.Errorf("stopCh must be empty for StatusSuccess, got %d signals", len(stopCh))
	}
}

// ── StatusInsufficientBalance ─────────────────────────────────────────────────

func TestHandleStatuses_InsufficientBalance_PersistsAndSignals(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{makeVoucher("sb-broke")}
	sts := []chain.SettlementStatus{chain.StatusInsufficientBalance}

	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	// Stop key persisted
	reason, err := rdb.Get(ctx, stopKey("sb-broke")).Result()
	if err != nil {
		t.Fatalf("stop key not written: %v", err)
	}
	if reason != "insufficient_balance" {
		t.Errorf("stop reason: got %q want %q", reason, "insufficient_balance")
	}
	// Signal delivered
	if len(stopCh) != 1 {
		t.Fatalf("expected 1 stop signal, got %d", len(stopCh))
	}
	sig := <-stopCh
	if sig.SandboxID != "sb-broke" {
		t.Errorf("signal SandboxID: got %q want %q", sig.SandboxID, "sb-broke")
	}
	if sig.Reason != "insufficient_balance" {
		t.Errorf("signal Reason: got %q want %q", sig.Reason, "insufficient_balance")
	}
}

// ── StatusNotAcknowledged ────────────────────────────────────────────────────

func TestHandleStatuses_NotAcknowledged_PersistsAndSignals(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{makeVoucher("sb-nack")}
	sts := []chain.SettlementStatus{chain.StatusNotAcknowledged}

	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	reason, _ := rdb.Get(ctx, stopKey("sb-nack")).Result()
	if reason != "not_acknowledged" {
		t.Errorf("stop reason: got %q want %q", reason, "not_acknowledged")
	}
	if len(stopCh) == 0 {
		t.Fatal("expected stop signal for NOT_ACKNOWLEDGED")
	}
	sig := <-stopCh
	if sig.Reason != "not_acknowledged" {
		t.Errorf("signal reason: got %q want %q", sig.Reason, "not_acknowledged")
	}
}

// ── StatusProviderMismatch → DLQ ─────────────────────────────────────────────

func TestHandleStatuses_ProviderMismatch_WritesToDLQ(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{makeVoucher("sb-mismatch")}
	sts := []chain.SettlementStatus{chain.StatusProviderMismatch}

	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	// DLQ has 1 entry
	dlq := dlqKey(testProvider)
	n, _ := rdb.LLen(ctx, dlq).Result()
	if n != 1 {
		t.Errorf("DLQ length: got %d want 1", n)
	}
	// No stop signal
	if len(stopCh) != 0 {
		t.Errorf("unexpected stop signal for PROVIDER_MISMATCH")
	}
	// No stop key
	exists, _ := rdb.Exists(ctx, stopKey("sb-mismatch")).Result()
	if exists != 0 {
		t.Error("stop key must not exist for PROVIDER_MISMATCH")
	}
}

func TestHandleStatuses_InvalidSignature_WritesToDLQ(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{makeVoucher("sb-badsig")}
	sts := []chain.SettlementStatus{chain.StatusInvalidSignature}

	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	dlq := dlqKey(testProvider)
	n, _ := rdb.LLen(ctx, dlq).Result()
	if n != 1 {
		t.Errorf("DLQ length: got %d want 1", n)
	}
	if len(stopCh) != 0 {
		t.Error("unexpected stop signal for INVALID_SIGNATURE")
	}
}

// ── StatusInvalidNonce → discard ─────────────────────────────────────────────

func TestHandleStatuses_InvalidNonce_Discarded(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{makeVoucher("sb-nonce")}
	sts := []chain.SettlementStatus{chain.StatusInvalidNonce}

	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	// No stop key, no DLQ, no signal
	exists, _ := rdb.Exists(ctx, stopKey("sb-nonce")).Result()
	if exists != 0 {
		t.Error("stop key must not exist for INVALID_NONCE")
	}
	dlq := dlqKey(testProvider)
	n, _ := rdb.LLen(ctx, dlq).Result()
	if n != 0 {
		t.Errorf("DLQ must be empty for INVALID_NONCE, got %d", n)
	}
	if len(stopCh) != 0 {
		t.Error("unexpected stop signal for INVALID_NONCE")
	}
}

// ── Batch queue management ────────────────────────────────────────────────────

// The contract: firstItem is already BLPOP'd; items [1:] sit in queue and must
// be LPOP'd by HandleStatuses as it processes them.
func TestHandleStatuses_Batch_PopsRemainingItems(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{
		makeVoucher("sb-b0"),
		makeVoucher("sb-b1"),
		makeVoucher("sb-b2"),
	}
	sts := []chain.SettlementStatus{
		chain.StatusSuccess,
		chain.StatusSuccess,
		chain.StatusSuccess,
	}

	// Simulate: item 0 already BLPOP'd; push items 1,2 into queue
	pushRemaining(t, rdb, testQueueKey, vs)
	if queueLen(t, rdb, testQueueKey) != 2 {
		t.Fatalf("setup: expected 2 items in queue before HandleStatuses")
	}

	raw0, _ := json.Marshal(vs[0])
	HandleStatuses(ctx, rdb, stopCh, testQueueKey, string(raw0), vs, sts, alert.Nop{}, zap.NewNop())

	// All items consumed; queue empty
	if n := queueLen(t, rdb, testQueueKey); n != 0 {
		t.Errorf("queue should be empty after batch, got %d items", n)
	}
}

// ── Mixed batch ───────────────────────────────────────────────────────────────

func TestHandleStatuses_MixedBatch(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 8)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{
		makeVoucher("sb-success"),
		makeVoucher("sb-broke"),
		makeVoucher("sb-nonce"),
		makeVoucher("sb-mismatch"),
	}
	sts := []chain.SettlementStatus{
		chain.StatusSuccess,
		chain.StatusInsufficientBalance,
		chain.StatusInvalidNonce,
		chain.StatusProviderMismatch,
	}

	pushRemaining(t, rdb, testQueueKey, vs)
	raw0, _ := json.Marshal(vs[0])
	HandleStatuses(ctx, rdb, stopCh, testQueueKey, string(raw0), vs, sts, alert.Nop{}, zap.NewNop())

	// Only sb-broke triggers a stop signal
	if len(stopCh) != 1 {
		t.Fatalf("expected 1 stop signal, got %d", len(stopCh))
	}
	sig := <-stopCh
	if sig.SandboxID != "sb-broke" {
		t.Errorf("signal SandboxID: got %q want %q", sig.SandboxID, "sb-broke")
	}

	// sb-mismatch goes to DLQ
	dlq := dlqKey(testProvider)
	n, _ := rdb.LLen(ctx, dlq).Result()
	if n != 1 {
		t.Errorf("DLQ length: got %d want 1", n)
	}

	// Queue empty
	if n := queueLen(t, rdb, testQueueKey); n != 0 {
		t.Errorf("queue should be empty after mixed batch, got %d", n)
	}
}

// ── stopCh full: signal dropped, stop key still persisted ────────────────────

func TestHandleStatuses_StopChFull_KeyStillPersisted(t *testing.T) {
	rdb := newTestRedis(t)
	// Zero-capacity channel: select default branch fires immediately
	stopCh := make(chan StopSignal, 0)
	ctx := context.Background()

	vs := []voucher.SandboxVoucher{makeVoucher("sb-full")}
	sts := []chain.SettlementStatus{chain.StatusInsufficientBalance}

	// Must not block or panic
	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	// Stop key still written (crash-safe persistence)
	reason, err := rdb.Get(ctx, stopKey("sb-full")).Result()
	if err != nil {
		t.Fatalf("stop key not persisted when channel full: %v", err)
	}
	if reason != "insufficient_balance" {
		t.Errorf("reason: got %q want %q", reason, "insufficient_balance")
	}
}

// ── persistStop (direct) ──────────────────────────────────────────────────────

func TestPersistStop_WritesKeyAndSignals(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 2)
	ctx := context.Background()

	if err := persistStop(ctx, rdb, stopCh, "sb-direct", "insufficient_balance", zap.NewNop()); err != nil {
		t.Fatalf("persistStop: unexpected error: %v", err)
	}

	val, err := rdb.Get(ctx, "stop:sandbox:sb-direct").Result()
	if err != nil || val != "insufficient_balance" {
		t.Errorf("stop key: got %q err=%v", val, err)
	}
	if len(stopCh) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(stopCh))
	}
	sig := <-stopCh
	if sig.SandboxID != "sb-direct" || sig.Reason != "insufficient_balance" {
		t.Errorf("signal: got %+v", sig)
	}
}

// ── DLQ entry is valid JSON of the original voucher ──────────────────────────

func TestHandleStatuses_DLQEntry_IsValidVoucher(t *testing.T) {
	rdb := newTestRedis(t)
	stopCh := make(chan StopSignal, 4)
	ctx := context.Background()

	original := makeVoucher("sb-dlq")
	original.Nonce = big.NewInt(42)
	vs := []voucher.SandboxVoucher{original}
	sts := []chain.SettlementStatus{chain.StatusProviderMismatch}

	HandleStatuses(ctx, rdb, stopCh, testQueueKey, "item0", vs, sts, alert.Nop{}, zap.NewNop())

	raw, err := rdb.RPop(ctx, dlqKey(testProvider)).Result()
	if err != nil {
		t.Fatalf("DLQ pop: %v", err)
	}
	var got voucher.SandboxVoucher
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("DLQ entry is not valid JSON: %v", err)
	}
	if got.SandboxID != "sb-dlq" {
		t.Errorf("DLQ SandboxID: got %q want %q", got.SandboxID, "sb-dlq")
	}
	if got.Nonce.Int64() != 42 {
		t.Errorf("DLQ Nonce: got %d want 42", got.Nonce.Int64())
	}
}

// NOT_ACKNOWLEDGED rejects BEFORE the contract consumes the nonce — the
// revenue is collectable once the user acknowledges. The voucher must be
// parked in the held list (nonce/signature cleared), not dropped with the pop.
func TestHandleStatuses_NotAcknowledged_ParksVoucher(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	stopCh := make(chan StopSignal, 4)

	v := voucher.SandboxVoucher{
		SandboxID: "sb-nack",
		User:      common.HexToAddress("0xAAA"),
		Provider:  common.HexToAddress("0xBBB"),
		TotalFee:  big.NewInt(777),
		Nonce:     big.NewInt(42),
		Signature: []byte{1, 2, 3},
	}
	HandleStatuses(context.Background(), rdb, stopCh, "q", "raw", []voucher.SandboxVoucher{v},
		[]chain.SettlementStatus{chain.StatusNotAcknowledged}, alert.Nop{}, zap.NewNop())

	heldKey := fmt.Sprintf(voucher.VoucherHeldKeyFmt,
		strings.ToLower(v.User.Hex()), strings.ToLower(v.Provider.Hex()))
	items, _ := rdb.LRange(context.Background(), heldKey, 0, -1).Result()
	if len(items) != 1 {
		t.Fatalf("not-acked voucher must be parked in held, got %d items", len(items))
	}
	var parked voucher.SandboxVoucher
	if err := json.Unmarshal([]byte(items[0]), &parked); err != nil {
		t.Fatal(err)
	}
	if parked.TotalFee.String() != "777" {
		t.Errorf("parked fee %s want 777", parked.TotalFee)
	}
	if parked.Nonce != nil || parked.Signature != nil {
		t.Error("settle-attempt artifacts (nonce/signature) must be cleared before parking")
	}
	// held-users index maintained → sweep will revisit after ack + balance change
	users, _ := voucher.HeldUsers(context.Background(), rdb, v.Provider)
	if len(users) != 1 {
		t.Errorf("held-users index: %v", users)
	}
}

// Finding #30 (#79) self-heal: INVALID_NONCE means the local counter disagrees
// with the chain. The counter must be deleted so the NEXT voucher reseeds from
// on-chain lastNonce — otherwise every subsequent voucher for the pair keeps
// getting rejected and discarded.
func TestHandleStatuses_InvalidNonce_ResetsCounter(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	stopCh := make(chan StopSignal, 1)

	v := voucher.SandboxVoucher{
		SandboxID: "sb-n", User: common.HexToAddress("0xAAA"), Provider: common.HexToAddress("0xBBB"),
		TotalFee: big.NewInt(9), Nonce: big.NewInt(1),
	}
	nonceKey := fmt.Sprintf(voucher.NonceKeyFmt,
		strings.ToLower(v.User.Hex()), strings.ToLower(v.Provider.Hex()))
	rdb.Set(context.Background(), nonceKey, "1", 0) // the stale counter that produced nonce=1

	HandleStatuses(context.Background(), rdb, stopCh, "q", "raw", []voucher.SandboxVoucher{v},
		[]chain.SettlementStatus{chain.StatusInvalidNonce}, alert.Nop{}, zap.NewNop())

	if mr.Exists(nonceKey) {
		t.Fatal("INVALID_NONCE must delete the stale counter so the next voucher reseeds from chain")
	}
}

// Review #117 nit: the self-heal's key must be the SAME one the signer creates,
// or the Del silently no-ops. Seed a counter via the signer's own IncrNonce,
// then feed the settler an INVALID_NONCE voucher for that pair and assert the
// signer-created key is gone — pinning the cross-package key-derivation coupling
// as a contract, not a coincidence.
func TestHandleStatuses_InvalidNonce_DeletesSignerCreatedKey(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	owner := "0xAaBbCcDdEeFf00112233445566778899aAbBcCdD"
	prov := "0x00112233445566778899aAbBcCdDeEfF00112233"

	// The signer derives and creates the counter key from its own logic.
	sgn := billing.NewSigner(testSignerKey(t), big.NewInt(1),
		common.HexToAddress("0x0000000000000000000000000000000000000abc"),
		common.HexToAddress(prov), rdb, &zeroNonceReader{}, zap.NewNop())
	if _, err := sgn.IncrNonce(context.Background(), owner, prov); err != nil {
		t.Fatalf("seed via signer: %v", err)
	}
	signerKey := fmt.Sprintf(voucher.NonceKeyFmt, strings.ToLower(owner), strings.ToLower(prov))
	if !mr.Exists(signerKey) {
		t.Fatalf("precondition: signer must have created %s", signerKey)
	}

	// The settler processes an INVALID_NONCE voucher for the SAME pair.
	v := voucher.SandboxVoucher{
		SandboxID: "sb", User: common.HexToAddress(owner), Provider: common.HexToAddress(prov),
		TotalFee: big.NewInt(1), Nonce: big.NewInt(1),
	}
	HandleStatuses(context.Background(), rdb, make(chan StopSignal, 1), "q", "raw",
		[]voucher.SandboxVoucher{v}, []chain.SettlementStatus{chain.StatusInvalidNonce}, alert.Nop{}, zap.NewNop())

	if mr.Exists(signerKey) {
		t.Fatal("settler must delete the exact key the signer created (key-derivation contract broken)")
	}
}

type zeroNonceReader struct{}

func (zeroNonceReader) GetLastNonce(context.Context, common.Address, common.Address) (*big.Int, error) {
	return big.NewInt(0), nil
}

func testSignerKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}
