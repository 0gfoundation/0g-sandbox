package broker

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

const sessionPrefix = "broker:session:"

// SessionEntry is stored in Redis under broker:session:<sandbox_id>.
type SessionEntry struct {
	SandboxID          string    `json:"sandbox_id"`
	User               string    `json:"user"`
	Provider           string    `json:"provider"`
	CPU                int64     `json:"cpu"`
	MemGB              int64     `json:"mem_gb"`
	PricePerSec        string    `json:"price_per_sec"`
	VoucherIntervalSec int64     `json:"voucher_interval_sec"`
	RegisteredAt       time.Time `json:"registered_at"`
}

// balanceChecker is the minimal chain interface the monitor needs.
type balanceChecker interface {
	GetBalanceBatch(ctx context.Context, users []common.Address, provider common.Address) ([]*big.Int, error)
	// GetServicePricing returns (nil, nil, nil, nil) when the provider has no
	// registered service. Used to stop topping up buckets of providers whose
	// service was removed — a rotated-out TEE signer can never settle again,
	// so deposits to it are money the user must claw back via refund.
	GetServicePricing(ctx context.Context, provider common.Address) (pricePerCPUPerSec, pricePerMemGBPerSec, createFee *big.Int, err error)
}

// Monitor polls on-chain balances for all registered sessions and triggers
// top-ups via the PaymentLayer when a balance falls below the threshold.
type Monitor struct {
	rdb                *redis.Client
	chain              balanceChecker
	payment            PaymentLayer
	log                *zap.Logger
	monitorInterval    time.Duration
	topupIntervals     int64
	thresholdIntervals int64
}

// NewMonitor creates a Monitor. Call Run to start the polling loop.
func NewMonitor(
	rdb *redis.Client,
	chain balanceChecker,
	payment PaymentLayer,
	log *zap.Logger,
	monitorIntervalSec, topupIntervals, thresholdIntervals int64,
) *Monitor {
	return &Monitor{
		rdb:                rdb,
		chain:              chain,
		payment:            payment,
		log:                log,
		monitorInterval:    time.Duration(monitorIntervalSec) * time.Second,
		topupIntervals:     topupIntervals,
		thresholdIntervals: thresholdIntervals,
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	m.log.Info("broker monitor started",
		zap.Duration("interval", m.monitorInterval))
	m.check(ctx)

	t := time.NewTicker(m.monitorInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.log.Info("broker monitor stopped")
			return
		case <-t.C:
			m.check(ctx)
		}
	}
}

// GetSessions returns all active session entries (for GET /api/monitor).
func (m *Monitor) GetSessions(ctx context.Context) []SessionEntry {
	sessions, _ := m.loadSessions(ctx)
	return sessions
}

// check scans all sessions, groups by provider, fetches balances in batch,
// and triggers top-ups as needed.
func (m *Monitor) check(ctx context.Context) {
	sessions, err := m.loadSessions(ctx)
	if err != nil {
		m.log.Warn("monitor: load sessions failed", zap.Error(err))
		return
	}
	m.log.Info("monitor: tick",
		zap.Int("sessions", len(sessions)))
	if len(sessions) == 0 {
		return
	}

	// Group sessions by provider.
	type userKey struct{ user, provider string }
	// providerUsers: provider → ordered list of unique users
	providerUsers := make(map[string][]common.Address)
	// userSessions: (user, provider) → aggregated burn rate + voucher interval
	type aggregate struct {
		burnRate           *big.Int
		voucherIntervalSec int64
	}
	userAgg := make(map[userKey]*aggregate)

	for _, s := range sessions {
		provAddr := strings.ToLower(s.Provider)
		userAddr := strings.ToLower(s.User)

		// Track unique users per provider.
		providerUsers[provAddr] = appendUnique(providerUsers[provAddr], common.HexToAddress(s.User))

		// Aggregate burn rate per (user, provider).
		k := userKey{userAddr, provAddr}
		pricePerSec := new(big.Int)
		pricePerSec.SetString(s.PricePerSec, 10)
		if agg, ok := userAgg[k]; ok {
			agg.burnRate.Add(agg.burnRate, pricePerSec)
			if s.VoucherIntervalSec > agg.voucherIntervalSec {
				agg.voucherIntervalSec = s.VoucherIntervalSec
			}
		} else {
			userAgg[k] = &aggregate{
				burnRate:           new(big.Int).Set(pricePerSec),
				voucherIntervalSec: s.VoucherIntervalSec,
			}
		}
	}

	// For each provider, batch-fetch balances.
	for provAddrLower, users := range providerUsers {
		provider := common.HexToAddress(provAddrLower)

		// Provider = TEE signer: after a machine rotation the owner removes
		// the old signer's service, and nothing can ever settle against it
		// again. Topping up that bucket would strand funds behind the refund
		// flow — skip it and let the stale sessions age out.
		if cpuPrice, _, _, err := m.chain.GetServicePricing(ctx, provider); err != nil {
			m.log.Warn("monitor: service lookup failed; skipping provider this tick",
				zap.String("provider", provAddrLower), zap.Error(err))
			continue
		} else if cpuPrice == nil {
			m.log.Warn("monitor: provider service no longer registered (rotated out?) — not topping up",
				zap.String("provider", provAddrLower),
				zap.Int("sessions_affected", len(users)))
			continue
		}

		balances, err := m.chain.GetBalanceBatch(ctx, users, provider)
		if err != nil {
			m.log.Warn("monitor: GetBalanceBatch failed",
				zap.String("provider", provAddrLower), zap.Error(err))
			continue
		}

		for i, user := range users {
			if i >= len(balances) {
				break
			}
			balance := balances[i]
			k := userKey{strings.ToLower(user.Hex()), provAddrLower}
			agg, ok := userAgg[k]
			if !ok {
				continue
			}

			interval := agg.voucherIntervalSec
			threshold := new(big.Int).Mul(agg.burnRate, big.NewInt(interval*m.thresholdIntervals))
			if balance.Cmp(threshold) >= 0 {
				continue
			}

			topup := new(big.Int).Mul(agg.burnRate, big.NewInt(interval*m.topupIntervals))
			deficit := new(big.Int).Sub(topup, balance)
			if deficit.Sign() <= 0 {
				deficit = topup
			}

			if IsInflightOrBackoff(ctx, m.rdb, user, provider) {
				m.log.Info("monitor: top-up skipped (inflight or backoff)",
					zap.String("user", user.Hex()),
					zap.String("provider", provider.Hex()))
				continue
			}

			m.log.Info("monitor: balance below threshold, requesting top-up",
				zap.String("user", user.Hex()),
				zap.String("provider", provider.Hex()),
				zap.String("balance", balance.String()),
				zap.String("threshold", threshold.String()),
				zap.String("topup", deficit.String()))

			if err := m.payment.RequestDeposit(ctx, user, provider, deficit); err != nil {
				m.log.Warn("monitor: RequestDeposit failed",
					zap.String("user", user.Hex()),
					zap.String("provider", provider.Hex()),
					zap.Error(err))
			}
		}
	}
}

func (m *Monitor) loadSessions(ctx context.Context) ([]SessionEntry, error) {
	keys, err := m.rdb.Keys(ctx, sessionPrefix+"*").Result()
	if err != nil {
		return nil, err
	}
	var sessions []SessionEntry
	for _, key := range keys {
		data, err := m.rdb.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var s SessionEntry
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// appendUnique appends addr only if not already in the slice.
func appendUnique(slice []common.Address, addr common.Address) []common.Address {
	for _, a := range slice {
		if a == addr {
			return slice
		}
	}
	return append(slice, addr)
}
