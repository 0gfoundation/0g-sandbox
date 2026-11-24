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
// settlement. It pays the running sandboxes' next vouchers in the order they
// fall due, one period each; the deadline is when the first one it cannot pay
// falls due. Paying a voucher moves both the balance and the sandbox's next
// voucher time, so the deadline stays put from one poll to the next: a reader
// that compares deadlines sees a change only when something changed.
type Balance struct {
	Chain    BalanceChain
	RDB      *redis.Client
	Provider common.Address
	// FallbackPricePerSec prices a session that recorded no price.
	FallbackPricePerSec *big.Int
	// Interval is the voucher period (VOUCHER_INTERVAL_SEC).
	Interval time.Duration
	// Within warns when the balance runs out within this. 0 never warns.
	Within time.Duration
	Now    func() time.Time
}

type account struct {
	addr      common.Address
	charges   []charge // one per running sandbox
	sandboxes []string
}

// charge is a running sandbox's next voucher: its fee, due at unix second at
// and every period after.
type charge struct {
	at  int64
	fee *big.Int
}

// Notices implements Source.
func (b *Balance) Notices(ctx context.Context, running []string) (Set, error) {
	period := int64(b.Interval / time.Second)
	if b.Within <= 0 || period <= 0 || len(running) == 0 {
		return nil, nil
	}
	now := b.Now()
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
			a = &account{addr: owner}
			byOwner[owner] = a
		}
		// An overdue voucher is emitted on the generator's next tick.
		a.charges = append(a.charges, charge{at: max(s.NextVoucherAt, now.Unix()), fee: new(big.Int).Mul(price, big.NewInt(period))})
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
		due, ok := runsOut(left, a.charges, period, now.Unix()+int64(b.Within/time.Second))
		if !ok {
			continue
		}
		deadline := time.Unix(due, 0).UTC()
		lasts := max(deadline.Sub(now), 0)
		n := Notice{Kind: KindBalanceLow, Severity: severityBy(deadline, now), Deadline: &deadline, Action: ActionStop}
		if left.Sign() == 0 || lasts < time.Minute {
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

// runsOut is when the first voucher that left cannot pay falls due, if that
// is no later than by. Every sandbox pays its fee at its next voucher time
// and every period after; left pays them in the order they fall due.
func runsOut(left *big.Int, charges []charge, period, by int64) (int64, bool) {
	sort.Slice(charges, func(i, j int) bool { return charges[i].at < charges[j].at })
	cycle := new(big.Int)
	for _, c := range charges {
		cycle.Add(cycle, c.fee)
	}
	if cycle.Sign() == 0 {
		return 0, false
	}
	// left pays `full` whole periods of every sandbox, then the remainder
	// pays the next vouchers in order until one does not fit.
	full, rem := new(big.Int).QuoRem(left, cycle, new(big.Int))
	if full.Cmp(big.NewInt((by-charges[0].at)/period+1)) > 0 {
		return 0, false // lasts past by
	}
	for _, c := range charges {
		if rem.Cmp(c.fee) < 0 {
			due := c.at + full.Int64()*period
			return due, due <= by
		}
		rem.Sub(rem, c.fee)
	}
	return 0, false // unreachable: rem < cycle
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
