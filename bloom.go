package ember

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/bits-and-blooms/bloom/v3"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"time"
)

func (c *MultiCache) periodicRebuild(ctx context.Context) {

	ticker := time.NewTicker(c.config.CacheBehaviorConfig.BloomFilterSettings.RebuildInterval)
	defer ticker.Stop()

	for range ticker.C {
		c.rebuildBloomFilter(ctx)
	}
}

func (c *MultiCache) saveBloomFilter(ctx context.Context) {

	var buf bytes.Buffer
	if _, err := c.filter.WriteTo(&buf); err != nil {
		c.config.Logger.Error("Failed to serialize bloom filter", zap.Error(err))
		return
	}

	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())
	if err := c.executeWithResilience(ctx, func() error {
		return c.remoteCache.Set(ctx, c.config.CacheBehaviorConfig.BloomFilterSettings.BloomFilterRedisKey, encoded, 0).Err()
	}); err != nil {
		c.config.Logger.Error("Failed to save bloom filter to Redis", zap.Error(err))
	}
}

func (c *MultiCache) loadBloomFilter(ctx context.Context) error {

	var encoded string

	if err := c.executeWithResilience(ctx, func() error {
		var err error
		encoded, err = c.remoteCache.Get(ctx, c.config.CacheBehaviorConfig.BloomFilterSettings.BloomFilterRedisKey).Result()
		return err
	}); err != nil {
		if errors.Is(err, redis.Nil) {
			return nil // No existing Bloom filter, which is okay
		}
		return fmt.Errorf("failed to load bloom filter from Redis: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("failed to decode bloom filter: %w", err)
	}

	if _, err = c.filter.ReadFrom(bytes.NewReader(decoded)); err != nil {
		return fmt.Errorf("failed to deserialize bloom filter: %w", err)
	}

	return nil
}

func (c *MultiCache) rebuildBloomFilter(ctx context.Context) {

	c.filter = bloom.NewWithEstimates(
		c.config.CacheBehaviorConfig.BloomFilterSettings.ExpectedItems,
		c.config.CacheBehaviorConfig.BloomFilterSettings.FalsePositiveRate)

	var cursor uint64
	for {
		var keys []string
		var err error
		keys, cursor, err = c.remoteCache.Scan(ctx, cursor, "*", 1000).Result()
		if err != nil {
			c.config.Logger.Error("Failed to scan Redis keys", zap.Error(err))
			return
		}

		for _, key := range keys {
			c.filter.Add([]byte(key))
		}

		if cursor == 0 {
			break
		}
	}

	c.saveBloomFilter(ctx)
}
