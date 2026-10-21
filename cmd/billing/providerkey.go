package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/tee"
)

// providerKeyRefreshInterval is how often a KMS provider asks for its key
// again. A tapp-server process lists in the attestation evidence only the
// keys it has itself handed out, so after a tapp-server restart (this
// container still running) the provider key is missing from the evidence
// until the app asks again; this bounds that gap.
const providerKeyRefreshInterval = 10 * time.Minute

// keepProviderKeyListed re-takes the KMS provider key every `every`, so the
// evidence keeps listing it, and records whether it is listed. The KMS
// derives one key per (app_id, material): a different address means the
// node now talks to another KMS cluster or app_id — logged as an error, and
// the running provider is left as it is.
func keepProviderKeyListed(ctx context.Context, want common.Address, fetch func(context.Context) (*tee.ProviderKey, error),
	every time.Duration, inEvidence *atomic.Bool, log *zap.Logger) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pk, err := fetch(ctx)
		if err != nil {
			log.Warn("provider key refresh failed; the evidence may stop listing it until the next attempt", zap.Error(err))
			continue
		}
		if pk.Address != want {
			log.Error("the KMS now derives a different provider key — another KMS cluster or app_id? Keeping the running provider",
				zap.String("provider", want.Hex()), zap.String("kms_key", pk.Address.Hex()))
			continue
		}
		inEvidence.Store(pk.InEvidence)
	}
}
