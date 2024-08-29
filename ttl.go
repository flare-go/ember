package ember

import (
	"go.uber.org/zap"
	"goflare.io/ember/models"
	"math"
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

	accessCount, _ := c.accessCount.Load(key)
	count := accessCount.(int)

	factor := math.Min(float64(count)/100, 1.0)
	ttl := c.config.CacheBehaviorConfig.AdaptiveTTLSettings.MinTTL + time.Duration(factor*float64(c.config.CacheBehaviorConfig.AdaptiveTTLSettings.MaxTTL-c.config.CacheBehaviorConfig.AdaptiveTTLSettings.MinTTL))

	return ttl
}
