package chain

import (
	"context"
	"fmt"
	"math/big"
	"time"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const sealIssuerTimeout = 5 * time.Second

// GetSealedAppInstance authenticates this KMS provider for a newly issued
// sealed attestation. Every contract read uses one block; no node-key or
// pre-KMS-contract fallback is permitted. registeredAt retains the registry's
// existing timestamp semantics rather than introducing a new generation ID.
func (c *Client) GetSealedAppInstance(ctx context.Context, appID string) (uint64, error) {
	if !c.kmsProvider {
		return 0, fmt.Errorf("sealed attestations require a KMS app signer")
	}
	if c.chainID == nil || c.chainID.Sign() <= 0 || !c.chainID.IsUint64() || c.tappAddr == (common.Address{}) {
		return 0, fmt.Errorf("invalid sealed issuer chain scope")
	}
	if len(appID) == 0 || len(appID) > 256 || !utf8.ValidString(appID) {
		return 0, fmt.Errorf("sealed issuer app_id must be 1..256 UTF-8 bytes")
	}
	ctx, cancel := context.WithTimeout(ctx, sealIssuerTimeout)
	defer cancel()
	rpcChainID, err := c.eth.ChainID(ctx)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer chain ID: %w", err)
	}
	if rpcChainID.Cmp(c.chainID) != 0 {
		return 0, fmt.Errorf("sealed issuer RPC chain ID differs from configured chain")
	}
	header, err := c.eth.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer block: %w", err)
	}
	opts := &bind.CallOpts{Context: ctx, BlockNumber: new(big.Int).Set(header.Number)}
	registry, err := c.contract.TappRegistry(opts)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer registry: %w", err)
	}
	if registry != c.tappAddr {
		return 0, fmt.Errorf("sealed issuer contract registry differs from configured registry")
	}
	info, err := c.tapp.GetAppInfo(opts, appID)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer app instance: %w", err)
	}
	if info.Owner == (common.Address{}) || info.RegisteredAt == nil || info.RegisteredAt.Sign() <= 0 || !info.RegisteredAt.IsUint64() {
		return 0, fmt.Errorf("sealed issuer app has no valid current registration")
	}
	service, err := c.contract.Services(opts, c.providerAddr)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer service: %w", err)
	}
	if service.AppId != appID {
		return 0, fmt.Errorf("sealed issuer service app_id differs from configured app")
	}
	binding, err := c.contract.KmsBindings(opts, c.providerAddr)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer KMS binding: %w", err)
	}
	if binding.Epoch == nil || binding.Epoch.Cmp(info.RegisteredAt) != 0 || binding.AppIdHash != crypto.Keccak256Hash([]byte(appID)) {
		return 0, fmt.Errorf("sealed issuer KMS binding does not match current app instance")
	}
	active, err := c.contract.IsActiveProvider(opts, c.providerAddr)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer active provider: %w", err)
	}
	if !active {
		return 0, fmt.Errorf("sealed issuer provider is not active")
	}
	// Detect a reorg during the number-pinned reads. Do not silently retry with
	// independent latest calls, which could combine different app instances.
	current, err := c.eth.HeaderByNumber(ctx, header.Number)
	if err != nil {
		return 0, fmt.Errorf("sealed issuer block recheck: %w", err)
	}
	if current.Hash() != header.Hash() {
		return 0, fmt.Errorf("sealed issuer block changed during lookup")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return info.RegisteredAt.Uint64(), nil
}
