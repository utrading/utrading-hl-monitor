# SymbolCache 重构设计

## 目标

将 `ws.SymbolConverter` 拆分为职责明确的两个组件，使用 `concurrent.Map` 实现无锁并发访问，支持 coin ↔ symbol 双向查询。

## 架构

### 组件拆分

**1. cache.SymbolCache（纯缓存层）**
- 基于 `concurrent.Map` 实现无锁并发访问
- 支持双向查询：coin ↔ symbol
- 不依赖外部服务，纯数据存储
- 提供统一的 `Set` 方法自动维护正向+反向索引

**2. ws.SymbolLoader（数据加载层）**
- 定期从 Hyperliquid HTTP API 获取元数据
- 调用 `cache.SymbolCache.Setxxx()` 更新缓存
- 独立管理 HTTP 客户端和重载逻辑

### 依赖关系

```
SymbolLoader ──更新──> SymbolCache
                    ↑
                    │ 查询
                    │
           SubscriptionManager
           PositionManager
```

### 组件删除

- 移除 `ws.SymbolConverter`（功能被 SymbolLoader + SymbolCache 替代）
- 移除 `cache.SymbolCacheInterface`（直接使用具体类型）

## 数据结构

### cache.SymbolCache

```go
type SymbolCache struct {
    // 现货双向映射
    spotNameToSymbol  concurrent.Map[string, string]  // "@123" -> "ETHUSDC"
    spotSymbolToName  concurrent.Map[string, string]  // "ETHUSDC" -> "@123"

    // 合约双向映射
    perpNameToSymbol  concurrent.Map[string, string]  // "BTC" -> "BTCUSDC"
    perpSymbolToName  concurrent.Map[string, string]  // "BTCUSDC" -> "BTC"
}

func NewSymbolCache() *SymbolCache
```

### ws.SymbolLoader

```go
type SymbolLoader struct {
    cache          *cache.SymbolCache
    client         *hyperliquid.Info
    httpURL        string
    reloadInterval time.Duration  // 默认 2 小时
    done           chan struct{}
}

func NewSymbolLoader(cache *cache.SymbolCache, httpURL string) (*SymbolLoader, error)
func (sl *SymbolLoader) Start()
func (sl *SymbolLoader) Close()
```

## API 设计

### cache.SymbolCache

```go
// 现货查询
func (c *SymbolCache) GetSpotSymbol(coin string) (string, bool)
func (c *SymbolCache) GetSpotName(symbol string) (string, bool)
func (c *SymbolCache) SetSpotSymbol(coin, symbol string)  // 自动维护双向

// 合约查询
func (c *SymbolCache) GetPerpSymbol(coin string) (string, bool)
func (c *SymbolCache) GetPerpName(symbol string) (string, bool)
func (c *SymbolCache) SetPerpSymbol(coin, symbol string)  // 自动维护双向

// 统计
func (c *SymbolCache) Stats() map[string]interface{}
```

### ws.SymbolLoader

```go
func (sl *SymbolLoader) loadMeta() error
func (sl *SymbolLoader) reloadLoop()
```

## 数据流

### SymbolLoader 初始化

```go
func NewSymbolLoader(cache *cache.SymbolCache, httpURL string) (*SymbolLoader, error) {
    sl := &SymbolLoader{
        cache:          cache,
        client:         hyperliquid.NewInfo(context.TODO(), httpURL, false, nil, nil),
        httpURL:        httpURL,
        reloadInterval: 2 * time.Hour,
        done:           make(chan struct{}),
    }

    // 首次加载（阻塞，失败返回错误）
    if err := sl.loadMeta(); err != nil {
        return nil, err
    }

    return sl
}
```

### 后台重载

```go
func (sl *SymbolLoader) Start() {
    ticker := time.NewTicker(sl.reloadInterval)
    go func() {
        for {
            select {
            case <-ticker.C:
                if err := sl.loadMeta(); err != nil {
                    logger.Error().Err(err).Msg("reload symbol meta failed")
                }
            case <-sl.done:
                return
            }
        }
    }()
}
```

### 加载元数据

