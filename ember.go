package ember

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"goflare.io/ember/internal/cache/multi_cache"
	"goflare.io/ember/internal/config"
	"goflare.io/ember/pkg/serialization"

	"github.com/redis/go-redis/v9"
)

// Option 定義初始化 Ember 的選項接口
type Option func(*config.Config) error

// WithLogger 設置自定義的日誌記錄器
func WithLogger(logger *zap.Logger) Option {
	return func(cfg *config.Config) error {
		cfg.Logger = logger
		return nil
	}
}

// WithMaxLocalSize 設置本地快取的最大大小（字節）
func WithMaxLocalSize(maxSize uint64) Option {
	return func(cfg *config.Config) error {
		cfg.MaxLocalSize = maxSize
		return nil
	}
}

// WithShardCount 設置分片數量
func WithShardCount(shardCount uint64) Option {
	return func(cfg *config.Config) error {
		cfg.ShardCount = shardCount
		return nil
	}
}

// WithDefaultExpiration 設置默認的過期時間
func WithDefaultExpiration(ttl time.Duration) Option {
	return func(cfg *config.Config) error {
		cfg.DefaultExpiration = ttl
		return nil
	}
}

// WithSerialization 設置序列化方式
func WithSerialization(serializer string) Option {
	return func(cfg *config.Config) error {
		switch serializer {
		case "json":
			cfg.Serialization.Encoder = serialization.JsonEncoder
			cfg.Serialization.Decoder = serialization.JsonDecoder
		case "gob":
			cfg.Serialization.Encoder = serialization.GobEncoder
			cfg.Serialization.Decoder = serialization.GobDecoder
		default:
			return fmt.Errorf("unsupported serialization type: %s", serializer)
		}
		return nil
	}
}

// Ember 定義 Ember 庫的主要結構體
type Ember struct {
	multiCache multi_cache.CacheOperations
	logger     *zap.Logger
}

// New 初始化 Ember 庫，接受多個配置選項
func New(ctx context.Context, redisOptions *redis.Options, opts ...Option) (*Ember, error) {
	// 初始化默認配置
	cfg, err := config.NewConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create config: %w", err)
	}

	// 應用選項
	for _, opt := range opts {
		if err := opt(cfg); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	// 初始化 Logger，如果未設置則使用默認
	if cfg.Logger == nil {
		logger, err := zap.NewProduction()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize default logger: %w", err)
		}
		cfg.Logger = logger
	}

	// 初始化 Redis 客戶端
	redisClient := redis.NewClient(redisOptions)
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// 初始化 MultiCache
	mc, err := multi_cache.NewMultiCache(ctx, cfg, redisClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MultiCache: %w", err)
	}

	return &Ember{
		multiCache: mc,
		logger:     cfg.Logger,
	}, nil
}

// Set 設置快取項目
func (e *Ember) Set(ctx context.Context, key string, value any, ttl ...time.Duration) error {
	return e.multiCache.Set(ctx, key, value, ttl...)
}

// Get 獲取快取項目
func (e *Ember) Get(ctx context.Context, key string, value any) (bool, error) {
	return e.multiCache.Get(ctx, key, value)
}

// Delete 刪除快取項目
func (e *Ember) Delete(ctx context.Context, key string) error {
	return e.multiCache.Delete(ctx, key)
}

// Clear 清空所有快取
func (e *Ember) Clear(ctx context.Context) error {
	return e.multiCache.Clear(ctx)
}

// GetMulti 獲取多個快取項目
func (e *Ember) GetMulti(ctx context.Context, keys []string) (map[string]any, error) {
	return e.multiCache.GetMulti(ctx, keys)
}

// SetMulti 設置多個快取項目
func (e *Ember) SetMulti(ctx context.Context, items map[string]any, ttl ...time.Duration) error {
	return e.multiCache.SetMulti(ctx, items, ttl...)
}

// Close 關閉 Ember 庫，釋放資源
func (e *Ember) Close() error {
	return e.multiCache.Close()
}
