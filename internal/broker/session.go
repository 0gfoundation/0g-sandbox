package broker

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/auth"
	"github.com/0gfoundation/0g-sandbox/internal/indexer"
)

const (
	seenKeyPrefix = "broker:seen:"
	seenKeyTTL    = 7 * 24 * time.Hour
)

// providerLookup is satisfied by *indexer.Indexer.
type providerLookup interface {
	Get(addr string) (indexer.ProviderRecord, bool)
}

// sessionChainClient is the minimal chain interface the SessionHandler needs.
type sessionChainClient interface {
	GetServicePricing(ctx context.Context, provider common.Address) (*big.Int, *big.Int, *big.Int, error)
	GetProviderBalance(ctx context.Context, user, provider common.Address) (*big.Int, *big.Int, *big.Int, error)
	IsActiveNode(ctx context.Context, appId string, signer common.Address) (bool, error)
}

// SessionHandler handles POST and DELETE /api/session on the Broker.
type SessionHandler struct {
	providers           providerLookup
	chain               sessionChainClient
	payment             PaymentLayer
	rdb                 *redis.Client
	log                 *zap.Logger
	topupIntervals      int64
	depositWaitTimeout  time.Duration
}

// NewSessionHandler creates a SessionHandler.
func NewSessionHandler(
	providers providerLookup,
	chain sessionChainClient,
	payment PaymentLayer,
	rdb *redis.Client,
	log *zap.Logger,
	topupIntervals int64,
	depositWaitTimeoutSec int64,
) *SessionHandler {
	return &SessionHandler{
		providers:          providers,
		chain:              chain,
		payment:            payment,
		rdb:                rdb,
		log:                log,
		topupIntervals:     topupIntervals,
		depositWaitTimeout: time.Duration(depositWaitTimeoutSec) * time.Second,
	}
}

// waitForBalance polls the on-chain balance until it reaches needed or the
// depositWaitTimeout elapses. Returns true if balance reached, false if timeout.
func (h *SessionHandler) waitForBalance(user, provider common.Address, needed *big.Int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), h.depositWaitTimeout)
	defer cancel()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			bal, _, _, err := h.chain.GetProviderBalance(ctx, user, provider)
			if err == nil && bal.Cmp(needed) >= 0 {
				return true
			}
		}
	}
}

type postSessionReq struct {
	SandboxID          string `json:"sandbox_id"`
	ProviderAddr       string `json:"provider_addr"`
	UserAddr           string `json:"user_addr"`
	CPU                int64  `json:"cpu"`
	MemGB              int64  `json:"mem_gb"`
	StartTime          int64  `json:"start_time"`
	VoucherIntervalSec int64  `json:"voucher_interval_sec"`
	Signature          string `json:"signature"`
}

