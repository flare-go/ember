package multi_cache

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bits-and-blooms/bloom/v3"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// BloomFilter 定義布隆過濾器的結構體
type BloomFilter struct {
	mlc    *MultiCache
	logger *zap.Logger
	mutex  sync.Mutex
}

// NewBloomFilter 創建一個新的 BloomFilter 實例
func NewBloomFilter(mlc *MultiCache) *BloomFilter {
	return &BloomFilter{
		mlc:    mlc,
		logger: mlc.logger,
	}
}

// Add 添加一個鍵到布隆過濾器
func (bf *BloomFilter) Add(key string) {
	bf.mlc.filter.Add([]byte(key))
}

// Test 測試一個鍵是否存在於布隆過濾器中
func (bf *BloomFilter) Test(key string) bool {
	return bf.mlc.filter.Test([]byte(key))
}

// Save 保存布隆過濾器到遠端快取
func (bf *BloomFilter) Save(ctx context.Context) error {
	bf.mutex.Lock()
	defer bf.mutex.Unlock()

	var buf bytes.Buffer
	if _, err := bf.mlc.filter.WriteTo(&buf); err != nil {
		bf.logger.Error("Failed to serialize bloom filter", zap.Error(err))
		return err
	}

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	if err := bf.mlc.executeWithResilience(ctx, func() error {
		return bf.mlc.remoteCache.Set(ctx, bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.BloomFilterRedisKey, encoded, 0).Err()
	}); err != nil {
		bf.logger.Error("Failed to save bloom filter to Redis", zap.Error(err))
		return err
	}

	return nil
}

// Load 載入布隆過濾器
func (bf *BloomFilter) Load(ctx context.Context) error {
	bf.mutex.Lock()
	defer bf.mutex.Unlock()

	var encoded string
	if err := bf.mlc.executeWithResilience(ctx, func() error {
		var err error
		encoded, err = bf.mlc.remoteCache.Get(ctx, bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.BloomFilterRedisKey).Result()
		return err
	}); err != nil {
		if errors.Is(err, redis.Nil) {
			bf.logger.Warn("Bloom filter key not found in Redis, initiating rebuild")
			return bf.Rebuild(ctx)
		}
		return fmt.Errorf("failed to get Bloom filter from Redis: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		bf.logger.Error("Failed to decode Bloom filter", zap.Error(err))
		return err
	}

	newFilter := bloom.NewWithEstimates(
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate,
	)

	if _, err := newFilter.ReadFrom(bytes.NewReader(decoded)); err != nil {
		bf.logger.Error("Failed to deserialize Bloom filter", zap.Error(err))
		return err
	}

	bf.mlc.filter = newFilter
	bf.logger.Info("Bloom filter loaded successfully")
	return nil
}

// Rebuild 重建布隆過濾器
func (bf *BloomFilter) Rebuild(ctx context.Context) error {
	bf.logger.Info("Starting Bloom filter rebuild")

	// 確保只有一個 goroutine 在執行重建
	bf.mlc.filterRebuildMu.Lock()
	defer bf.mlc.filterRebuildMu.Unlock()

	newFilter := bloom.NewWithEstimates(
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate,
	)

	var cursor uint64
	for {
		// 使用 SCAN 分批掃描鍵
		keys, newCursor, err := bf.mlc.remoteCache.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			bf.logger.Error("Failed to scan Redis keys during Bloom filter rebuild", zap.Error(err))
			return err
		}

		for _, key := range keys {
			newFilter.Add([]byte(key))
		}

		cursor = newCursor
		if cursor == 0 {
			break
		}
	}

	// 替換現有的 Bloom filter
	bf.mlc.filter = newFilter

	// 保存新的 Bloom filter
	if err := bf.Save(ctx); err != nil {
		bf.logger.Error("Failed to save new Bloom filter after rebuild", zap.Error(err))
		return err
	}

	bf.logger.Info("Bloom filter rebuild completed successfully")
	return nil
}

// PeriodicRebuild 定期重建布隆過濾器
func (bf *BloomFilter) PeriodicRebuild(ctx context.Context) {
	ticker := time.NewTicker(bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.RebuildInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := bf.Rebuild(ctx); err != nil {
				bf.logger.Warn("Bloom filter rebuild failed", zap.Error(err))
			}
		case <-ctx.Done():
			bf.logger.Info("Stopping Bloom filter periodic rebuild due to context cancellation")
			return
		}
	}
}
