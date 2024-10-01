// Package ember provides a flexible, multi-layer caching solution.
package ember

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"goflare.io/ember/internal/cache/limited"
	"goflare.io/ember/internal/cache/multi"
	"goflare.io/ember/internal/config"
	"goflare.io/ember/pkg/serialization"

	"github.com/redis/go-redis/v9"
)

// CacheOperations defines the interface for cache operations.
type CacheOperations interface {
	Set(ctx context.Context, key string, value any, ttl ...time.Duration) error
	Get(ctx context.Context, key string, value any) (bool, error)
	Delete(ctx context.Context, key string) error
	Clear(ctx context.Context) error
	GetMulti(ctx context.Context, keys []string) (map[string]any, error)
	SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error
	Close() error
}

// Option defines a function type for configuring Ember.
type Option func(*config.Config) error

// Ember represents the main structure of the Ember library.
type Ember struct {
	cache  CacheOperations
	logger *zap.Logger
	config *config.Config
}

// New initializes the Ember library with the provided options.
func New(ctx context.Context, opts ...Option) (*Ember, error) {
	cfg, err := config.NewConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create config: %w", err)
	}

	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	if cfg.Logger == nil {
		logger, err := zap.NewProduction()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize default logger: %w", err)
		}
		cfg.Logger = logger
	}

	var cache CacheOperations
	if cfg.RedisOptions != nil {
		redisClient := redis.NewClient(cfg.RedisOptions)
		if err := redisClient.Ping(ctx).Err(); err != nil {
			return nil, fmt.Errorf("failed to connect to Redis: %w", err)
		}
		cache, err = multi.NewCache(ctx, cfg, redisClient)
	} else {
		cache, err = limited.New(cfg.MaxLocalSize, cfg.DefaultExpiration, cfg.Logger)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize cache: %w", err)
	}

	return &Ember{
		cache:  cache,
		logger: cfg.Logger,
		config: cfg,
	}, nil
}

// Set sets a cache item.
func (e *Ember) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	return e.cache.Set(ctx, key, value, ttl...)
}

// Get retrieves a cache item.
func (e *Ember) Get(ctx context.Context, key string, value any) (bool, error) {
	return e.cache.Get(ctx, key, value)
}

// Delete removes a cache item.
func (e *Ember) Delete(ctx context.Context, key string) error {
	return e.cache.Delete(ctx, key)
}

// Clear removes all cache items.
func (e *Ember) Clear(ctx context.Context) error {
	return e.cache.Clear(ctx)
}

// GetMulti retrieves multiple cache items.
func (e *Ember) GetMulti(ctx context.Context, keys []string) (map[string]any, error) {
	return e.cache.GetMulti(ctx, keys)
}

// SetMulti sets multiple cache items.
func (e *Ember) SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error {
	return e.cache.SetMulti(ctx, items, ttl...)
}

// GetStats returns cache usage statistics if available.
func (e *Ember) GetStats() map[string]any {
	if stats, ok := e.cache.(interface{ Stats() map[string]any }); ok {
		return stats.Stats()
	}
	return nil
}

// Close closes the Ember library and releases resources.
func (e *Ember) Close() error {
	return e.cache.Close()
}

// Configuration options

// WithLogger sets a custom logger.
func WithLogger(logger *zap.Logger) Option {
	return func(cfg *config.Config) error {
		cfg.Logger = logger
		return nil
	}
}

// WithMaxLocalSize sets the maximum size for the local cache.
func WithMaxLocalSize(maxSize uint64) Option {
	return func(cfg *config.Config) error {
		cfg.MaxLocalSize = maxSize
		return nil
	}
}

// WithShardCount sets the number of shards for the local cache.
func WithShardCount(shardCount uint64) Option {
	return func(cfg *config.Config) error {
		cfg.ShardCount = shardCount
		return nil
	}
}

// WithDefaultExpiration sets the default expiration time for cache items.
func WithDefaultExpiration(ttl time.Duration) Option {
	return func(cfg *config.Config) error {
		cfg.DefaultExpiration = ttl
		return nil
	}
}

// WithRedis sets Redis options for distributed caching.
func WithRedis(options *redis.Options) Option {
	return func(cfg *config.Config) error {
		cfg.RedisOptions = options
		return nil
	}
}

// WithSerializer sets custom serialization functions.
func WithSerializer(encodeType string) Option {
	return func(cfg *config.Config) error {
		if encodeType == serialization.GobType {
			cfg.Serialization.Encoder = serialization.GobEncoder
			cfg.Serialization.Decoder = serialization.GobDecoder
		} else {
			cfg.Serialization.Encoder = serialization.JSONEncoder
			cfg.Serialization.Decoder = serialization.JSONDecoder
		}

		return nil
	}
}

// WithBloomFilter configures the Bloom filter settings.
func WithBloomFilter(expectedItems uint, falsePositiveRate float64) Option {
	return func(cfg *config.Config) error {
		cfg.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems = expectedItems
		cfg.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate = falsePositiveRate
		return nil
	}
}

// WithPrefetch configures the prefetch behavior.
func WithPrefetch(enable bool, threshold, count uint64) Option {
	return func(cfg *config.Config) error {
		cfg.CacheBehaviorConfig.EnablePrefetch = enable
		cfg.CacheBehaviorConfig.PrefetchThreshold = threshold
		cfg.CacheBehaviorConfig.PrefetchCount = count
		return nil
	}
}

// WithAdaptiveTTL configures adaptive TTL settings.
func WithAdaptiveTTL(enable bool, minTTL, maxTTL, adjustInterval time.Duration) Option {
	return func(cfg *config.Config) error {
		cfg.CacheBehaviorConfig.EnableAdaptiveTTL = enable
		cfg.CacheBehaviorConfig.AdaptiveTTLSettings.MinTTL = minTTL
		cfg.CacheBehaviorConfig.AdaptiveTTLSettings.MaxTTL = maxTTL
		cfg.CacheBehaviorConfig.AdaptiveTTLSettings.TTLAdjustInterval = adjustInterval
		return nil
	}
}

// WithCircuitBreaker configures circuit breaker settings.
func WithCircuitBreaker(maxRequests uint32, interval, timeout time.Duration) Option {
	return func(cfg *config.Config) error {
		cfg.ResilienceConfig.GlobalCircuitBreaker.MaxRequests = maxRequests
		cfg.ResilienceConfig.GlobalCircuitBreaker.Interval = interval
		cfg.ResilienceConfig.GlobalCircuitBreaker.Timeout = timeout
		return nil
	}
}
