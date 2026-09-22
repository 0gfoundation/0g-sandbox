// Package alert dispatches operator-facing alerts (low settler balance,
// settle failures, queue backlog, etc.) to a webhook with Redis-backed
// dedup. Falls back to log-only when no webhook is configured.
//
// Notify is fire-and-forget: it never blocks the caller and never returns
// an error. Failures to deliver (Redis down, webhook 5xx) are logged
// internally and dropped — alerts must not break the hot path.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Kind identifies the alert type for dedup and routing.
type Kind string

const (
	KindSettlerTxFailure       Kind = "settler_tx_failure"
	KindSettlerLowBalance      Kind = "settler_low_balance"
	KindSettlerNoBalance       Kind = "settler_no_balance"
	KindSettlerSignerMismatch  Kind = "settler_signer_mismatch"
	KindVoucherRejected        Kind = "voucher_rejected"
	KindVoucherInvalidNonce    Kind = "voucher_invalid_nonce"
	KindQueueBacklog           Kind = "queue_backlog"
	KindStopPersistFailure     Kind = "stop_persist_failure"
	KindArchiveFailure         Kind = "archive_failure"
)

// Severity is included in the webhook payload so receivers can route or
// colour-code (e.g. yellow for warning, red for critical).
type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// HistoryKey is the Redis list (LPUSH-newest, LTRIM-bounded) that backs
// the operator dashboard. Read via History().
const HistoryKey = "alert:history"

// HistoryMaxLen caps stored alert entries. Older entries are LTRIM'd off.
const HistoryMaxLen = 100

// Entry is one alert event, persisted to Redis for dashboard display.
type Entry struct {
	Kind      Kind           `json:"kind"`
	Severity  Severity       `json:"severity"`
	Timestamp string         `json:"timestamp"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

// Alerter is the dependency surface settler / monitors call. Notify must
// not block — implementations should dispatch I/O asynchronously.
type Alerter interface {
	Notify(ctx context.Context, kind Kind, sev Severity, message string, details map[string]any)
}

// Nop discards all notifications. Use when no webhook is configured —
// callers still log at the call site, so the event isn't lost.
type Nop struct{}

func (Nop) Notify(context.Context, Kind, Severity, string, map[string]any) {}

// Webhook posts JSON to webhookURL, deduped per (kind, subject) via a Redis
// key with TTL = dedupWindow. Repeats for the same subject within the window
// are suppressed; a different subject is a different alert.
type Webhook struct {
	webhookURL   string
	provider     string // address surfaced in payload for multi-provider routing
	rdb          *redis.Client
	dedupWindow  time.Duration
	httpClient   *http.Client
	log          *zap.Logger
}

// NewWebhook returns a Webhook alerter. dedupWindow is the per-(kind,
// subject) suppression window; pass 0 to disable dedup.
func NewWebhook(webhookURL, providerAddr string, rdb *redis.Client, dedupWindow time.Duration, log *zap.Logger) *Webhook {
	return &Webhook{
		webhookURL:  webhookURL,
		provider:    providerAddr,
		rdb:         rdb,
		dedupWindow: dedupWindow,
		httpClient:  &http.Client{Timeout: 5 * time.Second},
		log:         log.With(zap.String("component", "alert")),
	}
}

type payload struct {
	Kind      Kind           `json:"kind"`
	Severity  Severity       `json:"severity"`
	Provider  string         `json:"provider,omitempty"`
	Timestamp string         `json:"timestamp"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
}

// Notify logs the event, persists it to Redis (for the dashboard), and
// asynchronously dispatches to the webhook if one is configured.
//
// Dedup applies to BOTH persist and dispatch: within the dedup window only
// the first call per kind AND subject logs an entry and fires the webhook.
// Subsequent calls for that same subject are still logged at WARN (for ops
// grep) but suppressed from the dashboard history and webhook — otherwise a
// persistent failure (e.g. settler with 0 balance checked every 60s) spams
// the history with dozens of identical entries.
//
// The subject comes from details (see claimDedup), so a different user or
// sandbox reporting the same kind is a different alert and gets through.
func (w *Webhook) Notify(ctx context.Context, kind Kind, sev Severity, message string, details map[string]any) {
	// Always log — even when dedup suppresses everything else, ops can grep.
	w.log.Warn("alert",
		zap.String("kind", string(kind)),
		zap.String("severity", string(sev)),
		zap.String("message", message),
		zap.Any("details", details),
	)

	if w.dedupWindow > 0 && !w.claimDedup(ctx, kind, details) {
		return
	}

	entry := Entry{
		Kind:      kind,
		Severity:  sev,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Message:   message,
		Details:   details,
	}
	w.persist(ctx, entry)

	if w.webhookURL == "" {
		return
	}

	p := payload{
		Kind:      kind,
		Severity:  sev,
		Provider:  w.provider,
		Timestamp: entry.Timestamp,
		Message:   message,
		Details:   details,
	}
	// Detach from caller's context so a cancelled hot-path doesn't drop
	// the alert mid-flight.
	go w.dispatch(p)
}

// persist appends to the Redis-backed alert history (LPUSH + LTRIM). Best-
// effort: failures are logged but never propagate.
func (w *Webhook) persist(ctx context.Context, e Entry) {
	raw, err := json.Marshal(e)
	if err != nil {
		w.log.Error("alert persist marshal", zap.Error(err))
		return
	}
	pipe := w.rdb.Pipeline()
	pipe.LPush(ctx, HistoryKey, raw)
	pipe.LTrim(ctx, HistoryKey, 0, HistoryMaxLen-1)
	if _, err := pipe.Exec(ctx); err != nil {
		w.log.Warn("alert persist failed", zap.Error(err))
	}
}

