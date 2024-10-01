package config

import (
	"errors"
	"github.com/redis/go-redis/v9"
	"io"
	"math"
	"runtime"
	"time"

	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"goflare.io/ember/pkg/serialization"
)

// Config 包含缓存系统的配置选项
type Config struct {
	EnableLocalCache  bool
	MaxLocalSize      uint64
	ShardCount        uint64
	DefaultExpiration time.Duration
	CleanupInterval   time.Duration

	CacheBehaviorConfig CacheBehaviorConfig
	ResilienceConfig    ResilienceConfig
	Serialization       SerializationConfig
	RedisOptions        *redis.Options
	Logger              *zap.Logger
}

// CacheBehaviorConfig encapsulates configuration settings specific to cache behavior,
// including prefetching and TTL management.
type CacheBehaviorConfig struct {
	EnablePrefetch      bool
	PrefetchThreshold   uint64
	PrefetchCount       uint64
	PrefetchInterval    time.Duration
	WarmupKeys          []string
	EnableAdaptiveTTL   bool
	AdaptiveTTLSettings AdaptiveTTLConfig
	BloomFilterSettings BloomFilterConfig
}

// ResilienceConfig configures resilience mechanisms like circuit breakers and retry backoff.
type ResilienceConfig struct {
	GlobalCircuitBreaker gobreaker.Settings
	KeyCircuitBreaker    gobreaker.Settings
	RetrierBackoff       []time.Duration
	MaxRetries           int
	InitialInterval      time.Duration
	MaxInterval          time.Duration
	Multiplier           float64
	RandomizationFactor  float64
}

// BloomFilterConfig represents the configuration for a bloom filter.
type BloomFilterConfig struct {
	ExpectedItems       uint
	FalsePositiveRate   float64
	RebuildInterval     time.Duration
	BloomFilterRedisKey string
}

// AdaptiveTTLConfig defines a configuration for adaptive TTL (Time-To-Live) adjustments for cached items.
// It includes parameters for minimum and maximum TTL durations and the interval at which the TTL is adjusted.
type AdaptiveTTLConfig struct {
	MinTTL            time.Duration
	MaxTTL            time.Duration
	TTLAdjustInterval time.Duration
}

// SerializationConfig holds the configuration for serialization,
// including the type, encoder function, and decoder function.
type SerializationConfig struct {
	Type    string
	Encoder func(io.Writer) serialization.Encoder
	Decoder func(io.Reader) serialization.Decoder
}

// Option is a function type that takes a pointer to Config and returns an error. It is used for configuring instances.
type Option func(*Config) error

// ErrShardCountZero indicates an error condition where the shard count is zero, which is not allowed.
var (
	ErrShardCountZero = errors.New("shard count must be at least 1")
)

