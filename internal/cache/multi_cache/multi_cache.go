package multi_cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go.opentelemetry.io/otel"
	"goflare.io/ember/internal/cache/limited_cache"
	"goflare.io/ember/internal/retrier"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"

	"goflare.io/ember/internal/config"
	"goflare.io/ember/internal/models"
	"goflare.io/ember/internal/utils"
)

// MultiCache represents a multi-layer cache system with local and remote caches.
type MultiCache struct {
	localCaches []*limited_cache.LimitedCache
	metrics     *models.Metrics
	config      *config.Config
	retrier     *retrier.Retrier

	accessCount     *sync.Map
	shards          []sync.RWMutex
	filterRebuildMu sync.Mutex
	sf              *singleflight.Group

	remoteCache *redis.Client
	tracer      trace.Tracer
	filter      *bloom.BloomFilter
	globalCB    *gobreaker.CircuitBreaker
	cbMap       map[uint64]*gobreaker.CircuitBreaker
	logger      *zap.Logger

	// Components
	bloomFilter *BloomFilter
	ttlManager  *TTLManager
	prefetcher  *Prefetcher
	resilience  *Resilience
}

// NewMultiCache creates a new MultiCache instance.
func NewMultiCache(ctx context.Context, mlcConfig *config.Config, client *redis.Client) (CacheOperations, error) {
	// Initialize local caches
	var localCaches []*limited_cache.LimitedCache
	if mlcConfig.EnableLocalCache {
		localCaches = make([]*limited_cache.LimitedCache, mlcConfig.ShardCount)
		for i := uint64(0); i < mlcConfig.ShardCount; i++ {
			lc, err := limited_cache.NewLimitedCache(
				mlcConfig.MaxLocalSize/mlcConfig.ShardCount,
				mlcConfig.DefaultExpiration,
				mlcConfig.Logger,
			)
			if err != nil {
				// Cleanup initialized shards
				for j := uint64(0); j < i; j++ {
					if localCaches[j] != nil {
						localCaches[j].Close()
					}
				}
				return nil, fmt.Errorf("failed to create local cache shard %d: %w", i, err)
			}
			localCaches[i] = lc
		}
	}

	// Initialize Bloom Filter
	filter := bloom.NewWithEstimates(
		mlcConfig.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		mlcConfig.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate,
	)

	// Initialize Circuit Breaker
	globalCB := gobreaker.NewCircuitBreaker(mlcConfig.ResilienceConfig.GlobalCircuitBreaker)

	// Initialize Retrier
	r := retrier.NewRetrier(
		3,
		100*time.Millisecond,
		1*time.Second,
		2,
		0.1,
		retrier.ExponentialBackoff,
		nil,
	)

	// Initialize MultiCache
	mlc := &MultiCache{
		localCaches: localCaches,
		remoteCache: client,
		filter:      filter,
		config:      mlcConfig,
		globalCB:    globalCB,
		retrier:     r,

		tracer:  otel.Tracer("cache"),
		metrics: models.NewMetrics(),

		accessCount:     &sync.Map{},
		sf:              &singleflight.Group{},
		shards:          make([]sync.RWMutex, mlcConfig.ShardCount),
		filterRebuildMu: sync.Mutex{},
		cbMap:           make(map[uint64]*gobreaker.CircuitBreaker),
		logger:          mlcConfig.Logger,
	}

	// Initialize shard locks
	for i := uint64(0); i < mlcConfig.ShardCount; i++ {
		mlc.shards[i] = sync.RWMutex{}
	}

	// Initialize components
	mlc.bloomFilter = NewBloomFilter(mlc)
	mlc.ttlManager = NewTTLManager(mlc)
	mlc.prefetcher = NewPrefetcher(mlc)
	mlc.resilience = NewResilience(mlc)

	// Load Bloom Filter
	if err := mlc.bloomFilter.Load(ctx); err != nil {
		return nil, fmt.Errorf("failed to load Bloom filter: %w", err)
	}

	// Initialize per-shard Circuit Breakers
	for i := uint64(0); i < mlcConfig.ShardCount; i++ {
		mlc.cbMap[i] = gobreaker.NewCircuitBreaker(mlcConfig.ResilienceConfig.KeyCircuitBreaker)
	}

	// Start background tasks
	go mlc.bloomFilter.PeriodicRebuild(ctx)
	go mlc.ttlManager.Run(ctx)
	go mlc.prefetcher.Run(ctx)
	if mlcConfig.CacheBehaviorConfig.EnablePrefetch {
		mlc.prefetcher.Warmup(ctx)
	}

	return mlc, nil
}

