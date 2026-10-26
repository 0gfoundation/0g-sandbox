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
//
// IsProviderActive returns true when the provider key may settle by the
// contract's rule: a node-signer provider is an active node of its service's
// app in TappRegistry; a KMS provider's service is bound to the app's current
// instance.
type SignerClient interface {
	SettlerAddress() common.Address
	GetServiceAppId(ctx context.Context, provider common.Address) (string, error)
	IsProviderActive(ctx context.Context) (bool, error)
	KmsProvider() bool
}

// RunSignerMismatchMonitor checks whether the provider key may sign vouchers
// for the provider's app. If not — typically because a node-signer provider's
// current TEE address was never added as a node (or was removed), or a KMS
// provider's app was re-registered — every voucher silently fails
// INVALID_SIGNATURE and no on-chain event is emitted, so this alert is often
// the only signal until backlog explodes.
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
	appId, err := client.GetServiceAppId(ctx, provider)
	if err != nil {
		log.Warn("appId lookup failed", zap.Error(err))
		return
	}
	if appId == "" {
		// Provider hasn't bound a service yet — that's a setup state, not a
		// mismatch. Silent.
		return
	}
	active, err := client.IsProviderActive(ctx)
	if err != nil {
		log.Warn("provider readiness lookup failed", zap.Error(err))
		return
	}
	if active {
		return
	}
	settler := client.SettlerAddress()
	msg := "Local TEE key is not an active node in TappRegistry for the configured app — every voucher will fail INVALID_SIGNATURE"
	fix := "register the TEE address as a tapp node, then call sandbox.addOrUpdateService with appId " + appId
	if client.KmsProvider() {
		msg = "KMS provider is no longer bound to its app's current TappRegistry instance — every voucher will fail INVALID_SIGNATURE"
		fix = "the appId " + appId + " was re-registered since the provider was bound (or TappRegistry was redeployed: the contract owner runs repinKmsProvider)"
	}
	alerter.Notify(ctx, alert.KindSettlerSignerMismatch, alert.SeverityCritical, msg,
		map[string]any{
			"settler_addr": settler.Hex(),
			"provider":     provider.Hex(),
			"app_id":       appId,
			"fix":          fix,
		},
	)
}
