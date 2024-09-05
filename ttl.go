package ember

import (
	"go.uber.org/zap"
	"goflare.io/ember/models"
	"time"
)

func (c *MultiCache) adaptiveTTLRoutine() {
	if !c.config.CacheBehaviorConfig.EnableAdaptiveTTL {
		return
	}

	ticker := time.NewTicker(c.config.CacheBehaviorConfig.AdaptiveTTLSettings.TTLAdjustInterval)
	defer ticker.Stop()

	for range ticker.C {
		for i := range c.localCaches {
			c.shards[i].Lock()
			c.localCaches[i].keys.Range(func(key, _ any) bool {
				strKey := key.(string)
				value, found := c.localCaches[i].cache.Get(strKey)
				if !found {
					// Key no longer in cache, remove from our tracking
					c.localCaches[i].keys.Delete(strKey)
					return true
				}

				entry, ok := value.(*models.Entry)
				if !ok {
					c.config.Logger.Error("Invalid cache entry type", zap.String("key", strKey))
					return true
				}

				newTTL := c.getAdaptiveTTL(strKey)
				entry.Expiration = time.Now().Add(newTTL)

				// We need to set the entry back into the cache with the new expiration
				c.localCaches[i].cache.Set(strKey, entry, 1)

				return true
			})
			c.shards[i].Unlock()
		}
	}
}

func (c *MultiCache) getAdaptiveTTL(key string) time.Duration {
	if !c.config.CacheBehaviorConfig.EnableAdaptiveTTL {
		return c.config.DefaultExpiration
	}

	count, ok := c.accessCount.Load(key)
	if !ok || count == nil {
		c.config.Logger.Debug("No access count for key, using default expiration", zap.String("key", key))
		return c.config.DefaultExpiration
	}

	accessCount, ok := count.(int64)
	if !ok {
		c.config.Logger.Warn("Invalid access count type", zap.String("key", key), zap.Any("count", count))
		return c.config.DefaultExpiration
	}

	// 計算自適應 TTL
	ttl := c.calculateAdaptiveTTL(accessCount)
	c.config.Logger.Debug("Calculated adaptive TTL", zap.String("key", key), zap.Duration("ttl", ttl))
	return ttl
}

func (c *MultiCache) calculateAdaptiveTTL(accessCount int64) time.Duration {
	minTTL := c.config.CacheBehaviorConfig.AdaptiveTTLSettings.MinTTL
	maxTTL := c.config.CacheBehaviorConfig.AdaptiveTTLSettings.MaxTTL

	// 這裡使用一個簡單的線性計算，您可以根據需求調整這個邏輯
	factor := float64(accessCount) / 100.0 // 假設100次訪問達到最大TTL
	if factor > 1.0 {
		factor = 1.0
	}

	ttl := minTTL + time.Duration(float64(maxTTL-minTTL)*factor)
	return ttl
}
