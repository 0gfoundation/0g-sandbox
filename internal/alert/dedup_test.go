package alert

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

func newTestWebhook(t *testing.T) (*Webhook, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	// No webhook URL: persist-only, which is what the dashboard reads.
	return NewWebhook("", "0xPROV", rdb, time.Hour, zap.NewNop()), rdb
}

func historyKinds(t *testing.T, rdb *redis.Client) []Entry {
	t.Helper()
	entries, err := History(context.Background(), rdb, HistoryMaxLen)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// Two accounts hitting the same failure are two alerts, not one. Keying dedup
// on the kind alone made the second account's first report look like a repeat
// of the first account's: measured on dev, an account with 1140 rejections
// held the key continuously and a second account's 4 rejections never reached
// the history at all.
func TestDedup_DifferentUsersBothReachHistory(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": "0xAAA", "provider": "0xPROV", "amount": "100"})
	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": "0xBBB", "provider": "0xPROV", "amount": "200"})

	entries := historyKinds(t, rdb)
	if len(entries) != 2 {
		t.Fatalf("want 2 history entries (one per user), got %d: %+v", len(entries), entries)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Details["user"].(string)] = true
	}
	if !seen["0xAAA"] || !seen["0xBBB"] {
		t.Errorf("both users must appear; got %v", seen)
	}
}

// The noise this dedup exists for: one account failing over and over holds one
// slot, not many.
func TestDedup_SameUserSuppressedAfterFirst(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
			map[string]any{"user": "0xAAA", "provider": "0xPROV"})
	}

	if got := len(historyKinds(t, rdb)); got != 1 {
		t.Errorf("want 1 entry for a repeating user, got %d", got)
	}
}

// The trap: a detail that changes between reports of the same ongoing
// condition must not enter the key. Rejections carry the voucher amount, which
// differs every time — if it were part of the identity, dedup would never fire
// and one chronic account would churn the whole 100-entry ring, which is worse
// than the over-suppression this change fixes.
func TestDedup_VaryingAmountDoesNotDefeatSuppression(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	for i, amount := range []string{"100", "200", "300", "400"} {
		w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
			map[string]any{"user": "0xAAA", "provider": "0xPROV", "amount": amount, "nonce": i})
	}

	if got := len(historyKinds(t, rdb)); got != 1 {
		t.Errorf("want 1 entry despite varying amount/nonce, got %d — dedup is keyed on "+
			"something that changes per report and no longer suppresses anything", got)
	}
}

// Node-scoped kinds carry no subject and must keep the original
// one-per-window behaviour — there is a single settler, so the kind already
// identifies what is wrong.
func TestDedup_SubjectlessKindUnchanged(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		w.Notify(ctx, KindSettlerNoBalance, SeverityCritical, "settler out of gas",
			map[string]any{"balance_wei": "0"})
	}

	if got := len(historyKinds(t, rdb)); got != 1 {
		t.Errorf("want 1 entry for a node-scoped kind, got %d", got)
	}
}

// Sandbox-scoped kinds have the same flaw as user-scoped ones.
// stop_persist_failure means a sandbox could not be queued for stop and keeps
// billing, so losing all but the first is losing the others outright.
func TestDedup_DifferentSandboxesBothReachHistory(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	w.Notify(ctx, KindStopPersistFailure, SeverityCritical, "stop marker failed",
		map[string]any{"sandbox": "sb-alpha", "reason": "insufficient_balance"})
	w.Notify(ctx, KindStopPersistFailure, SeverityCritical, "stop marker failed",
		map[string]any{"sandbox": "sb-beta", "reason": "insufficient_balance"})

	if got := len(historyKinds(t, rdb)); got != 2 {
		t.Errorf("want 2 entries (one per sandbox), got %d", got)
	}
}

// The same account under different address casing is one subject, not two —
// details come from several call sites and not all of them checksum.
func TestDedup_UserCasingIsOneSubject(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": "0xAbCdEf", "provider": "0xPROV"})
	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": "0xabcdef", "provider": "0xPROV"})

	if got := len(historyKinds(t, rdb)); got != 1 {
		t.Errorf("want 1 entry for the same address in different casing, got %d", got)
	}
}

// Dedup disabled (window 0) must still let everything through.
func TestDedup_DisabledPersistsEveryAlert(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	w := NewWebhook("", "0xPROV", rdb, 0, zap.NewNop())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
			map[string]any{"user": "0xAAA"})
	}

	if got := len(historyKinds(t, rdb)); got != 3 {
		t.Errorf("want 3 entries with dedup disabled, got %d", got)
	}
}

// stringerAddr stands in for a typed identity value such as common.Address.
type stringerAddr string

func (s stringerAddr) String() string { return string(s) }

// A call site passing a typed identity value (common.Address rather than its
// .Hex()) must key the same as the string form. Skipping it would empty the
// subject and drop that kind back to per-kind dedup — the over-suppression
// this key format exists to prevent, and silent, since nothing errors.
func TestDedup_StringerValueKeysLikeItsString(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": stringerAddr("0xAAA"), "provider": "0xPROV"})
	// Same subject, spelled as a plain string: must be suppressed, not a
	// second slot.
	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": "0xaaa", "provider": "0xPROV"})
	// A different subject, also typed: must get through.
	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": stringerAddr("0xBBB"), "provider": "0xPROV"})

	// Assert on which subjects landed, not how many entries there are. If the
	// typed values were skipped, both of them would key on the provider alone:
	// 0xAAA claims that key, 0xaaa claims its own, and 0xBBB is swallowed as a
	// repeat of 0xAAA. The count is still two, so only naming the subjects
	// catches it.
	got := map[string]bool{}
	for _, e := range historyKinds(t, rdb) {
		got[strings.ToLower(fmt.Sprint(e.Details["user"]))] = true
	}
	if !got["0xaaa"] {
		t.Errorf("first subject missing from history; got %v", got)
	}
	if !got["0xbbb"] {
		t.Errorf("second subject was swallowed as a repeat of the first — the typed "+
			"value keyed on nothing, so both fell back to the same key; got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("want exactly 2 distinct subjects, got %v", got)
	}
}

// Two users must not be able to collide into one key through the ':' join.
// Today's identity values are hex addresses and UUIDs, so this is a guard on
// the format rather than a live hazard.
func TestDedup_AdjacentFieldsDoNotCollide(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": "0xAA", "provider": "0xBB"})
	w.Notify(ctx, KindVoucherRejected, SeverityCritical, "rejected",
		map[string]any{"user": "0xAAB", "provider": "0xB"})

	if got := len(historyKinds(t, rdb)); got != 2 {
		t.Errorf("want 2 entries for two distinct (user, provider) pairs, got %d", got)
	}
}

// One stale (user, provider) nonce counter is one condition, however many of
// that user's sandboxes trip over it. The settler's invalid-nonce site leaves
// the sandbox out of the details for this reason; this pins the behaviour the
// key format gives it.
func TestDedup_InvalidNonceIsOnePerUserProvider(t *testing.T) {
	w, rdb := newTestWebhook(t)
	ctx := context.Background()

	for _, nonce := range []string{"7", "8", "9"} {
		w.Notify(ctx, KindVoucherInvalidNonce, SeverityCritical, "invalid nonce",
			map[string]any{"user": "0xAAA", "provider": "0xPROV", "nonce": nonce})
	}

	if got := len(historyKinds(t, rdb)); got != 1 {
		t.Errorf("want 1 entry for one stale counter, got %d", got)
	}
}
