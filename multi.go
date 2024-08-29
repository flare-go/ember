package ember

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"golang.org/x/sync/singleflight"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"goflare.io/ember/config"
	"goflare.io/ember/models"
	"goflare.io/ember/retrier"
	"goflare.io/ember/utils"
)

type MultiCache struct {
	localCaches []*LimitedCache
	metrics     *models.Metrics
	config      *config.Config
	retrier     *retrier.Retrier

	accessCount *sync.Map
	shards      []sync.RWMutex
	sf          *singleflight.Group

	remoteCache *redis.Client
	tracer      trace.Tracer
	filter      *bloom.BloomFilter
	globalCB    *gobreaker.CircuitBreaker
	cbMap       map[uint64]*gobreaker.CircuitBreaker
}

func NewMultiCache(
	ctx context.Context,
	mlcConfig *config.Config,
	client *redis.Client) (*MultiCache, error) {

	var localCaches []*LimitedCache
	if mlcConfig.EnableLocalCache {
		localCaches = make([]*LimitedCache, mlcConfig.ShardCount)
		for i := range mlcConfig.ShardCount {
			lc, err := NewLimitedCache(mlcConfig.MaxLocalSize, mlcConfig.ShardCount, mlcConfig.DefaultExpiration, mlcConfig.Logger, ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to create local cache shard %d: %w", i, err)
			}
			localCaches[i] = lc
		}
	}

	filter := bloom.NewWithEstimates(
		mlcConfig.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		mlcConfig.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate)

	globalCB := gobreaker.NewCircuitBreaker(mlcConfig.ResilienceConfig.GlobalCircuitBreaker)

	r := retrier.NewRetrier(3, 100*time.Millisecond, 1*time.Second, 2, 0.1)

	mlc := &MultiCache{
		localCaches: localCaches,
		remoteCache: client,
		filter:      filter,
		config:      mlcConfig,
		globalCB:    globalCB,
		retrier:     r,

		tracer:  otel.Tracer("cache"),
		metrics: models.NewMetrics(),

		accessCount: &sync.Map{},
		sf:          &singleflight.Group{},
		shards:      make([]sync.RWMutex, mlcConfig.ShardCount),
	}

	if err := mlc.loadBloomFilter(ctx); err != nil {
		return nil, fmt.Errorf("failed to load Bloom filter: %w", err)
	}

	mlc.cbMap = make(map[uint64]*gobreaker.CircuitBreaker)
	for i := range mlcConfig.ShardCount {
		mlc.cbMap[i] = gobreaker.NewCircuitBreaker(mlcConfig.ResilienceConfig.KeyCircuitBreaker)
	}

	go mlc.periodicRebuild(ctx)
	go mlc.adaptiveTTLRoutine()
	go mlc.prefetchRoutine(ctx)

	if mlcConfig.CacheBehaviorConfig.EnablePrefetch {
		mlc.warmCache(ctx, mlcConfig.CacheBehaviorConfig.WarmupKeys)
	}

	return mlc, nil
}

func (c *MultiCache) Get(ctx context.Context, key string, value any) (bool, error) {

	ctx, span := c.tracer.Start(ctx, "MultiCache.Get", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	if !c.filter.Test([]byte(key)) {
		c.metrics.Misses.Inc()
	}

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].RLock()
		val, found := c.localCaches[shardIndex].Get(ctx, key)
		c.shards[shardIndex].RUnlock()
		if found {
			c.metrics.Hits.Inc()
			c.incrementAccessCount(key)
			return true, c.config.Serialization.Decoder(bytes.NewReader(val.([]byte))).Decode(value)
		}
	}

	v, err, _ := c.sf.Do(key, func() (any, error) {
		return c.GetFromRemoteCache(ctx, key)
	})

	if err != nil {
		if errors.Is(err, redis.Nil) {
			c.metrics.Misses.Inc()
			return false, nil
		}
		return false, err
	}

	data := v.([]byte)
	c.metrics.Hits.Inc()
	c.incrementAccessCount(key)

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].Lock()
		if err = c.localCaches[shardIndex].Set(ctx, key, data, c.getAdaptiveTTL(key)); err != nil {
			c.config.Logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
		}
		c.shards[shardIndex].Unlock()
	}

	return true, c.config.Serialization.Decoder(bytes.NewReader(data)).Decode(value)
}

