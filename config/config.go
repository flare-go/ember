package config

import (
	"github.com/sony/gobreaker"
	"go.uber.org/zap"
	"goflare.io/ember/serialization"
	"io"
	"runtime"
	"time"
)

// Config 用於 MultiLevelCache 的配置
// Config is the configuration for MultiLevelCache
type Config struct {
	EnableLocalCache  bool          // 是否啟用本地快取 (Enable local cache or not)
	MaxLocalSize      uint64        // 本地快取的最大大小 (Maximum size for local cache)
	ShardCount        uint64        // 本地快取的分片數量 (Number of shards for local cache)
	DefaultExpiration time.Duration // 預設的快取過期時間 (Default expiration time for cache)
	CleanupInterval   time.Duration // 清理過期項目的時間間隔 (Interval for cleaning up expired items)

	CacheBehaviorConfig CacheBehaviorConfig // 緩存行為相關配置 (Cache behavior-related configurations)
	ResilienceConfig    ResilienceConfig    // 重試和熔斷相關配置 (Retry and circuit breaker related configurations)
	Serialization       SerializationConfig // 序列化相關配置 (Serialization-related configurations)
	Logger              *zap.Logger         // 日誌記錄器 (Logger instance)
}

// CacheBehaviorConfig 緩存相關配置
// CacheBehaviorConfig contains cache behavior-related configurations
type CacheBehaviorConfig struct {
	EnablePrefetch      bool              // 是否啟用預取 (Enable prefetching or not)
	PrefetchThreshold   uint64            // 預取的訪問次數閾值 (Access count threshold for prefetching)
	PrefetchCount       uint64            // 預取的最大數量 (Maximum number of items to prefetch)
	WarmupKeys          []string          // 啟動時預加載的 key 列表 (List of keys to preload on startup)
	EnableAdaptiveTTL   bool              // 是否啟用自適應 TTL (Enable adaptive TTL or not)
	AdaptiveTTLSettings AdaptiveTTLConfig // 自適應 TTL 的配置 (Adaptive TTL settings)
	BloomFilterSettings BloomFilterConfig // 布隆過濾器的配置 (Bloom filter settings)
}

// ResilienceConfig 用於設置重試和熔斷器
// ResilienceConfig is for configuring retries and circuit breakers
type ResilienceConfig struct {
	GlobalCircuitBreaker gobreaker.Settings // 全局熔斷器的設置 (Global circuit breaker settings)
	KeyCircuitBreaker    gobreaker.Settings // 每個 key 熔斷器的設置 (Circuit breaker settings for each key)
	RetrierBackoff       []time.Duration    // 重試機制的退避時間間隔 (Backoff intervals for a retry mechanism)
}

// BloomFilterConfig 用於布隆過濾器的配置
// BloomFilterConfig is for configuring the Bloom filter
type BloomFilterConfig struct {
	ExpectedItems       uint          // 預期的項目數量 (Expected number of items)
	FalsePositiveRate   float64       // 假陽性率 (False positive rate)
	RebuildInterval     time.Duration // 布隆過濾器的重建間隔 (Interval for rebuilding the Bloom filter)
	BloomFilterRedisKey string        // 存儲布隆過濾器的 Redis 鍵名 (Redis key for storing the Bloom filter)
}

// AdaptiveTTLConfig 自適應 TTL 配置
// AdaptiveTTLConfig is the configuration for adaptive TTL
type AdaptiveTTLConfig struct {
	MinTTL            time.Duration // 最小 TTL (Minimum TTL)
	MaxTTL            time.Duration // 最大 TTL (Maximum TTL)
	TTLAdjustInterval time.Duration // 自適應 TTL 調整的時間間隔 (Interval for adjusting adaptive TTL)
}

// LoggingConfig 日誌相關配置
// LoggingConfig is for logging-related configurations
type LoggingConfig struct {
	Logger *zap.Logger // 日誌記錄器 (Logger instance)
}

// SerializationConfig 序列化相關配置
// SerializationConfig is for serialization-related configurations
type SerializationConfig struct {
	Type    string                                // 序列化類型 (Serialization type)
	Encoder func(io.Writer) serialization.Encoder // 編碼器函數 (Encoder function)
	Decoder func(io.Reader) serialization.Decoder // 解碼器函數 (Decoder function)
}

func NewConfig() Config {

	maxLocalSize := 1000 * 1024 * 1024 // 1GB
	avgItemSize := 1024                // 假設每個緩存項大小為1KB
	cpuCores := runtime.NumCPU()

	shardCount := CalculateDynamicShardCount(uint64(maxLocalSize), uint64(avgItemSize))
	if shardCount > uint64(cpuCores*4) { // 不能超過 CPU 核心數的 4 倍
		shardCount = uint64(cpuCores * 4)
	}

	return Config{
		EnableLocalCache:  true,
		MaxLocalSize:      uint64(maxLocalSize),
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
			GlobalCircuitBreaker: gobreaker.Settings{},
			KeyCircuitBreaker:    gobreaker.Settings{},
			RetrierBackoff:       []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond},
		},
		Serialization: SerializationConfig{
			Type:    serialization.JsonType,
			Encoder: serialization.JsonEncoder,
			Decoder: serialization.JsonDecoder,
		},
		Logger: zap.NewNop(),
	}
}

func CalculateDynamicShardCount(maxSize, avgItemSize uint64) uint64 {
	if avgItemSize == 0 {
		avgItemSize = 1024 // 默認假設每個緩存項的平均大小為1KB
	}

	memStats := &runtime.MemStats{}
	runtime.ReadMemStats(memStats)

	availableMemory := memStats.Sys // 可以根據實際需要調整這裡的邏輯
	shards := maxSize / (avgItemSize * 1024)
	if shards > availableMemory/(avgItemSize*1024) {
		shards = availableMemory / (avgItemSize * 1024)
	}

	if shards < 1 {
		shards = 1
	}
	return shards
}
