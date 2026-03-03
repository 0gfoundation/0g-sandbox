package billing

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox-billing/internal/events"
	"github.com/0gfoundation/0g-sandbox-billing/internal/voucher"
)

// EventHandler handles billing lifecycle events from the proxy layer.
type EventHandler struct {
	rdb                *redis.Client
	providerAddress    string
	computePricePerSec *big.Int
	createFee          *big.Int
	signer             VoucherSigner
	log                *zap.Logger
}

// VoucherSigner signs and enqueues a voucher into Redis.
type VoucherSigner interface {
	SignAndEnqueue(ctx context.Context, v *voucher.SandboxVoucher) error
	IncrNonce(ctx context.Context, owner, provider string) (*big.Int, error)
}

func NewEventHandler(
	rdb *redis.Client,
	providerAddress string,
	computePricePerSec *big.Int,
	createFee *big.Int,
	signer VoucherSigner,
	log *zap.Logger,
) *EventHandler {
	return &EventHandler{
		rdb:                rdb,
		providerAddress:    providerAddress,
		computePricePerSec: computePricePerSec,
		createFee:          createFee,
		signer:             signer,
		log:                log,
	}
}

// OnCreate handles POST /sandbox success: generate createFee voucher and start
// compute session immediately (Daytona auto-starts sandboxes on create).
func (h *EventHandler) OnCreate(ctx context.Context, sandboxID, ownerAddr string) {
	nonce, err := h.signer.IncrNonce(ctx, ownerAddr, h.providerAddress)
	if err != nil {
		h.log.Error("OnCreate: incr nonce", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	now := time.Now().Unix()
	v := &voucher.SandboxVoucher{
		SandboxID: sandboxID,
		User:      common.HexToAddress(ownerAddr),
		Provider:  common.HexToAddress(h.providerAddress),
		TotalFee:  new(big.Int).Set(h.createFee),
		UsageHash: voucher.BuildUsageHash(sandboxID, now, now, 0),
		Nonce:     nonce,
	}
	if err := h.signer.SignAndEnqueue(ctx, v); err != nil {
		h.log.Error("OnCreate: sign/enqueue", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	s := Session{
		SandboxID:     sandboxID,
		Owner:         ownerAddr,
		Provider:      h.providerAddress,
		StartTime:     now,
		LastVoucherAt: now,
	}
	if err := CreateSession(ctx, h.rdb, s); err != nil {
		h.log.Error("OnCreate: create session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
	_ = events.Push(ctx, h.rdb, events.Event{
		Type:      events.TypeCreated,
		Message:   fmt.Sprintf("Sandbox %s created, create-fee %s neuron", sandboxID, h.createFee.String()),
		SandboxID: sandboxID,
		User:      ownerAddr,
		Amount:    h.createFee.String(),
	})
}

// OnStart handles POST /sandbox/:id/start success: create billing session if
// none exists (idempotent — OnCreate already opens a session on initial start).
func (h *EventHandler) OnStart(ctx context.Context, sandboxID, ownerAddr string) {
	existing, err := GetSession(ctx, h.rdb, sandboxID)
	if err != nil {
		h.log.Error("OnStart: get session", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	if existing != nil {
		return // session already open (created by OnCreate or a previous start)
	}
	now := time.Now().Unix()
	s := Session{
		SandboxID:     sandboxID,
		Owner:         ownerAddr,
		Provider:      h.providerAddress,
		StartTime:     now,
		LastVoucherAt: now,
	}
	if err := CreateSession(ctx, h.rdb, s); err != nil {
		h.log.Error("OnStart: create session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
}

// OnStop handles POST /sandbox/:id/stop success: generate final voucher + delete session.
func (h *EventHandler) OnStop(ctx context.Context, sandboxID string) {
	h.generateFinalVoucher(ctx, sandboxID)
	if err := DeleteSession(ctx, h.rdb, sandboxID); err != nil {
		h.log.Warn("OnStop: delete session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
}

// OnDelete handles DELETE /sandbox/:id success.
func (h *EventHandler) OnDelete(ctx context.Context, sandboxID string) {
	h.generateFinalVoucher(ctx, sandboxID)
	if err := DeleteSession(ctx, h.rdb, sandboxID); err != nil {
		h.log.Warn("OnDelete: delete session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
}

// OnArchive handles POST /sandbox/:id/archive success.
func (h *EventHandler) OnArchive(ctx context.Context, sandboxID string) {
	h.OnDelete(ctx, sandboxID)
}

func (h *EventHandler) generateFinalVoucher(ctx context.Context, sandboxID string) {
	sess, err := GetSession(ctx, h.rdb, sandboxID)
	if err != nil {
		h.log.Error("generateFinalVoucher: get session", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	if sess == nil {
		return // no active session, nothing to bill
	}

	now := time.Now().Unix()
	periodStart := sess.LastVoucherAt
	periodEnd := now
	elapsedSec := periodEnd - periodStart
	if elapsedSec <= 0 {
		return
	}

	totalFee := new(big.Int).Mul(big.NewInt(elapsedSec), h.computePricePerSec)
	nonce, err := h.signer.IncrNonce(ctx, sess.Owner, h.providerAddress)
	if err != nil {
		h.log.Error("generateFinalVoucher: incr nonce", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}

	v := &voucher.SandboxVoucher{
		SandboxID: sandboxID,
		User:      common.HexToAddress(sess.Owner),
		Provider:  common.HexToAddress(h.providerAddress),
		TotalFee:  totalFee,
		Nonce:     nonce,
		UsageHash: voucher.BuildUsageHash(sandboxID, periodStart, periodEnd, elapsedSec),
	}
	if err := h.signer.SignAndEnqueue(ctx, v); err != nil {
		h.log.Error("generateFinalVoucher: sign/enqueue", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	_ = events.Push(ctx, h.rdb, events.Event{
		Type:      events.TypeStopped,
		Message:   fmt.Sprintf("Final voucher signed for sandbox %s, %s neuron", sandboxID, totalFee.String()),
		SandboxID: sandboxID,
		User:      sess.Owner,
		Amount:    totalFee.String(),
	})
}

