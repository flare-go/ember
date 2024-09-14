package config

import (
	"errors"
	"io"
	"runtime"
	"time"

	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"goflare.io/ember/pkg/serialization"
)

// Config 用於 MultiLevelCache 的配置
type Config struct {
	EnableLocalCache  bool
	MaxLocalSize      uint64
	ShardCount        uint64
	DefaultExpiration time.Duration
	CleanupInterval   time.Duration

	CacheBehaviorConfig CacheBehaviorConfig
	ResilienceConfig    ResilienceConfig
	Serialization       SerializationConfig
	Logger              *zap.Logger
}

// CacheBehaviorConfig 緩存相關配置
type CacheBehaviorConfig struct {
	EnablePrefetch      bool
	PrefetchThreshold   uint64
	PrefetchCount       uint64
	WarmupKeys          []string
	EnableAdaptiveTTL   bool
	AdaptiveTTLSettings AdaptiveTTLConfig
	BloomFilterSettings BloomFilterConfig
}

// ResilienceConfig 用於設置重試和熔斷器
type ResilienceConfig struct {
	GlobalCircuitBreaker gobreaker.Settings
	KeyCircuitBreaker    gobreaker.Settings
	RetrierBackoff       []time.Duration
}

// BloomFilterConfig 用於布隆過濾器的配置
type BloomFilterConfig struct {
	ExpectedItems       uint
	FalsePositiveRate   float64
	RebuildInterval     time.Duration
	BloomFilterRedisKey string
}

// AdaptiveTTLConfig 自適應 TTL 配置
type AdaptiveTTLConfig struct {
	MinTTL            time.Duration
	MaxTTL            time.Duration
	TTLAdjustInterval time.Duration
}

// SerializationConfig 序列化相關配置
type SerializationConfig struct {
	Type    string
	Encoder func(io.Writer) serialization.Encoder
	Decoder func(io.Reader) serialization.Decoder
}

// Option 函數類型
type Option func(*Config) error

var (
	ErrShardCountZero = errors.New("shard count must be at least 1")
)

// NewConfig 創建一個默認的 Config，允許覆蓋特定參數
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
		},
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
		},
		Serialization: SerializationConfig{
			Type:    serialization.JsonType,
			Encoder: serialization.JsonEncoder,
			Decoder: serialization.JsonDecoder,
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

// WithLogger 設置自定義 Logger
func WithLogger(logger *zap.Logger) Option {
	return func(c *Config) error {
		if logger != nil {
			c.Logger = logger
		}
		return nil
	}
}

// WithMaxLocalSize 設置本地快取的最大大小
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

// WithShardCount 設置分片數量
func WithShardCount(count uint64) Option {
	return func(c *Config) error {
		if count == 0 {
			return errors.New("shard count must be greater than 0")
		}
		c.ShardCount = count
		return nil
	}
}

// CalculateDynamicShardCount 計算動態分片數量
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
	if shards > uint64(cpuCores*4) {
		shards = uint64(cpuCores * 4)
	}

	return shards
}
