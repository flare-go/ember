package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"goflare.io/ember"
)

func main() {
	// 創建上下文
	ctx := context.Background()

	// 初始化 Logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func(logger *zap.Logger) {
		if err = logger.Sync(); err != nil {
			log.Fatalf("Failed to sync logger: %v", err)
		}
	}(logger)

	// 配置 Redis 選項
	redisOptions := &redis.Options{
		Addr:     "localhost:6379", // 根據實際情況設置
		Password: "",               // 無密碼
		DB:       0,                // 默認數據庫
	}

	// 初始化 Ember 庫
	emberCache, err := ember.New(
		ctx,
		ember.WithLogger(logger),
		ember.WithMaxLocalSize(2*1024*1024*1024), // 2GB
		ember.WithShardCount(16),
		ember.WithDefaultExpiration(10*time.Minute),
		ember.WithSerializer("json"),
		ember.WithRedis(redisOptions),
	)
	if err != nil {
		log.Fatalf("Failed to initialize Ember: %v", err)
	}
	defer func() {
		if err := emberCache.Close(); err != nil {
			logger.Error("Failed to close Ember", zap.Error(err))
		}
	}()

	// 設置快取項目
	key := "example_key"
	value := "Hello, Ember!"
	ttl := 5 * time.Minute

	if err := emberCache.Set(ctx, key, value, ttl); err != nil {
		log.Fatalf("Set failed: %v", err)
	}
	fmt.Printf("Set key '%s' with value '%s' and TTL %v\n", key, value, ttl)

	// 獲取快取項目
	var retrieved string
	found, err := emberCache.Get(ctx, key, &retrieved)
	if err != nil {
		log.Fatalf("Get failed: %v", err)
	}
	if found {
		fmt.Printf("Retrieved key '%s' with value '%s'\n", key, retrieved)
	} else {
		fmt.Printf("Key '%s' not found\n", key)
	}

	// 刪除快取項目
	if err := emberCache.Delete(ctx, key); err != nil {
		log.Fatalf("Delete failed: %v", err)
	}
	fmt.Printf("Deleted key '%s'\n", key)

	// 清空所有快取項目
	if err := emberCache.Clear(ctx); err != nil {
		log.Fatalf("Clear failed: %v", err)
	}
	fmt.Println("Cleared all cache entries")
}
