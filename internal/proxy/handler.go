package proxy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
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

	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/daytona"
	"github.com/0gfoundation/0g-sandbox/internal/events"
	"github.com/0gfoundation/0g-sandbox/internal/registry"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// overrideHeaders are caller-controlled headers that some frameworks and
// ingresses interpret as path/method/origin overrides. They must never reach
// Daytona alongside the injected admin bearer — the proxy's authorization is
// bound to the path and method IT routed, and any upstream reinterpretation
// would run with admin rights on a request the gates never saw.
var overrideHeaders = []string{
	// Routing / method overrides.
	"X-Original-Url",
	"X-Rewrite-Url",
	"X-Original-Uri",
	"X-Http-Method-Override",
	"X-Http-Method",
	"X-Method-Override",
	"X-Forwarded-Host",
	"X-Forwarded-Proto",
	"X-Forwarded-Port",
	"X-Forwarded-Prefix",
	"X-Forwarded-Path",
	"X-Forwarded-For",
	"X-Real-Ip",
	"Forwarded",
	// Session material foreign to Daytona.
	"Cookie",
	// The caller's OWN auth headers: the proxy consumed them at the gate;
	// forwarding EIP-191 signature triples to Daytona hands valid signed
	// requests to the upstream operator and anyone on that path — widening the
	// replay-capture surface (#92/#93) for zero benefit, Daytona has no use
	// for them.
	"X-Wallet-Address",
	"X-Signed-Message",
	"X-Wallet-Signature",
}

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
// sinceTimestamp is a Unix timestamp (seconds); 0 = all history.
// page/pageSize control pagination (0-indexed, newest-first); pageSize=0 returns all.
// Returns events, total count, current block number, and any error.
type EventFetcher interface {
	GetVoucherEvents(ctx context.Context, sinceTimestamp uint64, page, pageSize int) ([]chain.VoucherEvent, int, uint64, error)
}

// Handler wires up all proxy routes onto a Gin engine.
type Handler struct {
	dtona               *daytona.Client
	billing             BillingHooks
	rp                  *httputil.ReverseProxy
	balCheck            BalanceChecker // nil = no check
	ackCheck            AckChecker     // nil = no check
	eventFetcher        EventFetcher   // nil = events endpoint disabled
	createFee           *big.Int       // charged once on sandbox create
	pricePerCPUPerSec   *big.Int       // per CPU core per second
	pricePerMemGBPerSec *big.Int       // per GB memory per second
	voucherIntervalSec  int64
	providerAddress     string   // on-chain settlement identity; used by broker client and balance lookups
	adminAddresses      []string // operator wallets allowed to call admin-only endpoints (lowercased hex)
	sshGatewayHost      string   // if set, replaces localhost in SSH commands
	computePricePerSec  *big.Int
	rdb                 *redis.Client
	teeKey              *ecdsa.PrivateKey // TEE signing key; nil = sealed containers disabled
	broker              *brokerClient     // nil = broker integration disabled
	log                 *zap.Logger

	// SealedOnly, when true, rejects every create-sandbox request that
	// doesn't carry "sealed": true. Set by cmd/billing from Server.SealedOnly
	// config (env SEALED_ONLY=true). Off by default — providers serve both
	// sealed and unsealed workloads unless explicitly opted in.
	SealedOnly bool

	// AppOwner resolves the appId's current TappRegistry owner (lowercased
	// hex). Set by cmd/billing as a TTL-cached chain lookup on
	// getAppInfo(BACKEND_APP_NAME).owner. The owner is ALWAYS an admin on
	// top of adminAddresses — resolved live so it can never drift from the
	// on-chain truth. nil (tests, mock setups) = static admin list only.
	AppOwner func(ctx context.Context) (string, error)
}

func NewHandler(dtona *daytona.Client, bh BillingHooks, balCheck BalanceChecker, ackCheck AckChecker, eventFetcher EventFetcher, createFee, pricePerCPUPerSec, pricePerMemGBPerSec, computePricePerSec *big.Int, providerAddress string, adminAddresses []string, sshGatewayHost string, rdb *redis.Client, log *zap.Logger, brokerURL string, teeKey *ecdsa.PrivateKey, voucherIntervalSec int64) *Handler {
	target, _ := url.Parse(dtona.BaseURL())
	rp := httputil.NewSingleHostReverseProxy(target)

	// Inject admin key on every forwarded request
	orig := rp.Director
	rp.Director = func(req *http.Request) {
		orig(req)
		// Scrub caller-controlled routing / method-override headers BEFORE the
		// admin bearer goes on. The proxy's owner gates bind to the path+method
		// it routed; frameworks and ingresses that honor these headers would
		// reinterpret the request upstream (different path, different method)
		// under admin credentials — e.g. a gated POST /api/sandbox rewritten
		// into DELETE /api/sandbox/<victim> or PUT .../labels. Whether the
		// pinned Daytona honors them is off-repo behavior; the proxy must not
		// forward them regardless.
		for _, h := range overrideHeaders {
			req.Header.Del(h)
		}
		req.Header.Set("Authorization", "Bearer "+dtona.AdminKey())
		// Force an uncompressed (or transport-managed) upstream body: if the
		// CALLER's Accept-Encoding survives, Go's transport passes Daytona's
		// compressed bytes through untouched and the seal-key scrub in
		// ModifyResponse scans gzip data it cannot match — the caller then
		// decompresses the key client-side. With the header removed the
		// transport either gets identity or negotiates gzip itself, which it
		// transparently decompresses BEFORE ModifyResponse runs.
		req.Header.Del("Accept-Encoding")
		req.Host = target.Host
	}

	// Strip CORS headers from the upstream response so they are not duplicated
	// on top of the headers already set by gin's CORS middleware.
	// httputil.ReverseProxy uses Add() when copying upstream headers, which
	// would result in Access-Control-Allow-Origin: ["*", "*"] — browsers
	// reject responses with duplicate ACAO headers as a CORS error.
	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Access-Control-Allow-Origin")
		resp.Header.Del("Access-Control-Allow-Methods")
		resp.Header.Del("Access-Control-Allow-Headers")

		// Scrub the sealed-container private key from forwarded JSON bodies.
		// InjectSeal puts SANDBOX_SEAL_KEY into the container's env map, and
		// endpoints that forward Daytona's sandbox object verbatim (GET
		// /sandbox/:id, PUT /sandbox/:id/labels, the catch-all) hand that env
		// straight back to the caller — the owner of a sealed sandbox could
		// read its signing key with one authenticated GET. The strip in
		// handleCreate only covers the create response; scrubbing here covers
		// every forwarded response, present and future. JSON only: streaming
		// bodies (SSE toolbox logs) must not be buffered.
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.Contains(resp.Header.Get("Content-Type"), "json") {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return err
			}
			scrubbed := scrubSealKeyFromBody(body)
			resp.Body = io.NopCloser(bytes.NewReader(scrubbed))
			resp.ContentLength = int64(len(scrubbed))
			resp.Header.Set("Content-Length", strconv.Itoa(len(scrubbed)))
		}
		return nil
	}

	var broker *brokerClient
	if brokerURL != "" && teeKey != nil {
		broker = newBrokerClient(brokerURL, teeKey, providerAddress, voucherIntervalSec, log)
	}
	admins := make([]string, 0, len(adminAddresses))
	for _, a := range adminAddresses {
		a = strings.TrimSpace(a)
		if a != "" {
			admins = append(admins, strings.ToLower(a))
		}
	}
	return &Handler{dtona: dtona, billing: bh, rp: rp, balCheck: balCheck, ackCheck: ackCheck, eventFetcher: eventFetcher, createFee: createFee, pricePerCPUPerSec: pricePerCPUPerSec, pricePerMemGBPerSec: pricePerMemGBPerSec, voucherIntervalSec: voucherIntervalSec, computePricePerSec: computePricePerSec, providerAddress: providerAddress, adminAddresses: admins, sshGatewayHost: sshGatewayHost, rdb: rdb, teeKey: teeKey, broker: broker, log: log}
}

