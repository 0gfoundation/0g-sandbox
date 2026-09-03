package billing

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/events"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// EventHandler handles billing lifecycle events from the proxy layer.
type EventHandler struct {
	rdb                 *redis.Client
	providerAddress     string
	computePricePerSec  *big.Int // flat rate fallback
	pricePerCPUPerSec   *big.Int // per CPU core/sec (0 = use flat rate)
	pricePerMemGBPerSec *big.Int // per GB memory/sec (0 = use flat rate)
	createFee           *big.Int
	voucherIntervalSec  int64
	signer              VoucherSigner
	log                 *zap.Logger
}

// VoucherSigner enqueues an unsigned voucher into Redis.
// Nonce assignment and signing are deferred to the settler, which is
// single-threaded and guarantees strict nonce ordering.
type VoucherSigner interface {
	Enqueue(ctx context.Context, v *voucher.SandboxVoucher) error
}

func NewEventHandler(
	rdb *redis.Client,
	providerAddress string,
	computePricePerSec *big.Int,
	createFee *big.Int,
	pricePerCPUPerSec *big.Int,
	pricePerMemGBPerSec *big.Int,
	voucherIntervalSec int64,
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
		voucherIntervalSec:  voucherIntervalSec,
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

// emitPeriodVoucher signs and enqueues a pre-charge voucher covering one full
// voucherIntervalSec window starting at periodStart. Returns the next
// NextVoucherAt value (periodStart + voucherIntervalSec).
// emitPeriodVoucher charges `intervals` billing periods starting at
// periodStart in ONE voucher (fee = price × interval × intervals) and returns
// the new NextVoucherAt. intervals > 1 is the backlog catch-up path: after
// downtime the generator owes many periods, and one voucher per period per
// tick could never close the gap (see runGeneration).
func (h *EventHandler) emitPeriodVoucher(ctx context.Context, sandboxID, ownerAddr string, price *big.Int, periodStart, intervals int64) (int64, error) {
	if intervals < 1 {
		intervals = 1
	}
	nextVoucherAt := periodStart + h.voucherIntervalSec*intervals
	fee := new(big.Int).Mul(price, big.NewInt(h.voucherIntervalSec*intervals))
	if fee.Sign() == 0 {
		return nextVoucherAt, nil
	}
	v := &voucher.SandboxVoucher{
		SandboxID: sandboxID,
		User:      common.HexToAddress(ownerAddr),
		Provider:  common.HexToAddress(h.providerAddress),
		TotalFee:  fee,
		UsageHash: voucher.BuildUsageHash(sandboxID, periodStart, nextVoucherAt, h.voucherIntervalSec),
	}
	if err := h.signer.Enqueue(ctx, v); err != nil {
		return 0, err
	}
	return nextVoucherAt, nil
}

// OnCreate handles POST /sandbox success: emit createFee voucher, pre-charge
// the first compute period, and open the billing session.
// cpu and memGB are the sandbox's allocated resources used to compute billing rate.
func (h *EventHandler) OnCreate(ctx context.Context, sandboxID, ownerAddr string, cpu, memGB int) {
	now := time.Now().Unix()
	v := &voucher.SandboxVoucher{
		SandboxID: sandboxID,
		User:      common.HexToAddress(ownerAddr),
		Provider:  common.HexToAddress(h.providerAddress),
		TotalFee:  new(big.Int).Set(h.createFee),
		UsageHash: voucher.BuildUsageHash(sandboxID, now, now, 0),
	}
	if err := h.signer.Enqueue(ctx, v); err != nil {
		h.log.Error("OnCreate: enqueue create-fee", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}

	price := h.computePrice(cpu, memGB)
	nextVoucherAt, err := h.emitPeriodVoucher(ctx, sandboxID, ownerAddr, price, now, 1)
	if err != nil {
		h.log.Error("OnCreate: emit first period", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}

	s := Session{
		SandboxID:     sandboxID,
		Owner:         ownerAddr,
		Provider:      h.providerAddress,
		NextVoucherAt: nextVoucherAt,
		PricePerSec:   price.String(),
	}
	if err := CreateSession(ctx, h.rdb, s); err != nil {
		h.log.Error("OnCreate: create session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
	periodFee := new(big.Int).Mul(price, big.NewInt(h.voucherIntervalSec))
	totalUpfront := new(big.Int).Add(h.createFee, periodFee)
	Release(ctx, h.rdb, ownerAddr, h.providerAddress, totalUpfront)
	_ = events.Push(ctx, h.rdb, events.Event{
		Type:      events.TypeCreated,
		Message:   fmt.Sprintf("Sandbox %s created, create-fee %s + first-period %s neuron, rate %s neuron/sec", sandboxID, h.createFee.String(), periodFee.String(), price.String()),
		SandboxID: sandboxID,
		User:      ownerAddr,
		Amount:    totalUpfront.String(),
	})
}

// OnStart handles POST /sandbox/:id/start success: create billing session if
// none exists (idempotent — OnCreate already opens a session on initial start).
// Pre-charges the first compute period, same as OnCreate.
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
	nextVoucherAt, err := h.emitPeriodVoucher(ctx, sandboxID, ownerAddr, price, now, 1)
	if err != nil {
		h.log.Error("OnStart: emit first period", zap.String("sandbox", sandboxID), zap.Error(err))
		return
	}
	s := Session{
		SandboxID:     sandboxID,
		Owner:         ownerAddr,
		Provider:      h.providerAddress,
		NextVoucherAt: nextVoucherAt,
		PricePerSec:   price.String(),
	}
	if err := CreateSession(ctx, h.rdb, s); err != nil {
		h.log.Error("OnStart: create session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
	periodFee := new(big.Int).Mul(price, big.NewInt(h.voucherIntervalSec))
	Release(ctx, h.rdb, ownerAddr, h.providerAddress, periodFee)
}

// OnStop handles POST /sandbox/:id/stop success: delete billing session.
// No final voucher is emitted — the current period was already pre-charged.
func (h *EventHandler) OnStop(ctx context.Context, sandboxID string) {
	if err := DeleteSession(ctx, h.rdb, sandboxID); err != nil {
		h.log.Warn("OnStop: delete session", zap.String("sandbox", sandboxID), zap.Error(err))
	}
}

// OnDelete handles DELETE /sandbox/:id success.
func (h *EventHandler) OnDelete(ctx context.Context, sandboxID string) {
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
