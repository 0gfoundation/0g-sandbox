// Package observability hosts background monitors that surface operator
// alerts (settler wallet balance, queue backlog) so silent failures
// can't accumulate the way 348k unsigned vouchers did in May 2026.
package observability

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
)

// BalanceClient is the slice of *chain.Client needed by RunBalanceMonitor.
// Defined here as an interface so the monitor is unit-testable without an
// RPC connection.
type BalanceClient interface {
	SettlerAddress() common.Address
	BalanceAt(ctx context.Context, addr common.Address) (*big.Int, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
}

// Assumed gas per SettleFeesWithTEE submission. Used purely to translate a
// configured "factor" (how many tx the wallet should cover) into a wei
// threshold. Conservative — real cost depends on batch size and contract
// version, but we want the alert to fire well before the wallet actually dries.
const settleTxGas = uint64(300_000)

// RunBalanceMonitor periodically samples the settler wallet balance and
// fires alerts when it drops below derived thresholds:
//
//   - critical: balance < 1 settle tx worth → KindSettlerNoBalance
//   - warning:  balance < factor settle txs → KindSettlerLowBalance
//
// Where factor is the configured SettlerLowBalanceFactor (default 100).
// Returns when ctx is cancelled.
func RunBalanceMonitor(ctx context.Context, client BalanceClient, alerter alert.Alerter, lowBalanceFactor int64, log *zap.Logger) {
	addr := client.SettlerAddress()
	log = log.With(zap.String("monitor", "balance"), zap.String("addr", addr.Hex()))
	log.Info("balance monitor started", zap.Int64("low_balance_factor", lowBalanceFactor))

	// Initial sample without waiting a tick — surface a problem at startup.
	check(ctx, client, addr, alerter, lowBalanceFactor, log)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("balance monitor stopped")
			return
		case <-ticker.C:
			check(ctx, client, addr, alerter, lowBalanceFactor, log)
		}
	}
}

func check(ctx context.Context, client BalanceClient, addr common.Address, alerter alert.Alerter, lowBalanceFactor int64, log *zap.Logger) {
	bal, err := client.BalanceAt(ctx, addr)
	if err != nil {
		log.Warn("balance lookup failed", zap.Error(err))
		return
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		log.Warn("gas price lookup failed", zap.Error(err))
		return
	}

	// Cost of one settle tx in wei: gasPrice * settleTxGas.
	oneTx := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(settleTxGas))
	warningThreshold := new(big.Int).Mul(oneTx, big.NewInt(lowBalanceFactor))

	details := map[string]any{
		"balance_wei":   bal.String(),
		"gas_price_wei": gasPrice.String(),
		"one_tx_cost":   oneTx.String(),
	}

	switch {
	case bal.Cmp(oneTx) < 0:
		details["threshold"] = oneTx.String()
		alerter.Notify(ctx, alert.KindSettlerNoBalance, alert.SeverityCritical,
			"Settler wallet balance below 1 settle tx — settlement will fail",
			details,
		)
	case bal.Cmp(warningThreshold) < 0:
		details["threshold"] = warningThreshold.String()
		details["factor"] = lowBalanceFactor
		alerter.Notify(ctx, alert.KindSettlerLowBalance, alert.SeverityWarning,
			"Settler wallet balance running low — top up before depletion",
			details,
		)
	}
}
