package ember

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/dgraph-io/ristretto"
	"go.uber.org/zap"
	"goflare.io/ember/models"
	"goflare.io/ember/utils"
	"sync"
	"time"
)

type LimitedCache struct {
	maxSize    uint64
	shardCount uint64
	defaultTTL time.Duration

	keys      *sync.Map
	entryPool *sync.Pool
	segments  []*sync.RWMutex

	metrics *models.Metrics

	cache  *ristretto.Cache
	logger *zap.Logger
}

func NewLimitedCache(
	maxSize, shardCount uint64,
	defaultTTL time.Duration,
	logger *zap.Logger,
	ctx context.Context,
) (*LimitedCache, error) {

	lc := &LimitedCache{
		maxSize:    maxSize,
		shardCount: shardCount,
		defaultTTL: defaultTTL,

		segments: make([]*sync.RWMutex, shardCount),
		keys:     &sync.Map{},
		metrics:  models.NewMetrics(),
		entryPool: &sync.Pool{
			New: func() any {
				return &models.Entry{}
			},
		},

		logger: logger, // Default to no-op logger
	}

	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: int64(maxSize * 10),
		MaxCost:     int64(maxSize),
		BufferItems: 64,
		OnEvict: func(item *ristretto.Item) {
			lc.metrics.Evictions.Inc()
			lc.metrics.Size.Dec()
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create ristretto cache: %w", err)
	}

	lc.cache = cache

	for i := range shardCount {
		lc.segments[i] = &sync.RWMutex{}
	}

	go func(ctx context.Context) {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			lc.keys.Range(func(key, _ any) bool {
				if _, found := lc.Get(ctx, key.(string)); !found {
					lc.Delete(ctx, key.(string))
				}
				return true
			})
		}
	}(ctx)

	return lc, nil
}

func (lc *LimitedCache) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	segmentIndex := utils.ShardIndex(lc.shardCount, key)
	lc.segments[segmentIndex].Lock()
	defer lc.segments[segmentIndex].Unlock()

	if _, ok := lc.keys.Load(key); !ok {
		if lc.metrics.Size.Load() >= int64(lc.maxSize) {
			return ErrMaxSizeReached
		}
		lc.metrics.Size.Inc()
	}

	expirationTime := utils.GetExpirationTime(lc.defaultTTL, ttl...)

	entry := lc.entryPool.Get().(*models.Entry)
	defer func() {
		entry.Data = nil // 清理數據
		entry.Expiration = time.Time{}
		lc.entryPool.Put(entry)
	}()

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(value); err != nil {
		return fmt.Errorf("failed to encode value: %w", err)
	}
	entry.Data = buf.Bytes()

	entry.Expiration = time.Now().Add(expirationTime)

	if !lc.cache.SetWithTTL(key, entry, 1, expirationTime) {
		return ErrSetFailed
	}

	lc.keys.Store(key, struct{}{})
	return nil
}

func (lc *LimitedCache) Get(ctx context.Context, key string) (any, bool) {
	select {
	case <-ctx.Done():
		return nil, false
	default:
	}

	segmentIndex := utils.ShardIndex(lc.shardCount, key)
	lc.segments[segmentIndex].RLock()
	defer lc.segments[segmentIndex].RUnlock()

	value, found := lc.cache.Get(key)
	if !found {
		lc.metrics.Misses.Inc()
		lc.keys.Delete(key)
		return nil, false
	}

	entry := value.(*models.Entry)
	if time.Now().After(entry.Expiration) {
		lc.cache.Del(key)
		lc.keys.Delete(key)
		lc.metrics.Size.Dec()
		lc.metrics.Misses.Inc()
		return nil, false
	}

	lc.metrics.Hits.Inc()
	return entry.Data, true
}

func (lc *LimitedCache) Delete(ctx context.Context, key string) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	segmentIndex := utils.ShardIndex(lc.shardCount, key)
	lc.segments[segmentIndex].Lock()
	defer lc.segments[segmentIndex].Unlock()

	if _, ok := lc.keys.Load(key); ok {
		lc.cache.Del(key)
		lc.keys.Delete(key)
		lc.metrics.Size.Dec()
	}
}

func (lc *LimitedCache) Flush(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	for i := range lc.segments {
		lc.segments[i].Lock()
	}
	defer func() {
		for i := range lc.segments {
			lc.segments[i].Unlock()
		}
	}()

	lc.cache.Clear()
	lc.keys = &sync.Map{}
	lc.metrics.Size.Store(0)
}

func (lc *LimitedCache) GetMulti(ctx context.Context, keys []string) map[string]any {

	result := make(map[string]any)
	var wg sync.WaitGroup
	resultChan := make(chan struct {
		key   string
		value any
	}, len(keys))

	for _, key := range keys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			if value, found := lc.Get(ctx, k); found {
				resultChan <- struct {
					key   string
					value any
				}{k, value}
			}
		}(key)
	}

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	for r := range resultChan {
		result[r.key] = r.value
	}

	return result
}

func (lc *LimitedCache) SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error {
	for key, value := range items {
		if err := lc.Set(ctx, key, value, ttl...); err != nil {
			return fmt.Errorf("failed to set key %s: %w", key, err)
		}
	}
	return nil
}

func (lc *LimitedCache) ItemCount() int {
	return int(lc.metrics.Size.Load())
}

func (lc *LimitedCache) Close() {
	lc.cache.Clear()
}
