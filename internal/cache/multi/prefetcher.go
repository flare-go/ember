package multi

import (
	"context"
	"math"
	"sync"
	"time"

	"go.uber.org/atomic"
	"go.uber.org/zap"
)

// Prefetcher implements the prefetching mechanism.
type Prefetcher struct {
	cache   *Cache
	logger  *zap.Logger
	enabled bool
}

// NewPrefetcher creates a new Prefetcher instance.
func NewPrefetcher(cache *Cache) *Prefetcher {
	return &Prefetcher{
		cache:   cache,
		logger:  cache.logger,
		enabled: cache.config.CacheBehaviorConfig.EnablePrefetch,
	}
}

// Run starts the prefetcher routine.
func (p *Prefetcher) Run(ctx context.Context) {
	if !p.enabled {
		return
	}

	ticker := time.NewTicker(p.cache.config.CacheBehaviorConfig.PrefetchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.prefetchPopularKeys(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// prefetchPopularKeys prefetches frequently accessed keys.
func (p *Prefetcher) prefetchPopularKeys(ctx context.Context) {
	threshold := p.cache.config.CacheBehaviorConfig.PrefetchThreshold
	limit := p.cache.config.CacheBehaviorConfig.PrefetchCount

	popularKeys := p.getPopularKeys(threshold, limit)

	var wg sync.WaitGroup
	for _, key := range popularKeys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			var value interface{}
			if _, err := p.cache.Get(ctx, k, &value); err != nil {
				p.logger.Warn("Failed to prefetch key", zap.String("key", k), zap.Error(err))
			}
		}(key)
	}
	wg.Wait()
}

func (p *Prefetcher) getPopularKeys(threshold, limit uint64) []string {
	var popularKeys []string
	p.cache.accessCount.Range(func(key, value interface{}) bool {
		count := value.(*atomic.Int64)
		countValue := count.Load()
		if countValue >= 0 {
			safeCount := uint64(countValue)
			if safeCount >= threshold {
				popularKeys = append(popularKeys, key.(string))
			}
		}
		return len(popularKeys) < int(math.Min(float64(limit), float64(math.MaxInt)))
	})
	return popularKeys
}

// Warmup preloads specified keys into the cache.
func (p *Prefetcher) Warmup(ctx context.Context) {
	var wg sync.WaitGroup
	for _, key := range p.cache.config.CacheBehaviorConfig.WarmupKeys {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			var value interface{}
			if _, err := p.cache.Get(ctx, k, &value); err != nil {
				p.logger.Warn("Failed to warm up key", zap.String("key", k), zap.Error(err))
			}
		}(key)
	}
	wg.Wait()
}
