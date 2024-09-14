package models

import (
	"errors"
	"time"

	"go.uber.org/atomic"
)

// Entry 定義快取項目
type Entry struct {
	Data           []byte
	Expiration     time.Time
	AccessCount    int64
	LastAccessTime time.Time
}

// Metrics 定義指標統計
type Metrics struct {
	Hits   atomic.Int64
	Misses atomic.Int64
	// 其他指標
}

// NewMetrics 創建新的 Metrics 實例
func NewMetrics() *Metrics {
	return &Metrics{}
}

// 定義常見錯誤
var (
	ErrSetFailed   = errors.New("failed to set cache entry")
	ErrKeyNotFound = errors.New("key not found in cache")
)