// IsAdmin is the exported admin check for routes registered outside this
// handler (cmd/billing's registry/queue/session endpoints) — one source of
// truth for "who is an operator".
func (h *Handler) IsAdmin(wallet string) bool { return h.isAdmin(wallet) }

// isAdmin reports whether wallet may call operator-only endpoints: either in
// the configured ADMIN_ADDRESSES list, or the appId's current TappRegistry
// owner (resolved live via AppOwner, case-insensitive).
func (h *Handler) isAdmin(wallet string) bool {
	if wallet == "" {
		return false
	}
	target := strings.ToLower(wallet)
	for _, a := range h.adminAddresses {
		if a == target {
			return true
		}
	}
	if h.AppOwner != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if owner, err := h.AppOwner(ctx); err == nil && owner == target {
			return true
		} else if err != nil {
			h.log.Warn("isAdmin: app owner lookup failed; falling back to static admin list", zap.Error(err))
		}
	}
	return false
}

// BrokerDeregister removes a sandbox from broker monitoring. No-op if broker is disabled.
func (h *Handler) BrokerDeregister(ctx context.Context, sandboxID string) {
	if h.broker == nil {
		return
	}
	if err := h.broker.deregisterSession(ctx, sandboxID); err != nil {
		h.log.Warn("broker deregister (archive)", zap.String("id", sandboxID), zap.Error(err))
	}
}

// Register mounts all routes. authMiddleware should already be applied to the group.
//
// Route structure:
//   - Static routes without sub-actions are registered normally.
//   - All /sandbox/:id/* routes go through a single catch-all handler to avoid
//     Gin's restriction on mixing static segments and wildcard catch-alls.
func (h *Handler) Register(rg *gin.RouterGroup) {
	// The traversal guard ships WITH the package: every engine that mounts
	// these routes gets it, not just binaries that remember the engine-wide
	// mount (authorization binds to :id while the raw path is forwarded as
	// admin — see PathTraversalGuard).
	rg.Use(PathTraversalGuard())
	// ── Create sandbox ─────────────────────────────────────────────────────
	rg.POST("/sandbox", h.handleCreate)

	// ── Balance: the caller's spendable balance as the gates see it ───────
	rg.GET("/balance", h.handleBalance)

	// ── List / paginated (filter by owner) ────────────────────────────────
	rg.GET("/sandbox", h.handleList)
	rg.GET("/sandbox/paginated", h.handleList)
	rg.GET("/volumes", h.handleVolumesList)
	rg.POST("/snapshots", h.handleSnapshotCreate)
	rg.DELETE("/snapshots/:id", h.handleSnapshotDelete)

	// ── DELETE /sandbox/:id (no action suffix, safe to register separately) ─
	rg.DELETE("/sandbox/:id", h.withOwnerOrAdmin(h.handleDelete))

	// ── Catch-all for /sandbox/:id/<action> ────────────────────────────────
	// Blocked (autostop/autoarchive), lifecycle hooks, label protection, and
	// transparent forwarding are all dispatched here to keep Gin happy.
	rg.Any("/sandbox/:id/*action", h.handleCatchAll)

	// ── GET /sandbox/:id (no wildcard suffix) ─────────────────────────────
	rg.GET("/sandbox/:id", h.withOwnerOrAdmin(h.forward))

	// ── Toolbox API (/api/toolbox/:id/*) — owner check + sealed check + transparent forward
	rg.Any("/toolbox/:id/*action", h.withOwnerNotSealed(h.forward))

	// ── Admin-only: archive all running sandboxes (pre-deploy) ─────────────
	rg.POST("/archive-all", h.handleArchiveAll)

	// ── Admin-only: list all billing sessions ──────────────────────────────
	rg.GET("/sessions", h.handleSessions)

	// ── Admin-only: close one orphan billing session ───────────────────────
	rg.DELETE("/sessions/:id", h.handleCloseSession)

	// ── Admin-only: local Redis billing audit log (created/stopped/auto_stopped/settled) ──
	rg.GET("/audit-log", h.handleAuditLog)
}

// RegisterPublic mounts endpoints that don't need wallet auth — typically
// reads of on-chain data that anyone could query via RPC. Keeps them out of
// the auth.Middleware-protected group so dashboards/explorers can hit them
// without a signed request.
func (h *Handler) RegisterPublic(rg *gin.RouterGroup) {
	rg.Use(PathTraversalGuard())
	// On-chain VoucherSettled events. Anyone can derive the same data from
	// the public RPC + contract address; no value in gating it.
	rg.GET("/events", h.handleEvents)
}

// ── Create ─────────────────────────────────────────────────────────────────