// Get retrieves a value from the cache.
func (c *MultiCache) Get(ctx context.Context, key string, value any) (bool, error) {
	ctx, span := c.tracer.Start(ctx, "MultiCache.Get", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	if !c.bloomFilter.Test(key) {
		c.metrics.Misses.Inc()
		exists, err := c.remoteCache.Exists(ctx, key).Result()
		if err != nil {
			c.logger.Error("Failed to check key existence in remote cache", zap.Error(err), zap.String("key", key))
			return false, err
		}
		if exists == 0 {
			// Key does not exist
			return false, nil
		}
		// Key exists but might be a false positive; proceed to fetch from remote cache
	}

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].RLock()
		entry, found := c.localCaches[shardIndex].Get(key)
		c.shards[shardIndex].RUnlock()
		if found {
			if time.Now().After(entry.Expiration) {
				c.localCaches[shardIndex].Delete(key)
				c.metrics.Misses.Inc()
				return false, nil
			}

			if err := c.config.Serialization.Decoder(bytes.NewReader(entry.Data)).Decode(value); err != nil {
				c.logger.Error("Failed to decode value", zap.Error(err), zap.String("key", key))
				return false, err
			}

			c.metrics.Hits.Inc()
			c.incrementAccessCount(key, entry)
			return true, nil
		}
	}

	// Fetch from a remote cache using singleflight to prevent duplicate requests
	v, err, _ := c.sf.Do(key, func() (any, error) {
		return c.GetFromRemoteCache(ctx, key)
	})

	if err != nil {
		if errors.Is(err, models.ErrKeyNotFound) {
			c.metrics.Misses.Inc()
			return false, nil
		}
		return false, err
	}

	data, ok := v.(*models.Entry)
	if !ok {
		c.logger.Error("Invalid type assertion for remote cache value", zap.String("key", key))
		return false, fmt.Errorf("invalid remote cache value type for key: %s", key)
	}

	c.metrics.Hits.Inc()
	c.incrementAccessCount(key, data)

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].Lock()
		err = c.localCaches[shardIndex].Set(key, data, data.Expiration.Sub(time.Now()))
		if err != nil {
			c.logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
		}
		c.shards[shardIndex].Unlock()
	}

	// Decode the value
	err = c.config.Serialization.Decoder(bytes.NewReader(data.Data)).Decode(value)
	if err != nil {
		c.logger.Error("Failed to decode value", zap.Error(err), zap.String("key", key))
		return false, err
	}

	return true, nil
}

// Set sets a value in the cache.
func (c *MultiCache) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	if key == "" {
		c.logger.Error("Set operation failed: key is empty")
		return fmt.Errorf("key cannot be empty")
	}

	ctx, span := c.tracer.Start(ctx, "MultiCache.Set", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	c.logger.Debug("Starting Set operation", zap.String("key", key))

	_, err, shared := c.sf.Do(key, func() (any, error) {
		defer func() {
			if r := recover(); r != nil {
				c.logger.Error("Panic in Set operation",
					zap.String("key", key),
					zap.Any("panic", r),
					zap.Stack("stack"))
			}
		}()

		// Serialize data
		buf := bytes.Buffer{}
		if err := c.config.Serialization.Encoder(&buf).Encode(value); err != nil {
			c.logger.Error("Failed to encode value", zap.Error(err), zap.String("key", key))
			return nil, fmt.Errorf("failed to encode value: %w", err)
		}

		data := buf.Bytes()
		expirationTime := c.ttlManager.GetTTL(key)
		if len(ttl) > 0 && ttl[0] > 0 {
			expirationTime = ttl[0]
		}
		c.logger.Debug("Calculated expiration time", zap.Duration("ttl", expirationTime), zap.String("key", key))

		// Create Entry
		entry := &models.Entry{
			Data:           data,
			Expiration:     time.Now().Add(expirationTime),
			AccessCount:    1,
			LastAccessTime: time.Now(),
		}

		// Set to remote cache
		if err := c.resilience.Set(ctx, key, entry, expirationTime); err != nil {
			c.logger.Error("Failed to set in remote cache", zap.Error(err), zap.String("key", key))
			return nil, fmt.Errorf("redis set failed: %w", err)
		}
		c.logger.Info("Successfully set in remote cache", zap.String("key", key))

		// Set to local cache
		if c.config.EnableLocalCache {
			shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
			c.shards[shardIndex].Lock()
			defer c.shards[shardIndex].Unlock()
			if err := c.localCaches[shardIndex].Set(key, entry, expirationTime); err != nil {
				c.logger.Warn("Failed to set in local cache", zap.Error(err), zap.String("key", key))
			} else {
				c.logger.Debug("Successfully set in local cache", zap.String("key", key))
			}
		}

		// Add to Bloom filter
		c.bloomFilter.Add(key)
		go func() {
			if err := c.bloomFilter.Save(ctx); err != nil {
				c.logger.Warn("Failed to save Bloom filter", zap.Error(err))
			}
		}()

		return entry, nil
	})

	if err != nil {
		c.logger.Error("Error in Set operation", zap.String("key", key), zap.Error(err))
		return fmt.Errorf("set operation failed: %w", err)
	}

	if shared {
		c.logger.Info("Set operation completed successfully with shared result",
			zap.String("key", key),
			zap.Bool("shared", shared))
	} else {
		c.logger.Info("Set operation completed successfully",
			zap.String("key", key),
			zap.Bool("shared", shared))
	}

	return nil
}

