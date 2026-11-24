package notice

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"

	"github.com/0gfoundation/0g-sandbox/internal/billing"
	"github.com/0gfoundation/0g-sandbox/internal/voucher"
)

// BalanceChain reads balances with this provider.
type BalanceChain interface {
	GetBalanceBatch(ctx context.Context, users []common.Address, provider common.Address) ([]*big.Int, error)
}

// Balance warns the sandboxes of an owner whose balance with this provider
// runs out soon. Billing pre-charges each running sandbox one voucher period
// at a time; once the owner's balance cannot cover the next one, the settler
// stops the owner's sandboxes.
//
// The balance left is counted as the gates count it (GET /api/balance): the
// on-chain balance less reservations, held debt and vouchers queued for
// settlement. It lasts that divided by the per-second price of the owner's
// running sandboxes. The stop comes at a voucher period's start, so the
// estimate can be off by up to one period.
type Balance struct {
	Chain    BalanceChain
	RDB      *redis.Client
	Provider common.Address
	// FallbackPricePerSec prices a session that recorded no price.
	FallbackPricePerSec *big.Int
	// Within warns when the balance runs out within this. 0 never warns.
	Within time.Duration
	Now    func() time.Time
}

type account struct {
	addr      common.Address
	rate      *big.Int // neuron per second, all running sandboxes
	sandboxes []string
}

// Notices implements Source.
func (b *Balance) Notices(ctx context.Context, running []string) (Set, error) {
	if b.Within <= 0 || len(running) == 0 {
		return nil, nil
	}
	isRunning := make(map[string]bool, len(running))
	for _, id := range running {
		isRunning[id] = true
	}
	sessions, err := billing.ScanAllSessions(ctx, b.RDB)
	if err != nil {
		return nil, err
	}
	byOwner := map[common.Address]*account{}
	for _, s := range sessions {
		if !isRunning[s.SandboxID] || !common.IsHexAddress(s.Owner) || !strings.EqualFold(s.Provider, b.Provider.Hex()) {
			continue
		}
		price, ok := new(big.Int).SetString(s.PricePerSec, 10)
		if !ok || price.Sign() < 0 {
			price = b.FallbackPricePerSec
		}
		if price == nil || price.Sign() == 0 {
			continue
		}
		owner := common.HexToAddress(s.Owner)
		a := byOwner[owner]
		if a == nil {
			a = &account{addr: owner, rate: new(big.Int)}
			byOwner[owner] = a
		}
		a.rate.Add(a.rate, price)
		a.sandboxes = append(a.sandboxes, s.SandboxID)
	}
	if len(byOwner) == 0 {
		return nil, nil
	}
	accounts := make([]*account, 0, len(byOwner))
	for _, a := range byOwner {
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].addr.Hex() < accounts[j].addr.Hex() })
	users := make([]common.Address, len(accounts))
	for i, a := range accounts {
		users[i] = a.addr
	}
	balances, err := b.Chain.GetBalanceBatch(ctx, users, b.Provider)
	if err != nil {
		return nil, fmt.Errorf("balances: %w", err)
	}
	pending, err := b.queued(ctx)
	if err != nil {
		return nil, fmt.Errorf("queued vouchers: %w", err)
	}

	now := b.Now()
	set := Set{}
	for i, a := range accounts {
		if i >= len(balances) || balances[i] == nil {
			continue
		}
		held, err := voucher.HeldDebt(ctx, b.RDB, a.addr, b.Provider)
		if err != nil {
			return nil, fmt.Errorf("held debt: %w", err)
		}
		left := new(big.Int).Set(balances[i])
		left.Sub(left, billing.GetReserved(ctx, b.RDB, a.addr.Hex(), b.Provider.Hex()))
		left.Sub(left, held)
		if p := pending[a.addr]; p != nil {
			left.Sub(left, p)
		}
		if left.Sign() < 0 {
			left.SetInt64(0)
		}
		secs := new(big.Int).Quo(left, a.rate)
		if secs.Cmp(big.NewInt(int64(b.Within/time.Second))) > 0 {
			continue
		}
		lasts := time.Duration(secs.Int64()) * time.Second
		deadline := now.Add(lasts)
		n := Notice{Kind: KindBalanceLow, Severity: severityBy(deadline, now), Deadline: &deadline, Action: ActionStop}
		if lasts < time.Minute {
			n.Message = "Your owner's balance with this provider is used up, and this provider is stopping the sandboxes it pays for, this one included. Save your work now. Files are kept while the sandbox is stopped; the owner can deposit more and start it again."
		} else {
			n.Message = fmt.Sprintf("Your owner's balance with this provider covers about %s more of its running sandboxes (%d, this one included). Around %s this provider stops them unless the owner deposits more. Save your work before then, and tell your owner if you can. Files are kept while the sandbox is stopped.",
				about(lasts), len(a.sandboxes), utc(deadline))
		}
		for _, id := range a.sandboxes {
			set.Add(id, n)
		}
	}
	return set, nil
}

// queued sums each user's vouchers waiting in the settle queue.
func (b *Balance) queued(ctx context.Context) (map[common.Address]*big.Int, error) {
	rows, _, _, err := voucher.SummarizeQueue(ctx, b.RDB, fmt.Sprintf(voucher.VoucherQueueKeyFmt, b.Provider.Hex()), 1)
	if err != nil {
		return nil, err
	}
	out := map[common.Address]*big.Int{}
	for _, r := range rows {
		if !strings.EqualFold(r.Provider, b.Provider.Hex()) || !common.IsHexAddress(r.User) {
			continue
		}
		fee, ok := new(big.Int).SetString(r.TotalFeeWei, 10)
		if !ok {
			continue
		}
		u := common.HexToAddress(r.User)
		if out[u] == nil {
			out[u] = new(big.Int)
		}
		out[u].Add(out[u], fee)
	}
	return out, nil
}

// about writes a duration for a message.
func about(d time.Duration) string {
	switch {
	case d < 2*time.Minute:
		return "a minute"
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	default:
		return fmt.Sprintf("%.1f hours", d.Hours())
	}
}
