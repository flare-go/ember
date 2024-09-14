package multi_cache

import (
	"context"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"math"
	"sync"
	"time"
)

// TTLManager 管理自適應 TTL
type TTLManager struct {
	mlc            *MultiCache
	logger         *zap.Logger
	minTTL         time.Duration
	maxTTL         time.Duration
	adjustInterval time.Duration
}

// NewTTLManager 創建一個新的 TTLManager 實例
func NewTTLManager(mlc *MultiCache) *TTLManager {
	cfg := mlc.config.CacheBehaviorConfig.AdaptiveTTLSettings
	return &TTLManager{
		mlc:            mlc,
		logger:         mlc.logger,
		minTTL:         cfg.MinTTL,
		maxTTL:         cfg.MaxTTL,
		adjustInterval: cfg.TTLAdjustInterval,
	}
}

// Run 開始運行 TTL 管理例程
func (tm *TTLManager) Run(ctx context.Context) {
	ticker := time.NewTicker(tm.adjustInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.adjustAdaptiveTTL()
		case <-ctx.Done():
			tm.logger.Info("Stopping adaptive TTL routine due to context cancellation")
			return
		}
	}
}

// adjustAdaptiveTTL 調整所有鍵的 TTL
func (tm *TTLManager) adjustAdaptiveTTL() {
	tm.logger.Debug("Adjusting adaptive TTL for all keys")

	var wg sync.WaitGroup
	for i := uint64(0); i < tm.mlc.config.ShardCount; i++ {
		wg.Add(1)
		go func(shardIndex uint64) {
			defer wg.Done()
			tm.mlc.shards[shardIndex].Lock()
			defer tm.mlc.shards[shardIndex].Unlock()

			tm.mlc.localCaches[shardIndex].KeyTracker.Range(func(key string) bool {
				entry, found := tm.mlc.localCaches[shardIndex].Get(key)
				if !found {
					tm.mlc.localCaches[shardIndex].Delete(key)
					return true
				}

				newTTL := tm.calculateAdaptiveTTL(entry.AccessCount, entry.LastAccessTime)
				entry.Expiration = time.Now().Add(newTTL)

				// 更新快取中的 Entry
				if err := tm.mlc.localCaches[shardIndex].Set(key, entry, newTTL); err != nil {
					tm.logger.Warn("Failed to update TTL in local cache", zap.String("key", key), zap.Error(err))
				}

				return true
			})
		}(i)
	}
	wg.Wait()
}

// calculateAdaptiveTTL 計算自適應 TTL
func (tm *TTLManager) calculateAdaptiveTTL(accessCount int64, lastAccessTime time.Time) time.Duration {
	minTTL := tm.minTTL
	maxTTL := tm.maxTTL

	now := time.Now()
	durationSinceLastAccess := now.Sub(lastAccessTime).Minutes()

	// 基於訪問次數和最近訪問時間計算 TTL
	factor := float64(accessCount) / 100.0 // 假設100次訪問達到最大TTL
	if factor > 1.0 {
		factor = 1.0
	}

	decayFactor := math.Exp(-durationSinceLastAccess / 60.0) // 半衰期為60分鐘

	ttl := minTTL + time.Duration(float64(maxTTL-minTTL)*factor*decayFactor)
	if ttl < minTTL {
		ttl = minTTL
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}

	return ttl
}

// GetTTL 獲取某個鍵的 TTL
func (tm *TTLManager) GetTTL(key string) time.Duration {
	actual, found := tm.mlc.accessCount.Load(key)
	if !found {
		return tm.adjustInterval
	}
	accessCount := actual.(*atomic.Int64).Load()
	// 假設我們有一個方法可以獲取 LastAccessTime，這裡暫時使用當前時間
	lastAccessTime := time.Now()
	return tm.calculateAdaptiveTTL(accessCount, lastAccessTime)
}
