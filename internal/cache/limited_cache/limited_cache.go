package limited_cache

import (
	"time"

	"go.uber.org/zap"

	"goflare.io/ember/internal/models"
)

// LimitedCache 結構體結合了快取操作和鍵跟蹤
type LimitedCache struct {
	cache      *Cache
	KeyTracker *KeyTracker
	defaultTTL time.Duration
}

// NewLimitedCache 創建一個新的 LimitedCache 實例
func NewLimitedCache(maxSize uint64, defaultTTL time.Duration, logger *zap.Logger) (*LimitedCache, error) {
	cache, err := NewCache(maxSize, defaultTTL, logger)
	if err != nil {
		return nil, err
	}

	keyTracker := NewKeyTracker(logger)

	return &LimitedCache{
		cache:      cache,
		KeyTracker: keyTracker,
		defaultTTL: defaultTTL,
	}, nil
}

// Set 設置快取項目並跟蹤鍵
func (lc *LimitedCache) Set(key string, entry *models.Entry, ttl time.Duration) error {
	err := lc.cache.Set(key, entry, ttl)
	if err != nil {
		return err
	}
	lc.KeyTracker.Add(key)
	return nil
}

// Get 獲取快取項目
func (lc *LimitedCache) Get(key string) (*models.Entry, bool) {
	return lc.cache.Get(key)
}

// Delete 刪除快取項目並移除鍵跟蹤
func (lc *LimitedCache) Delete(key string) {
	lc.cache.Delete(key)
	lc.KeyTracker.Remove(key)
}

// Flush 清空快取並清除所有鍵跟蹤
func (lc *LimitedCache) Flush() {
	lc.cache.Flush()
	lc.KeyTracker.Range(func(key string) bool {
		lc.KeyTracker.Remove(key)
		return true
	})
}

// Close 關閉 LimitedCache
func (lc *LimitedCache) Close() {
	lc.cache.Close()
}

// GetMulti 獲取多個快取項目
func (lc *LimitedCache) GetMulti(keys []string) map[string]any {
	return lc.cache.GetMulti(keys)
}

// SetMulti 設置多個快取項目並跟蹤鍵
func (lc *LimitedCache) SetMulti(items map[string]any, ttl ...time.Duration) error {
	err := lc.cache.SetMulti(items, ttl...)
	if err != nil {
		return err
	}

	for key := range items {
		lc.KeyTracker.Add(key)
	}

	return nil
}
