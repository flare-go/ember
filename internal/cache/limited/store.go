package limited

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"goflare.io/ember/internal/models"

	"github.com/dgraph-io/ristretto"
	"go.uber.org/zap"
)

// Store defines the interface for cache operations.
type Store interface {
	Set(ctx context.Context, key string, entry *models.Entry) error
	Get(ctx context.Context, key string) (*models.Entry, bool)
	Delete(ctx context.Context, key string)
	Flush(ctx context.Context)
	Close()
	GetMulti(ctx context.Context, keys []string) map[string]any
	SetMulti(ctx context.Context, items map[string]any, ttl time.Duration) error
}

// RistrettoStore implements the Store interface using Ristretto.
type RistrettoStore struct {
	cache      *ristretto.Cache[string, any]
	logger     *zap.Logger
	defaultTTL time.Duration
}

// NewRistrettoStore creates a new RistrettoStore instance.
func NewRistrettoStore(maxSize uint64, defaultTTL time.Duration, logger *zap.Logger) (Store, error) {
	numCounters := int64(math.Min(float64(10*maxSize), float64(math.MaxInt64)))
	maxCost := int64(math.Min(float64(maxSize), float64(math.MaxInt64)))

	c, err := ristretto.NewCache(&ristretto.Config[string, any]{
		NumCounters: numCounters,
		MaxCost:     maxCost,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Ristretto cache: %w", err)
	}

	return &RistrettoStore{
		cache:      c,
		logger:     logger,
		defaultTTL: defaultTTL,
	}, nil
}

// Set sets a cache entry.
func (s *RistrettoStore) Set(ctx context.Context, key string, entry *models.Entry) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		ttl := time.Until(entry.Expiration)
		if ttl <= 0 {
			ttl = s.defaultTTL
		}

		if !s.cache.SetWithTTL(key, entry, 1, ttl) {
			s.logger.Warn("Ristretto SetWithTTL failed", zap.String("key", key))
			return fmt.Errorf("failed to set cache entry")
		}

		return nil
	}
}

// Get retrieves a cache entry.
func (s *RistrettoStore) Get(ctx context.Context, key string) (*models.Entry, bool) {
	select {
	case <-ctx.Done():
		return nil, false
	default:
		value, found := s.cache.Get(key)
		if !found {
			return nil, false
		}

		entry, ok := value.(*models.Entry)
		if !ok {
			s.logger.Error("Invalid cache entry type", zap.String("key", key))
			return nil, false
		}

		if entry.IsExpired() {
			s.cache.Del(key)
			return nil, false
		}

		entry.IncrementAccess()
		return entry, true
	}
}

// Delete removes a cache entry.
func (s *RistrettoStore) Delete(ctx context.Context, key string) {
	select {
	case <-ctx.Done():
		return
	default:
		s.cache.Del(key)
	}
}

// Flush clears the entire cache.
func (s *RistrettoStore) Flush(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
		s.cache.Clear()
	}
}

// Close closes the cache.
func (s *RistrettoStore) Close() {
	s.cache.Close()
}

// GetMulti retrieves multiple cache entries.
func (s *RistrettoStore) GetMulti(ctx context.Context, keys []string) map[string]any {
	result := make(map[string]any)
	for _, key := range keys {
		select {
		case <-ctx.Done():
			return result
		default:
			if entry, found := s.Get(ctx, key); found {
				var decodedValue any
				if err := json.Unmarshal(entry.Data, &decodedValue); err != nil {
					s.logger.Error("Failed to decode value", zap.Error(err), zap.String("key", key))
					continue
				}
				result[key] = decodedValue
			}
		}
	}
	return result
}

// SetMulti sets multiple cache entries.
func (s *RistrettoStore) SetMulti(ctx context.Context, items map[string]any, ttl time.Duration) error {
	for key, value := range items {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			data, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("failed to marshal value for key %s: %w", key, err)
			}

			entry := models.NewEntry(data, time.Now().Add(ttl))
			if err := s.Set(ctx, key, entry); err != nil {
				return fmt.Errorf("failed to set key %s: %w", key, err)
			}
		}
	}
	return nil
}