func (h *Handler) handleCreate(c *gin.Context) {
	wallet := c.GetString("wallet_address")

	// Read body early so we can extract cpu/mem for the broker top-up call
	// and then pass the (possibly modified) body to InjectOwner.
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
		return
	}
	// Snapshot-only policy: reject custom cpu/memory/disk/gpu/image so the
	// billed spec always equals the provisioned one (#73/#77). Runs before any
	// balance reservation or Daytona call.
	if err := requireSnapshotCreate(body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Snapshot-only policy guarantees no custom cpu/memory in the body, so the
	// snapshot record is the ONLY billing spec source. Look it up and FAIL
	// CLOSED when billing is on: a transient GetSnapshot error must not admit
	// on createFee alone while Daytona provisions the snapshot's real spec
	// (that reopens #77). This matches the sealed path, which already fails
	// closed on a snapshot-resolution error. Best-effort only when billing is
	// disabled (balCheck nil, e.g. unit tests that don't exercise the gate).
	reqCPU, reqMemGB := 0, 0
	snapName := extractSnapshotName(body)
	snap, snapErr := h.dtona.GetSnapshot(c.Request.Context(), snapName)
	switch {
	case snapErr == nil && snap != nil:
		reqCPU, reqMemGB = snap.CPU, snap.Mem
	case h.balCheck == nil:
		// Billing disabled — spec is not needed; best-effort, don't block.
	case snapErr != nil:
		// Transient lookup failure: fail closed, do NOT admit on createFee
		// alone while Daytona provisions the real spec (reopens #77). Mirrors
		// the sealed path, which already fails closed on snapshot resolution.
		h.log.Error("create: snapshot spec lookup failed; failing closed to protect billing",
			zap.String("snapshot", snapName), zap.Error(snapErr))
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not resolve snapshot spec for billing"})
		return
	default: // snap == nil: snapshot not found
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("snapshot %q not found", snapName)})
		return
	}

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

	// Pre-check: reject if on-chain balance is below the minimum required.
	// create requires createFee + one voucher interval of compute for the requested spec.
	// available = chainBalance - reserved prevents concurrent requests from double-spending.
	var createRequired *big.Int
	createReserved := false
	if h.balCheck != nil {
		createRequired = new(big.Int).Add(h.createFee, h.intervalCost(reqCPU, reqMemGB))
		held, pending := h.outstandingDebt(c.Request.Context(), wallet)
		heldDebt := new(big.Int).Add(held, pending)
		balance, err := h.balCheck.GetBalance(c.Request.Context(), common.HexToAddress(wallet), common.HexToAddress(h.providerAddress))
		if err != nil {
			h.log.Error("balance check", zap.String("wallet", wallet), zap.Error(err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "balance check failed"})
			return
		}
		// Reserve FIRST, judge against the post-increment total, roll back on
		// failure. Check-then-reserve had a TOCTOU window: N concurrent creates
		// all read the pre-reservation total, all passed, and N sandboxes
		// started on a balance that covered one (#74). The INCRBY inside
		// Reserve is the serialization point — under any interleaving, request
		// k sees k×required reserved and judges accordingly. TTL is a safety
		// net: if the process crashes before OnCreate fires, the reservation
		// auto-expires after 2 voucher intervals.
		ttl := time.Duration(h.voucherIntervalSec*2) * time.Second
		totalReserved, rerr := billing.Reserve(c.Request.Context(), h.rdb, wallet, h.providerAddress, createRequired, ttl)
		if rerr != nil {
			// Redis unavailable: fall back to the advisory (racy) check rather
			// than blocking all creates — billing is degraded anyway and the
			// debt ledger still catches overspend after the fact.
			h.log.Warn("balance reservation failed; advisory check only", zap.String("wallet", wallet), zap.Error(rerr))
			totalReserved = new(big.Int).Add(billing.GetReserved(c.Request.Context(), h.rdb, wallet, h.providerAddress), createRequired)
		} else {
			createReserved = true
		}
		// shortfall = balance − heldDebt − totalReserved (totalReserved already
		// includes THIS request); negative → cannot afford.
		shortfall := new(big.Int).Sub(new(big.Int).Sub(balance, heldDebt), totalReserved)
		if shortfall.Sign() < 0 && h.broker != nil {
			// Ask the broker to top up the user's balance (funding-only call:
			// sandbox_id="" means no monitoring session is registered yet).
			if berr := h.broker.registerSession(c.Request.Context(), "", wallet, int64(reqCPU), int64(reqMemGB)); berr != nil {
				h.log.Warn("broker pre-create fund", zap.String("wallet", wallet), zap.Error(berr))
			} else {
				// Re-read balance after top-up (the reservation stays put).
				balance, err = h.balCheck.GetBalance(c.Request.Context(), common.HexToAddress(wallet), common.HexToAddress(h.providerAddress))
				if err != nil {
					if createReserved {
						billing.Release(c.Request.Context(), h.rdb, wallet, h.providerAddress, createRequired)
					}
					h.log.Error("balance re-check", zap.String("wallet", wallet), zap.Error(err))
					c.JSON(http.StatusBadGateway, gin.H{"error": "balance check failed"})
					return
				}
				shortfall = new(big.Int).Sub(new(big.Int).Sub(balance, heldDebt), totalReserved)
			}
		}
		if shortfall.Sign() < 0 {
			if createReserved {
				billing.Release(c.Request.Context(), h.rdb, wallet, h.providerAddress, createRequired)
				createReserved = false
			}
			// Spendable BEFORE this request's own reservation, floored at 0.
			display := new(big.Int).Add(shortfall, createRequired)
			if display.Sign() < 0 {
				display = new(big.Int)
			}
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":              "insufficient balance",
				"available":          display.String(),
				"required":           createRequired.String(),
				"outstanding_debt":   held.String(),    // parked debt — must be topped up
				"pending_settlement": pending.String(), // queued usage — charged on next settle
			})
			return
		}
	}

	// Sealed containers: resolve image hash and inject TEE attestation + keypair
	// before forwarding to Daytona.
	sealed := extractSealed(body)
	if err := ValidatePublicPorts(body, sealed); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !sealed && h.SealedOnly {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "this provider only accepts sealed sandboxes; set \"sealed\": true in the create request",
		})
		return
	}
	if sealed && h.teeKey == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "sealed containers not available: TEE key not configured"})
		return
	}
	var imageHash string
	if sealed {
		imageRef, err := h.resolveImageRef(c.Request.Context(), body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sealed containers require an image or snapshot: " + err.Error()})
			return
		}
		imageHash, err = registry.GetDigest(c.Request.Context(), imageRef)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":  "sealed containers require the image to be present in the registry",
				"detail": err.Error(),
			})
			return
		}
		// Pin the forwarded image to the attested digest. The attestation is
		// signed over imageHash, but a mutable TAG forwarded to Daytona can be
		// re-pointed between resolution and the runner's pull — the container
		// would then run code the attestation never covered (attestation-
		// identity forgery; the whole sealed trust chain gates on this digest).
		// Rewriting to repo@sha256:<digest> makes the pulled image byte-
		// identical to the attested one by content addressing.
		//
		// Snapshot creates are not rewritten: Daytona resolves the snapshot
		// internally (rewriting to an image would drop the snapshot's resource
		// defaults), its image names here are content-addressed already, and
		// re-tagging would require push access to the internal registry, which
		// tenants cannot reach.
		if hasDirectImage(body) {
			pinned, perr := registry.PinRef(imageRef, imageHash)
			if perr != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid image reference"})
				return
			}
			body, err = rewriteImage(body, pinned)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
				return
			}
		}
	}

	modified, err := InjectOwner(body, wallet)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if sealed {
		modified, err = InjectSeal(modified, h.teeKey, imageHash)
		if err != nil {
			if strings.HasPrefix(err.Error(), "seal_id must be") {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			h.log.Error("InjectSeal failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "seal initialization failed"})
			return
		}
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

	// Compute the final response body first so we can set Content-Length to match.
	// For sealed containers, strip SANDBOX_SEAL_KEY — the private key must never
	// leave the enclave, and stripping shortens the body so the upstream
	// Content-Length header must not be forwarded verbatim.
	result := upstream.Result()
	respBytes := upstream.Body.Bytes()
	if sealed && result.StatusCode >= 200 && result.StatusCode < 300 {
		if stripped, err := stripSealKey(respBytes); err == nil {
			respBytes = stripped
		}
	}

	// publicPorts round-trip check: a stock Daytona API silently strips the
	// field (whitelist validation), which would hand the user an unrestricted
	// sandbox while they believe ports are locked down. Fail the create loudly
	// instead, and stop the orphan so it doesn't run unbilled.
	if _, requestedPorts, _ := parsePublicPorts(body); requestedPorts && result.StatusCode >= 200 && result.StatusCode < 300 {
		decorated, supported, derr := decoratePublicPorts(respBytes)
		if derr == nil && !supported {
			id := extractID(respBytes)
			if id != "" {
				go func() {
					ctx := context.WithoutCancel(c.Request.Context())
					if serr := h.dtona.StopSandbox(ctx, id); serr != nil {
						h.log.Error("stop sandbox after unsupported publicPorts", zap.String("id", id), zap.Error(serr))
					}
				}()
			}
			h.log.Error("publicPorts requested but Daytona backend dropped the field — fork images not deployed?", zap.String("id", id))
			c.JSON(http.StatusBadGateway, gin.H{
				"error": "publicPorts is not supported by this provider's Daytona backend; the sandbox was not started",
			})
			return
		}
		if derr == nil {
			respBytes = decorated
		}
	}
	for k, vs := range result.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue // recomputed below from actual body length
		}
		for _, v := range vs {
			c.Writer.Header().Add(k, v)
		}
	}
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(respBytes)))
	c.Writer.WriteHeader(result.StatusCode)
	c.Writer.Write(respBytes) //nolint:errcheck

	if result.StatusCode >= 200 && result.StatusCode < 300 {
		if id := extractID(upstream.Body.Bytes()); id != "" {
			// Bill the provisioned spec: prefer what Daytona reports in the
			// create response (the actual provisioned truth), and fall back to
			// the gate-resolved snapshot spec when the response omits it.
			// Depending on the response echo ALONE was the bug — if Daytona
			// stops echoing cpu/mem the session would open at 0 rate and #77
			// returns silently; the gate-resolved fallback prevents that. Under
			// snapshot-only the two agree, and #116's clamped release makes any
			// residual reserve/release asymmetry harmless.
			cpu, memGB := reqCPU, reqMemGB
			if rc, rm := extractResources(upstream.Body.Bytes()); rc != 0 || rm != 0 {
				cpu, memGB = rc, rm
			}
			go func() {
				ctx := context.WithoutCancel(c.Request.Context())
				// Register the real sandbox ID with the broker for ongoing
				// balance monitoring.
				if h.broker != nil {
					if berr := h.broker.registerSession(ctx, id, wallet, int64(cpu), int64(memGB)); berr != nil {
						h.log.Warn("broker post-create register", zap.String("id", id), zap.Error(berr))
					}
				}
				h.billing.OnCreate(ctx, id, wallet, cpu, memGB)
				// OnCreate enqueues vouchers; reservation released there.
			}()
		} else if createReserved {
			// 2xx but no sandbox ID extracted — release reservation immediately.
			billing.Release(c.Request.Context(), h.rdb, wallet, h.providerAddress, createRequired)
		}
	} else if createReserved {
		// Daytona returned an error — release reservation immediately.
		billing.Release(c.Request.Context(), h.rdb, wallet, h.providerAddress, createRequired)
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

	// Fetch sandbox spec once; used for both broker registration and balance check.
	cpu, memGB := 0, 0
	if sb, err := h.dtona.GetSandbox(c.Request.Context(), id); err == nil {
		cpu, memGB = sb.CPU, sb.Memory
	}

	// A start against an ALREADY-OPEN billing session (create-then-start with
	// no stop in between) is a billing no-op: OnStart returns early and emits
	// no voucher. Skip the whole reserve/gate then — taking a reservation here
	// would leak for its TTL, since OnStart's release only runs when it opens a
	// session (review #116 F1). Redis error → treat as absent and gate as
	// usual (safe direction).
	sessionOpen := false
	if existing, gerr := billing.GetSession(c.Request.Context(), h.rdb, id); gerr == nil && existing != nil {
		sessionOpen = true
	}

	// Pre-check: reject if on-chain balance is below one voucher interval for this sandbox's spec.
	// If insufficient and broker is configured, request a top-up and wait for it to land.
	// available = chainBalance - reserved prevents concurrent requests from double-spending.
	var startRequired *big.Int
	startReserved := false
	if h.balCheck != nil && !sessionOpen {
		startRequired = h.intervalCost(cpu, memGB)
		held, pending := h.outstandingDebt(c.Request.Context(), wallet)
		heldDebt := new(big.Int).Add(held, pending)
		balance, err := h.balCheck.GetBalance(c.Request.Context(), common.HexToAddress(wallet), common.HexToAddress(h.providerAddress))
		if err != nil {
			h.log.Error("balance check (start)", zap.String("wallet", wallet), zap.Error(err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "balance check failed"})
			return
		}
		// Reserve-first, same TOCTOU shape as the create gate (#74): judge
		// against the post-increment total, roll back the reservation on
		// rejection.
		ttl := time.Duration(h.voucherIntervalSec*2) * time.Second
		totalReserved, rerr := billing.Reserve(c.Request.Context(), h.rdb, wallet, h.providerAddress, startRequired, ttl)
		if rerr != nil {
			h.log.Warn("balance reservation failed; advisory check only", zap.String("wallet", wallet), zap.Error(rerr))
			totalReserved = new(big.Int).Add(billing.GetReserved(c.Request.Context(), h.rdb, wallet, h.providerAddress), startRequired)
		} else {
			startReserved = true
		}
		shortfall := new(big.Int).Sub(new(big.Int).Sub(balance, heldDebt), totalReserved)
		if shortfall.Sign() < 0 && h.broker != nil {
			if berr := h.broker.registerSession(c.Request.Context(), id, wallet, int64(cpu), int64(memGB)); berr != nil {
				h.log.Warn("broker pre-start fund", zap.String("id", id), zap.Error(berr))
			} else {
				// Re-check balance after broker waited for deposit.
				balance, err = h.balCheck.GetBalance(c.Request.Context(), common.HexToAddress(wallet), common.HexToAddress(h.providerAddress))
				if err != nil {
					if startReserved {
						billing.Release(c.Request.Context(), h.rdb, wallet, h.providerAddress, startRequired)
					}
					h.log.Error("balance re-check (start)", zap.String("wallet", wallet), zap.Error(err))
					c.JSON(http.StatusBadGateway, gin.H{"error": "balance check failed"})
					return
				}
				shortfall = new(big.Int).Sub(new(big.Int).Sub(balance, heldDebt), totalReserved)
			}
		} else if h.broker != nil {
			// Balance sufficient: register for monitoring only (non-blocking).
			go h.broker.registerSession(context.WithoutCancel(c.Request.Context()), id, wallet, int64(cpu), int64(memGB))
		}
		if shortfall.Sign() < 0 {
			if startReserved {
				billing.Release(c.Request.Context(), h.rdb, wallet, h.providerAddress, startRequired)
				startReserved = false
			}
			display := new(big.Int).Add(shortfall, startRequired)
			if display.Sign() < 0 {
				display = new(big.Int)
			}
			c.JSON(http.StatusPaymentRequired, gin.H{
				"error":              "insufficient balance",
				"available":          display.String(),
				"required":           startRequired.String(),
				"outstanding_debt":   held.String(),    // parked debt — must be topped up
				"pending_settlement": pending.String(), // queued usage — charged on next settle
			})
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
			// OnStart enqueues voucher; reservation released there.
		}()
	} else if startReserved {
		billing.Release(c.Request.Context(), h.rdb, wallet, h.providerAddress, startRequired)
	}
}

