package ember

import (
	"context"
	"go.uber.org/zap"
	"time"
)

func (c *MultiCache) prefetchRoutine(ctx context.Context) {
	if !c.config.CacheBehaviorConfig.EnablePrefetch {
		return
	}

	ticker := time.NewTicker(5 * time.Minute) // Adjust as needed
	defer ticker.Stop()

	for range ticker.C {
		c.prefetchPopularKeys(ctx)
	}
}

func (c *MultiCache) prefetchPopularKeys(ctx context.Context) {
	keys := make([]string, 0)
	c.accessCount.Range(func(key, value any) bool {
		if value.(uint64) >= c.config.CacheBehaviorConfig.PrefetchThreshold {
			keys = append(keys, key.(string))
		}
		return true
	})

	for _, key := range keys[:min(len(keys), int(c.config.CacheBehaviorConfig.PrefetchCount))] {
		go func(k string) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var value any
			if _, err := c.Get(ctx, k, &value); err != nil {
				c.config.Logger.Warn("Failed to prefetch key", zap.String("key", k), zap.Error(err))
			}
		}(key)
	}
}

func (c *MultiCache) warmCache(
	ctx context.Context,
	keys []string) {
	for _, key := range keys {
		go func(k string) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			var value any
			if _, err := c.Get(ctx, k, &value); err != nil {
				c.config.Logger.Warn("Failed to warm cache for key", zap.Error(err), zap.String("key", k))
			}
		}(key)
	}
}
