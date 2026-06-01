package observability

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
)

// SignerClient is the slice of chain.Client needed by RunSignerMismatchMonitor.
// Surfaced as an interface for unit tests.
type SignerClient interface {
	SettlerAddress() common.Address
	GetServiceTEESignerAddress(ctx context.Context, provider common.Address) (common.Address, error)
}

// RunSignerMismatchMonitor compares the settler's local TEE key address
// (which signs every voucher) against the on-chain `services[provider].teeSignerAddress`
// (which the contract expects ecrecover to match). When they diverge — typically
// because KMS rotated the TEE key but provider forgot to re-register on-chain —
// every voucher silently fails with INVALID_SIGNATURE and no on-chain event is
// emitted, so this alert is often the only signal until backlog explodes.
//
// Runs immediately at startup (boot-time check) then every 60s. Chain query
// errors are logged but don't fire alerts — RPC blips would create false
// positives. Only confirmed mismatches alert.
func RunSignerMismatchMonitor(ctx context.Context, client SignerClient, provider common.Address, alerter alert.Alerter, log *zap.Logger) {
	log = log.With(
		zap.String("monitor", "signer_mismatch"),
		zap.String("settler", client.SettlerAddress().Hex()),
		zap.String("provider", provider.Hex()),
	)
	log.Info("signer mismatch monitor started")

	checkSigner(ctx, client, provider, alerter, log)

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("signer mismatch monitor stopped")
			return
		case <-ticker.C:
			checkSigner(ctx, client, provider, alerter, log)
		}
	}
}

func checkSigner(ctx context.Context, client SignerClient, provider common.Address, alerter alert.Alerter, log *zap.Logger) {
	settler := client.SettlerAddress()
	onchain, err := client.GetServiceTEESignerAddress(ctx, provider)
	if err != nil {
		// RPC failure — don't false-alarm
		log.Warn("on-chain signer lookup failed", zap.Error(err))
		return
	}
	if onchain == (common.Address{}) {
		// Provider not registered yet — that's a different problem, but not
		// a mismatch per se. Skip silently; provider registration is a setup step.
		return
	}
	if settler == onchain {
		return // healthy
	}
	alerter.Notify(ctx, alert.KindSettlerSignerMismatch, alert.SeverityCritical,
		"Settler TEE key does not match on-chain teeSignerAddress — every voucher will fail INVALID_SIGNATURE",
		map[string]any{
			"settler_addr":         settler.Hex(),
			"onchain_signer_addr":  onchain.Hex(),
			"provider":             provider.Hex(),
			"fix":                  "run `cmd/provider register --tee-signer " + settler.Hex() + "` (or update via dashboard)",
		},
	)
}
