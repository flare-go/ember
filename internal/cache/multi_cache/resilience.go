package multi_cache

import (
	"context"
	"goflare.io/ember/internal/models"
	"time"

	"go.uber.org/zap"
)

// Resilience 管理熔斷器和重試機制
type Resilience struct {
	cache  *MultiCache
	logger *zap.Logger
}

// NewResilience 創建一個新的 Resilience 實例
func NewResilience(cache *MultiCache) *Resilience {
	return &Resilience{
		cache:  cache,
		logger: cache.logger,
	}
}

// Set 使用熔斷器和重試機制設置遠端快取
func (r *Resilience) Set(ctx context.Context, key string, entry *models.Entry, ttl time.Duration) error {
	return r.cache.executeWithResilience(ctx, func() error {
		return r.cache.remoteCache.Set(ctx, key, entry, ttl).Err()
	})
}

// Delete 使用熔斷器和重試機制刪除遠端快取
func (r *Resilience) Delete(ctx context.Context, key string) error {
	return r.cache.executeWithResilience(ctx, func() error {
		return r.cache.remoteCache.Del(ctx, key).Err()
	})
}

// Clear 使用熔斷器和重試機制清空遠端快取
func (r *Resilience) Clear(ctx context.Context) error {
	return r.cache.executeWithResilience(ctx, func() error {
		return r.cache.remoteCache.FlushDB(ctx).Err()
	})
}