func (h *Handler) handleStop(c *gin.Context) {
	id := c.Param("id")
	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		ctx := context.WithoutCancel(c.Request.Context())
		go h.billing.OnStop(ctx, id)
		if h.broker != nil {
			go func() {
				if berr := h.broker.deregisterSession(ctx, id); berr != nil {
					h.log.Warn("broker deregister (stop)", zap.String("id", id), zap.Error(berr))
				}
			}()
		}
	}
}

func (h *Handler) handleDelete(c *gin.Context) {
	id := c.Param("id")
	h.rp.ServeHTTP(safeWriter{c.Writer}, c.Request)
	if c.Writer.Status() >= 200 && c.Writer.Status() < 300 {
		ctx := context.WithoutCancel(c.Request.Context())
		go h.billing.OnDelete(ctx, id)
		if h.broker != nil {
			go func() {
				if berr := h.broker.deregisterSession(ctx, id); berr != nil {
					h.log.Warn("broker deregister (delete)", zap.String("id", id), zap.Error(berr))
				}
			}()
		}
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
// Sealed sandboxes are rejected — SSH is an external access channel.
func (h *Handler) handleSSHAccess(c *gin.Context) {
	id := c.Param("id")

	// Check sealed status. withOwner already confirmed ownership, so we only
	// need the label here; the extra GetSandbox call is acceptable on this path.
	sb, err := h.dtona.GetSandbox(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "ssh access failed"})
		return
	}
	if sealBlocksAccess(sb) {
		c.JSON(http.StatusForbidden, gin.H{"error": "sealed sandbox: SSH access not allowed"})
		return
	}

	access, err := h.dtona.CreateSSHAccess(c.Request.Context(), id)
	if err != nil {
		h.log.Warn("ssh-access failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "ssh access failed"})
		return
	}
	// If SSH_GATEWAY_HOST is configured, rewrite the host in the SSH command
	// server-side. Otherwise leave localhost as a placeholder for the frontend
	// to replace with window.location.hostname.
	if h.sshGatewayHost != "" {
		access.SSHCommand = strings.ReplaceAll(access.SSHCommand, "localhost", h.sshGatewayHost)
	}
	c.JSON(http.StatusOK, access)
}

// handleArchiveAll stops then archives every started/starting sandbox.
// Admin-only. Daytona requires stop before archive: stop removes the
// container, archive then backs up the filesystem to object storage so it can
// be restored later.
func (h *Handler) handleArchiveAll(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
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

// handleForceDelete deletes any sandbox regardless of owner. Admin only.
func (h *Handler) handleForceDelete(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
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

// handleForceStop stops any sandbox regardless of owner. Admin only.
// Semantically distinct from owner-stop ("user finished work"): force-stop
// signals an operator-induced halt, which downstream pipelines (alerts,
// refunds, attestation revocation) may want to treat differently.
//
// Synchronous: blocks until Daytona reports the sandbox in stopped/archived/error
// state. Daytona's /stop API is async — returning eagerly to the caller leaves
// a window where a follow-up start can race the in-progress stop and push the
// sandbox into errored state. Callers can rely on a 2xx here meaning the
// sandbox is genuinely stopped.
func (h *Handler) handleForceStop(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	id := c.Param("id")
	h.log.Info("admin force-stop", zap.String("admin", wallet), zap.String("sandbox", id))

	if err := h.dtona.StopSandbox(c.Request.Context(), id); err != nil {
		h.log.Warn("admin force-stop: stop call failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	waitCtx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := h.dtona.WaitStopped(waitCtx, id); err != nil {
		h.log.Warn("admin force-stop: wait stopped failed", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusGatewayTimeout, gin.H{"error": "stopped state timeout: " + err.Error()})
		return
	}

	ctx := context.WithoutCancel(c.Request.Context())
	h.billing.OnStop(ctx, id)
	if h.broker != nil {
		if berr := h.broker.deregisterSession(ctx, id); berr != nil {
			h.log.Warn("broker deregister (force-stop)", zap.String("id", id), zap.Error(berr))
		}
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "state": "stopped"})
}

// handleAuditLog returns the local Redis-backed billing event log
// (created / stopped / auto_stopped / settled). Distinct from /events,
// which queries on-chain VoucherSettled. Admin-only because the log spans
// all owners.
func (h *Handler) handleAuditLog(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	list, err := events.List(c.Request.Context(), h.rdb)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []events.Event{}
	}
	c.JSON(http.StatusOK, list)
}

// handleEvents returns on-chain VoucherSettled events for this contract.
// Accepts optional ?from_block=<n> query param; defaults to last ~50k blocks.
// Chain data is public so no provider restriction is applied.
func (h *Handler) handleEvents(c *gin.Context) {
	if h.eventFetcher == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "events not configured"})
		return
	}
	// ?since=<unix_ts>: return events with block.timestamp >= since.
	// 0 or omitted defaults to a 7-day window — "all history" blows past RPC
	// response limits on any contract with real history (observed: a contract
	// past nonce 514k 502s on every unbounded query), so an explicit recent
	// window is the only default that always works. Callers wanting deeper
	// history page backwards with explicit since values.
	var sinceTimestamp uint64
	if s := c.Query("since"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid since"})
			return
		}
		sinceTimestamp = n
	}
	if sinceTimestamp == 0 {
		sinceTimestamp = uint64(time.Now().Add(-7 * 24 * time.Hour).Unix())
	}
	page := 0
	if s := c.Query("page"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid page"})
			return
		}
		page = n
	}
	pageSize := 50
	if s := c.Query("page_size"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid page_size"})
			return
		}
		pageSize = n
	}
	evts, total, currentBlock, err := h.eventFetcher.GetVoucherEvents(c.Request.Context(), sinceTimestamp, page, pageSize)
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
	c.JSON(http.StatusOK, gin.H{
		"current_block": currentBlock,
		"since":         sinceTimestamp,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
		"events":        result,
	})
}