func (c *MultiCache) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	ctx, span := c.tracer.Start(ctx, "MultiLevelCache.Set", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	_, err, _ := c.sf.Do(key, func() (interface{}, error) {
		var buf bytes.Buffer
		if err := c.config.Serialization.Encoder(&buf).Encode(value); err != nil {
			return nil, fmt.Errorf("failed to encode value: %w", err)
		}

		data := buf.Bytes()
		expirationTime := utils.GetExpirationTime(c.getAdaptiveTTL(key), ttl...)

		if err := c.executeWithResilience(ctx, func() error {
			return c.remoteCache.Set(ctx, key, data, expirationTime).Err()
		}); err != nil {
			return nil, fmt.Errorf("redis set failed: %w", err)
		}

		if c.config.EnableLocalCache {
			shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
			c.shards[shardIndex].Lock()
			defer c.shards[shardIndex].Unlock()
			if err := c.localCaches[shardIndex].Set(ctx, key, data, expirationTime); err != nil {
				c.config.Logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
			}
		}

		c.filter.Add([]byte(key))
		go c.saveBloomFilter(ctx)

		return nil, nil
	})

	return err
}

func (c *MultiCache) Delete(ctx context.Context, key string) error {
	ctx, span := c.tracer.Start(ctx, "MultiLevelCache.Delete", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	_, err, _ := c.sf.Do(key, func() (interface{}, error) {
		if c.config.EnableLocalCache {
			shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
			c.shards[shardIndex].Lock()
			defer c.shards[shardIndex].Unlock()
			c.localCaches[shardIndex].Delete(ctx, key)
		}

		if err := c.executeWithResilience(ctx, func() error {
			return c.remoteCache.Del(ctx, key).Err()
		}); err != nil {
			return nil, fmt.Errorf("redis delete failed: %w", err)
		}

		c.accessCount.Delete(key)

		return nil, nil
	})

	return err
}

func (c *MultiCache) Clear(ctx context.Context) error {
	ctx, span := c.tracer.Start(ctx, "MultiCache.Clear")
	defer span.End()

	if c.config.EnableLocalCache {
		for i, cache := range c.localCaches {
			c.shards[i].Lock()
			cache.Flush(ctx)
			c.shards[i].Unlock()
		}
	}

	if err := c.executeWithResilience(ctx, func() error {
		return c.remoteCache.FlushDB(ctx).Err()
	}); err != nil {
		return fmt.Errorf("redis flush failed: %w", err)
	}

	c.rebuildBloomFilter(ctx)
	c.accessCount = &sync.Map{}

	return nil
}

func (c *MultiCache) GetMulti(ctx context.Context, keys []string) (map[string]any, error) {
	ctx, span := c.tracer.Start(ctx, "MultiCache.GetMulti", trace.WithAttributes(attribute.Int("keyCount", len(keys))))
	defer span.End()

	result := make(map[string]any)

	filteredKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if c.filter.Test([]byte(key)) {
			filteredKeys = append(filteredKeys, key)
		}
	}

	if err := c.executeWithResilience(ctx, func() error {
		values, err := c.remoteCache.MGet(ctx, filteredKeys...).Result()
		if err != nil {
			return err
		}

		for i, key := range filteredKeys {
			if values[i] != nil {
				var value any
				if err = c.config.Serialization.Decoder(bytes.NewReader([]byte(values[i].(string)))).Decode(&value); err != nil {
					return err
				}
				result[key] = value

				if c.config.EnableLocalCache {
					shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
					c.shards[shardIndex].Lock()
					if err = c.localCaches[shardIndex].Set(ctx, key, []byte(values[i].(string)), c.getAdaptiveTTL(key)); err != nil {
						c.config.Logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
					}
					c.shards[shardIndex].Unlock()
				}

				c.incrementAccessCount(key)
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("redis mget failed: %w", err)
	}

	return result, nil
}

func (c *MultiCache) SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error {
	ctx, span := c.tracer.Start(ctx, "MultiCache.SetMulti", trace.WithAttributes(attribute.Int("itemCount", len(items))))
	defer span.End()

	pipe := c.remoteCache.Pipeline()
	localUpdates := make(map[string][]byte)

	for key, value := range items {
		var buf bytes.Buffer
		if err := c.config.Serialization.Encoder(&buf).Encode(value); err != nil {
			return fmt.Errorf("failed to encode value for key %s: %w", key, err)
		}

		data := buf.Bytes()
		expirationTime := c.getAdaptiveTTL(key)
		if len(ttl) > 0 {
			expirationTime = ttl[0]
		}

		pipe.Set(ctx, key, data, expirationTime)
		localUpdates[key] = data
		c.filter.Add([]byte(key))
	}

	if err := c.executeWithResilience(ctx, func() error {
		_, err := pipe.Exec(ctx)
		return err
	}); err != nil {
		return fmt.Errorf("redis mset failed: %w", err)
	}

	// Update local cache after successful remote update
	if c.config.EnableLocalCache {
		for key, data := range localUpdates {
			shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
			c.shards[shardIndex].Lock()
			if err := c.localCaches[shardIndex].Set(ctx, key, data, c.getAdaptiveTTL(key)); err != nil {
				c.config.Logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
			}
			c.shards[shardIndex].Unlock()
		}
	}

	go c.saveBloomFilter(ctx)

	return nil
}

func (c *MultiCache) GetTTL(ctx context.Context, key string) (time.Duration, error) {

	ctx, span := c.tracer.Start(ctx, "MultiCache.GetTTL", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	var ttl time.Duration
	if err := c.executeWithResilience(ctx, func() error {
		var err error
		ttl, err = c.remoteCache.TTL(ctx, key).Result()
		return err
	}); err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, ErrKeyNotFound
		}
		return 0, fmt.Errorf("failed to get TTL: %w", err)
	}

	return ttl, nil
}

func (c *MultiCache) Exists(ctx context.Context, key string) (bool, error) {

	ctx, span := c.tracer.Start(ctx, "MultiCache.Exists", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].RLock()
		_, found := c.localCaches[shardIndex].Get(ctx, key)
		c.shards[shardIndex].RUnlock()
		if found {
			return true, nil
		}
	}

	var exists int64
	if err := c.executeWithResilience(ctx, func() error {
		var err error
		exists, err = c.remoteCache.Exists(ctx, key).Result()
		return err
	}); err != nil {
		return false, fmt.Errorf("failed to check key existence: %w", err)
	}

	return exists == 1, nil
}

func (c *MultiCache) Incr(ctx context.Context, key string) (int64, error) {
	ctx, span := c.tracer.Start(ctx, "MultiLevelCache.Incr", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	var value int64
	if err := c.executeWithResilience(ctx, func() error {
		var err error
		value, err = c.remoteCache.Incr(ctx, key).Result()
		return err
	}); err != nil {
		return 0, fmt.Errorf("failed to increment key: %w", err)
	}

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].Lock()
		defer c.shards[shardIndex].Unlock()

		// 重新獲取最新的值並設置本地快取
		c.localCaches[shardIndex].Delete(ctx, key)
		// 可以選擇設置新的值到本地快取
		if err := c.localCaches[shardIndex].Set(ctx, key, value, c.getAdaptiveTTL(key)); err != nil {
			return 0, err
		}
	}

	c.filter.Add([]byte(key))

	return value, nil
}

