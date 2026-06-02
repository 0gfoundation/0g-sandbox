package indexer

import (
	"context"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
)

// ── Mock chain client ─────────────────────────────────────────────────────────

type mockChain struct {
	events      []chain.ProviderEvent
	latestBlock uint64
	eventsErr   error
	services    map[string]*chain.ServiceInfo // keyed by provider.Hex()
}

func (m *mockChain) GetServiceUpdatedEvents(_ context.Context, _ uint64) ([]chain.ProviderEvent, uint64, error) {
	return m.events, m.latestBlock, m.eventsErr
}

func (m *mockChain) GetServiceInfo(_ context.Context, provider common.Address) (*chain.ServiceInfo, error) {
	svc, ok := m.services[provider.Hex()]
	if !ok {
		return nil, nil
	}
	return svc, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return redis.NewClient(&redis.Options{Addr: mr.Addr()})
}

var (
	providerAddr = common.HexToAddress("0xABCDEF0000000000000000000000000000000001")
	_            = common.HexToAddress("0x0000000000000000000000000000000000000002") // legacy signerAddr placeholder
	testSvcInfo  = &chain.ServiceInfo{
		URL:                 "http://provider-a.example.com:8080",
		AppId: "test-app",
		PricePerCPUPerMin:   big.NewInt(1200), // → 20/sec
		PricePerMemGBPerMin: big.NewInt(600),  // → 10/sec
		CreateFee:           big.NewInt(5_000_000),
		
	}
	testEvent = chain.ProviderEvent{
		Provider:         providerAddr,
		URL:              testSvcInfo.URL,
		AppId: "test-app",
		
		Block:            100,
	}
)