// Delete removes a key from the cache.
func (c *MultiCache) Delete(ctx context.Context, key string) error {
	ctx, span := c.tracer.Start(ctx, "MultiCache.Delete", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	_, err, _ := c.sf.Do(key, func() (any, error) {
		if c.config.EnableLocalCache {
			shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
			c.shards[shardIndex].Lock()
			defer c.shards[shardIndex].Unlock()
			c.localCaches[shardIndex].Delete(key)
		}

		if err := c.resilience.Delete(ctx, key); err != nil {
			return nil, fmt.Errorf("redis delete failed: %w", err)
		}

		c.accessCount.Delete(key)
		// Note: Bloom filter does not support deletion

		return nil, nil
	})

	return err
}

// Clear clears all cache entries.
func (c *MultiCache) Clear(ctx context.Context) error {
	// 開始追蹤 span
	ctx, span := c.tracer.Start(ctx, "MultiCache.Clear", trace.WithAttributes(attribute.Int("shardCount", len(c.shards))))
	defer span.End()

	// 清空本地快取
	if c.config.EnableLocalCache {
		for i, cache := range c.localCaches {
			c.shards[i].Lock()
			cache.Flush()
			c.shards[i].Unlock()
			c.logger.Info("Successfully flushed local cache shard", zap.Int("shard", i))
		}
	}

	// 清空遠端快取
	if err := c.resilience.Clear(ctx); err != nil {
		c.logger.Error("Failed to clear remote cache", zap.Error(err))
		span.RecordError(err)
		return fmt.Errorf("failed to clear remote cache: %w", err)
	}
	c.logger.Info("Successfully cleared remote cache")

	// 重建布隆過濾器
	if err := c.bloomFilter.Rebuild(ctx); err != nil {
		c.logger.Error("Failed to rebuild Bloom filter", zap.Error(err))
		span.RecordError(err)
		return fmt.Errorf("failed to rebuild Bloom filter: %w", err)
	}
	c.logger.Info("Successfully rebuilt Bloom filter")

	// 重置 accessCount
	c.accessCount = &sync.Map{}
	c.logger.Info("Successfully reset accessCount")

	// 重置其他相關的統計數據（如果有的話）
	// 例如：c.metrics.Reset()

	return nil
}

// GetMulti retrieves multiple keys from the cache.
func (c *MultiCache) GetMulti(ctx context.Context, keys []string) (map[string]any, error) {
	ctx, span := c.tracer.Start(ctx, "MultiCache.GetMulti", trace.WithAttributes(attribute.Int("keyCount", len(keys))))
	defer span.End()

	result := make(map[string]any)
	batchSize := 100 // Adjust as needed
	for i := 0; i < len(keys); i += batchSize {
		end := i + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]

		values, err := c.remoteCache.MGet(ctx, batch...).Result()
		if err != nil {
			return nil, fmt.Errorf("redis mget failed: %w", err)
		}

		for j, key := range batch {
			if values[j] != nil {
				dataStr, ok := values[j].(string)
				if !ok {
					c.logger.Error("Invalid type assertion for MGet value", zap.String("key", key))
					continue
				}
				data := []byte(dataStr)

				var decodedValue any
				if err := c.config.Serialization.Decoder(bytes.NewReader(data)).Decode(&decodedValue); err != nil {
					c.logger.Error("Failed to decode value", zap.Error(err), zap.String("key", key))
					continue
				}

				result[key] = decodedValue

				if c.config.EnableLocalCache {
					shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
					entry := &models.Entry{
						Data:           data,
						Expiration:     time.Now().Add(c.ttlManager.GetTTL(key)),
						AccessCount:    1,
						LastAccessTime: time.Now(),
					}
					if err := c.localCaches[shardIndex].Set(key, entry, c.ttlManager.GetTTL(key)); err != nil {
						c.logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
					}
				}

				c.incrementAccessCount(key, &models.Entry{
					AccessCount:    1,
					LastAccessTime: time.Now(),
				})
			}
		}
	}

	return result, nil
}

