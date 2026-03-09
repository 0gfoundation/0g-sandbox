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
	rdb                 *redis.Client
	providerAddress     string
	computePricePerSec  *big.Int // flat rate fallback
	pricePerCPUPerSec   *big.Int // per CPU core/sec (0 = use flat rate)
	pricePerMemGBPerSec *big.Int // per GB memory/sec (0 = use flat rate)
	createFee           *big.Int
	signer              VoucherSigner
	log                 *zap.Logger
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
	pricePerCPUPerSec *big.Int,
	pricePerMemGBPerSec *big.Int,
	signer VoucherSigner,
	log *zap.Logger,
) *EventHandler {
	return &EventHandler{
		rdb:                 rdb,
		providerAddress:     providerAddress,
		computePricePerSec:  computePricePerSec,
		pricePerCPUPerSec:   pricePerCPUPerSec,
		pricePerMemGBPerSec: pricePerMemGBPerSec,
		createFee:           createFee,
		signer:              signer,
		log:                 log,
	}
}

// computePrice returns the per-second billing rate for a sandbox with the given
// resources. If per-resource pricing is configured (either unit price > 0),
// uses cpu*pricePerCPU + mem*pricePerMem; otherwise falls back to the flat rate.
func (h *EventHandler) computePrice(cpu, memGB int) *big.Int {
	if h.pricePerCPUPerSec.Sign() > 0 || h.pricePerMemGBPerSec.Sign() > 0 {
		p := new(big.Int)
		p.Add(p, new(big.Int).Mul(big.NewInt(int64(cpu)), h.pricePerCPUPerSec))
		p.Add(p, new(big.Int).Mul(big.NewInt(int64(memGB)), h.pricePerMemGBPerSec))
		return p
	}
	return new(big.Int).Set(h.computePricePerSec)
}

// priceFromSession returns the per-second rate stored in the session, or falls
// back to the flat rate if the session was created before per-resource pricing.
func (h *EventHandler) priceFromSession(sess *Session) *big.Int {
	if sess.PricePerSec != "" {
		if p, ok := new(big.Int).SetString(sess.PricePerSec, 10); ok && p.Sign() > 0 {
			return p
		}
	}
	return h.computePricePerSec
}

// OnCreate handles POST /sandbox success: generate createFee voucher and start
// compute session immediately (Daytona auto-starts sandboxes on create).
// cpu and memGB are the sandbox's allocated resources used to compute billing rate.
func (h *EventHandler) OnCreate(ctx context.Context, sandboxID, ownerAddr string, cpu, memGB int) {
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
	price := h.computePrice(cpu, memGB)
	s := Session{
		SandboxID:     sandboxID,
		Owner:         ownerAddr,
		Provider:      h.providerAddress,
		StartTime:     now,
		LastVoucherAt: now,
		PricePerSec:   price.String(),
	}
	if err := CreateSession(ctx, h.rdb, s); err != nil {
		h.log.Error("OnCreate: create session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
	_ = events.Push(ctx, h.rdb, events.Event{
		Type:      events.TypeCreated,
		Message:   fmt.Sprintf("Sandbox %s created, create-fee %s neuron, rate %s neuron/sec", sandboxID, h.createFee.String(), price.String()),
		SandboxID: sandboxID,
		User:      ownerAddr,
		Amount:    h.createFee.String(),
	})
}

// OnStart handles POST /sandbox/:id/start success: create billing session if
// none exists (idempotent — OnCreate already opens a session on initial start).
// cpu and memGB are the sandbox's allocated resources used to compute billing rate.
func (h *EventHandler) OnStart(ctx context.Context, sandboxID, ownerAddr string, cpu, memGB int) {
	existing, err := GetSession(ctx, h.rdb, sandboxID)
	if err != nil {
		h.log.Error("OnStart: get session", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	if existing != nil {
		return // session already open (created by OnCreate or a previous start)
	}
	price := h.computePrice(cpu, memGB)
	now := time.Now().Unix()
	s := Session{
		SandboxID:     sandboxID,
		Owner:         ownerAddr,
		Provider:      h.providerAddress,
		StartTime:     now,
		LastVoucherAt: now,
		PricePerSec:   price.String(),
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

// EnsureSession is idempotent: if a billing session already exists for this
// sandbox it does nothing. If not (e.g. the create request returned 502 before
// the billing hook could fire), it calls OnCreate to emit the create-fee
// voucher and open the session.
func (h *EventHandler) EnsureSession(ctx context.Context, sandboxID, ownerAddr string) {
	existing, err := GetSession(ctx, h.rdb, sandboxID)
	if err != nil {
		h.log.Error("EnsureSession: get session", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	if existing != nil {
		return // already billed
	}
	h.OnCreate(ctx, sandboxID, ownerAddr, 0, 0) // resources unknown at recovery; uses flat rate
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

	totalFee := new(big.Int).Mul(big.NewInt(elapsedSec), h.priceFromSession(sess))
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

