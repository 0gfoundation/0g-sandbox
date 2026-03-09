package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox-billing/internal/billing"
	"github.com/0gfoundation/0g-sandbox-billing/internal/chain"
	"github.com/0gfoundation/0g-sandbox-billing/internal/daytona"
)

// BillingHooks is satisfied by billing.EventHandler.
// Decoupled here so proxy tests can use a mock.
type BillingHooks interface {
	OnCreate(ctx context.Context, sandboxID, ownerAddr string, cpu, memGB int)
	OnStart(ctx context.Context, sandboxID, ownerAddr string, cpu, memGB int)
	OnStop(ctx context.Context, sandboxID string)
	OnDelete(ctx context.Context, sandboxID string)
	OnArchive(ctx context.Context, sandboxID string)
	EnsureSession(ctx context.Context, sandboxID, ownerAddr string)
}

// BalanceChecker looks up the on-chain balance for a user with a specific provider.
// A nil implementation disables the balance pre-check on create.
type BalanceChecker interface {
	GetBalance(ctx context.Context, user, provider common.Address) (*big.Int, error)
}

// AckChecker checks whether a user has acknowledged the TEE signer.
// A nil implementation disables the acknowledgement pre-check on start.
type AckChecker interface {
	IsAcknowledged(ctx context.Context, addr common.Address) (bool, error)
}

// EventFetcher retrieves on-chain VoucherSettled events.
// lookback is blocks to look back; 0 = all history.
// Returns events, current block number, and any error.
type EventFetcher interface {
	GetVoucherEvents(ctx context.Context, lookback uint64) ([]chain.VoucherEvent, uint64, error)
}

// Handler wires up all proxy routes onto a Gin engine.
type Handler struct {
	dtona              *daytona.Client
	billing            BillingHooks
	rp                 *httputil.ReverseProxy
	balCheck           BalanceChecker // nil = no check
	ackCheck           AckChecker     // nil = no check
	eventFetcher       EventFetcher   // nil = events endpoint disabled
	minBalance         *big.Int       // minimum balance required to create a sandbox
	providerAddress    string         // only this wallet may call provider-only endpoints
	computePricePerSec *big.Int
	rdb                *redis.Client
	log                *zap.Logger
}

func NewHandler(dtona *daytona.Client, bh BillingHooks, balCheck BalanceChecker, ackCheck AckChecker, eventFetcher EventFetcher, minBalance, computePricePerSec *big.Int, providerAddress string, rdb *redis.Client, log *zap.Logger) *Handler {
	target, _ := url.Parse(dtona.BaseURL())
	rp := httputil.NewSingleHostReverseProxy(target)

	// Inject admin key on every forwarded request
	orig := rp.Director
	rp.Director = func(req *http.Request) {
		orig(req)
		req.Header.Set("Authorization", "Bearer "+dtona.AdminKey())
		req.Host = target.Host
	}

	return &Handler{dtona: dtona, billing: bh, rp: rp, balCheck: balCheck, ackCheck: ackCheck, eventFetcher: eventFetcher, minBalance: minBalance, computePricePerSec: computePricePerSec, providerAddress: providerAddress, rdb: rdb, log: log}
}

// Register mounts all routes. authMiddleware should already be applied to the group.
//
// Route structure:
//   - Static routes without sub-actions are registered normally.
//   - All /sandbox/:id/* routes go through a single catch-all handler to avoid
//     Gin's restriction on mixing static segments and wildcard catch-alls.
func (h *Handler) Register(rg *gin.RouterGroup) {
	// ── Create sandbox ─────────────────────────────────────────────────────
	rg.POST("/sandbox", h.handleCreate)

	// ── List / paginated (filter by owner) ────────────────────────────────
	rg.GET("/sandbox", h.handleList)
	rg.GET("/sandbox/paginated", h.handleList)
	rg.GET("/volumes", h.handleListGeneric("daytona-owner"))
	rg.POST("/snapshots", h.handleSnapshotCreate)
	rg.DELETE("/snapshots/:id", h.handleSnapshotDelete)


	// ── DELETE /sandbox/:id (no action suffix, safe to register separately) ─
	rg.DELETE("/sandbox/:id", h.withOwner(h.handleDelete))

	// ── Catch-all for /sandbox/:id/<action> ────────────────────────────────
	// Blocked (autostop/autoarchive), lifecycle hooks, label protection, and
	// transparent forwarding are all dispatched here to keep Gin happy.
	rg.Any("/sandbox/:id/*action", h.handleCatchAll)

	// ── GET /sandbox/:id (no wildcard suffix) ─────────────────────────────
	rg.GET("/sandbox/:id", h.withOwner(h.forward))

	// ── Toolbox API (/api/toolbox/:id/*) — owner check + transparent forward
	rg.Any("/toolbox/:id/*action", h.withOwner(h.forward))

	// ── Provider-only: archive all running sandboxes (pre-deploy) ──────────
	rg.POST("/archive-all", h.handleArchiveAll)

	// ── Provider-only: list all billing sessions ────────────────────────────
	rg.GET("/sessions", h.handleSessions)

	// ── On-chain voucher events (public chain data, wallet auth only) ───────
	rg.GET("/events", h.handleEvents)
}