// History returns up to n most recent alerts (newest first). Reads from
// HistoryKey; safe to call without holding a Webhook instance.
func History(ctx context.Context, rdb *redis.Client, n int) ([]Entry, error) {
	if n <= 0 {
		n = HistoryMaxLen
	}
	items, err := rdb.LRange(ctx, HistoryKey, 0, int64(n-1)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(items))
	for _, raw := range items {
		var e Entry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue // skip malformed
		}
		out = append(out, e)
	}
	return out, nil
}

// subjectKeys are the detail fields that identify WHAT an alert is about, in
// the order they join the dedup key. Identity only: a field that varies
// between two reports of the same ongoing condition (an amount, a nonce, a
// timestamp) would make every report unique and defeat dedup entirely, which
// is worse than the over-suppression this fixes — one chronic account would
// then fill the whole history ring instead of holding one slot in it.
//
// A kind whose call sites pass different identity fields gets one slot per
// shape — voucher_rejected is raised both for an exhausted balance (user,
// provider) and for a malformed voucher (user, provider, sandbox), so one
// user can hold two slots under that kind in a window. They report different
// conditions, so two alerts is the intent, not a leak.
//
// A call site should pass only the fields its condition is actually about.
// Adding one that is incidental splits a single root cause across slots and
// reports it once per value; see the invalid-nonce site in
// internal/settler/handler.go, where the counter is per (user, provider) and
// the sandbox is deliberately left off the details.
var subjectKeys = []string{"user", "provider", "sandbox"}

// subject builds the identity half of the dedup key from an alert's details.
// Returns "" for kinds that carry no subject — those are about the node
// itself, where the kind already is the subject.
//
// Values may be strings or fmt.Stringer. Accepting Stringer matters: every
// caller today passes addr.Hex(), but a call site that passes the typed
// common.Address instead would otherwise be skipped, the subject would come
// back empty, and that kind would silently fall back to per-kind dedup — the
// exact over-suppression this key format exists to prevent, with nothing
// failing to show it. Anything else is skipped, since a value with no stable
// text form would vary between reports and defeat dedup the other way.
//
// Components are joined on ':' without escaping. Every identity field in
// practice is a hex address or a Daytona UUID, neither of which contains a
// colon, so two different subjects cannot produce one key. A future identity
// field with free-form text would need escaping here.
func subject(details map[string]any) string {
	var b strings.Builder
	for _, k := range subjectKeys {
		v, ok := details[k]
		if !ok {
			continue
		}
		var s string
		switch t := v.(type) {
		case string:
			s = t
		case fmt.Stringer:
			s = t.String()
		default:
			continue
		}
		if s == "" {
			continue
		}
		b.WriteByte(':')
		b.WriteString(strings.ToLower(s))
	}
	return b.String()
}

// claimDedup returns true if this is the first call for this kind AND subject
// within the dedup window. SETNX-style atomic check.
//
// The subject is part of the key because a kind is a category, not an event:
// voucher_rejected covers every user's rejection, stop_persist_failure every
// sandbox's. Keying on the kind alone let one chronically failing account hold
// the key for the whole window and silenced everyone else's first report —
// measured on dev, an account with 1140 rejections kept a second account's 4
// from reaching the history at all.
//
// Kinds about the node itself (settler balance, signer mismatch, queue
// backlog) carry no subject, so they keep the bare key and the
// one-per-window behaviour the dedup was built for.
func (w *Webhook) claimDedup(ctx context.Context, kind Kind, details map[string]any) bool {
	key := "alert:dedup:" + string(kind) + subject(details)
	ok, err := w.rdb.SetNX(ctx, key, "1", w.dedupWindow).Result()
	if err != nil {
		// Redis trouble: fail open so we don't lose a real alert.
		w.log.Warn("alert dedup check failed, dispatching anyway", zap.Error(err))
		return true
	}
	return ok
}

func (w *Webhook) dispatch(p payload) {
	body, err := json.Marshal(p)
	if err != nil {
		w.log.Error("alert marshal", zap.Error(err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.webhookURL, bytes.NewReader(body))
	if err != nil {
		w.log.Error("alert request build", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.httpClient.Do(req)
	if err != nil {
		w.log.Error("alert webhook post", zap.Error(err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		w.log.Error("alert webhook non-2xx",
			zap.Int("status", resp.StatusCode),
			zap.String("kind", string(p.Kind)),
		)
		return
	}
	w.log.Debug("alert delivered", zap.String("kind", string(p.Kind)))
}

// String helper for callers that need a stable kind label (e.g. metrics).
func (k Kind) String() string { return string(k) }

// ClassifyChainErr returns a short label for a chain client error, used as
// the "err_type" detail on settler_tx_failure alerts so receivers can route
// e.g. "low_funds" to a different channel than "rpc_unreachable".
func ClassifyChainErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "insufficient funds"):
		return "low_funds"
	case strings.Contains(s, "nonce too low"), strings.Contains(s, "nonce too high"):
		return "nonce_drift"
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline exceeded"):
		return "timeout"
	case strings.Contains(s, "connection refused"), strings.Contains(s, "EOF"):
		return "rpc_unreachable"
	}
	return "other"
}

// Compile-time interface check.
var _ Alerter = (*Webhook)(nil)
var _ Alerter = Nop{}
