// Package admin exposes read-only operator endpoints protected by X-Admin-Key.
package admin

import (
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox-billing/internal/billing"
	"github.com/0gfoundation/0g-sandbox-billing/internal/config"
	"github.com/0gfoundation/0g-sandbox-billing/internal/daytona"
	"github.com/0gfoundation/0g-sandbox-billing/internal/events"
)

// Handler serves /admin/* endpoints.
type Handler struct {
	rdb   *redis.Client
	cfg   *config.Config
	dtona *daytona.Client
	log   *zap.Logger
}

// New creates an admin Handler.
func New(rdb *redis.Client, cfg *config.Config, dtona *daytona.Client, log *zap.Logger) *Handler {
	return &Handler{rdb: rdb, cfg: cfg, dtona: dtona, log: log}
}

// AuthMiddleware rejects requests whose X-Admin-Key header doesn't match adminKey.
func AuthMiddleware(adminKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("X-Admin-Key") != adminKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// Register mounts the admin routes onto r.
func (h *Handler) Register(r *gin.RouterGroup) {
	r.GET("/status", h.status)
	r.GET("/sandboxes", h.sandboxes)
	r.GET("/events", h.eventList)
	r.POST("/archive-all", h.archiveAll)
}

// ── /admin/status ──────────────────────────────────────────────────────────

type statusResp struct {
	ActiveSandboxes    int    `json:"active_sandboxes"`
	NextFlushInSec     int64  `json:"next_flush_in_sec"`
	VoucherIntervalSec int64  `json:"voucher_interval_sec"`
	ComputePricePerSec string `json:"compute_price_per_sec"`
	CreateFee          string `json:"create_fee"`
	ContractAddress    string `json:"contract_address"`
	ProviderAddress    string `json:"provider_address"`
	ChainID            int64  `json:"chain_id"`
}

func (h *Handler) status(c *gin.Context) {
	sessions, err := billing.ScanAllSessions(c.Request.Context(), h.rdb)
	if err != nil {
		h.log.Error("admin/status: scan sessions", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Next flush ETA: interval minus elapsed since earliest LastVoucherAt.
	var nextFlush int64
	var oldest int64
	for _, s := range sessions {
		if oldest == 0 || s.LastVoucherAt < oldest {
			oldest = s.LastVoucherAt
		}
	}
	if oldest > 0 {
		nextAt := oldest + h.cfg.Billing.VoucherIntervalSec
		if eta := nextAt - time.Now().Unix(); eta > 0 {
			nextFlush = eta
		}
	}

	c.JSON(http.StatusOK, statusResp{
		ActiveSandboxes:    len(sessions),
		NextFlushInSec:     nextFlush,
		VoucherIntervalSec: h.cfg.Billing.VoucherIntervalSec,
		ComputePricePerSec: h.cfg.Billing.ComputePricePerSec,
		CreateFee:          h.cfg.Billing.CreateFee,
		ContractAddress:    h.cfg.Chain.ContractAddress,
		ProviderAddress:    h.cfg.Chain.ProviderAddress,
		ChainID:            h.cfg.Chain.ChainID,
	})
}

// ── /admin/sandboxes ───────────────────────────────────────────────────────

type sandboxInfo struct {
	SandboxID     string `json:"sandbox_id"`
	Owner         string `json:"owner"`
	Provider      string `json:"provider"`
	StartTime     int64  `json:"start_time"`
	LastVoucherAt int64  `json:"last_voucher_at"`
	AccruedNeuron string `json:"accrued_neuron"`
}

func (h *Handler) sandboxes(c *gin.Context) {
	sessions, err := billing.ScanAllSessions(c.Request.Context(), h.rdb)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	pricePerSec, _ := new(big.Int).SetString(h.cfg.Billing.ComputePricePerSec, 10)
	now := time.Now().Unix()

	result := make([]sandboxInfo, 0, len(sessions))
	for _, s := range sessions {
		var accrued string
		if pricePerSec != nil && s.StartTime > 0 {
			elapsed := now - s.StartTime
			if elapsed < 0 {
				elapsed = 0
			}
			accrued = new(big.Int).Mul(pricePerSec, big.NewInt(elapsed)).String()
		}
		result = append(result, sandboxInfo{
			SandboxID:     s.SandboxID,
			Owner:         s.Owner,
			Provider:      s.Provider,
			StartTime:     s.StartTime,
			LastVoucherAt: s.LastVoucherAt,
			AccruedNeuron: accrued,
		})
	}

	c.JSON(http.StatusOK, result)
}

// ── /admin/events ──────────────────────────────────────────────────────────

func (h *Handler) eventList(c *gin.Context) {
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

// ── /admin/archive-all ─────────────────────────────────────────────────────

// archiveAll archives every started/starting sandbox. Call this before
// redeploying so all running containers are backed up to object storage and
// can be restarted after the stack comes back up.
func (h *Handler) archiveAll(c *gin.Context) {
	sandboxes, err := h.dtona.ListSandboxes(c.Request.Context())
	if err != nil {
		h.log.Error("admin/archive-all: list sandboxes", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to list sandboxes"})
		return
	}

	var archived, skipped, failed []string
	for _, s := range sandboxes {
		state := strings.ToLower(s.State)
		if state != "started" && state != "starting" {
			skipped = append(skipped, s.ID)
			continue
		}
		// Step 1: stop (Daytona requires stopped state before archive)
		if err := h.dtona.StopSandbox(c.Request.Context(), s.ID); err != nil {
			h.log.Warn("admin/archive-all: stop failed",
				zap.String("id", s.ID), zap.Error(err))
			failed = append(failed, s.ID)
			continue
		}
		// Step 2: wait for stopped state (stop is async in Daytona)
		if err := h.dtona.WaitStopped(c.Request.Context(), s.ID); err != nil {
			h.log.Warn("admin/archive-all: wait stopped failed",
				zap.String("id", s.ID), zap.Error(err))
			failed = append(failed, s.ID)
			continue
		}
		// Step 3: archive (backup to object storage)
		if err := h.dtona.ArchiveSandbox(c.Request.Context(), s.ID); err != nil {
			h.log.Warn("admin/archive-all: archive failed",
				zap.String("id", s.ID), zap.Error(err))
			failed = append(failed, s.ID)
		} else {
			h.log.Info("admin/archive-all: archived", zap.String("id", s.ID))
			archived = append(archived, s.ID)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"archived": archived,
		"skipped":  skipped,
		"failed":   failed,
	})
}
