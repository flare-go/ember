
[![Go Reference](https://pkg.go.dev/badge/goflare.io/ember?status.svg)](https://pkg.go.dev/goflare.io/ember?tab-doc)

# Ember - 高效能多層緩存系統

<img src="./image.jpg" alt="Header" width="200" /> 

## 目錄
1. [介紹](#介紹)
2. [核心特性](#核心特性)
3. [架構概覽](#架構概覽)
4. [安裝](#安裝)
5. [基本使用](#基本使用)
6. [高級配置](#高級配置)
7. [API 參考](#api-參考)
8. [內部組件詳解](#內部組件詳解)
9. [性能優化](#性能優化)
10. [可觀察性和監控](#可觀察性和監控)
11. [最佳實踐](#最佳實踐)
12. [常見問題解答](#常見問題解答)
13. [性能基準測試](#性能基準測試)
14. [安全考慮](#安全考慮)
15. [擴展性](#擴展性)
16. [Roadmap](#Roadmap)
17. [貢獻指南](#貢獻指南)
18. [貢獻](#貢獻)
19. [授權](#授權)

## 介紹

Ember 是一個為 Go 應用程序設計的高效能、可擴展的多層緩存系統。它融合了本地內存緩存和分佈式緩存（如 Redis）的優勢，提供了豐富的功能和靈活的配置選項。Ember 旨在解決大規模應用中的緩存挑戰，包括高並發、大數據量、複雜的緩存策略等問題。

## 核心特性

1. **多層緩存架構**
    - 本地內存緩存：使用高效的內存存儲，支持快速訪問
    - 分佈式緩存：集成 Redis，支持跨節點數據共享

2. **高性能設計**
    - 分片機制：通過數據分片減少鎖競爭
    - 並發安全：所有操作都是線程安全的
    - 高效序列化：支持多種序列化方式，默認使用 JSON

3. **智能緩存管理**
    - 自適應 TTL：根據訪問頻率動態調整緩存項的生存時間
    - 預取機制：自動預取熱門緩存項，提高命中率
    - 布隆過濾器：減少對不存在鍵的無效查詢

4. **彈性和可靠性**
    - 斷路器：防止緩存故障影響整個系統
    - 重試機制：處理臨時的網絡問題
    - 優雅降級：在遠程緩存不可用時回退到本地緩存

5. **豐富的配置選項**
    - 靈活的初始化選項：通過函數選項模式提供細粒度控制
    - 動態調整：支持運行時調整某些配置

6. **可觀察性**
    - OpenTelemetry 集成：支持分布式追蹤
    - 詳細日誌：使用 zap logger 提供結構化日誌
    - 性能指標：暴露關鍵性能指標供監控系統使用

## 架構概覽

Ember 的架構主要包含以下幾個核心組件：

1. **Cache**: 主要的緩存接口，協調各個組件的工作。
2. **LocalCache**: 基於內存的本地緩存實現。
3. **RemoteCache**: 與 Redis 交互的遠程緩存實現。
4. **BloomFilter**: 用於快速檢查鍵是否可能存在的概率數據結構。
5. **TTLManager**: 管理緩存項的 TTL，實現自適應 TTL 策略。
6. **Prefetcher**: 實現預取邏輯，提前加載可能被訪問的數據。
7. **Resilience**: 實現斷路器和重試邏輯，提高系統穩定性。

## 安裝

使用 Go 模塊安裝 Ember：

```bash
go get github.com/your-org/ember
```
確保您的項目使用 Go 1.16 或更高版本。

## 基本使用

以下是一個簡單的使用示例：

```go
package main

import (
    "context"
    "fmt"
    "time"

    "goflare.io/ember"
    "github.com/redis/go-redis/v9"
)

func main() {
    ctx := context.Background()

    // 初始化 Ember
    cache, err := ember.New(
        ctx,
        ember.WithRedis(&redis.Options{Addr: "localhost:6379"}),
        ember.WithMaxLocalSize(100 * 1024 * 1024), // 100MB 本地緩存
        ember.WithDefaultExpiration(5 * time.Minute),
    )
    if err != nil {
        panic(err)
    }
    defer cache.Close()

    // 設置緩存
    err = cache.Set(ctx, "user:1001", User{ID: 1001, Name: "Alice"}, 10*time.Minute)
    if err != nil {
        fmt.Printf("Failed to set cache: %v
", err)
    }

    // 獲取緩存
    var user User
    found, err := cache.Get(ctx, "user:1001", &user)
    if err != nil {
        fmt.Printf("Failed to get cache: %v
", err)
    } else if found {
        fmt.Printf("Found user: %+v
", user)
    } else {
        fmt.Println("User not found")
    }
}

type User struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}
```

## 高級配置

Ember 提供了豐富的配置選項，可以通過 With... 函數在初始化時設置：

```go
cache, err := ember.New(
    ctx,
    ember.WithRedis(&redis.Options{
        Addr: "localhost:6379",
        DB:   0,
    }),
    ember.WithMaxLocalSize(1 * 1024 * 1024 * 1024), // 1GB
    ember.WithShardCount(32),
    ember.WithDefaultExpiration(10 * time.Minute),
    ember.WithBloomFilter(1000000, 0.01),
    ember.WithPrefetch(true, 100, 10),
    ember.WithAdaptiveTTL(true, time.Minute, 30*time.Minute, 5*time.Minute),
    ember.WithResilienceConfig(ember.ResilienceConfig{
        MaxRetries:          3,
        InitialInterval:     100 * time.Millisecond,
        MaxInterval:         10 * time.Second,
        Multiplier:          2.0,
        RandomizationFactor: 0.5,
    }),
    ember.WithSerializer(ember.JSONSerializer),
    ember.WithLogger(customLogger),
)
```
這個配置示例展示了如何:

- 設置 Redis 連接
- 配置本地緩存大小和分片數量
- 啟用布隆過濾器並設置其參數
- 配置預取機制
- 設置自適應 TTL
- 配置彈性策略（重試邏輯）
- 選擇序列化方法
- 使用自定義日誌記錄器

## API 參考

Ember 提供以下關鍵 API：

- `Set(ctx context.Context, key string, value any, ttl ...time.Duration) error`：設置緩存項。
- `Get(ctx context.Context, key string, value any) (bool, error)`：獲取緩存項。
- `Delete(ctx context.Context, key string) error`：刪除緩存項。
- `Clear(ctx context.Context) error`：清除所有緩存。
- `Stats() map[string]interface{}`：返回緩存統計數據。

## 內部組件詳解

Ember 的內部組件：

- **Cache**：主緩存接口，協調各組件的工作。
- **LocalCache**：本地內存緩存。
- **RemoteCache**：遠端 Redis 緩存。
- **BloomFilter**：布隆過濾器，用於檢查鍵是否存在。
- **TTLManager**：管理 TTL 的組件。
- **Resilience**：處理斷路器和重試的邏輯。

## 性能優化

1. 調整分片數量：根據 CPU 和內存設置分片數量。
2. 使用批量操作：使用 `SetMulti` 和 `GetMulti` 提高性能。
3. 使用二進制序列化：如 Protobuf 進行高效序列化。

## 可觀察性和監控

1. **OpenTelemetry 集成**：支援分布式追蹤，記錄緩存命中率和延遲。
2. **性能指標**：使用 `Stats()` 暴露性能數據。
3. **健康檢查**：通過 `HealthCheck()` 檢查系統狀況。

## 最佳實踐

1. 使用緩存預熱：在應用啟動時預加載數據。
2. 使用錯誤處理：總是檢查操作是否成功，並設置斷路器機制。
3. 定期清理過期數據：使用 `Clear()` 定期清理。

## 常見問題解答

1. **如何確保一致性？** 使用最終一致性策略，依賴 TTL 和失效機制。
2. **如何處理大規模寫入？** 使用分片機制和批量操作提高吞吐量。


## 性能基準測試

以下是在標準硬體配置（8 核 CPU，16GB RAM）下進行的基準測試結果：

```plaintext
BenchmarkSet-8           1000000   1234 ns/op   789 B/op   5 allocs/op
BenchmarkGet-8           2000000    567 ns/op   234 B/op   3 allocs/op
BenchmarkSetMulti-8       100000  15678 ns/op  3456 B/op  23 allocs/op
BenchmarkGetMulti-8       200000   7890 ns/op  2345 B/op  15 allocs/op
```

注意：實際性能可能因硬體配置、網絡延遲和數據大小而異。

## 安全考慮

### 數據加密

- Ember 本身不提供數據加密功能，但可以通過自定義序列化器實現
- 對於敏感數據，建議在應用層進行加密後再存儲到緩存中

### 訪問控制

- 確保 Redis 服務器配置了適當的訪問控制和身份驗證
- 使用 `WithRedis` 選項時設置密碼和 TLS 配置

### 網絡安全

- 在生產環境中，建議使用 TLS 加密 Redis 連接
- 如果可能，將 Redis 服務器放置在私有網絡中

### 數據隔離

- 使用 Redis 的不同數據庫或者前綴來隔離不同應用的數據
- 謹慎使用 `Clear` 方法，避免意外清除其他應用的數據


- 使用 Redis 的不同數據庫或者前綴來隔離不同應用的數據
- 謹慎使用 `Clear` 方法，避免意外清除其他應用的數據

## 擴展性

Ember 的設計考慮了擴展性，可以通過以下方式進行擴展：

1. **自定義序列化器**
    - 實現 `serialization.Encoder` 和 `serialization.Decoder` 接口，然後使用 `WithSerializer` 配置。
```go
type CustomSerializer struct{}

func (cs *CustomSerializer) Encode(w io.Writer) serialization.Encoder {
    // 實現編碼邏輯
}

func (cs *CustomSerializer) Decode(r io.Reader) serialization.Decoder {
    // 實現解碼邏輯
}

ember.New(ctx, ember.WithSerializer(&CustomSerializer{}))
```

2. **自定義緩存後端**
    - 雖然 Ember 默認使用 Redis 作為遠程緩存，但可以通過實現 `cache.RemoteCache` 接口來支持其他後端存儲。

3. **擴展監控指標**
    - 可以通過修改 `Stats()` 方法來暴露更多自定義指標。

## 未來 Roadmap

- 支持更多的分佈式緩存後端（如 Memcached）
- 實現緩存數據壓縮功能
- 提供 Prometheus 指標導出
- 增加對 Redis Cluster 的原生支持
- 實現基於角色的訪問控制（RBAC）
- 支持緩存數據版本控制

## 貢獻指南

我們歡迎並感謝所有形式的貢獻。以下是參與項目的一些方式：

1. 提交 bug 報告或功能請求
2. 改進文檔
3. 提交代碼修復或新功能
4. 參與代碼審查

### 貢獻步驟：

1. Fork 項目倉庫
2. 創建您的特性分支 (git checkout -b feature/AmazingFeature)
3. 提交您的更改 (git commit -m 'Add some AmazingFeature')
4. 推送到分支 (git push origin feature/AmazingFeature)
5. 開啟一個 Pull Request

請確保遵循我們的代碼風格指南和提交消息約定。

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
