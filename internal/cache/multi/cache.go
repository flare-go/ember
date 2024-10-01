// Package multi implements a multi-level caching system with local and remote caches.
package multi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"goflare.io/ember/internal/cache/limited"
	"goflare.io/ember/internal/config"
	"goflare.io/ember/internal/models"
	"goflare.io/ember/internal/retrier"
	"goflare.io/ember/internal/utils"
)

// CacheOperations defines the interface for cache operations.
type CacheOperations interface {
	Get(ctx context.Context, key string, value any) (bool, error)
	Set(ctx context.Context, key string, value any, ttl ...time.Duration) error
	Delete(ctx context.Context, key string) error
	Clear(ctx context.Context) error
	GetMulti(ctx context.Context, keys []string) (map[string]any, error)
	SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error
	Close() error
}

// Cache represents a multi-layer cache system with local and remote caches.
type Cache struct {
	localCaches []*limited.Cache
	config      *config.Config
	retrier     *retrier.Retrier

	accessCount     *sync.Map
	shards          []sync.RWMutex
	filterRebuildMu sync.Mutex
	sf              *singleflight.Group

	remoteCache redis.Cmdable
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

// NewCache creates a new Cache instance.
func NewCache(ctx context.Context, cfg *config.Config, client redis.Cmdable) (CacheOperations, error) {
	localCaches, err := initializeLocalCaches(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize local caches: %w", err)
	}

	filter := bloom.NewWithEstimates(
		cfg.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		cfg.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate,
	)

	globalCB := gobreaker.NewCircuitBreaker(cfg.ResilienceConfig.GlobalCircuitBreaker)

	r, err := retrier.NewRetrier(
		cfg.ResilienceConfig.MaxRetries,
		cfg.ResilienceConfig.InitialInterval,
		cfg.ResilienceConfig.MaxInterval,
		cfg.ResilienceConfig.Multiplier,
		cfg.ResilienceConfig.RandomizationFactor,
		retrier.ExponentialBackoff,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create retrier: %w", err)
	}

	mlc := &Cache{
		localCaches: localCaches,
		remoteCache: client,
		filter:      filter,
		config:      cfg,
		globalCB:    globalCB,
		retrier:     r,

		tracer: otel.Tracer("cache"),

		accessCount:     &sync.Map{},
		sf:              &singleflight.Group{},
		shards:          make([]sync.RWMutex, cfg.ShardCount),
		filterRebuildMu: sync.Mutex{},
		cbMap:           make(map[uint64]*gobreaker.CircuitBreaker),
		logger:          cfg.Logger,
	}

	for i := uint64(0); i < cfg.ShardCount; i++ {
		mlc.shards[i] = sync.RWMutex{}
		mlc.cbMap[i] = gobreaker.NewCircuitBreaker(cfg.ResilienceConfig.KeyCircuitBreaker)
	}

	mlc.bloomFilter = NewBloomFilter(mlc)
	mlc.ttlManager = NewTTLManager(mlc)
	mlc.prefetcher = NewPrefetcher(mlc)
	mlc.resilience = NewResilience(mlc)

	if err := mlc.bloomFilter.Load(ctx); err != nil {
		return nil, fmt.Errorf("failed to load Bloom filter: %w", err)
	}

	go mlc.bloomFilter.PeriodicRebuild(ctx)
	go mlc.ttlManager.Run(ctx)
	go mlc.prefetcher.Run(ctx)

	if cfg.CacheBehaviorConfig.EnablePrefetch {
		mlc.prefetcher.Warmup(ctx)
	}

	return mlc, nil
}

func initializeLocalCaches(cfg *config.Config) ([]*limited.Cache, error) {
	if !cfg.EnableLocalCache {
		return nil, nil
	}

	localCaches := make([]*limited.Cache, cfg.ShardCount)
	for i := uint64(0); i < cfg.ShardCount; i++ {
		lc, err := limited.New(cfg.MaxLocalSize/cfg.ShardCount, cfg.DefaultExpiration, cfg.Logger)
		if err != nil {
			for j := uint64(0); j < i; j++ {
				if localCaches[j] != nil {
					if err = localCaches[j].Close(); err != nil {
						return nil, err
					}
				}
			}
			return nil, fmt.Errorf("failed to create local cache shard %d: %w", i, err)
		}
		localCaches[i] = lc
	}
	return localCaches, nil
}

// Set sets a value in the cache.
func (c *Cache) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	ctx, span := c.tracer.Start(ctx, "Cache.Set", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	expirationTime := c.ttlManager.GetTTL(key)
	if len(ttl) > 0 && ttl[0] > 0 {
		expirationTime = ttl[0]
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	entry := models.NewEntry(data, time.Now().Add(expirationTime))

	if err := c.resilience.Set(ctx, key, entry, expirationTime); err != nil {
		return err
	}

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].Lock()
		err = c.localCaches[shardIndex].Set(ctx, key, entry, time.Until(entry.Expiration))
		c.shards[shardIndex].Unlock()
		if err != nil {
			c.logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
		}
	}

	c.bloomFilter.Add(key)
	go c.bloomFilter.Save(ctx)

	return nil
}

