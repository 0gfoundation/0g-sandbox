package settler

import (
	"context"
	"encoding/json"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// sweepWhilePending checks the backlog without rewriting the queue. The
// pending batch owns the head of the queue until its receipt is processed.
// Exclude the entire batch: it could mine between the receipt and balance
// reads, so counting it against the observed balance could double-count usage.
func sweepWhilePending(ctx context.Context, rdb *redis.Client, onchain AggregatorChain, queueKey string, stopCh chan<- StopSignal, p *pendingTx, log *zap.Logger) {
	consumed := p.Consumed
	if consumed <= 0 {
		consumed = len(p.Vouchers) // records written before batch collapsing
	}
	firstUnsubmitted := int64(max(0, consumed-1)) // the first entry was BLPOP'd
	queueLen, err := rdb.LLen(ctx, queueKey).Result()
	if err != nil {
		log.Warn("pending stop check: queue length", zap.Error(err))
		return
	}
	if firstUnsubmitted >= queueLen {
		return
	}

	provider := onchain.ProviderAddress()
	sessions, err := billing.ScanAllSessions(ctx, rdb)
	if err != nil {
		log.Warn("pending stop check: scan sessions", zap.Error(err))
		return
	}
	active := make(map[string]common.Address)
	owners := make(map[common.Address]bool)
	var users []common.Address
	for _, s := range sessions {
		if s.SandboxID == "" || common.HexToAddress(s.Provider) != provider || !common.IsHexAddress(s.Owner) {
			continue
		}
		u := common.HexToAddress(s.Owner)
		active[s.SandboxID] = u
		if !owners[u] {
			owners[u] = true
			users = append(users, u)
		}
	}
	if len(users) == 0 {
		return
	}
	balances, err := onchain.GetBalanceBatch(ctx, users, provider)
	if err != nil || len(balances) != len(users) {
		log.Warn("pending stop check: batch balance read", zap.Error(err), zap.Int("users", len(users)), zap.Int("balances", len(balances)))
		return
	}
	remaining := make(map[common.Address]*big.Int, len(users))
	for i, u := range users {
		if balances[i] == nil {
			continue
		}
		debt, err := voucher.HeldDebt(ctx, rdb, u, provider)
		if err != nil {
			log.Warn("pending stop check: read held debt", zap.String("user", u.Hex()), zap.Error(err))
			continue
		}
		remaining[u] = new(big.Int).Sub(balances[i], debt)
	}

	// Walk the whole snapshot in pages. A head-only scan could indefinitely
	// miss users whose vouchers are behind a backlog that cannot drain.
	const pageSize = 1000
	for i := firstUnsubmitted; i < queueLen; i += pageSize {
		items, err := rdb.LRange(ctx, queueKey, i, min(i+pageSize, queueLen)-1).Result()
		if err != nil {
			log.Warn("pending stop check: read queue page", zap.Error(err))
			return
		}
		for _, raw := range items {
			var v voucher.SandboxVoucher
			if json.Unmarshal([]byte(raw), &v) != nil || v.Provider != provider || v.TotalFee == nil || v.TotalFee.Sign() <= 0 {
				continue
			}
			balance := remaining[v.User]
			if balance == nil || balance.Sub(balance, v.TotalFee).Sign() >= 0 {
				continue
			}
			if owner, ok := active[v.SandboxID]; ok && owner == v.User {
				delete(active, v.SandboxID)
				if err := persistStop(ctx, rdb, stopCh, v.SandboxID, "insufficient_balance", log); err != nil {
					log.Error("pending stop check: persist stop", zap.String("sandbox", v.SandboxID), zap.Error(err))
				}
			}
		}
	}
}