// ── Create ─────────────────────────────────────────────────────────────────

func (h *Handler) handleCreate(c *gin.Context) {
	wallet := c.GetString("wallet_address")

	// Pre-check: reject if on-chain balance is below the minimum required.
	if h.balCheck != nil && h.minBalance != nil {
		balance, err := h.balCheck.GetBalance(c.Request.Context(), common.HexToAddress(wallet), common.HexToAddress(h.providerAddress))
		if err != nil {
			h.log.Error("balance check", zap.String("wallet", wallet), zap.Error(err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "balance check failed"})
			return
		}
		if balance.Cmp(h.minBalance) < 0 {
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":    "insufficient balance",
				"balance":  balance.String(),
				"required": h.minBalance.String(),
			})
			return
		}
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
		return
	}

	modified, err := InjectOwner(body, wallet)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(modified))
	c.Request.ContentLength = int64(len(modified))

	// Detach the upstream request from the client context so that Daytona
	// continues creating the sandbox even if the browser disconnects before
	// the response arrives (creation can take 30-90 s on first image pull).
	// Without this, a client disconnect cancels the Daytona request and the
	// proxy returns 502 even though the sandbox may have been created.
	detachedReq := c.Request.Clone(context.WithoutCancel(c.Request.Context()))

	// Use a plain httptest.Recorder to buffer the upstream response so we
	// can extract the sandbox ID without wrapping gin.ResponseWriter
	// (which causes http.CloseNotifier interface issues in tests).
	upstream := httptest.NewRecorder()
	h.rp.ServeHTTP(upstream, detachedReq)

	// Copy recorded response → real writer
	result := upstream.Result()
	for k, vs := range result.Header {
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.WriteHeader(result.StatusCode)
	io.Copy(c.Writer, result.Body) //nolint:errcheck

	if result.StatusCode >= 200 && result.StatusCode < 300 {
		if id := extractID(upstream.Body.Bytes()); id != "" {
			cpu, memGB := extractResources(upstream.Body.Bytes())
			go h.billing.OnCreate(context.WithoutCancel(c.Request.Context()), id, wallet, cpu, memGB)
		}
	}
}

// ── Lifecycle ───────────────────────────────────────────────────────────────
// For these endpoints we only need the status code; write directly to c.Writer
// and read c.Writer.Status() afterwards.

func (h *Handler) handleStart(c *gin.Context) {
	id := c.Param("id")
	wallet := c.GetString("wallet_address")

	// Pre-check: reject if user has not acknowledged the TEE signer.
	if h.ackCheck != nil {
		acked, err := h.ackCheck.IsAcknowledged(c.Request.Context(), common.HexToAddress(wallet))
		if err != nil {
			h.log.Error("ack check", zap.String("wallet", wallet), zap.Error(err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "acknowledgement check failed"})
			return
		}
		if !acked {
			c.JSON(http.StatusPaymentRequired, gin.H{"error": "TEE signer not acknowledged"})
			return
		}
	}

	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		go func() {
			ctx := context.WithoutCancel(c.Request.Context())
			cpu, memGB := 0, 0
			if sb, err := h.dtona.GetSandbox(ctx, id); err == nil {
				cpu, memGB = sb.CPU, sb.Memory
			}
			h.billing.OnStart(ctx, id, wallet, cpu, memGB)
		}()
	}
}

func (h *Handler) handleStop(c *gin.Context) {
	id := c.Param("id")
	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		go h.billing.OnStop(context.WithoutCancel(c.Request.Context()), id)
	}
}

