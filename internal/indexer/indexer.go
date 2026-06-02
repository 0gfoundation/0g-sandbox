package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/0gfoundation/0g-sandbox/internal/chain"
)

const (
	redisLastBlockKey = "indexer:provider:last_block"
	redisCachePrefix  = "indexer:provider:"
	pollInterval      = 60 * time.Second
)

// ProviderRecord holds a provider's on-chain service data for the market API.
// TEE signer identity now lives in TappRegistry under AppId; consumers that
// need the active signer set call tapp.getNodeList(appId) directly.
type ProviderRecord struct {
	Address             string    `json:"address"`
	URL                 string    `json:"url"`
	AppId               string    `json:"app_id"`
	PricePerCPUPerMin   string    `json:"price_per_cpu_per_min"`
	PricePerCPUPerSec   string    `json:"price_per_cpu_per_sec"`
	PricePerMemGBPerMin string    `json:"price_per_mem_gb_per_min"`
	PricePerMemGBPerSec string    `json:"price_per_mem_gb_per_sec"`
	CreateFee           string    `json:"create_fee"`
	LastBlock           uint64    `json:"last_indexed_block"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// chainClient is the minimal interface the indexer needs from the chain layer.
type chainClient interface {
	GetServiceUpdatedEvents(ctx context.Context, fromBlock uint64) ([]chain.ProviderEvent, uint64, error)
	GetServiceInfo(ctx context.Context, provider common.Address) (*chain.ServiceInfo, error)
}

// Indexer maintains a live in-memory index of all providers registered on-chain,
// backed by Redis for persistence across restarts.
type Indexer struct {
	chain chainClient
	rdb   *redis.Client
	log   *zap.Logger

	mu    sync.RWMutex
	store map[string]ProviderRecord // keyed by lowercase hex address
}

// New creates an Indexer. Call LoadFromRedis then Run to start it.
func New(c chainClient, rdb *redis.Client, log *zap.Logger) *Indexer {
	return &Indexer{
		chain: c,
		rdb:   rdb,
		log:   log,
		store: make(map[string]ProviderRecord),
	}
}

// Run starts the polling loop. It syncs immediately, then every pollInterval.
// Blocks until ctx is cancelled.
func (idx *Indexer) Run(ctx context.Context) {
	idx.log.Info("provider indexer started")
	idx.sync(ctx)

	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			idx.log.Info("provider indexer stopped")
			return
		case <-t.C:
			idx.sync(ctx)
		}
	}
}

// LoadFromRedis restores the in-memory store from Redis on startup so the
// first GET /api/providers doesn't return an empty list while syncing.
func (idx *Indexer) LoadFromRedis(ctx context.Context) {
	keys, err := idx.rdb.Keys(ctx, redisCachePrefix+"*").Result()
	if err != nil {
		idx.log.Warn("indexer: LoadFromRedis keys failed", zap.Error(err))
		return
	}
	loaded := 0
	for _, key := range keys {
		if key == redisLastBlockKey {
			continue
		}
		data, err := idx.rdb.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var rec ProviderRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		idx.mu.Lock()
		idx.store[strings.ToLower(rec.Address)] = rec
		idx.mu.Unlock()
		loaded++
	}
	idx.log.Info("indexer: restored from Redis", zap.Int("providers", loaded))
}

// GetAll returns a snapshot of all indexed providers.
func (idx *Indexer) GetAll() []ProviderRecord {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]ProviderRecord, 0, len(idx.store))
	for _, r := range idx.store {
		out = append(out, r)
	}
	return out
}

// Get returns the record for a specific provider address (case-insensitive).
func (idx *Indexer) Get(addr string) (ProviderRecord, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	r, ok := idx.store[strings.ToLower(addr)]
	return r, ok
}

// sync fetches new ServiceUpdated events since the last indexed block and
// refreshes the provider records for any provider that emitted a new event.
func (idx *Indexer) sync(ctx context.Context) {
	fromBlock := idx.lastBlock(ctx) + 1

	events, latestBlock, err := idx.chain.GetServiceUpdatedEvents(ctx, fromBlock)
	if err != nil {
		idx.log.Warn("indexer: GetServiceUpdatedEvents failed", zap.Error(err))
		return
	}

	// Deduplicate: for providers that emitted multiple events in this batch,
	// keep only the latest one (highest block number).
	latest := make(map[string]chain.ProviderEvent)
	for _, ev := range events {
		key := strings.ToLower(ev.Provider.Hex())
		if prev, ok := latest[key]; !ok || ev.Block > prev.Block {
			latest[key] = ev
		}
	}

	refreshed := 0
	for addrKey, ev := range latest {
		svcInfo, err := idx.chain.GetServiceInfo(ctx, ev.Provider)
		if err != nil || svcInfo == nil {
			idx.log.Warn("indexer: GetServiceInfo failed",
				zap.String("provider", addrKey), zap.Error(err))
			continue
		}

		cpuPerSec := new(big.Int).Div(svcInfo.PricePerCPUPerMin, big.NewInt(60))
		memPerSec := new(big.Int).Div(svcInfo.PricePerMemGBPerMin, big.NewInt(60))

		rec := ProviderRecord{
			Address:             ev.Provider.Hex(),
			URL:                 svcInfo.URL,
			AppId:               svcInfo.AppId,
			PricePerCPUPerMin:   svcInfo.PricePerCPUPerMin.String(),
			PricePerCPUPerSec:   cpuPerSec.String(),
			PricePerMemGBPerMin: svcInfo.PricePerMemGBPerMin.String(),
			PricePerMemGBPerSec: memPerSec.String(),
			CreateFee:           svcInfo.CreateFee.String(),
			LastBlock:           ev.Block,
			UpdatedAt:           time.Now().UTC(),
		}

		idx.mu.Lock()
		idx.store[addrKey] = rec
		idx.mu.Unlock()

		if err := idx.persistProvider(ctx, addrKey, rec); err != nil {
			idx.log.Warn("indexer: persist provider failed",
				zap.String("provider", addrKey), zap.Error(err))
		}
		refreshed++
	}

	if latestBlock > 0 {
		idx.saveLastBlock(ctx, latestBlock)
	}
	if refreshed > 0 {
		idx.log.Info("indexer: sync complete",
			zap.Int("refreshed", refreshed),
			zap.Uint64("latest_block", latestBlock))
	}
}

func (idx *Indexer) lastBlock(ctx context.Context) uint64 {
	val, err := idx.rdb.Get(ctx, redisLastBlockKey).Result()
	if err != nil {
		return 0
	}
	var n uint64
	fmt.Sscan(val, &n)
	return n
}

func (idx *Indexer) saveLastBlock(ctx context.Context, block uint64) {
	idx.rdb.Set(ctx, redisLastBlockKey, fmt.Sprintf("%d", block), 0) //nolint:errcheck
}

func (idx *Indexer) persistProvider(ctx context.Context, addrKey string, rec ProviderRecord) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return idx.rdb.Set(ctx, redisCachePrefix+addrKey, data, 0).Err()
}
