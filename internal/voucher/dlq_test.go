package voucher

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
)

func enqueueDLQ(t *testing.T, rdb *redis.Client, provider common.Address, v SandboxVoucher) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dlqKey := fmt.Sprintf(VoucherDLQKeyFmt, provider.Hex())
	if err := rdb.RPush(context.Background(), dlqKey, string(raw)).Err(); err != nil {
		t.Fatalf("rpush: %v", err)
	}
}

func TestListDLQ_ParsesEntries(t *testing.T) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")

	v1 := SandboxVoucher{
		SandboxID: "sb-1", User: user, Provider: prov,
		TotalFee: big.NewInt(100), Nonce: big.NewInt(42),
	}
	v2 := SandboxVoucher{
		SandboxID: AggregatedSandboxID, User: user, Provider: prov,
		TotalFee: big.NewInt(999), Nonce: big.NewInt(100),
	}
	enqueueDLQ(t, rdb, prov, v1)
	enqueueDLQ(t, rdb, prov, v2)

	entries, err := ListDLQ(context.Background(), rdb, prov)
	if err != nil {
		t.Fatalf("ListDLQ: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries: %d want 2", len(entries))
	}
	if entries[0].SandboxID != "sb-1" || entries[0].Nonce != "42" {
		t.Errorf("entry 0: %+v", entries[0])
	}
	if !entries[1].Aggregated || entries[1].Nonce != "100" {
		t.Errorf("entry 1: aggregated=%v nonce=%s", entries[1].Aggregated, entries[1].Nonce)
	}
}

func TestDiscardFromDLQ(t *testing.T) {
	mr, _ := miniredis.Run()
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	user := common.HexToAddress("0xAAA")
	prov := common.HexToAddress("0xBBB")
	dlqKey := fmt.Sprintf(VoucherDLQKeyFmt, prov.Hex())

	enqueueDLQ(t, rdb, prov, SandboxVoucher{User: user, Provider: prov, TotalFee: big.NewInt(100), Nonce: big.NewInt(42)})
	enqueueDLQ(t, rdb, prov, SandboxVoucher{User: user, Provider: prov, TotalFee: big.NewInt(200), Nonce: big.NewInt(43)})

	removed, err := DiscardFromDLQ(context.Background(), rdb, user, prov, "42")
	if err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed: %d want 1", removed)
	}
	dlqLen, _ := rdb.LLen(context.Background(), dlqKey).Result()
	if dlqLen != 1 {
		t.Errorf("dlq: %d want 1", dlqLen)
	}
}
