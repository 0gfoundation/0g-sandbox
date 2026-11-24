package settler

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
)

// CheckPendingSettlement matches the operator's expected transaction against
// the provider's stored in-flight record without changing the billing state.
func CheckPendingSettlement(ctx context.Context, rdb *redis.Client, provider common.Address, hash common.Hash, nonce uint64) error {
	p, err := loadPendingTx(ctx, rdb, provider)
	if err != nil {
		return fmt.Errorf("read pending settlement: %w", err)
	}
	if p == nil || p.TxHash == (common.Hash{}) || p.TxHash != hash || p.AccountNonce != nonce {
		return fmt.Errorf("no pending settlement matching provider %s, hash %s and nonce %d", provider, hash, nonce)
	}
	return nil
}