func (c *MultiCache) SetNX(ctx context.Context, key string, value any, ttl ...time.Duration) (bool, error) {

	ctx, span := c.tracer.Start(ctx, "MultiCache.SetNX", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	var buf bytes.Buffer
	if err := c.config.Serialization.Encoder(&buf).Encode(value); err != nil {
		return false, fmt.Errorf("failed to encode value: %w", err)
	}
	data := buf.Bytes()

	expirationTime := c.config.DefaultExpiration
	if len(ttl) > 0 {
		expirationTime = ttl[0]
	}

	var set bool
	if err := c.executeWithResilience(ctx, func() error {
		var err error
		set, err = c.remoteCache.SetNX(ctx, key, data, expirationTime).Result()
		return err
	}); err != nil {
		return false, fmt.Errorf("SetNX failed: %w", err)
	}

	if set {
		if c.config.EnableLocalCache {
			shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
			c.shards[shardIndex].Lock()
			if err := c.localCaches[shardIndex].Set(ctx, key, data, c.getAdaptiveTTL(key)); err != nil {
				c.config.Logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
			}
			c.shards[shardIndex].Unlock()
		}
		c.filter.Add([]byte(key))
	}

	return set, nil
}

func (c *MultiCache) Close() error {
	if c.config.EnableLocalCache {
		for i, cache := range c.localCaches {
			c.shards[i].Lock()
			cache.Close()
			c.shards[i].Unlock()
		}
	}
	return c.remoteCache.Close()
}