// handleSessions lists all sandboxes enriched with billing session data
// (accrued fees) where available. Admin only.
func (h *Handler) handleSessions(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
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

	type row struct {
		SandboxID     string `json:"sandbox_id"`
		Owner         string `json:"owner"`
		State         string `json:"state"`
		NextVoucherAt int64  `json:"next_voucher_at,omitempty"`
		PricePerSec   string `json:"price_per_sec,omitempty"`
	}
	result := make([]row, 0, len(sandboxes))
	for _, sb := range sandboxes {
		r := row{
			SandboxID: sb.ID,
			Owner:     sb.Labels[ownerLabel],
			State:     sb.State,
		}
		if sess, ok := sessionMap[sb.ID]; ok {
			r.NextVoucherAt = sess.NextVoucherAt
			r.PricePerSec = sess.PricePerSec
		}
		result = append(result, r)
	}
	c.JSON(http.StatusOK, result)
}

// handleCloseSession deletes the billing session for one sandbox and
// deregisters it from the broker. Used to clean up orphan sessions left
// behind when Daytona auto-archives a sandbox (bypassing OnStop). Admin only.
//
// Charging continues from the user's pre-deducted reserve until this is
// called, so the action is one-shot and idempotent: calling it on a sandbox
// without an open session is a no-op success.
func (h *Handler) handleCloseSession(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sandbox id required"})
		return
	}
	ctx := c.Request.Context()
	if err := billing.DeleteSession(ctx, h.rdb, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.BrokerDeregister(ctx, id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ── Labels ──────────────────────────────────────────────────────────────────

func (h *Handler) handleLabels(c *gin.Context) {
	// Daytona's replaceLabels is a wholesale replace, so the protected labels
	// must be re-injected from the live sandbox — a payload-only strip would
	// have the replace DELETE them (ownership bricked; sealed flag cleared →
	// SSH/toolbox reopen and the seal key becomes readable).
	sb, err := h.dtona.GetSandbox(c.Request.Context(), c.Param("id"))
	if err != nil || sb == nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "sandbox lookup failed"})
		return
	}
	body, _ := io.ReadAll(c.Request.Body)
	merged, err := MergeProtectedLabels(body, sb.Labels)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid label payload"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(merged))
	c.Request.ContentLength = int64(len(merged))
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

