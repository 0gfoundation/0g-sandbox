package auth

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
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
type SignedRequest struct {
	Action     string          `json:"action"`
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
}

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
