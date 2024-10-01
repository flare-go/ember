package multi

import (
	"context"
	"math"
	"sync"
	"time"

	"go.uber.org/atomic"
	"go.uber.org/zap"
	"goflare.io/ember/internal/models"
)

// TTLManager manages adaptive TTL.
type TTLManager struct {
	cache          *Cache
	logger         *zap.Logger
	minTTL         time.Duration
	maxTTL         time.Duration
	adjustInterval time.Duration
}

// NewTTLManager creates a new TTLManager instance.
func NewTTLManager(cache *Cache) *TTLManager {
	cfg := cache.config.CacheBehaviorConfig.AdaptiveTTLSettings
	return &TTLManager{
		cache:          cache,
		logger:         cache.logger,
		minTTL:         cfg.MinTTL,
		maxTTL:         cfg.MaxTTL,
		adjustInterval: cfg.TTLAdjustInterval,
	}
}

// Run starts the TTL adjustment routine.
func (tm *TTLManager) Run(ctx context.Context) {
	ticker := time.NewTicker(tm.adjustInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.adjustAdaptiveTTL(ctx)
		case <-ctx.Done():
			tm.logger.Info("Stopping TTL manager due to context cancellation")
			return
		}
	}
}

// adjustAdaptiveTTL adjusts the TTL for all keys.
func (tm *TTLManager) adjustAdaptiveTTL(ctx context.Context) {
	tm.logger.Debug("Adjusting adaptive TTL for all keys")

	var wg sync.WaitGroup
	for i := uint64(0); i < tm.cache.config.ShardCount; i++ {
		wg.Add(1)
		go func(shardIndex uint64) {
			defer wg.Done()
			tm.adjustShardTTL(ctx, shardIndex)
		}(i)
	}
	wg.Wait()
}

// adjustShardTTL adjusts the TTL for all keys in a specific shard.
func (tm *TTLManager) adjustShardTTL(ctx context.Context, shardIndex uint64) {
	tm.cache.shards[shardIndex].Lock()
	defer tm.cache.shards[shardIndex].Unlock()

	keys := tm.cache.localCaches[shardIndex].Keys(ctx)
	for _, key := range keys {
		select {
		case <-ctx.Done():
			return
		default:
			entry := &models.Entry{}
			found, err := tm.cache.localCaches[shardIndex].Get(ctx, key, entry)
			if !found {
				continue
			}
			if err != nil {
				tm.logger.Warn("Failed to get entry from local cache", zap.String("key", key), zap.Error(err))
				continue
			}
			newTTL := tm.calculateAdaptiveTTL(key, entry)
			entry.Expiration = time.Now().Add(newTTL)
			if err := tm.cache.localCaches[shardIndex].Set(ctx, key, entry, newTTL); err != nil {
				tm.logger.Warn("Failed to update TTL in local cache", zap.String("key", key), zap.Error(err))
			}
		}
	}
}

// calculateAdaptiveTTL calculates the adaptive TTL for a key.
func (tm *TTLManager) calculateAdaptiveTTL(key string, entry *models.Entry) time.Duration {
	accessCountValue, ok := tm.cache.accessCount.Load(key)
	if !ok {
		return tm.minTTL
	}

	accessCount := accessCountValue.(*atomic.Int64).Load()
	lastAccessTime := entry.LastAccessTime.Load()

	now := time.Now()
	durationSinceLastAccess := now.Sub(lastAccessTime).Minutes()

	// Calculate TTL based on access frequency and recency
	frequencyFactor := math.Min(float64(accessCount)/100.0, 1.0) // Normalize to [0, 1]
	recencyFactor := math.Exp(-durationSinceLastAccess / 60.0)   // Decay factor, half-life of 1 hour

	ttlRange := float64(tm.maxTTL - tm.minTTL)
	adaptiveTTL := time.Duration(float64(tm.minTTL) + ttlRange*frequencyFactor*recencyFactor)

	return adaptiveTTL
}

// GetTTL returns the TTL for a specific key.
func (tm *TTLManager) GetTTL(key string) time.Duration {
	accessCountValue, ok := tm.cache.accessCount.Load(key)
	if !ok {
		return tm.minTTL
	}

	accessCount := accessCountValue.(*atomic.Int64).Load()
	now := time.Now()

	entry := &models.Entry{
		AccessCount:    atomic.NewInt64(accessCount),
		LastAccessTime: atomic.NewTime(now),
	}

	return tm.calculateAdaptiveTTL(key, entry)
}
