package limited_cache

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto"
	"go.uber.org/zap"
	"goflare.io/ember/internal/models"
)

// CacheInterface 定義快取操作的接口
type CacheInterface interface {
	Set(key string, entry *models.Entry, ttl time.Duration) error
	Get(key string) (*models.Entry, bool)
	Delete(key string)
	Flush()
	Close()
	GetMulti(keys []string) map[string]any
	SetMulti(items map[string]any, ttl ...time.Duration) error
}

// Cache 定義快取操作的具體實現
type Cache struct {
	cache      *ristretto.Cache
	logger     *zap.Logger
	defaultTTL time.Duration
}

// NewCache 創建一個新的 Cache 實例
func NewCache(maxSize uint64, defaultTTL time.Duration, logger *zap.Logger) (*Cache, error) {
	c, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: 10 * int64(maxSize),
		MaxCost:     int64(maxSize),
		BufferItems: 64,
		OnEvict: func(item *ristretto.Item) {
			// Evict 相關邏輯，可根據需求添加
		},
	})
	if err != nil {
		return nil, err
	}

	return &Cache{
		cache:      c,
		logger:     logger,
		defaultTTL: defaultTTL,
	}, nil
}

// Set 設置快取項目
func (c *Cache) Set(key string, entry *models.Entry, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = c.defaultTTL
	}

	if !c.cache.SetWithTTL(key, entry, 1, ttl) {
		c.logger.Warn("Ristretto SetWithTTL failed", zap.String("key", key))
		return models.ErrSetFailed
	}

	return nil
}

// Get 獲取快取項目
func (c *Cache) Get(key string) (*models.Entry, bool) {
	value, found := c.cache.Get(key)
	if !found {
		return nil, false
	}

	entry, ok := value.(*models.Entry)
	if !ok {
		c.logger.Error("Invalid cache entry type", zap.String("key", key))
		return nil, false
	}

	if time.Now().After(entry.Expiration) {
		c.cache.Del(key)
		return nil, false
	}

	return entry, true
}

// Delete 刪除快取項目
func (c *Cache) Delete(key string) {
	c.cache.Del(key)
}

// Flush 清空快取
func (c *Cache) Flush() {
	c.cache.Clear()
}

// Close 關閉快取
func (c *Cache) Close() {
	c.cache.Close()
}

// GetMulti 獲取多個快取項目
func (c *Cache) GetMulti(keys []string) map[string]any {
	result := make(map[string]any)
	for _, key := range keys {
		if entry, found := c.Get(key); found {
			var decodedValue any
			if err := json.Unmarshal(entry.Data, &decodedValue); err != nil {
				c.logger.Error("Failed to decode value", zap.Error(err), zap.String("key", key))
				continue
			}
			result[key] = decodedValue
		}
	}
	return result
}

// SetMulti 設置多個快取項目
func (c *Cache) SetMulti(items map[string]any, ttl ...time.Duration) error {
	for key, value := range items {
		var entry models.Entry
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("failed to marshal value for key %s: %w", key, err)
		}
		entry.Data = data
		entry.AccessCount = 1
		entry.LastAccessTime = time.Now()

		var ttlDuration time.Duration
		if len(ttl) > 0 && ttl[0] > 0 {
			ttlDuration = ttl[0]
		} else {
			ttlDuration = c.defaultTTL
		}

		entry.Expiration = time.Now().Add(ttlDuration)

		if err := c.Set(key, &entry, ttlDuration); err != nil {
			return fmt.Errorf("failed to set key %s: %w", key, err)
		}
	}
	return nil
}