func (h *Handler) handleDelete(c *gin.Context) {
	id := c.Param("id")
	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		go h.billing.OnDelete(context.WithoutCancel(c.Request.Context()), id)
	}
}

func (h *Handler) handleArchive(c *gin.Context) {
	id := c.Param("id")
	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		go h.billing.OnArchive(context.WithoutCancel(c.Request.Context()), id)
	}
}

// handleEnsureBilling ensures a billing session exists for a sandbox that was
// successfully created but whose billing hook may not have fired (e.g. because
// the HTTP connection dropped before the 2xx response was delivered).
// Idempotent: if a session already exists, this is a no-op.
func (h *Handler) handleEnsureBilling(c *gin.Context) {
	id := c.Param("id")
	wallet := c.GetString("wallet_address")
	go h.billing.EnsureSession(context.WithoutCancel(c.Request.Context()), id, wallet)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleSSHAccess creates a temporary SSH access token for a sandbox and
// returns the sshCommand with the gateway host rewritten if configured.
func (h *Handler) handleSSHAccess(c *gin.Context) {
	id := c.Param("id")
	access, err := h.dtona.CreateSSHAccess(c.Request.Context(), id)
	if err != nil {
		h.log.Warn("ssh-access failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "ssh access failed"})
		return
	}
	// Return sshCommand with localhost placeholder — the frontend replaces
	// it with window.location.hostname so no server-side config is needed.
	c.JSON(http.StatusOK, access)
}

// handleArchiveAll stops then archives every started/starting sandbox.
// Restricted to the provider address so only the operator can trigger this.
// Daytona requires stop before archive: stop removes the container, archive
// then backs up the filesystem to object storage so it can be restored later.
func (h *Handler) handleArchiveAll(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !strings.EqualFold(wallet, h.providerAddress) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "provider only"})
		return
	}

	sandboxes, err := h.dtona.ListSandboxes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
		return
	}

	var archived, skipped, failed []string
	for _, s := range sandboxes {
		state := strings.ToLower(s.State)
		switch state {
		case "started", "starting":
			// Must stop before archive (Daytona requires stopped state).
			// Ignore stop errors — sandbox may already be transitioning.
			if err := h.dtona.StopSandbox(c.Request.Context(), s.ID); err != nil {
				h.log.Warn("archive-all: stop error (continuing)", zap.String("id", s.ID), zap.Error(err))
			}
			if err := h.dtona.WaitStopped(c.Request.Context(), s.ID); err != nil {
				h.log.Warn("archive-all: wait stopped failed", zap.String("id", s.ID), zap.Error(err))
				failed = append(failed, s.ID)
				continue
			}
			fallthrough // now stopped — archive below
		case "stopped":
			// Already stopped: archive directly.
			if err := h.dtona.ArchiveSandbox(c.Request.Context(), s.ID); err != nil {
				h.log.Warn("archive-all: archive failed", zap.String("id", s.ID), zap.Error(err))
				failed = append(failed, s.ID)
			} else {
				archived = append(archived, s.ID)
				// Fire billing hook: generates final voucher + clears Redis session.
				go h.billing.OnArchive(context.WithoutCancel(c.Request.Context()), s.ID)
			}
		default:
			skipped = append(skipped, s.ID)
		}
	}
	c.JSON(http.StatusOK, gin.H{"archived": archived, "skipped": skipped, "failed": failed})
}

// handleForceDelete deletes any sandbox regardless of owner. Provider only.
func (h *Handler) handleForceDelete(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !strings.EqualFold(wallet, h.providerAddress) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "provider only"})
		return
	}
	id := c.Param("id")
	// Rewrite to DELETE /api/sandbox/:id and forward
	c.Request.Method = http.MethodDelete
	c.Request.URL.Path = "/api/sandbox/" + id
	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		go h.billing.OnDelete(context.WithoutCancel(c.Request.Context()), id)
	}
}

