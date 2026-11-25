package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// SignedRequest is the JSON payload inside X-Signed-Message (fields sorted).
//
// Provider binds the signature to ONE provider: without it, a signed triple
// captured at any provider (nonce dedup is provider-local Redis) can be
// replayed verbatim at another provider within the expiry window and passes
// every owner gate there — SSH/toolbox access, stop/delete, creates billed to
// the victim. Clients set it to the destination's TEE signer address
// (provider_address from GET /api/info).
//
// BodySHA256 binds the signature to ONE request: with it, Action must be
// "METHOD:path" (plus "?query" when the request has one) and BodySHA256 the
// SHA-256 of the exact body bytes sent, empty body included. Without it, a
// message signed for one operation on a sandbox authorizes any other
// operation on that sandbox — whoever relays the request can rewrite the
// method, path or body and the signature still verifies.
type SignedRequest struct {
	Action     string          `json:"action"`
	BodySHA256 string          `json:"body_sha256,omitempty"`
	ExpiresAt  int64           `json:"expires_at"`
	Nonce      string          `json:"nonce"`
	Payload    json.RawMessage `json:"payload"`
	Provider   string          `json:"provider,omitempty"`
	ResourceID string          `json:"resource_id"`
}

const maxFutureWindow = 5 * time.Minute

// Options tunes the binding checks.
type Options struct {
	// ProviderAddress is this node's on-chain identity (the TEE signer). A
	// signed message carrying a DIFFERENT provider is always rejected.
	ProviderAddress string
	// Strict additionally rejects messages that omit the provider field, and
	// requires resource_id to be present on :id routes. Off by default while
	// pre-provider-binding clients are still in circulation (AUTH_STRICT env);
	// flip on once clients ship — an omitted field is exactly what a replayed
	// legacy capture looks like.
	Strict bool
	// RequireRequestBinding additionally rejects messages without
	// body_sha256. Separate from Strict so a deployment that already runs
	// strict keeps accepting provider-bound clients that predate request
	// binding (AUTH_REQUIRE_REQUEST_BINDING env); flip on once they ship.
	// A message that does carry body_sha256 is verified either way.
	RequireRequestBinding bool
}

// maxBoundBodyBytes caps the body the middleware buffers to hash a bound
// request. nginx in front of the API already caps bodies at 16 MiB; this
// only stops a direct client from making the middleware buffer unbounded.
const maxBoundBodyBytes = 32 << 20

var errBodyTooLarge = errors.New("request body too large")

// Middleware returns a Gin handler that validates EIP-191 wallet signatures.
func Middleware(rdb *redis.Client) gin.HandlerFunc {
	return MiddlewareWithOptions(rdb, Options{})
}

// MiddlewareWithOptions validates EIP-191 wallet signatures and binds the
// signed message to this provider and to the addressed resource.
func MiddlewareWithOptions(rdb *redis.Client, opts Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		walletAddr := c.GetHeader("X-Wallet-Address")
		signedMsgB64 := c.GetHeader("X-Signed-Message")
		sigHex := c.GetHeader("X-Wallet-Signature")

		if walletAddr == "" || signedMsgB64 == "" || sigHex == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing auth headers"})
			return
		}

		// Decode signed message
		msgBytes, err := base64.StdEncoding.DecodeString(signedMsgB64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid X-Signed-Message encoding"})
			return
		}

		var req SignedRequest
		if err := json.Unmarshal(msgBytes, &req); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signed message JSON"})
			return
		}

		now := time.Now().Unix()

		// Check expiry
		if req.ExpiresAt <= now {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "request expired"})
			return
		}
		if req.ExpiresAt > now+int64(maxFutureWindow.Seconds()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "expires_at too far in future"})
			return
		}

		// Decode signature
		sigHex = strings.TrimPrefix(sigHex, "0x")
		sig, err := hex.DecodeString(sigHex)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature hex"})
			return
		}

		// Recover signer
		recovered, err := Recover(msgBytes, sig)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}
		if !strings.EqualFold(recovered.Hex(), walletAddr) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			return
		}

		// Provider binding: a message signed for another provider is a replay,
		// not a mistake — reject before consuming the nonce. Empty is allowed
		// only outside Strict mode (legacy clients).
		if opts.ProviderAddress != "" {
			if req.Provider != "" && !strings.EqualFold(req.Provider, opts.ProviderAddress) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signed message bound to a different provider"})
				return
			}
			if req.Provider == "" && opts.Strict {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signed message must include the provider address"})
				return
			}
		}

		// Resource binding: on :id routes the signed resource_id must address
		// THIS resource, so a captured message cannot be re-aimed at another
		// sandbox. Empty is allowed only outside Strict mode (legacy clients).
		if id := c.Param("id"); id != "" {
			if req.ResourceID != "" && req.ResourceID != id {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signed message bound to a different resource"})
				return
			}
			if req.ResourceID == "" && opts.Strict {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signed message must include the resource id"})
				return
			}
		}

		// Request binding: the signed action and body must be this request's.
		// Checked before the nonce is consumed, like the bindings above.
		if req.BodySHA256 != "" {
			if !actionMatches(req.Action, c.Request) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signed message bound to a different request"})
				return
			}
			body, err := readBody(c.Request)
			if errors.Is(err, errBodyTooLarge) {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
				return
			}
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "read request body"})
				return
			}
			sum := sha256.Sum256(body)
			if !strings.EqualFold(strings.TrimPrefix(req.BodySHA256, "0x"), hex.EncodeToString(sum[:])) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signed message bound to a different body"})
				return
			}
			if body != nil {
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
			}
		} else if opts.RequireRequestBinding {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signed message must bind the request (action METHOD:path and body_sha256)"})
			return
		}

		// Nonce dedup via Redis SET NX, scoped per wallet so two users who
		// happen to generate the same nonce string don't collide (128-bit
		// random makes that theoretical, but scoping costs nothing).
		nonceKey := "nonce:" + strings.ToLower(walletAddr) + ":" + req.Nonce
		ttl := time.Duration(req.ExpiresAt-now) * time.Second
		set, err := rdb.SetNX(context.Background(), nonceKey, 1, ttl).Result()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		if !set {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "nonce already used"})
			return
		}

		c.Set("wallet_address", walletAddr)
		c.Next()
	}
}

// actionMatches reports whether a signed action names this request:
// "METHOD:path", plus "?query" when the request has one. The path is compared
// decoded: the broker relay rewrites URL.Path and leaves a RawPath that no
// longer matches it, so the wire encoding is not stable between client and
// server — but the decoded path is what routing, and the :id the resource
// binding checks, are derived from.
func actionMatches(action string, r *http.Request) bool {
	method, target, ok := strings.Cut(action, ":")
	if !ok || method != r.Method {
		return false
	}
	path, query, _ := strings.Cut(target, "?")
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return false
	}
	return decoded == r.URL.Path && query == r.URL.RawQuery
}

// readBody drains the request body, up to maxBoundBodyBytes.
func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBoundBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBoundBodyBytes {
		return nil, errBodyTooLarge
	}
	return body, nil
}
