package multi_cache

import (
	"context"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"time"
)

// Prefetcher 實現預取機制
type Prefetcher struct {
	mlc     *MultiCache
	logger  *zap.Logger
	enabled bool
}

// NewPrefetcher 創建一個新的 Prefetcher 實例
func NewPrefetcher(mlc *MultiCache) *Prefetcher {
	return &Prefetcher{
		mlc:     mlc,
		logger:  mlc.logger,
		enabled: mlc.config.CacheBehaviorConfig.EnablePrefetch,
	}
}

// Run 開始運行預取例程
func (p *Prefetcher) Run(ctx context.Context) {
	if !p.enabled {
		return
	}

	ticker := time.NewTicker(5 * time.Minute) // 調整為需要的頻率
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.prefetchPopularKeys(ctx)
		case <-ctx.Done():
			p.logger.Info("Stopping prefetch routine due to context cancellation")
			return
		}
	}
}

// prefetchPopularKeys 預取熱門鍵
func (p *Prefetcher) prefetchPopularKeys(ctx context.Context) {
	keys := make([]string, 0)
	p.mlc.accessCount.Range(func(key, value any) bool {
		if atomicCount, ok := value.(*atomic.Int64); ok {
			if atomicCount.Load() >= int64(p.mlc.config.CacheBehaviorConfig.PrefetchThreshold) {
				keys = append(keys, key.(string))
			}
		} else {
			p.logger.Warn("Invalid access count type", zap.String("key", key.(string)))
		}
		return true
	})

	limit := int(p.mlc.config.CacheBehaviorConfig.PrefetchCount)
	if len(keys) < limit {
		limit = len(keys)
	}
	for _, key := range keys[:limit] {
		go func(k string) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			var value any
			if _, err := p.mlc.Get(ctx, k, &value); err != nil {
				p.logger.Warn("Failed to prefetch key", zap.String("key", k), zap.Error(err))
			}
		}(key)
	}
}

// Warmup 熱身快取
func (p *Prefetcher) Warmup(ctx context.Context) {
	for _, key := range p.mlc.config.CacheBehaviorConfig.WarmupKeys {
		go func(k string) {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			var value any
			if _, err := p.mlc.Get(ctx, k, &value); err != nil {
				p.logger.Warn("Failed to warm cache for key", zap.Error(err), zap.String("key", k))
			}
		}(key)
	}
}
