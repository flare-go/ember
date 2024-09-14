package limited_cache

import (
	"sync"

	"go.uber.org/zap"
)

// KeyTracker 用於跟蹤所有鍵
type KeyTracker struct {
	trackedKeys sync.Map
	logger      *zap.Logger
}

// NewKeyTracker 創建一個新的 KeyTracker 實例
func NewKeyTracker(logger *zap.Logger) *KeyTracker {
	return &KeyTracker{
		trackedKeys: sync.Map{},
		logger:      logger,
	}
}

// Add 添加一個鍵到跟蹤中
func (kt *KeyTracker) Add(key string) {
	kt.trackedKeys.Store(key, struct{}{})
}

// Remove 從跟蹤中移除一個鍵
func (kt *KeyTracker) Remove(key string) {
	kt.trackedKeys.Delete(key)
}

// Range 遍歷所有跟蹤的鍵
func (kt *KeyTracker) Range(f func(key string) bool) {
	kt.trackedKeys.Range(func(k, v any) bool {
		if strKey, ok := k.(string); ok {
			return f(strKey)
		}
		kt.logger.Warn("Invalid key type in KeyTracker", zap.Any("key", k))
		return true
	})
}
