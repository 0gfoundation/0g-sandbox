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

// Webhook posts JSON to webhookURL, deduped per (kind) via a Redis key
// with TTL = dedupWindow. Same-kind alerts within the window are suppressed.
type Webhook struct {
	webhookURL   string
	provider     string // address surfaced in payload for multi-provider routing
	rdb          *redis.Client
	dedupWindow  time.Duration
	httpClient   *http.Client
	log          *zap.Logger
}

// NewWebhook returns a Webhook alerter. dedupWindow is the per-kind
// suppression window; pass 0 to disable dedup.
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
// the first call per kind logs an entry and fires the webhook. Subsequent
// calls in the window are still logged at WARN (for ops grep) but suppressed
// from the dashboard history and webhook — otherwise a persistent failure
// (e.g. settler with 0 balance checked every 60s) spams the history with
// dozens of identical entries.
func (w *Webhook) Notify(ctx context.Context, kind Kind, sev Severity, message string, details map[string]any) {
	// Always log — even when dedup suppresses everything else, ops can grep.
	w.log.Warn("alert",
		zap.String("kind", string(kind)),
		zap.String("severity", string(sev)),
		zap.String("message", message),
		zap.Any("details", details),
	)

	if w.dedupWindow > 0 && !w.claimDedup(ctx, kind) {
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

// claimDedup returns true if this is the first call for kind within the
// dedup window. SETNX-style atomic check.
func (w *Webhook) claimDedup(ctx context.Context, kind Kind) bool {
	key := "alert:dedup:" + string(kind)
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
