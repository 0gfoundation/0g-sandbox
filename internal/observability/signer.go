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
// IsLocalTEEActiveNode returns true when the locally-derived TEE address is
// an active node for the provider's configured app in TappRegistry. It
// internally reads sandbox.services[provider].appId and queries
// tapp.getNode(appId, settler).addedAt.
type SignerClient interface {
	SettlerAddress() common.Address
	GetServiceAppId(ctx context.Context, provider common.Address) (string, error)
	IsLocalTEEActiveNode(ctx context.Context) (bool, error)
}

// RunSignerMismatchMonitor checks whether the local TEE key is recognised
// as an active node for the provider's app in TappRegistry. If not — typically
// because the provider hasn't run `cmd/provider register` with the current
// TEE address, or because the node was removed — every voucher silently fails
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
	isNode, err := client.IsLocalTEEActiveNode(ctx)
	if err != nil {
		log.Warn("tapp node lookup failed", zap.Error(err))
		return
	}
	if isNode {
		return
	}
	settler := client.SettlerAddress()
	alerter.Notify(ctx, alert.KindSettlerSignerMismatch, alert.SeverityCritical,
		"Local TEE key is not an active node in TappRegistry for the configured app — every voucher will fail INVALID_SIGNATURE",
		map[string]any{
			"settler_addr": settler.Hex(),
			"provider":     provider.Hex(),
			"app_id":       appId,
			"fix":          "register the TEE address as a tapp node, then call sandbox.addOrUpdateService with appId " + appId,
		},
	)
}