// Get retrieves a value from the cache.
func (c *Cache) Get(ctx context.Context, key string, value any) (bool, error) {
	ctx, span := c.tracer.Start(ctx, "Cache.Get", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	if !c.bloomFilter.Test(key) {
		c.logger.Debug("Bloom filter negative for key", zap.String("key", key))
		return false, nil
	}

	if c.config.EnableLocalCache {
		found, err := c.getFromLocalCache(ctx, key, value)
		if found || err != nil {
			return found, err
		}
	}

	return c.getFromRemoteCache(ctx, key, value)
}

func (c *Cache) getFromLocalCache(ctx context.Context, key string, value any) (bool, error) {
	shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
	c.shards[shardIndex].RLock()
	entry := models.NewEntry(nil, time.Now())
	found, err := c.localCaches[shardIndex].Get(ctx, key, entry)
	c.shards[shardIndex].RUnlock()

	if !found {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if entry.IsExpired() {
		if err = c.localCaches[shardIndex].Delete(ctx, key); err != nil {
			return false, err
		}
		return false, nil
	}

	if err := json.Unmarshal(entry.Data, value); err != nil {
		c.logger.Error("Failed to unmarshal value from local cache", zap.Error(err), zap.String("key", key))
		return false, err
	}

	entry.IncrementAccess()
	return true, nil
}

func (c *Cache) getFromRemoteCache(ctx context.Context, key string, value any) (bool, error) {
	v, err, _ := c.sf.Do(key, func() (interface{}, error) {
		var entry models.Entry
		err := c.resilience.Get(ctx, key, &entry)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil, nil
			}
			return nil, err
		}
		return &entry, nil
	})

	if err != nil {
		return false, err
	}

	if v == nil {
		return false, nil
	}

	entry := v.(*models.Entry)
	if err := json.Unmarshal(entry.Data, value); err != nil {
		return false, err
	}

	if c.config.EnableLocalCache {
		c.setLocalCache(ctx, key, entry)
	}

	entry.IncrementAccess()
	return true, nil
}

func (c *Cache) setLocalCache(ctx context.Context, key string, entry *models.Entry) {
	shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
	c.shards[shardIndex].Lock()
	err := c.localCaches[shardIndex].Set(ctx, key, entry, time.Until(entry.Expiration))
	c.shards[shardIndex].Unlock()
	if err != nil {
		c.logger.Warn("Failed to set local cache", zap.Error(err), zap.String("key", key))
	}
}

// Delete removes a key from the cache.
func (c *Cache) Delete(ctx context.Context, key string) error {
	ctx, span := c.tracer.Start(ctx, "Cache.Delete", trace.WithAttributes(attribute.String("key", key)))
	defer span.End()

	if c.config.EnableLocalCache {
		shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
		c.shards[shardIndex].Lock()
		if err := c.localCaches[shardIndex].Delete(ctx, key); err != nil {
			c.shards[shardIndex].Unlock()
			return fmt.Errorf("failed to delete key from local cache: %w", err)
		}
		c.shards[shardIndex].Unlock()
	}

	if err := c.resilience.Delete(ctx, key); err != nil {
		return err
	}

	c.accessCount.Delete(key)
	return nil
}