// NewConfig initializes a new Config object with default values and applies optional configuration options.
func NewConfig(options ...Option) (*Config, error) {
	defaultLogger, err := zap.NewProduction()
	if err != nil {
		return nil, err
	}

	maxLocalSize := uint64(1 * 1024 * 1024 * 1024) // 1GB
	avgItemSize := uint64(1024)                    // 1KB
	shardCount := CalculateDynamicShardCount(maxLocalSize, avgItemSize)
	if shardCount == 0 {
		shardCount = 1
	}

	cfg := &Config{
		EnableLocalCache:  true,
		MaxLocalSize:      maxLocalSize,
		ShardCount:        shardCount,
		DefaultExpiration: 5 * time.Minute,
		CleanupInterval:   10 * time.Minute,
		CacheBehaviorConfig: CacheBehaviorConfig{
			EnablePrefetch:    true,
			PrefetchThreshold: 100,
			PrefetchCount:     10,
			EnableAdaptiveTTL: true,
			AdaptiveTTLSettings: AdaptiveTTLConfig{
				MinTTL:            1 * time.Minute,
				MaxTTL:            10 * time.Minute,
				TTLAdjustInterval: 5 * time.Minute,
			},
			BloomFilterSettings: BloomFilterConfig{
				ExpectedItems:       1000,
				FalsePositiveRate:   0.01,
				RebuildInterval:     1 * time.Hour,
				BloomFilterRedisKey: "bloom_filter_key",
			},
			PrefetchInterval: 5 * time.Minute,
		},
		RedisOptions: nil,
		ResilienceConfig: ResilienceConfig{
			GlobalCircuitBreaker: gobreaker.Settings{
				Name:        "GlobalCircuitBreaker",
				MaxRequests: 3,
				Interval:    60 * time.Second,
				Timeout:     30 * time.Second,
				ReadyToTrip: func(counts gobreaker.Counts) bool {
					return counts.ConsecutiveFailures > 5
				},
			},
			KeyCircuitBreaker: gobreaker.Settings{
				Name:        "KeyCircuitBreaker",
				MaxRequests: 3,
				Interval:    60 * time.Second,
				Timeout:     30 * time.Second,
				ReadyToTrip: func(counts gobreaker.Counts) bool {
					return counts.ConsecutiveFailures > 3
				},
			},
			RetrierBackoff: []time.Duration{
				100 * time.Millisecond,
				200 * time.Millisecond,
				400 * time.Millisecond,
			},
			MaxRetries:          3,
			InitialInterval:     100 * time.Millisecond,
			MaxInterval:         10 * time.Second,
			Multiplier:          2.0,
			RandomizationFactor: 0.5,
		},
		Serialization: SerializationConfig{
			Type:    serialization.JSONType,
			Encoder: serialization.JSONEncoder,
			Decoder: serialization.JSONDecoder,
		},
		Logger: defaultLogger,
	}

	// 應用所有選項
	for _, option := range options {
		if err := option(cfg); err != nil {
			return nil, err
		}
	}

	// 最終檢查
	if cfg.ShardCount == 0 {
		return nil, ErrShardCountZero
	}

	return cfg, nil
}

// Option 函數示例

// WithLogger sets the logger instance for the configuration.
// If the provided logger is non-nil, it assigns it to the Logger field in the Config structure.
func WithLogger(logger *zap.Logger) Option {
	return func(c *Config) error {
		if logger != nil {
			c.Logger = logger
		}
		return nil
	}
}

// WithMaxLocalSize sets the maximum size for the local cache,
// dynamically calculates shard count, and returns an Option.
func WithMaxLocalSize(size uint64) Option {
	return func(c *Config) error {
		if size == 0 {
			return errors.New("max local size must be greater than 0")
		}
		c.MaxLocalSize = size
		c.ShardCount = CalculateDynamicShardCount(c.MaxLocalSize, 1024)
		if c.ShardCount == 0 {
			c.ShardCount = 1
		}
		return nil
	}
}

// WithShardCount sets the number of shards for the cache and returns an Option. It requires a count greater than 0.
func WithShardCount(count uint64) Option {
	return func(c *Config) error {
		if count == 0 {
			return errors.New("shard count must be greater than 0")
		}
		c.ShardCount = count
		return nil
	}
}

// WithResilienceConfig sets the ResilienceConfig in the Config struct and returns an Option.
func WithResilienceConfig(rc ResilienceConfig) Option {
	return func(c *Config) error {
		c.ResilienceConfig = rc
		return nil
	}
}

// WithPrefetchInterval sets the prefetch interval for the cache, controlling how often prefetching should occur.
func WithPrefetchInterval(interval time.Duration) Option {
	return func(c *Config) error {
		c.CacheBehaviorConfig.PrefetchInterval = interval
		return nil
	}
}

// CalculateDynamicShardCount dynamically calculates the number of shards needed based on maxSize and avgItemSize.
func CalculateDynamicShardCount(maxSize, avgItemSize uint64) uint64 {
	if avgItemSize == 0 {
		avgItemSize = 1024 // 默認假設每個緩存項的平均大小為1KB
	}

	// 使用 runtime 處理器數量和系統可用內存的合理估計
	cpuCores := runtime.NumCPU()
	// 假設每個 shard 管理 100 個項目
	shards := maxSize / (avgItemSize * 100)
	if shards == 0 {
		shards = 1
	}

	// 限制分片數量不超過 CPU 核心數的 4 倍
	maxShards := uint64(math.Min(float64(cpuCores*4), float64(math.MaxUint64)))
	if shards > maxShards {
		shards = maxShards
	}

	return shards
}
