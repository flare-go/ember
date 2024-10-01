package multi

import (
	"context"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"goflare.io/ember/internal/models"
	"time"
)

// Resilience manages circuit breakers and retry mechanisms.
type Resilience struct {
	cache  *Cache
	logger *zap.Logger
}

// NewResilience creates a new Resilience instance.
func NewResilience(cache *Cache) *Resilience {
	return &Resilience{
		cache:  cache,
		logger: cache.logger,
	}
}

// Set sets a value in the remote cache with resilience.
func (r *Resilience) Set(ctx context.Context, key string, entry *models.Entry, ttl time.Duration) error {
	return r.cache.retrier.Run(ctx, func() error {
		return r.cache.remoteCache.Set(ctx, key, entry, ttl).Err()
	})
}

// Get retrieves a value from the remote cache with resilience.
func (r *Resilience) Get(ctx context.Context, key string, entry *models.Entry) error {
	return r.cache.retrier.Run(ctx, func() error {
		return r.cache.remoteCache.Get(ctx, key).Scan(entry)
	})
}

// Delete removes a key from the remote cache with resilience.
func (r *Resilience) Delete(ctx context.Context, key string) error {
	return r.cache.retrier.Run(ctx, func() error {
		return r.cache.remoteCache.Del(ctx, key).Err()
	})
}

// Clear removes all keys from the remote cache with resilience.
func (r *Resilience) Clear(ctx context.Context) error {
	return r.cache.retrier.Run(ctx, func() error {
		return r.cache.remoteCache.FlushAll(ctx).Err()
	})
}

// GetMulti retrieves multiple entries from the remote cache with resilience.
func (r *Resilience) GetMulti(ctx context.Context, keys []string) (map[string]*models.Entry, error) {
	var result map[string]*models.Entry
	err := r.cache.retrier.Run(ctx, func() error {
		pipe := r.cache.remoteCache.Pipeline()
		cmds := make(map[string]*redis.StringCmd, len(keys))
		for _, key := range keys {
			cmds[key] = pipe.Get(ctx, key)
		}
		_, err := pipe.Exec(ctx)
		if err != nil && !errors.Is(err, redis.Nil) {
			return fmt.Errorf("failed to execute pipeline: %w", err)
		}

		result = make(map[string]*models.Entry)
		for key, cmd := range cmds {
			var entry models.Entry
			if err := cmd.Scan(&entry); err == nil {
				result[key] = &entry
			} else if !errors.Is(err, redis.Nil) {
				return fmt.Errorf("failed to scan entry for key %s: %w", key, err)
			}
		}
		return nil
	})

	return result, err
}

// SetMulti sets multiple entries in the remote cache with resilience.
func (r *Resilience) SetMulti(ctx context.Context, entries map[string]*models.Entry, ttl time.Duration) error {
	return r.cache.retrier.Run(ctx, func() error {
		pipe := r.cache.remoteCache.Pipeline()
		for key, entry := range entries {
			pipe.Set(ctx, key, entry, ttl)
		}
		_, err := pipe.Exec(ctx)
		if err != nil {
			return fmt.Errorf("failed to execute SetMulti pipeline: %w", err)
		}
		return nil
	})
}