// Clear clears all cache entries.
func (c *Cache) Clear(ctx context.Context) error {
	ctx, span := c.tracer.Start(ctx, "Cache.Clear")
	defer span.End()

	if c.config.EnableLocalCache {
		for i, cache := range c.localCaches {
			c.shards[i].Lock()
			cache.Flush(ctx)
			c.shards[i].Unlock()
		}
	}

	if err := c.resilience.Clear(ctx); err != nil {
		return err
	}

	if err := c.bloomFilter.Rebuild(ctx); err != nil {
		return err
	}

	c.accessCount = &sync.Map{}
	return nil
}

// GetMulti retrieves multiple keys from the cache.
func (c *Cache) GetMulti(ctx context.Context, keys []string) (map[string]any, error) {
	ctx, span := c.tracer.Start(ctx, "Cache.GetMulti", trace.WithAttributes(attribute.Int("keyCount", len(keys))))
	defer span.End()

	result := make(map[string]any)
	missingKeys := make([]string, 0, len(keys))

	if c.config.EnableLocalCache {
		for _, key := range keys {
			value, found, err := c.getFromLocalCacheMulti(ctx, key)
			if err != nil {
				continue
			}
			if found {
				result[key] = value
			} else {
				missingKeys = append(missingKeys, key)
			}
		}
	} else {
		missingKeys = keys
	}

	if len(missingKeys) > 0 {
		remoteResults, err := c.resilience.GetMulti(ctx, missingKeys)
		if err != nil {
			return result, err
		}

		for key, entry := range remoteResults {
			var value any
			if err := json.Unmarshal(entry.Data, &value); err != nil {
				c.logger.Error("Failed to unmarshal value from remote cache", zap.Error(err), zap.String("key", key))
				continue
			}
			result[key] = value
			c.incrementAccessCount(key, entry)

			if c.config.EnableLocalCache {
				c.setLocalCache(ctx, key, entry)
			}
		}
	}

	return result, nil
}

func (c *Cache) getFromLocalCacheMulti(ctx context.Context, key string) (any, bool, error) {
	shardIndex := utils.ShardIndex(uint64(len(c.shards)), key)
	c.shards[shardIndex].RLock()
	entry := models.NewEntry(nil, time.Now())
	found, err := c.localCaches[shardIndex].Get(ctx, key, entry)
	c.shards[shardIndex].RUnlock()
	if !found {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var value any
	if err := json.Unmarshal(entry.Data, &value); err != nil {
		c.logger.Error("Failed to unmarshal value from local cache", zap.Error(err), zap.String("key", key))
		return nil, false, err
	}
	c.incrementAccessCount(key, entry)
	return value, true, nil
}

// SetMulti sets multiple keys in the cache.
func (c *Cache) SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error {
	ctx, span := c.tracer.Start(ctx, "Cache.SetMulti", trace.WithAttributes(attribute.Int("itemCount", len(items))))
	defer span.End()

	expirationTime := c.config.DefaultExpiration
	if len(ttl) > 0 && ttl[0] > 0 {
		expirationTime = ttl[0]
	}

	entries := make(map[string]*models.Entry, len(items))
	for key, value := range items {
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("failed to marshal value for key %s: %w", key, err)
		}

		entries[key] = models.NewEntry(data, time.Now().Add(expirationTime))
	}

	if err := c.resilience.SetMulti(ctx, entries, expirationTime); err != nil {
		return err
	}

	if c.config.EnableLocalCache {
		for key, entry := range entries {
			c.setLocalCache(ctx, key, entry)
		}
	}

	for key := range entries {
		c.bloomFilter.Add(key)
	}
	go c.bloomFilter.Save(ctx)

	return nil
}

// Close closes all local and remote caches associated with the Cache instance, returning an error if any occur.
func (c *Cache) Close() error {
	c.logger.Info("Closing Cache")

	var errs []error

	if c.config.EnableLocalCache {
		for i, cache := range c.localCaches {
			c.shards[i].Lock()
			if err := cache.Close(); err != nil {
				errs = append(errs, fmt.Errorf("failed to close local cache shard %d: %w", i, err))
			}
			c.shards[i].Unlock()
		}
	}

	if closer, ok := c.remoteCache.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close remote cache connection: %w", err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors occurred while closing Cache: %v", errs)
	}

	return nil
}

// incrementAccessCount increments the access count for a key.
func (c *Cache) incrementAccessCount(key string, entry *models.Entry) {
	entry.IncrementAccess()
	c.accessCount.Store(key, entry.AccessCount)
}
