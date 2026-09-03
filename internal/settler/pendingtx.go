package settler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/alert"
	"github.com/0gfoundation/0g-sandbox/internal/chain"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// PendingTxKeyFmt persists the settler's single in-flight settlement tx:
// voucher payloads (SIGNED — the nonces they carry are the ones the tx uses),
// tx hash, and the tx's account nonce. Written after broadcast, cleared once
// the tx's fate is known. Survives restarts, so a crash between broadcast and
// receipt cannot cause the same usage to be re-signed and settled twice.
const PendingTxKeyFmt = "settler:pending-tx:%s"

// pendingTxPollInterval is how often the fate of an in-flight tx is
// re-checked. Package var so tests can shrink it.
var pendingTxPollInterval = 5 * time.Second

type pendingTx struct {
	TxHash       common.Hash              `json:"tx_hash"`
	AccountNonce uint64                   `json:"account_nonce"`
	Vouchers     []voucher.SandboxVoucher `json:"vouchers"`
	// FirstItem is the raw (unsigned) BLPOP'd queue item, needed by
	// HandleStatuses' pop bookkeeping when the fate resolves to mined.
	FirstItem string `json:"first_item"`
}

// fateResolver is the slice of the chain client the pending-tx machinery uses.
type fateResolver interface {
	ResolveTxFate(ctx context.Context, txHash common.Hash, accountNonce uint64) (chain.TxFate, *types.Receipt, error)
	SettleStatusesFromReceipt(ctx context.Context, receipt *types.Receipt, vouchers []voucher.SandboxVoucher) ([]chain.SettlementStatus, error)
}

func savePendingTx(ctx context.Context, rdb *redis.Client, provider common.Address, p pendingTx) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, fmt.Sprintf(PendingTxKeyFmt, provider.Hex()), string(raw), 0).Err()
}

func loadPendingTx(ctx context.Context, rdb *redis.Client, provider common.Address) (*pendingTx, error) {
	raw, err := rdb.Get(ctx, fmt.Sprintf(PendingTxKeyFmt, provider.Hex())).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p pendingTx
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func clearPendingTx(ctx context.Context, rdb *redis.Client, provider common.Address) {
	rdb.Del(ctx, fmt.Sprintf(PendingTxKeyFmt, provider.Hex())) //nolint:errcheck
}

// resolvePendingTx blocks until the in-flight settlement tx's fate is known,
// then routes the outcome:
//   - mined  → statuses parsed from the receipt, handled exactly like a
//     synchronous settlement (HandleStatuses pops the queue items);
//   - mined-but-reverted → whole tx consumed nothing on-chain; vouchers are
//     re-queued raw (nonce/signature stripped — the next round re-signs);
//   - dropped (account nonce advanced past it, no receipt) → same re-queue.
//
// It NEVER re-signs while the fate is unknown: the original tx mining after a
// re-sign would settle the same usage twice (double charge — the contract
// dedupes by strictly-increasing nonce only).
func resolvePendingTx(ctx context.Context, rdb *redis.Client, resolver fateResolver, queueKey string, stopCh chan<- StopSignal, provider common.Address, p *pendingTx, alerter alert.Alerter, log *zap.Logger) []chain.SettlementStatus {
	for {
		fate, receipt, err := resolver.ResolveTxFate(ctx, p.TxHash, p.AccountNonce)
		if err != nil {
			log.Warn("settler: pending-tx fate check failed; retrying", zap.String("tx", p.TxHash.Hex()), zap.Error(err))
		}
		switch fate {
		case chain.TxMined:
			statuses, serr := resolver.SettleStatusesFromReceipt(ctx, receipt, p.Vouchers)
			if serr != nil {
				// Whole tx reverted: nothing on-chain consumed — safe to retry.
				log.Warn("settler: pending tx reverted; re-queueing vouchers", zap.String("tx", p.TxHash.Hex()), zap.Error(serr))
				requeueRaw(ctx, rdb, queueKey, p, log)
				clearPendingTx(ctx, rdb, provider)
				return nil
			}
			log.Info("settler: pending tx mined; applying statuses", zap.String("tx", p.TxHash.Hex()), zap.Int("batch", len(p.Vouchers)))
			HandleStatuses(ctx, rdb, stopCh, queueKey, p.FirstItem, p.Vouchers, statuses, alerter, log)
			clearPendingTx(ctx, rdb, provider)
			return statuses
		case chain.TxDropped:
			log.Warn("settler: pending tx provably dropped; re-queueing vouchers", zap.String("tx", p.TxHash.Hex()))
			requeueRaw(ctx, rdb, queueKey, p, log)
			clearPendingTx(ctx, rdb, provider)
			return nil
		}
		select {
		case <-ctx.Done():
			return nil // pending record kept — resolved on next startup
		case <-time.After(pendingTxPollInterval):
		}
	}
}

// requeueRaw pushes the batch's vouchers back to the FRONT of the queue with
// settle-attempt artifacts (nonce, signature) stripped; the next round
// re-signs with fresh nonces (gaps are fine — the contract requires strictly
// increasing, not consecutive). FirstItem was BLPOP'd so every voucher needs
// re-pushing; the rest were only peeked, so only FirstItem goes back.
func requeueRaw(ctx context.Context, rdb *redis.Client, queueKey string, p *pendingTx, log *zap.Logger) {
	if err := rdb.LPush(ctx, queueKey, p.FirstItem).Err(); err != nil {
		log.Error("settler: re-queue after dropped tx failed", zap.Error(err))
	}
}
