package multi_cache

import (
	"context"
	"time"
)

// CacheOperations 定義基本快取操作的接口
type CacheOperations interface {
	Get(ctx context.Context, key string, value any) (bool, error)
	Set(ctx context.Context, key string, value any, ttl ...time.Duration) error
	Delete(ctx context.Context, key string) error
	Clear(ctx context.Context) error
	GetMulti(ctx context.Context, keys []string) (map[string]any, error)
	SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error
	Close() error
}
