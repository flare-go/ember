package models

import "go.uber.org/atomic"

// Metrics stores cache statistics
type Metrics struct {
	Hits      *atomic.Int64
	Misses    *atomic.Int64
	Evictions *atomic.Int64
	Size      *atomic.Int64
}

func NewMetrics() *Metrics {
	return &Metrics{
		Hits:      atomic.NewInt64(0),
		Misses:    atomic.NewInt64(0),
		Evictions: atomic.NewInt64(0),
		Size:      atomic.NewInt64(0),
	}
}