// handleVolumesList gates GET /api/volumes to admins. Volumes are not a wired
// feature in 0g-sandbox (no create path, so they carry no daytona-owner label),
// which means an owner-scoped filter has nothing to match on and forwarding as
// admin to a non-admin caller would leak every tenant's volume IDs. Deny-by-
// default: admins get the raw list (ops view), everyone else gets 403. When the
// volume feature is built, replace this with an owner-scoped list (filter the
// forwarded response by the caller's daytona-owner label).
func (h *Handler) handleVolumesList(c *gin.Context) {
	if !h.isAdmin(c.GetString("wallet_address")) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}
	h.forward(c)
}

// handleListSnapshots lists all Daytona snapshots. Snapshots are admin-managed
// base images; any authenticated user may see and use them.
func (h *Handler) handleListSnapshots(c *gin.Context) {
	h.forward(c)
}

// handleSnapshotCreate registers a Docker image as a named Daytona snapshot.
// Admin-only: accepts {name, imageName}, forwards to Daytona internally.
//
// Before forwarding, the caller-supplied imageName is resolved to its content
// digest and rewritten to a derived tag "<repo>:d-<shortdigest>" (created in
// the registry alongside the original tag). This freezes the snapshot to one
// specific image revision: if the caller later re-pushes the same base tag
// with different content, they must delete and recreate the snapshot to pick
// it up. That delete+create cycle produces a new derived tag, which in turn
// generates a fresh Daytona-side cache key on the runner — otherwise stale
// wrapped images keyed on the old imageName get reused and the new sandbox
// silently runs old content.
//
// We use a derived tag rather than "<repo>@sha256:..." because Daytona
// rejects digest-form imageNames as "invalid reference format".
func (h *Handler) handleSnapshotCreate(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body"})
		return
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	imageName, _ := m["imageName"].(string)
	if imageName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "imageName required"})
		return
	}

	pinned, err := registry.TagByDigest(c.Request.Context(), imageName)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":  "resolve image digest failed",
			"detail": err.Error(),
		})
		return
	}
	m["imageName"] = pinned

	newBody, err := json.Marshal(m)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "marshal body"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(newBody))
	c.Request.ContentLength = int64(len(newBody))

	h.forward(c)
}