func singleProviderChain() *mockChain {
	return &mockChain{
		events:      []chain.ProviderEvent{testEvent},
		latestBlock: 100,
		services:    map[string]*chain.ServiceInfo{providerAddr.Hex(): testSvcInfo},
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGetAll_empty(t *testing.T) {
	idx := New(&mockChain{}, newRedis(t), zap.NewNop())
	if got := idx.GetAll(); len(got) != 0 {
		t.Fatalf("expected empty slice, got %d entries", len(got))
	}
}

func TestGet_missing(t *testing.T) {
	idx := New(&mockChain{}, newRedis(t), zap.NewNop())
	if _, ok := idx.Get(providerAddr.Hex()); ok {
		t.Fatal("expected not found for un-indexed address")
	}
}

func TestSync_indexesProvider(t *testing.T) {
	idx := New(singleProviderChain(), newRedis(t), zap.NewNop())
	idx.sync(context.Background())

	rec, ok := idx.Get(providerAddr.Hex())
	if !ok {
		t.Fatal("provider not found after sync")
	}
	if rec.URL != testSvcInfo.URL {
		t.Errorf("URL = %q, want %q", rec.URL, testSvcInfo.URL)
	}
	if rec.PricePerCPUPerSec != "20" {
		t.Errorf("PricePerCPUPerSec = %q, want 20", rec.PricePerCPUPerSec)
	}
	if rec.PricePerMemGBPerSec != "10" {
		t.Errorf("PricePerMemGBPerSec = %q, want 10", rec.PricePerMemGBPerSec)
	}
	if rec.CreateFee != "5000000" {
		t.Errorf("CreateFee = %q, want 5000000", rec.CreateFee)
	}
	if rec.LastBlock != 100 {
		t.Errorf("LastBlock = %d, want 100", rec.LastBlock)
	}
	if rec.Address != providerAddr.Hex() {
		t.Errorf("Address = %q, want %q", rec.Address, providerAddr.Hex())
	}
	if rec.AppId != "test-app" {
		t.Errorf("AppId = %q, want test-app", rec.AppId)
	}
	if rec.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestSync_persistsToRedis(t *testing.T) {
	rdb := newRedis(t)
	idx := New(singleProviderChain(), rdb, zap.NewNop())
	idx.sync(context.Background())

	ctx := context.Background()

	// Provider record persisted (indexer stores keys in lowercase).
	addrKey := strings.ToLower(providerAddr.Hex())
	data, err := rdb.Get(ctx, redisCachePrefix+addrKey).Bytes()
	if err != nil {
		t.Fatalf("provider record not in Redis: %v", err)
	}
	var rec ProviderRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal provider record: %v", err)
	}
	if rec.LastBlock != 100 {
		t.Errorf("persisted LastBlock = %d, want 100", rec.LastBlock)
	}

	// last_block updated.
	blockStr, err := rdb.Get(ctx, redisLastBlockKey).Result()
	if err != nil {
		t.Fatalf("last_block not in Redis: %v", err)
	}
	if blockStr != "100" {
		t.Errorf("last_block = %q, want 100", blockStr)
	}
}

func TestLoadFromRedis_restoresState(t *testing.T) {
	rdb := newRedis(t)
	ctx := context.Background()

	// Pre-seed Redis as if a previous run had indexed this provider.
	rec := ProviderRecord{
		Address:             providerAddr.Hex(),
		URL:                 "http://example.com",
		AppId:               "test-app-load",
		PricePerCPUPerMin:   "1200",
		PricePerCPUPerSec:   "20",
		PricePerMemGBPerMin: "600",
		PricePerMemGBPerSec: "10",
		CreateFee:           "5000000",
		LastBlock:           50,
		UpdatedAt:           time.Now().UTC(),
	}
	data, _ := json.Marshal(rec)
	rdb.Set(ctx, redisCachePrefix+providerAddr.Hex(), data, 0) //nolint:errcheck

	// Fresh indexer — no sync yet.
	idx := New(&mockChain{}, rdb, zap.NewNop())
	idx.LoadFromRedis(ctx)

	got, ok := idx.Get(providerAddr.Hex())
	if !ok {
		t.Fatal("LoadFromRedis did not restore provider into memory")
	}
	if got.URL != rec.URL {
		t.Errorf("URL = %q, want %q", got.URL, rec.URL)
	}
	if got.LastBlock != 50 {
		t.Errorf("LastBlock = %d, want 50", got.LastBlock)
	}
}

func TestSync_resumesFromLastBlock(t *testing.T) {
	rdb := newRedis(t)
	ctx := context.Background()

	// Simulate a previous run that stopped at block 500.
	rdb.Set(ctx, redisLastBlockKey, "500", 0) //nolint:errcheck

	var capturedFrom uint64
	mc := &mockChainCapture{captured: &capturedFrom, latestBlock: 600}
	idx := New(mc, rdb, zap.NewNop())
	idx.sync(ctx)

	// Should scan from 501, not from 1.
	if capturedFrom != 501 {
		t.Errorf("fromBlock = %d, want 501", capturedFrom)
	}
}

// mockChainCapture records the fromBlock passed to GetServiceUpdatedEvents.
type mockChainCapture struct {
	captured    *uint64
	latestBlock uint64
}

func (m *mockChainCapture) GetServiceUpdatedEvents(_ context.Context, fromBlock uint64) ([]chain.ProviderEvent, uint64, error) {
	*m.captured = fromBlock
	return nil, m.latestBlock, nil
}
func (m *mockChainCapture) GetServiceInfo(_ context.Context, _ common.Address) (*chain.ServiceInfo, error) {
	return nil, nil
}

func TestSync_deduplicatesEvents(t *testing.T) {
	rdb := newRedis(t)
	mc := &mockChain{
		// Same provider appears twice; block 30 is newer and should win.
		events: []chain.ProviderEvent{
			{Provider: providerAddr, URL: "http://old.example.com", AppId: "test-app",  Block: 10},
			{Provider: providerAddr, URL: "http://new.example.com", AppId: "test-app",  Block: 30},
		},
		latestBlock: 30,
		services:    map[string]*chain.ServiceInfo{providerAddr.Hex(): testSvcInfo},
	}
	idx := New(mc, rdb, zap.NewNop())
	idx.sync(context.Background())

	// Only one call to GetServiceInfo should have been made (dedup).
	rec, _ := idx.Get(providerAddr.Hex())
	if rec.LastBlock != 30 {
		t.Errorf("LastBlock = %d, want 30 (latest event wins)", rec.LastBlock)
	}
}

func TestGetAll_returnsAllProviders(t *testing.T) {
	provider2 := common.HexToAddress("0xABCDEF0000000000000000000000000000000002")
	svc2 := &chain.ServiceInfo{
		URL:                 "http://provider-b.example.com:8080",
		AppId: "test-app",
		PricePerCPUPerMin:   big.NewInt(600),
		PricePerMemGBPerMin: big.NewInt(300),
		CreateFee:           big.NewInt(1_000_000),
		
	}
	mc := &mockChain{
		events: []chain.ProviderEvent{
			testEvent,
			{Provider: provider2, URL: svc2.URL, AppId: "test-app",  Block: 101},
		},
		latestBlock: 101,
		services: map[string]*chain.ServiceInfo{
			providerAddr.Hex(): testSvcInfo,
			provider2.Hex():    svc2,
		},
	}
	idx := New(mc, newRedis(t), zap.NewNop())
	idx.sync(context.Background())

	all := idx.GetAll()
	if len(all) != 2 {
		t.Errorf("GetAll returned %d providers, want 2", len(all))
	}
}

func TestGet_caseInsensitive(t *testing.T) {
	idx := New(singleProviderChain(), newRedis(t), zap.NewNop())
	idx.sync(context.Background())

	// Mixed-case and uppercase variants should all resolve.
	for _, variant := range []string{
		providerAddr.Hex(),
		"0xabcdef0000000000000000000000000000000001",
		"0xABCDEF0000000000000000000000000000000001",
	} {
		if _, ok := idx.Get(variant); !ok {
			t.Errorf("Get(%q) returned false, want true", variant)
		}
	}
}