```go
func (sl *SymbolLoader) loadMeta() error {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // 加载 SpotMeta
    spotMeta, err := sl.client.SpotMeta(ctx)
    if err != nil {
        return err
    }

    // 构建现货缓存
    for _, spotInfo := range spotMeta.Universe {
        baseCoin := hyperliquid.MainnetToAlias(spotMeta.Tokens[spotInfo.Tokens[0]].Name)
        quoteCoin := spotMeta.Tokens[spotInfo.Tokens[1]].Name
        symbol := baseCoin + quoteCoin

        sl.cache.SetSpotSymbol(spotInfo.Name, symbol)  // 自动双向
    }

    // 加载 PerpMeta（类似逻辑）
    perpMeta, err := sl.client.PerpMeta(ctx)
    if err != nil {
        return err
    }

    for _, meta := range perpMeta {
        for _, assetInfo := range meta.Universe {
            cleanName := assetInfo.Name
            if strings.Contains(assetInfo.Name, ":") {
                parts := strings.Split(assetInfo.Name, ":")
                if len(parts) == 2 && parts[0] == "xyz" {
                    cleanName = parts[1]
                }
            }

            symbol := cleanName + "USDC"
            sl.cache.SetPerpSymbol(cleanName, symbol)
        }
    }

    return nil
}
```

## 调用方改造

### main.go

```go
// 旧代码
symbolConverter, err := ws.NewSymbolConverter(hyperliquid.MainnetAPIURL)
subManager := ws.NewSubscriptionManager(poolManager, publisher, symbolConverter, balanceCalc)
posManager := position.NewPositionManager(poolManager.Client(), priceCache, symbolConverter)

// 新代码
symbolCache := cache.NewSymbolCache()
symbolLoader := ws.NewSymbolLoader(symbolCache, hyperliquid.MainnetAPIURL)
defer symbolLoader.Close()
symbolLoader.Start()

subManager := ws.NewSubscriptionManager(poolManager, publisher, symbolCache, balanceCalc)
posManager := position.NewPositionManager(poolManager.Client(), priceCache, symbolCache)
```

### ws.SubscriptionManager

```go
// 旧：参数类型 ws.SymbolConverterInterface
func NewSubscriptionManager(..., sc SymbolConverterInterface, ...)

// 新：参数类型 *cache.SymbolCache
func NewSubscriptionManager(..., symbolCache *cache.SymbolCache, ...)

// 使用处
func (sm *SubscriptionManager) handleOrderFill(...) {
    symbol, err := sm.symbolCache.GetSpotSymbol(fill.Coin)
}
```

### position.PositionManager

```go
// 旧：字段 ws.SymbolConverter
type PositionManager struct {
    symbolConverter *ws.SymbolConverter
}

// 新：字段 *cache.SymbolCache
type PositionManager struct {
    symbolCache *cache.SymbolCache
}

// 使用处
func (m *PositionManager) processPositionCache(...) {
    converted, err := m.symbolCache.GetPerpSymbol(coin)
}
```

## 错误处理

### 关键场景

1. **查询未找到的 key**
   - `concurrent.Map.Load()` 返回 `(value, false)`
   - 调用方检查 `ok` 布尔值

2. **SymbolLoader 首次加载失败**
   - `NewSymbolLoader()` 返回错误
   - `main.go` 中 `Fatal` 级别退出

3. **后台重载失败**
   - 记录 Error 日志
   - 继续使用旧缓存数据
   - 不影响服务运行

4. **并发写入冲突**
   - `concurrent.Map` 内部处理
   - 后一个 `Store` 会覆盖前一个
   - 对于缓存场景可接受

## 测试策略

### cache/symbol_cache_test.go

```go
func TestSymbolCache_BidirectionalMapping()
func TestSymbolCache_ConcurrentAccess()
func TestSymbolCache_Stats()
```

### ws/symbol_loader_test.go

```go
func TestSymbolLoader_LoadMeta()
func TestSymbolLoader_ReloadLoop()
```

## 文件变更清单

### 新增文件
- `internal/ws/symbol_loader.go`

### 修改文件
- `internal/cache/symbol_cache.go` - 添加反向映射和双向 API
- `internal/cache/interface.go` - 移除 SymbolCacheInterface
- `internal/ws/subscription.go` - 修改参数类型
- `internal/position/manager.go` - 修改字段类型
- `cmd/hl_monitor/main.go` - 更新初始化逻辑

### 删除文件
- `internal/ws/symbol_converter.go`

## 设计原则

1. **职责分离**：缓存层不依赖外部服务，加载层独立管理更新逻辑
2. **双向索引**：统一的 Set 方法自动维护正向+反向映射
3. **无锁并发**：基于 `concurrent.Map` 实现高性能并发访问
4. **容错设计**：重载失败不影响服务，使用旧缓存数据
5. **简洁 API**：移除接口抽象，直接使用具体类型
