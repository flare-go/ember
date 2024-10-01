package limited

import (
	"context"
	"encoding/json"
	"fmt"
	"goflare.io/ember/internal/models"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Cache combines cache operations with key tracking.
type Cache struct {
	store      Store
	tracker    *Tracker
	defaultTTL time.Duration
}

// New creates a new Cache instance.
func New(maxSize uint64, defaultTTL time.Duration, logger *zap.Logger) (*Cache, error) {
	store, err := NewRistrettoStore(maxSize, defaultTTL, logger)
	if err != nil {
		return nil, err
	}

	tracker := NewTracker(logger)

	return &Cache{
		store:      store,
		tracker:    tracker,
		defaultTTL: defaultTTL,
	}, nil
}

// Set sets a cache entry and tracks the key.
func (c *Cache) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}
	expirationTime := c.defaultTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		expirationTime = ttl[0]
	}

	entry := models.NewEntry(data, time.Now().Add(expirationTime))
	if err := c.store.Set(ctx, key, entry); err != nil {
		return err
	}
	c.tracker.Add(ctx, key)
	return nil
}

// Get retrieves a cache entry.
func (c *Cache) Get(ctx context.Context, key string, value any) (bool, error) {
	entry, found := c.store.Get(ctx, key)
	if !found {
		return false, nil
	}

	if err := json.Unmarshal(entry.Data, value); err != nil {
		return false, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	return true, nil
}

// Delete removes a cache entry and stops tracking the key.
func (c *Cache) Delete(ctx context.Context, key string) error {
	c.store.Delete(ctx, key)
	c.tracker.Remove(ctx, key)
	return nil
}

// Flush clears the entire cache and stops tracking all keys.
func (c *Cache) Flush(ctx context.Context) {
	c.store.Flush(ctx)
	c.tracker.Range(ctx, func(key string) bool {
		c.tracker.Remove(ctx, key)
		return true
	})
}

// Close closes the Cache.
func (c *Cache) Close() error {
	c.store.Close()
	return nil
}

// GetMulti retrieves multiple cache entries.
func (c *Cache) GetMulti(ctx context.Context, keys []string) (map[string]any, error) {
	return c.store.GetMulti(ctx, keys), nil
}

// SetMulti sets multiple cache entries and tracks the keys.
func (c *Cache) SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error {
	expirationTime := c.defaultTTL
	if len(ttl) > 0 && ttl[0] > 0 {
		expirationTime = ttl[0]
	}
	return c.store.SetMulti(ctx, items, expirationTime)
}

// Keys returns all keys in the cache.
func (c *Cache) Keys(ctx context.Context) []string {
	var keys []string
	var mu sync.Mutex

	c.tracker.Range(ctx, func(key string) bool {
		mu.Lock()
		keys = append(keys, key)
		mu.Unlock()
		return true
	})

	return keys
}

// Clear clears the entire cache and stops tracking all keys.
func (c *Cache) Clear(ctx context.Context) error {
	c.store.Flush(ctx)
	c.tracker.Range(ctx, func(key string) bool {
		c.tracker.Remove(ctx, key)
		return true
	})
	return nil
}