// HandlePost handles POST /api/session.
//
// When sandbox_id is empty the call is treated as a funding-only request:
// the account is topped up but no broker:session entry is written.
// When sandbox_id is non-empty the session is also registered for monitoring.
func (h *SessionHandler) HandlePost(c *gin.Context) {
	var req postSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// 1. Look up provider's teeSignerAddress from indexer.
	rec, ok := h.providers.Get(req.ProviderAddr)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "provider not registered"})
		return
	}

	// 2. Verify EIP-191 signature.
	msgHash := sessionMsgHash(req)
	sig, err := decodeSig(req.Signature)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid signature encoding"})
		return
	}
	signer, err := auth.Recover(msgHash, sig)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}
	ctx := c.Request.Context()
	provider := common.HexToAddress(req.ProviderAddr)
	user := common.HexToAddress(req.UserAddr)
	if rec.AppId == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "provider has no app bound"})
		return
	}
	isNode, err := h.chain.IsActiveNode(ctx, rec.AppId, signer)
	if err != nil || !isNode {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "signature does not match an active TEE node"})
		return
	}

	// 3. Anti-replay (only when sandbox_id is set).
	if req.SandboxID != "" {
		seenKey := fmt.Sprintf("%s%s:%d", seenKeyPrefix, req.SandboxID, req.StartTime)
		if n, _ := h.rdb.Exists(ctx, seenKey).Result(); n > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "already processed"})
			return
		}
	}

	// 4. Read pricing from chain — never trust billing proxy's passed-in prices.
	pricePerCPUPerSec, pricePerMemGBPerSec, createFee, err := h.chain.GetServicePricing(ctx, provider)
	if err != nil {
		h.log.Warn("session: GetServicePricing failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "pricing unavailable"})
		return
	}
	cpu, memGB := req.CPU, req.MemGB
	if cpu == 0 && memGB == 0 {
		cpu, memGB = 1, 1 // fallback to minimum spec when not provided
	}
	pricePerSec := new(big.Int).Add(
		new(big.Int).Mul(pricePerCPUPerSec, big.NewInt(cpu)),
		new(big.Int).Mul(pricePerMemGBPerSec, big.NewInt(memGB)),
	)

	// 5. Compute deficit.
	balance, _, _, err := h.chain.GetProviderBalance(ctx, user, provider)
	if err != nil {
		h.log.Warn("session: GetProviderBalance failed", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "balance check failed"})
		return
	}
	needed := new(big.Int).Mul(pricePerSec, big.NewInt(req.VoucherIntervalSec*h.topupIntervals))
	// Pre-create calls (sandbox_id == "") also need to cover the create fee.
	if req.SandboxID == "" && createFee != nil {
		needed.Add(needed, createFee)
	}
	deficit := new(big.Int)
	if balance.Cmp(needed) < 0 {
		deficit.Sub(needed, balance)
	}

	// 6. Fund via PaymentLayer if deficit > 0, then wait for deposit to land.
	if deficit.Sign() > 0 {
		if err := h.payment.RequestDeposit(ctx, user, provider, deficit); err != nil {
			h.log.Warn("session: PaymentLayer failed", zap.Error(err))
			c.JSON(http.StatusBadGateway, gin.H{"error": "payment layer failed"})
			return
		}
		if !h.waitForBalance(user, provider, needed) {
			h.log.Warn("session: deposit not confirmed in time",
				zap.String("user", user.Hex()),
				zap.String("provider", provider.Hex()))
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "deposit timeout"})
			return
		}
	}

	// 7. Write anti-replay key and session entry (only when sandbox_id is set).
	if req.SandboxID != "" {
		seenKey := fmt.Sprintf("%s%s:%d", seenKeyPrefix, req.SandboxID, req.StartTime)
		h.rdb.Set(ctx, seenKey, "1", seenKeyTTL) //nolint:errcheck

		entry := SessionEntry{
			SandboxID:          req.SandboxID,
			User:               user.Hex(),
			Provider:           provider.Hex(),
			CPU:                cpu,
			MemGB:              memGB,
			PricePerSec:        pricePerSec.String(),
			VoucherIntervalSec: req.VoucherIntervalSec,
			RegisteredAt:       time.Now().UTC(),
		}
		data, _ := json.Marshal(entry)
		h.rdb.Set(ctx, sessionPrefix+req.SandboxID, data, 0) //nolint:errcheck
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "amount_funded": deficit.String()})
}

type deleteSessionReq struct {
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}

// HandleDelete handles DELETE /api/session/:id.
func (h *SessionHandler) HandleDelete(c *gin.Context) {
	sandboxID := c.Param("id")

	var req deleteSessionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	ctx := c.Request.Context()

	// 1. Load session to get provider address.
	data, err := h.rdb.Get(ctx, sessionPrefix+sandboxID).Bytes()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	var entry SessionEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid session data"})
		return
	}

	// 2. Look up teeSignerAddress for this provider.
	rec, ok := h.providers.Get(entry.Provider)
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "provider not registered"})
		return
	}

	// 3. Verify signature: EIP-191( keccak256("deregister" | sandbox_id | timestamp) ).
	msgHash := deregisterMsgHash(sandboxID, req.Timestamp)
	sig, err := decodeSig(req.Signature)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid signature encoding"})
		return
	}
	signer, err := auth.Recover(msgHash, sig)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
		return
	}
	if rec.AppId == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "provider has no app bound"})
		return
	}
	isNode, err := h.chain.IsActiveNode(ctx, rec.AppId, signer)
	if err != nil || !isNode {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "signature does not match an active TEE node"})
		return
	}

	// 4. Timestamp freshness (±300 s).
	diff := time.Now().Unix() - req.Timestamp
	if diff < 0 {
		diff = -diff
	}
	if diff > 300 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "timestamp expired"})
		return
	}

	// 5. Delete session.
	h.rdb.Del(ctx, sessionPrefix+sandboxID) //nolint:errcheck
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ── Message hash helpers ──────────────────────────────────────────────────────

func sessionMsgHash(req postSessionReq) []byte {
	return crypto.Keccak256(
		[]byte(req.SandboxID),
		common.HexToAddress(req.ProviderAddr).Bytes(),
		common.HexToAddress(req.UserAddr).Bytes(),
		common.LeftPadBytes(big.NewInt(req.CPU).Bytes(), 8),
		common.LeftPadBytes(big.NewInt(req.MemGB).Bytes(), 8),
		common.LeftPadBytes(big.NewInt(req.StartTime).Bytes(), 8),
		common.LeftPadBytes(big.NewInt(req.VoucherIntervalSec).Bytes(), 8),
	)
}

func deregisterMsgHash(sandboxID string, ts int64) []byte {
	return crypto.Keccak256(
		[]byte("deregister"),
		[]byte(sandboxID),
		common.LeftPadBytes(big.NewInt(ts).Bytes(), 8),
	)
}

func decodeSig(hexSig string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(hexSig, "0x"))
}