// handleEvents returns on-chain VoucherSettled events for this contract.
// Accepts optional ?from_block=<n> query param; defaults to last ~50k blocks.
// Chain data is public so no provider restriction is applied.
func (h *Handler) handleEvents(c *gin.Context) {
	if h.eventFetcher == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "events not configured"})
		return
	}
	// ?lookback=N: look back N blocks from latest. 0 or omitted = all history.
	var lookback uint64 = 43200 // default: ~24h at 2s/block
	if s := c.Query("lookback"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid lookback"})
			return
		}
		lookback = n
	}
	evts, currentBlock, err := h.eventFetcher.GetVoucherEvents(c.Request.Context(), lookback)
	if err != nil {
		h.log.Error("GetVoucherEvents", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "chain query failed"})
		return
	}
	type row struct {
		User      string `json:"user"`
		Provider  string `json:"provider"`
		TotalFee  string `json:"total_fee"`
		Nonce     string `json:"nonce"`
		Status    string `json:"status"`
		TxHash    string `json:"tx_hash"`
		Block     uint64 `json:"block"`
		Timestamp uint64 `json:"timestamp"`
	}
	result := make([]row, len(evts))
	for i, e := range evts {
		result[i] = row{
			User:      e.User.Hex(),
			Provider:  e.Provider.Hex(),
			TotalFee:  e.TotalFee.String(),
			Nonce:     e.Nonce.String(),
			Status:    e.Status.String(),
			TxHash:    e.TxHash,
			Block:     e.Block,
			Timestamp: e.Timestamp,
		}
	}
	fromBlock := uint64(1)
	if lookback > 0 && currentBlock > lookback {
		fromBlock = currentBlock - lookback
	}
	c.JSON(http.StatusOK, gin.H{
		"current_block": currentBlock,
		"from_block":    fromBlock,
		"events":        result,
	})
}

// handleSessions returns all sandboxes visible to the provider, enriched with
// billing session data (accrued fees) where available. Restricted to provider.
func (h *Handler) handleSessions(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !strings.EqualFold(wallet, h.providerAddress) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "provider only"})
		return
	}

	// Fetch all sandboxes from Daytona
	sandboxes, err := h.dtona.ListSandboxes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
		return
	}

	// Fetch active billing sessions indexed by sandbox ID
	sessions, err := billing.ScanAllSessions(c.Request.Context(), h.rdb)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	sessionMap := make(map[string]billing.Session, len(sessions))
	for _, s := range sessions {
		sessionMap[s.SandboxID] = s
	}

	now := time.Now().Unix()
	type row struct {
		SandboxID     string `json:"sandbox_id"`
		Owner         string `json:"owner"`
		State         string `json:"state"`
		StartTime     int64  `json:"start_time,omitempty"`
		LastVoucherAt int64  `json:"last_voucher_at,omitempty"`
		AccruedNeuron string `json:"accrued_neuron,omitempty"`
	}
	result := make([]row, 0, len(sandboxes))
	for _, sb := range sandboxes {
		r := row{
			SandboxID: sb.ID,
			Owner:     sb.Labels[ownerLabel],
			State:     sb.State,
		}
		if sess, ok := sessionMap[sb.ID]; ok {
			elapsed := now - sess.StartTime
			if elapsed < 0 {
				elapsed = 0
			}
			accrued := new(big.Int).Mul(big.NewInt(elapsed), h.computePricePerSec)
			r.StartTime = sess.StartTime
			r.LastVoucherAt = sess.LastVoucherAt
			r.AccruedNeuron = accrued.String()
		}
		result = append(result, r)
	}
	c.JSON(http.StatusOK, result)
}

// ── Labels ──────────────────────────────────────────────────────────────────

func (h *Handler) handleLabels(c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	stripped, err := StripOwnerLabel(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid label payload"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(stripped))
	c.Request.ContentLength = int64(len(stripped))
	h.forward(c)
}

// ── List ────────────────────────────────────────────────────────────────────

func (h *Handler) handleList(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	sandboxes, err := h.dtona.ListSandboxes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "upstream error"})
		return
	}
	var filtered []daytona.Sandbox
	for _, s := range sandboxes {
		if strings.EqualFold(s.Labels[ownerLabel], wallet) {
			filtered = append(filtered, s)
		}
	}
	c.JSON(http.StatusOK, filtered)
}

func (h *Handler) handleListGeneric(_ string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h.forward(c)
	}
}

// handleListSnapshots lists all Daytona snapshots. Snapshots are provider-managed
// base images; any authenticated user may see and use them.
func (h *Handler) handleListSnapshots(c *gin.Context) {
	h.forward(c)
}

// handleSnapshotCreate registers a Docker image as a named Daytona snapshot.
// Provider-only: accepts {name, imageName}, forwards to Daytona internally.
func (h *Handler) handleSnapshotCreate(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !strings.EqualFold(wallet, h.providerAddress) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "provider only"})
		return
	}
	h.forward(c)
}