// handleSnapshotDelete deletes a snapshot by ID. Admin-only.
//
// Daytona has a bug where DELETE succeeds but then the audit log INSERT fails
// because the admin key carries no actorId in the request context, causing a
// spurious 500. We detect this case: if Daytona returns 500, we verify the
// snapshot is actually gone and return 200 if so.
//
// On successful delete, the derived "<repo>:d-<shortdigest>" tag that
// handleSnapshotCreate planted in the registry is removed too — otherwise
// those tags accumulate indefinitely. Caller-supplied tags and tags still
// referenced by other snapshots are left alone.
func (h *Handler) handleSnapshotDelete(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if !h.isAdmin(wallet) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin only"})
		return
	}

	snapshotID := c.Param("id")

	// Capture imageName before the delete; afterwards the snapshot is gone
	// and we'd have nothing to clean up against.
	var imageName string
	if pre, err := h.dtona.GetSnapshot(c.Request.Context(), snapshotID); err == nil && pre != nil {
		imageName = pre.ImageName
	}

	rec := httptest.NewRecorder()
	h.rp.ServeHTTP(rec, c.Request)

	deleted := rec.Code >= 200 && rec.Code < 300
	if !deleted && rec.Code == http.StatusInternalServerError {
		// Daytona audit-log bug — verify whether the delete actually went through.
		if snap, err := h.dtona.GetSnapshot(c.Request.Context(), snapshotID); err == nil && snap == nil {
			deleted = true
		}
	}

	if !deleted {
		copyRecorder(c, rec)
		return
	}

	h.cleanupDerivedTag(c.Request.Context(), snapshotID, imageName)
	c.Status(http.StatusOK)
}

// cleanupDerivedTag removes the derived "<repo>:d-<shortdigest>" tag from the
// internal registry, but only if no other snapshot still references it. Tag
// deletion failure is non-fatal — we've already deleted the Daytona snapshot.
func (h *Handler) cleanupDerivedTag(ctx context.Context, snapshotID, imageName string) {
	if imageName == "" || !registry.IsDerivedTag(imageName) {
		return
	}

	snaps, err := h.dtona.ListSnapshots(ctx)
	if err != nil {
		h.log.Warn("snapshot delete: list snapshots for tag cleanup",
			zap.String("id", snapshotID), zap.Error(err))
		return
	}
	for _, s := range snaps {
		if s.ID != snapshotID && s.ImageName == imageName {
			// Another snapshot still uses this content-addressed tag.
			return
		}
	}

	if err := registry.DeleteTag(ctx, imageName); err != nil {
		h.log.Warn("snapshot delete: registry tag cleanup failed",
			zap.String("id", snapshotID), zap.String("tag", imageName), zap.Error(err))
	}
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
		h.withOwnerOrAdmin(h.handleStop)(c)
	case method == http.MethodPost && action == "/archive":
		h.withOwnerOrAdmin(h.handleArchive)(c)
	case method == http.MethodPost && action == "/ensure-billing":
		h.withOwnerOrAdmin(h.handleEnsureBilling)(c)
	case method == http.MethodPost && action == "/ssh-access":
		h.withOwner(h.handleSSHAccess)(c)
	case method == http.MethodDelete && action == "/force":
		h.handleForceDelete(c)
	case method == http.MethodPost && action == "/force-stop":
		h.handleForceStop(c)

	// ── Label protection ───────────────────────────────────────────────────
	case method == http.MethodPut && action == "/labels":
		h.withOwner(h.handleLabels)(c)

	// ── Transparent proxy (owner check) ───────────────────────────────────
	default:
		h.withOwner(h.stripVolumesThenForward)(c)
	}
}

// stripVolumesThenForward removes any caller-supplied "volumes" (any case) from
// a JSON body before the transparent forward. The catch-all forwards arbitrary
// sandbox-scoped actions to Daytona as admin; if the backend ever accepts
// volume attach/modify through one of them, an unvalidated volumes array would
// reopen the cross-tenant mount closed at create (deny-by-default until
// per-volume ownership validation lands, #81). Non-JSON or empty bodies pass
// through untouched.
func (h *Handler) stripVolumesThenForward(c *gin.Context) {
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		body, err := io.ReadAll(c.Request.Body)
		if err == nil {
			var m map[string]any
			if json.Unmarshal(body, &m) == nil {
				changed := false
				for k := range m {
					if strings.EqualFold(k, "volumes") {
						delete(m, k)
						changed = true
					}
				}
				if changed {
					if nb, err := json.Marshal(m); err == nil {
						body = nb
					}
				}
			}
			c.Request.Body = io.NopCloser(bytes.NewReader(body))
			c.Request.ContentLength = int64(len(body))
		}
	}
	h.forward(c)
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

// withOwnerOrAdmin lets admins act on any sandbox without holding the owner
// key, while still enforcing ownership for non-admins. The /sandbox/:id/force*
// routes predate this and remain as explicit operator-intent endpoints (the
// distinct path makes operator overrides easy to audit), but the standard
// /stop and /delete routes route through here so admins don't need the side
// path for routine cleanup.
func (h *Handler) withOwnerOrAdmin(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		wallet := c.GetString("wallet_address")
		if h.isAdmin(wallet) {
			next(c)
			return
		}
		id := c.Param("id")
		if err := CheckOwner(c.Request.Context(), h.dtona, id, wallet); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		next(c)
	}
}

// withOwnerNotSealed wraps a handler with ownership + sealed checks.
// Used for toolbox routes: sealed sandboxes block all remote access channels.
func (h *Handler) withOwnerNotSealed(next gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		wallet := c.GetString("wallet_address")
		sb, err := h.dtona.GetSandbox(c.Request.Context(), id)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		owner := sb.Labels[ownerLabel]
		if !strings.EqualFold(owner, wallet) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		if sealBlocksAccess(sb) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "sealed sandbox: external access not allowed"})
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

