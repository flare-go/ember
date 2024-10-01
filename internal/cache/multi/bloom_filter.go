package multi

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/bits-and-blooms/bloom/v3"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"goflare.io/ember/internal/models"
	"sync"
	"time"
)

// BloomFilter defines the bloom filter structure.
type BloomFilter struct {
	mlc    *Cache
	logger *zap.Logger
	mutex  sync.Mutex
}

// NewBloomFilter creates a new BloomFilter instance.
func NewBloomFilter(mlc *Cache) *BloomFilter {
	return &BloomFilter{
		mlc:    mlc,
		logger: mlc.logger,
	}
}

// Add adds a key to the bloom filter.
func (bf *BloomFilter) Add(key string) {
	bf.mlc.filter.Add([]byte(key))
}

// Test checks if a key might be in the bloom filter.
func (bf *BloomFilter) Test(key string) bool {
	return bf.mlc.filter.Test([]byte(key))
}

// Save persists the bloom filter to the remote cache.
func (bf *BloomFilter) Save(ctx context.Context) {
	bf.mutex.Lock()
	defer bf.mutex.Unlock()

	var buf bytes.Buffer
	if _, err := bf.mlc.filter.WriteTo(&buf); err != nil {
		bf.logger.Error("Failed to serialize bloom filter", zap.Error(err))
		return
	}

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	err := bf.mlc.resilience.Set(ctx, bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.BloomFilterRedisKey, &models.Entry{
		Data:       []byte(encoded),
		Expiration: time.Now().Add(24 * time.Hour),
	}, 24*time.Hour)
	if err != nil {
		bf.logger.Error("Failed to save bloom filter to remote cache", zap.Error(err))
	}
}

// Load retrieves the bloom filter from the remote cache.
func (bf *BloomFilter) Load(ctx context.Context) error {
	bf.mutex.Lock()
	defer bf.mutex.Unlock()

	var entry models.Entry
	err := bf.mlc.resilience.Get(ctx, bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.BloomFilterRedisKey, &entry)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			bf.logger.Info("Bloom filter not found in remote cache, creating new one")
			return nil
		}
		return fmt.Errorf("failed to load bloom filter from remote cache: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(string(entry.Data))
	if err != nil {
		return fmt.Errorf("failed to decode bloom filter data: %w", err)
	}

	filter := bloom.NewWithEstimates(
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate,
	)

	if _, err := filter.ReadFrom(bytes.NewReader(decoded)); err != nil {
		return fmt.Errorf("failed to deserialize bloom filter: %w", err)
	}

	bf.mlc.filter = filter
	return nil
}

// Rebuild reconstructs the bloom filter from all keys in the remote cache.
func (bf *BloomFilter) Rebuild(ctx context.Context) error {
	bf.mutex.Lock()
	defer bf.mutex.Unlock()

	newFilter := bloom.NewWithEstimates(
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate,
	)

	var cursor uint64
	for {
		var keys []string
		var err error
		keys, cursor, err = bf.mlc.remoteCache.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			return fmt.Errorf("failed to scan keys from remote cache: %w", err)
		}

		for _, key := range keys {
			newFilter.Add([]byte(key))
		}

		if cursor == 0 {
			break
		}
	}

	bf.mlc.filter = newFilter
	bf.Save(ctx)

	return nil
}

// PeriodicRebuild periodically rebuilds the bloom filter.
func (bf *BloomFilter) PeriodicRebuild(ctx context.Context) {
	ticker := time.NewTicker(bf.mlc.config.CacheBehaviorConfig.BloomFilterSettings.RebuildInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := bf.Rebuild(ctx); err != nil {
				bf.logger.Error("Failed to rebuild bloom filter", zap.Error(err))
			}
		case <-ctx.Done():
			return
		}
	}
}
