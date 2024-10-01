package limited

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// Tracker tracks all keys in the cache.
type Tracker struct {
	trackedKeys sync.Map
	logger      *zap.Logger
}

// NewTracker creates a new Tracker instance.
func NewTracker(logger *zap.Logger) *Tracker {
	return &Tracker{
		trackedKeys: sync.Map{},
		logger:      logger,
	}
}

// Add adds a key to the tracker.
func (t *Tracker) Add(ctx context.Context, key string) {
	select {
	case <-ctx.Done():
		return
	default:
		t.trackedKeys.Store(key, struct{}{})
	}
}

// Remove removes a key from the tracker.
func (t *Tracker) Remove(ctx context.Context, key string) {
	select {
	case <-ctx.Done():
		return
	default:
		t.trackedKeys.Delete(key)
	}
}

// Range iterates over all tracked keys.
func (t *Tracker) Range(ctx context.Context, f func(key string) bool) {
	t.trackedKeys.Range(func(k, v any) bool {
		select {
		case <-ctx.Done():
			return false
		default:
			if strKey, ok := k.(string); ok {
				return f(strKey)
			}
			t.logger.Warn("Invalid key type in Tracker", zap.Any("key", k))
			return true
		}
	})
}