// intervalCost returns the compute cost for one voucher interval given cpu/mem.
// Uses per-resource prices if set; falls back to flat computePricePerSec.
func (h *Handler) intervalCost(cpu, memGB int) *big.Int {
	interval := big.NewInt(h.voucherIntervalSec)
	if h.pricePerCPUPerSec != nil && h.pricePerCPUPerSec.Sign() > 0 ||
		h.pricePerMemGBPerSec != nil && h.pricePerMemGBPerSec.Sign() > 0 {
		cpuCost := new(big.Int).Mul(h.pricePerCPUPerSec, big.NewInt(int64(cpu)))
		memCost := new(big.Int).Mul(h.pricePerMemGBPerSec, big.NewInt(int64(memGB)))
		perSec := new(big.Int).Add(cpuCost, memCost)
		return new(big.Int).Mul(perSec, interval)
	}
	return new(big.Int).Mul(h.computePricePerSec, interval)
}

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

// availableBalance returns chainBalance - reserved, floored at zero.
// availableBalance is the balance a caller may spend on new work: the on-chain
// balance minus in-flight reservations minus any outstanding held debt.
// Outstanding debt is settled (oldest first) before new compute, so it is not
// available — a user with debt must top up enough to cover it before starting
// or creating a sandbox. Clamped at zero.
func availableBalance(chainBalance, reserved, heldDebt *big.Int) *big.Int {
	available := new(big.Int).Sub(chainBalance, reserved)
	if heldDebt != nil {
		available.Sub(available, heldDebt)
	}
	if available.Sign() < 0 {
		available.SetInt64(0)
	}
	return available
}

// handleBalance returns the caller's balance exactly as the create/start gates
// compute it, so a user can see WHY a launch is rejected instead of only
// hitting the 402: on-chain balance, in-flight reservations, outstanding held
// debt (must be topped up and settled before new work), and the resulting
// spendable remainder.
func (h *Handler) handleBalance(c *gin.Context) {
	wallet := c.GetString("wallet_address")
	if h.balCheck == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "balance check not configured"})
		return
	}
	balance, err := h.balCheck.GetBalance(c.Request.Context(), common.HexToAddress(wallet), common.HexToAddress(h.providerAddress))
	if err != nil {
		h.log.Error("balance lookup", zap.String("wallet", wallet), zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "balance lookup failed"})
		return
	}
	reserved := billing.GetReserved(c.Request.Context(), h.rdb, wallet, h.providerAddress)
	held, pending := h.outstandingDebt(c.Request.Context(), wallet)
	debt := new(big.Int).Add(held, pending)
	c.JSON(http.StatusOK, gin.H{
		"provider": h.providerAddress,
		"balance":  balance.String(),
		"reserved": reserved.String(),
		// held debt (parked, needs top-up) + pending settlement (queued, will be
		// charged as soon as the settler submits) — both already owed.
		"outstanding_debt":   held.String(),
		"pending_settlement": pending.String(),
		"available":          availableBalance(balance, reserved, debt).String(),
	})
}

// outstandingDebt returns what the user already owes this provider but has not
// yet been charged on-chain, in two parts:
//
//   - held: parked unpayable debt (voucher:held:<user>:<provider>);
//   - pending: vouchers still sitting in the settle queue — accrued usage that
//     WILL be deducted as soon as the settler submits it. While the settler is
//     stalled this can be large (and the raw chain balance correspondingly
//     misleading), so the gates and the balance endpoint must count it.
//
// Lookup failures degrade to zero (logged) — a transient Redis error must not
// wrongly block a create/start. Note this is deliberately fail-open: during a
// Redis hiccup a debt-carrying user could pass the gate. Accepted because it
// matches the existing GetReserved posture, a down Redis breaks session
// creation anyway, and the on-chain settle path remains the money authority.
func (h *Handler) outstandingDebt(ctx context.Context, wallet string) (held, pending *big.Int) {
	user := common.HexToAddress(wallet)
	provider := common.HexToAddress(h.providerAddress)
	held, err := voucher.HeldDebt(ctx, h.rdb, user, provider)
	if err != nil {
		h.log.Warn("held debt lookup failed; treating as zero", zap.String("wallet", wallet), zap.Error(err))
		held = new(big.Int)
	}
	queueKey := fmt.Sprintf(voucher.VoucherQueueKeyFmt, provider.Hex())
	pending, err = voucher.QueuedPending(ctx, h.rdb, queueKey, user, provider)
	if err != nil {
		h.log.Warn("queued pending lookup failed; treating as zero", zap.String("wallet", wallet), zap.Error(err))
		pending = new(big.Int)
	}
	return held, pending
}

// extractSnapshotName parses the "snapshot" field from a sandbox create request body.
func extractSnapshotName(body []byte) string {
	var m struct {
		Snapshot string `json:"snapshot"`
	}
	json.NewDecoder(bytes.NewReader(body)).Decode(&m) //nolint:errcheck
	return m.Snapshot
}

// extractSealed parses the "sealed" boolean from a sandbox create request body.
func extractSealed(body []byte) bool {
	var m struct {
		Sealed bool `json:"sealed"`
	}
	json.NewDecoder(bytes.NewReader(body)).Decode(&m) //nolint:errcheck
	return m.Sealed
}

// resolveImageRef extracts the image reference from a create request body and,
// for snapshot-based sandboxes, resolves the snapshot name to its ImageName.
// hasDirectImage reports whether the create body names an image directly
// (as opposed to a snapshot) — only direct refs are caller-controlled and
// need digest pinning.
func hasDirectImage(body []byte) bool {
	var m struct {
		Image string `json:"image"`
	}
	json.NewDecoder(bytes.NewReader(body)).Decode(&m) //nolint:errcheck
	return m.Image != ""
}

// rewriteImage replaces the body's image field with the pinned reference.
func rewriteImage(body []byte, pinned string) ([]byte, error) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["image"] = pinned
	return json.Marshal(m)
}

func (h *Handler) resolveImageRef(ctx context.Context, body []byte) (string, error) {
	var m struct {
		Image    string `json:"image"`
		Snapshot string `json:"snapshot"`
	}
	json.NewDecoder(bytes.NewReader(body)).Decode(&m) //nolint:errcheck

	if m.Image != "" {
		return m.Image, nil
	}
	if m.Snapshot != "" {
		snaps, err := h.dtona.ListSnapshots(ctx)
		if err != nil {
			return "", fmt.Errorf("list snapshots: %w", err)
		}
		for _, s := range snaps {
			if s.Name == m.Snapshot {
				return s.ImageName, nil
			}
		}
		return "", fmt.Errorf("snapshot %q not found", m.Snapshot)
	}
	return "", fmt.Errorf("no image or snapshot specified")
}