// handleSnapshotDelete deletes a snapshot by ID. Provider-only.
//
// Daytona has a bug where DELETE succeeds but then the audit log INSERT fails
// because the admin key carries no actorId in the request context, causing a
// spurious 500. We detect this case: if Daytona returns 500, we verify the
// snapshot is actually gone and return 200 if so.
func (h *Handler) handleSnapshotDelete(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !strings.EqualFold(wallet, h.providerAddress) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "provider only"})
		return
	}

	rec := httptest.NewRecorder()
	h.rp.ServeHTTP(rec, c.Request)

	if rec.Code != http.StatusInternalServerError {
		copyRecorder(c, rec)
		return
	}

	// 500: verify whether the snapshot was actually deleted despite the error.
	snapshotID := c.Param("id")
	snap, err := h.dtona.GetSnapshot(c.Request.Context(), snapshotID)
	if err == nil && snap == nil {
		// Snapshot is gone — the delete succeeded; audit log bug caused the 500.
		c.Status(http.StatusOK)
		return
	}

	// Snapshot still exists or verification failed — forward the original error.
	copyRecorder(c, rec)
}

func copyRecorder(c *gin.Context, rec *httptest.ResponseRecorder) {
	for k, vs := range rec.Header() {
		for _, v := range vs {
			c.Header(k, v)
		}
	}
	c.Data(rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes())
}


// ── Helpers ──────────────────────────────────────────────────────────────────

// handleCatchAll dispatches all /sandbox/:id/<action> requests.
// Gin requires a single catch-all to avoid routing-tree conflicts between
// static sub-paths and wildcard segments.
func (h *Handler) handleCatchAll(c *gin.Context) {
	action := c.Param("action") // e.g. "/start", "/stop", "/autostop", "/labels"
	method := c.Request.Method

	// ── Blocked actions ────────────────────────────────────────────────────
	if action == "/autostop" || action == "/autoarchive" {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "managed by billing proxy"})
		return
	}

	// ── Lifecycle with billing hooks ───────────────────────────────────────
	switch {
	case method == http.MethodPost && action == "/start":
		h.withOwner(h.handleStart)(c)
	case method == http.MethodPost && action == "/stop":
		h.withOwner(h.handleStop)(c)
	case method == http.MethodPost && action == "/archive":
		h.withOwner(h.handleArchive)(c)
	case method == http.MethodPost && action == "/ensure-billing":
		h.withOwner(h.handleEnsureBilling)(c)
	case method == http.MethodPost && action == "/ssh-access":
		h.withOwner(h.handleSSHAccess)(c)
	case method == http.MethodDelete && action == "/force":
		h.handleForceDelete(c)

	// ── Label protection ───────────────────────────────────────────────────
	case method == http.MethodPut && action == "/labels":
		h.withOwner(h.handleLabels)(c)

	// ── Transparent proxy (owner check) ───────────────────────────────────
	default:
		h.withOwner(h.forward)(c)
	}
}

// withOwner wraps a handler with an ownership check.
func (h *Handler) withOwner(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		wallet := c.GetString("wallet_address")
		if err := CheckOwner(c.Request.Context(), h.dtona, id, wallet); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		next(c)
	}
}

// forward passes the request to Daytona as-is.
func (h *Handler) forward(c *gin.Context) {
	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
}

// safeWriter wraps gin.ResponseWriter and overrides CloseNotify so that the
// reverse proxy never triggers a type-assertion on the underlying writer.
// gin.ResponseWriter implements the deprecated http.CloseNotifier, but the
// concrete writer in tests (*httptest.ResponseRecorder) does not, causing a
// panic inside net/http when the interface method is called.
//
//nolint:staticcheck
type safeWriter struct{ gin.ResponseWriter }

//nolint:staticcheck
func (s safeWriter) CloseNotify() <-chan bool { return make(chan bool, 1) }

// extractID tries to parse {"id": "..."} from a JSON response body.
func extractID(body []byte) string {
	var m struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&m); err == nil {
		return m.ID
	}
	return ""
}

// extractResources parses cpu and memory from a Daytona sandbox JSON response.
// Returns (0, 0) if parsing fails; callers fall back to flat-rate billing.
func extractResources(body []byte) (cpu, memGB int) {
	var m struct {
		CPU    int `json:"cpu"`
		Memory int `json:"memory"`
	}
	json.NewDecoder(bytes.NewReader(body)).Decode(&m) //nolint:errcheck
	return m.CPU, m.Memory
}
