package models

import "go.uber.org/atomic"

// Metrics 定義指標統計
type Metrics struct {
	Hits   atomic.Int64
	Misses atomic.Int64
}

// NewMetrics 創建新的 Metrics 實例
func NewMetrics() *Metrics {
	return &Metrics{}
}
