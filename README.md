
[![Go Reference](https://pkg.go.dev/badge/goflare.io/ember?status.svg)](https://pkg.go.dev/goflare.io/ember?tab-doc)

# Ember

<img src="./image.jpg" alt="Header" width="200" /> 



## 專案介紹 (Introduction)

Ember 是一個先進的多層快取系統，設計用於提升應用程式的效能和響應速度。它結合了本地快取和分散式快取的優勢，提供高效能、低延遲的數據存取能力。Ember 支持多種先進功能，如布隆過濾器（Bloom Filter）、自適應 TTL（Time-to-Live）、預取（Prefetching）功能等，使其成為處理高並發數據訪問和大規模數據存取的理想選擇。

Ember 提供了一個靈活且可擴展的解決方案，適用於各種使用場景，包括但不限於：
- Web 應用程式的數據快取
- API 響應優化
- 分散式系統的數據同步

## 功能特色 (Features)

- **多層快取系統**：支持本地快取（Local Cache）和遠端快取（Remote Cache），提供高效能的數據存取能力。
- **布隆過濾器支持**：使用布隆過濾器來高效檢測數據存在性，減少對遠端快取的訪問次數。
- **自適應 TTL**：根據數據的訪問頻率動態調整 TTL，有效提高數據的命中率和快取使用率。
- **預取功能**：支持預取功能，根據使用模式主動加載數據，進一步減少延遲。
- **高效的熔斷器和重試機制**：內建熔斷器和重試機制，提升系統穩定性和容錯能力。
- **可配置性強**：支持多種配置選項，適應不同的應用需求和場景。
- **輕量化依賴**：依賴最小化，易於集成和部署。

## 安裝與使用 (Installation and Usage)

### 安裝 (Installation)

```bash
go get -u goflare.io/ember
```

### 使用 (Usage)

以下是一個簡單的使用範例：

```go
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
	defer logger.Sync()

	// 配置 Redis 選項
	redisOptions := &redis.Options{
		Addr:     "localhost:6379", // 根據實際情況設置
		Password: "",               // 無密碼
		DB:       0,                // 默認數據庫
	}

	// 初始化 Ember 庫
	emberCache, err := ember.New(
		ctx,
		redisOptions,
		ember.WithLogger(logger),
		ember.WithMaxLocalSize(2*1024*1024*1024), // 2GB
		ember.WithShardCount(16),
		ember.WithDefaultExpiration(10*time.Minute),
		ember.WithSerialization("json"),
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

```

## 配置說明 (Configuration)

Ember 提供了豐富的配置選項，以下是主要配置參數的詳細說明：

- **EnableLocalCache**: 是否啟用本地快取，默認為 `true`。
- **MaxLocalSize**: 本地快取的最大大小，以字節為單位。
- **ShardCount**: 本地快取的分片數量，建議設置為 CPU 核心數的 2 到 4 倍。
- **DefaultExpiration**: 默認的數據過期時間。
- **CleanupInterval**: 清理過期項目的時間間隔。
- **CacheBehaviorConfig**: 包含預取（Prefetching）、自適應 TTL（Adaptive TTL）、布隆過濾器（Bloom Filter）等行為配置。
- **ResilienceConfig**: 包含熔斷器和重試機制的配置。
- **Serialization**: 指定數據的序列化和反序列化方式。

## 貢獻 (Contributing)

歡迎任何形式的貢獻！請參閱 [CONTRIBUTING.md](CONTRIBUTING.md) 了解更多信息。

## 授權 (License)

Ember 根據 MIT 許可證分發。詳細信息請參閱 [LICENSE](LICENSE)。

---

## Project Introduction (Introduction)

Ember is an advanced multi-level caching system designed to enhance application performance and responsiveness. It combines the benefits of local and distributed caching, providing efficient and low-latency data access capabilities. Ember supports a variety of advanced features such as Bloom Filter support, Adaptive TTL, and Prefetching, making it ideal for handling high-concurrency data access and large-scale data storage scenarios.

Ember provides a flexible and scalable solution suitable for various use cases, including but not limited to:
- Data caching for web applications
- API response optimization
- Data synchronization in distributed systems

## Features

- **Multi-Level Caching System**: Supports both Local Cache and Remote Cache, providing high-performance data access.
- **Bloom Filter Support**: Uses Bloom Filters to efficiently detect data existence, reducing access frequency to remote caches.
- **Adaptive TTL**: Dynamically adjusts TTL based on data access frequency, improving hit rate and cache utilization.
- **Prefetching**: Supports prefetching, proactively loading data based on usage patterns to further reduce latency.
- **Resilient Circuit Breaker and Retry Mechanism**: Built-in circuit breakers and retry mechanisms to enhance system stability and fault tolerance.
- **Highly Configurable**: Offers various configuration options to suit different application needs and scenarios.
- **Lightweight Dependencies**: Minimal dependencies for easy integration and deployment.

## Installation and Usage

### Installation

```bash
go get -u goflare.io/ember
```

### Usage

Below is a simple usage example:

```go
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
	defer logger.Sync()

	// 配置 Redis 選項
	redisOptions := &redis.Options{
		Addr:     "localhost:6379", // 根據實際情況設置
		Password: "",               // 無密碼
		DB:       0,                // 默認數據庫
	}

	// 初始化 Ember 庫
	emberCache, err := ember.New(
		ctx,
		redisOptions,
		ember.WithLogger(logger),
		ember.WithMaxLocalSize(2*1024*1024*1024), // 2GB
		ember.WithShardCount(16),
		ember.WithDefaultExpiration(10*time.Minute),
		ember.WithSerialization("json"),
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
```

## Configuration

Ember offers a rich set of configuration options. Below are the main configuration parameters:

- **EnableLocalCache**: Enable or disable local caching, default is `true`.
- **MaxLocalSize**: Maximum size for the local cache in bytes.
- **ShardCount**: Number of shards for local cache, recommended to set 2 to 4 times the CPU core count.
- **DefaultExpiration**: Default data expiration time.
- **CleanupInterval**: Interval for cleaning up expired items.
- **CacheBehaviorConfig**: Includes configurations for Prefetching, Adaptive TTL, and Bloom Filter.
- **ResilienceConfig**: Includes configurations for circuit breakers and retry mechanisms.
- **Serialization**: Specifies the serialization and deserialization methods for data.

## Contributing

We welcome all forms of contribution! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for more information.

## License

Ember is distributed under the MIT License. For more details, see [LICENSE](LICENSE).