// SetMulti sets multiple keys in the cache.
func (c *MultiCache) SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error {
	pipe := c.remoteCache.Pipeline()
	localUpdates := make(map[string][]byte)

	for key, value := range items {
		buf := bytes.Buffer{}
		if err := c.config.Serialization.Encoder(&buf).Encode(value); err != nil {
			return fmt.Errorf("failed to encode value for key %s: %w", key, err)
		}

		data := buf.Bytes()
		expirationTime := c.ttlManager.GetTTL(key)
		if len(ttl) > 0 && ttl[0] > 0 {
			expirationTime = ttl[0]
		}

		entry := &models.Entry{
			Data:           data,
			Expiration:     time.Now().Add(expirationTime),
			AccessCount:    1,
			LastAccessTime: time.Now(),
		}

		pipe.Set(ctx, key, entry, expirationTime)
		localUpdates[key] = data
		c.bloomFilter.Add(key)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("redis mset failed: %w", err)
	}

	if c.config.EnableLocalCache {
		for key, data := range localUpdates {
			shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
			entry := &models.Entry{
				Data:           data,
				Expiration:     time.Now().Add(c.ttlManager.GetTTL(key)),
				AccessCount:    1,
				LastAccessTime: time.Now(),
			}
			if err := c.localCaches[shardIndex].Set(key, entry, c.ttlManager.GetTTL(key)); err != nil {
				c.logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
			}
			c.incrementAccessCount(key, entry)
		}
	}

	// Asynchronously save Bloom filter
	go func() {
		if err := c.bloomFilter.Save(ctx); err != nil {
			c.logger.Warn("Failed to save Bloom filter", zap.Error(err))
		}
	}()

	return nil
}

// GetFromRemoteCache retrieves data from the remote cache.
func (c *MultiCache) GetFromRemoteCache(ctx context.Context, key string) (*models.Entry, error) {
	shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
	cb := c.cbMap[shardIndex]

	res, err := cb.Execute(func() (interface{}, error) {
		var entry models.Entry
		err := c.remoteCache.Get(ctx, key).Scan(&entry)
		if errors.Is(err, redis.Nil) {
			return nil, models.ErrKeyNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("redis get failed: %w", err)
		}
		return &entry, nil
	})

	if err != nil {
		return nil, err
	}

	data, ok := res.(*models.Entry)
	if !ok {
		return nil, fmt.Errorf("invalid type assertion for remote cache value for key: %s", key)
	}

	return data, nil
}

// executeWithResilience executes a function with retry and circuit breaker.
func (c *MultiCache) executeWithResilience(ctx context.Context, fn func() error) error {
	return c.retrier.Run(ctx, fn)
}

// Close closes the MultiCache and all its local caches.
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

// incrementAccessCount increments the access count and updates LastAccessTime.
func (c *MultiCache) incrementAccessCount(key string, entry *models.Entry) {
	actual, loaded := c.accessCount.LoadOrStore(key, &atomic.Int64{})
	if loaded {
		count := actual.(*atomic.Int64)
		count.Add(1)
	} else {
		initialCount := &atomic.Int64{}
		initialCount.Store(1)
		c.accessCount.Store(key, initialCount)
	}

	// Update LastAccessTime
	c.updateLastAccessTime(key, entry.LastAccessTime)
}

// updateLastAccessTime updates the LastAccessTime for a key.
func (c *MultiCache) updateLastAccessTime(key string, lastAccessTime time.Time) {
	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].RLock()
		entry, found := c.localCaches[shardIndex].Get(key)
		c.shards[shardIndex].RUnlock()
		if found {
			entry.LastAccessTime = lastAccessTime
			if err := c.localCaches[shardIndex].Set(key, entry, entry.Expiration.Sub(time.Now())); err != nil {
				return
			}
		}
	}
}
