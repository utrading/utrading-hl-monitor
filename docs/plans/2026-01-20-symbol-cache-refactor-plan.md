# SymbolCache 重构实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**目标:** 将 `ws.SymbolConverter` 拆分为 `cache.SymbolCache`（纯缓存）和 `ws.SymbolLoader`（数据加载），使用 `concurrent.Map` 实现无锁双向索引。

**架构:** 分离关注点 - SymbolCache 只做存储（不依赖外部服务），SymbolLoader 负责定期从 Hyperliquid API 加载元数据并更新缓存。

**技术栈:** Go 1.23+, concurrent.Map (sync.Map 泛型封装), hyperliquid-go SDK

---

## Task 1: 增强 cache.SymbolCache 添加反向映射

**文件:**
- 修改: `internal/cache/symbol_cache.go`

**Step 1: 修改 SymbolCache 结构添加反向映射**

```go
type SymbolCache struct {
    // 现货双向映射
    spotNameToSymbol concurrent.Map[string, string] // "@123" -> "ETHUSDC"
    spotSymbolToName concurrent.Map[string, string] // "ETHUSDC" -> "@123"

    // 合约双向映射
    perpNameToSymbol concurrent.Map[string, string] // "BTC" -> "BTCUSDC"
    perpSymbolToName concurrent.Map[string, string] // "BTCUSDC" -> "BTC"
}
```

**Step 2: 添加反向查询方法**

在文件末尾添加：

```go
// GetSpotName 根据 symbol 获取现货 coin（反向查询）
func (c *SymbolCache) GetSpotName(symbol string) (string, bool) {
    return c.spotSymbolToName.Load(symbol)
}

// GetPerpName 根据 symbol 获取合约 coin（反向查询）
func (c *SymbolCache) GetPerpName(symbol string) (string, bool) {
    return c.perpSymbolToName.Load(symbol)
}
```

**Step 3: 修改 Set 方法自动维护反向索引**

修改现有的 `SetSpotSymbol` 和 `SetPerpSymbol` 方法：

```go
// SetSpotSymbol 设置现货 symbol（自动维护双向索引）
func (c *SymbolCache) SetSpotSymbol(coin string, symbol string) {
    c.spotNameToSymbol.Store(coin, symbol)
    c.spotSymbolToName.Store(symbol, coin)
}

// SetPerpSymbol 设置合约 symbol（自动维护双向索引）
func (c *SymbolCache) SetPerpSymbol(coin, symbol string) {
    c.perpNameToSymbol.Store(coin, symbol)
    c.perpSymbolToName.Store(symbol, coin)
}
```

**Step 4: 验证编译**

Run: `go build ./internal/cache/...`
Expected: 无错误

**Step 5: 提交**

```bash
git add internal/cache/symbol_cache.go
git commit -m "refactor(cache): 增强 SymbolCache 添加双向映射"
```

---

## Task 2: 创建 ws.SymbolLoader 组件

**文件:**
- 创建: `internal/ws/symbol_loader.go`

**Step 1: 创建文件并编写基本结构**

```go
package ws

import (
    "context"
    "strings"
    "time"

    "github.com/sonirico/go-hyperliquid"
    "github.com/utrading/utrading-hl-monitor/internal/cache"
    "github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// SymbolLoader Symbol 元数据加载器
// 定期从 Hyperliquid API 加载元数据并更新缓存
type SymbolLoader struct {
    cache          *cache.SymbolCache
    client         *hyperliquid.Info
    httpURL        string
    reloadInterval time.Duration
    done           chan struct{}
}

// NewSymbolLoader 创建 SymbolLoader
// 首次加载失败会返回错误
func NewSymbolLoader(symbolCache *cache.SymbolCache, httpURL string) (*SymbolLoader, error) {
    sl := &SymbolLoader{
        cache:          symbolCache,
        client:         hyperliquid.NewInfo(context.TODO(), httpURL, false, nil, nil),
        httpURL:        httpURL,
        reloadInterval: 2 * time.Hour,
        done:           make(chan struct{}),
    }

    // 首次加载（阻塞）
    if err := sl.loadMeta(); err != nil {
        return nil, err
    }

    logger.Info().
        Int("spot_count", sl.getSpotCount()).
        Int("perp_count", sl.getPerpCount()).
        Msg("SymbolLoader initialized")

    return sl, nil
}

// Start 启动后台重载
func (sl *SymbolLoader) Start() {
    ticker := time.NewTicker(sl.reloadInterval)
    go func() {
        defer ticker.Stop()
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

// Close 停止重载
func (sl *SymbolLoader) Close() {
    close(sl.done)
}
```

**Step 2: 实现 loadMeta 方法**

在文件末尾添加：

```go
// loadMeta 从 Hyperliquid API 加载元数据
func (sl *SymbolLoader) loadMeta() error {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    // 加载 SpotMeta
    spotMeta, err := sl.client.SpotMeta(ctx)
    if err != nil {
        return err
    }

    // 构建现货缓存
    sl.buildSpotCache(spotMeta)

    // 加载 PerpMeta
    perpMeta, err := sl.client.PerpMeta(ctx)
    if err != nil {
        return err
    }

    // 构建合约缓存
    sl.buildPerpCache(perpMeta)

    logger.Info().
        Int("spot_count", sl.getSpotCount()).
        Int("perp_count", sl.getPerpCount()).
        Msg("symbol meta reloaded")

    return nil
}
```

**Step 3: 实现 buildSpotCache 方法**

```go
// buildSpotCache 构建现货缓存
func (sl *SymbolLoader) buildSpotCache(spotMeta *hyperliquid.SpotMeta) {
    spotTokenLen := len(spotMeta.Tokens)

    for _, spotInfo := range spotMeta.Universe {
        // 边界检查
        if len(spotInfo.Tokens) < 2 ||
            spotTokenLen <= spotInfo.Tokens[1] ||
            spotTokenLen <= spotInfo.Tokens[0] {
            continue
        }

        baseToken := spotMeta.Tokens[spotInfo.Tokens[0]]
        quoteCoin := spotMeta.Tokens[spotInfo.Tokens[1]].Name

        // 调用 MainnetToAlias 处理 U 前缀（如 USOL -> SOL）
        baseCoin := hyperliquid.MainnetToAlias(baseToken.Name)

        symbol := baseCoin + quoteCoin
        sl.cache.SetSpotSymbol(spotInfo.Name, symbol)
    }
}
```

**Step 4: 实现 buildPerpCache 方法**

```go
// buildPerpCache 构建合约缓存
func (sl *SymbolLoader) buildPerpCache(perpMeta []*hyperliquid.Meta) {
    for _, meta := range perpMeta {
        for _, assetInfo := range meta.Universe {
            // 处理 xyz:BTC 格式
            cleanName := assetInfo.Name
            if strings.Contains(assetInfo.Name, ":") {
                parts := strings.Split(assetInfo.Name, ":")
                if len(parts) == 2 && parts[0] == "xyz" {
                    cleanName = parts[1]
                }
            }

            // 统一加 USDC 后缀
            symbol := cleanName + "USDC"
            sl.cache.SetPerpSymbol(cleanName, symbol)

            // 也缓存原始名称（如果是 xyz: 格式）
            if assetInfo.Name != cleanName {
                sl.cache.SetPerpSymbol(assetInfo.Name, symbol)
            }
        }
    }
}
```

**Step 5: 添加辅助方法**

```go
// getSpotCount 获取现货缓存数量
func (sl *SymbolLoader) getSpotCount() int {
    stats := sl.cache.Stats()
    if spotCount, ok := stats["spot_count"].(int64); ok {
        return int(spotCount)
    }
    return 0
}

// getPerpCount 获取合约缓存数量
func (sl *SymbolLoader) getPerpCount() int {
    stats := sl.cache.Stats()
    if perpCount, ok := stats["perp_count"].(int64); ok {
        return int(perpCount)
    }
    return 0
}
```

**Step 6: 验证编译**

Run: `go build ./internal/ws/...`
Expected: 无错误

**Step 7: 提交**

```bash
git add internal/ws/symbol_loader.go
git commit -m "feat(ws): 创建 SymbolLoader 组件实现元数据加载"
```

---

## Task 3: 修改 ws.SubscriptionManager 使用 SymbolCache

**文件:**
- 修改: `internal/ws/subscription.go`

**Step 1: 修改 SubscriptionManager 结构**

将字段类型从 `SymbolConverterInterface` 改为 `*cache.SymbolCache`：

```go
type SubscriptionManager struct {
    // ... 其他字段 ...
    symbolCache *cache.SymbolCache  // 原: symbolConverter SymbolConverterInterface
    // ... 其他字段 ...
}
```

**Step 2: 修改 NewSubscriptionManager 函数签名**

```go
func NewSubscriptionManager(
    poolManager *PoolManager,
    publisher Publisher,
    symbolCache *cache.SymbolCache,  // 原参数名: sc, 类型: SymbolConverterInterface
    balanceCalc BalanceCalculatorInterface,
) *SubscriptionManager {
    // ...
    sm.symbolCache = symbolCache  // 原: sm.symbolConverter = sc
    // ...
}
```

**Step 3: 修改 handleOrderFill 中的调用**

找到使用 `sm.symbolConverter.Convert` 的地方，改为使用 `sm.symbolCache`：

```go
// 原代码:
// symbol, err := sm.symbolConverter.Convert(fill.Coin, fill.Dir)

// 新代码:
var symbol string
var err error
if isSpotDir(fill.Dir) {
    symbol, err = sm.getSpotSymbol(fill.Coin)
} else {
    symbol, err = sm.getPerpSymbol(fill.Coin)
}
if err != nil {
    logger.Warn().
        Str("coin", fill.Coin).
        Str("dir", fill.Dir).
        Err(err).
        Msg("symbol not found, using raw coin")
    symbol = fill.Coin
}
```

**Step 4: 添加辅助方法**

在 SubscriptionManager 中添加：

```go
// isSpotDir 判断是否为现货方向
func (sm *SubscriptionManager) isSpotDir(dir string) bool {
    return dir == "Buy" || dir == "Sell"
}

// getSpotSymbol 获取现货 symbol
func (sm *SubscriptionManager) getSpotSymbol(coin string) (string, error) {
    symbol, ok := sm.symbolCache.GetSpotSymbol(coin)
    if !ok {
        return "", fmt.Errorf("spot coin not found: %s", coin)
    }
    return symbol, nil
}

// getPerpSymbol 获取合约 symbol
func (sm *SubscriptionManager) getPerpSymbol(coin string) (string, error) {
    // 处理 xyz:BTC 格式
    cleanCoin := coin
    if strings.Contains(coin, ":") {
        parts := strings.Split(coin, ":")
        if len(parts) == 2 && parts[0] == "xyz" {
            cleanCoin = parts[1]
        }
    }

    symbol, ok := sm.symbolCache.GetPerpSymbol(cleanCoin)
    if !ok {
        return "", fmt.Errorf("perp coin not found: %s", cleanCoin)
    }
    return symbol, nil
}
```

**Step 5: 验证编译**

Run: `go build ./internal/ws/...`
Expected: 无错误

**Step 6: 提交**

```bash
git add internal/ws/subscription.go
git commit -m "refactor(ws): SubscriptionManager 使用 SymbolCache 替换 SymbolConverter"
```

---

## Task 4: 修改 position.PositionManager 使用 SymbolCache

**文件:**
- 修改: `internal/position/manager.go`

**Step 1: 修改 PositionManager 结构**

```go
type PositionManager struct {
    // ... 其他字段 ...
    symbolCache *cache.SymbolCache  // 原: symbolConverter *ws.SymbolConverter
    // ... 其他字段 ...
}
```

**Step 2: 修改 NewPositionManager 函数签名**

```go
func NewPositionManager(
    pool *hyperliquid.WebsocketClient,
    priceCache *cache.PriceCache,
    symbolCache *cache.SymbolCache,  // 原: symbolConverter *ws.SymbolConverter
) *PositionManager {
    return &PositionManager{
        // ...
        symbolCache: symbolCache,  // 原: symbolConverter: symbolConverter
        // ...
    }
}
```

**Step 3: 修改 processPositionCache 中的调用**

找到使用 `m.symbolConverter.Convert` 的地方，改为使用 `m.symbolCache`：

```go
// 原代码:
// if m.symbolConverter != nil {
//     if converted, err := m.symbolConverter.Convert(coin, "Long"); err == nil {
//         coin = converted
//     }
// }

// 新代码:
if m.symbolCache != nil {
    if converted, ok := m.symbolCache.GetPerpSymbol(coin); ok {
        coin = converted
    }
}
```

**Step 4: 验证编译**

Run: `go build ./internal/position/...`
Expected: 无错误

**Step 5: 提交**

```bash
git add internal/position/manager.go
git commit -m "refactor(position): PositionManager 使用 SymbolCache 替换 SymbolConverter"
```

---

## Task 5: 修改 main.go 使用新的组件

**文件:**
- 修改: `cmd/hl_monitor/main.go`

**Step 1: 修改初始化逻辑**

找到 `// 初始化 SymbolConverter` 部分，替换为：

```go
// 创建 SymbolCache
symbolCache := cache.NewSymbolCache()

// 创建 SymbolLoader 并启动
symbolLoader, err := ws.NewSymbolLoader(symbolCache, hyperliquid.MainnetAPIURL)
if err != nil {
    logger.Fatal().Err(err).Msg("init symbol loader failed")
}
defer symbolLoader.Close()

// 启动后台重载
symbolLoader.Start()
```

**Step 2: 修改 SubscriptionManager 初始化**

```go
// 原代码:
// subManager := ws.NewSubscriptionManager(poolManager, publisher, symbolConverter, balanceCalc)

// 新代码:
subManager := ws.NewSubscriptionManager(poolManager, publisher, symbolCache, balanceCalc)
```

**Step 3: 修改 PositionManager 初始化**

```go
// 原代码:
// posManager := position.NewPositionManager(poolManager.Client(), priceCache, symbolConverter)

// 新代码:
posManager := position.NewPositionManager(poolManager.Client(), priceCache, symbolCache)
```

**Step 4: 验证编译**

Run: `go build ./cmd/hl_monitor`
Expected: 无错误

**Step 5: 提交**

```bash
git add cmd/hl_monitor/main.go
git commit -m "refactor(main): 使用 SymbolLoader 和 SymbolCache 替换 SymbolConverter"
```

---

## Task 6: 删除废弃文件和接口

**文件:**
- 删除: `internal/ws/symbol_converter.go`
- 修改: `internal/cache/interface.go`

**Step 1: 删除 SymbolConverter 文件**

```bash
rm internal/ws/symbol_converter.go
```

**Step 2: 删除测试文件（如果存在）**

```bash
rm internal/ws/symbol_converter_test.go
```

**Step 3: 从 interface.go 移除 SymbolCacheInterface**

编辑 `internal/cache/interface.go`，删除：

```go
// SymbolCacheInterface Symbol 缓存接口
type SymbolCacheInterface interface {
    GetSpotSymbol(assetID int) (string, bool)
    SetSpotSymbol(assetID int, symbol string)
    GetPerpSymbol(coin string) (string, bool)
    SetPerpSymbol(coin, symbol string)
    Stats() map[string]interface{}
}
```

**Step 4: 验证编译**

Run: `go build ./...`
Expected: 无错误

**Step 5: 提交**

```bash
git add internal/cache/interface.go
git rm internal/ws/symbol_converter.go internal/ws/symbol_converter_test.go
git commit -m "refactor: 删除废弃的 SymbolConverter 和接口"
```

---

## Task 7: 更新 CLAUDE.md 文档

**文件:**
- 修改: `CLAUDE.md`

**Step 1: 更新缓存层架构表**

找到缓存层架构表格，更新 SymbolCache 描述：

```markdown
| 缓存类型 | 实现库 | 用途 | TTL |
|---------|-------|------|-----|
| DedupCache | go-cache | 订单去重（address-oid-direction） | 30 分钟 |
| SymbolCache | concurrent.Map | Symbol 双向转换（coin↔symbol） | 持久 |
| PriceCache | concurrent.Map | 现货/合约价格缓存 | LRU |
```

**Step 2: 添加 SymbolLoader 说明**

在缓存层架构后面添加新章节：

```markdown
### Symbol 元数据管理

`ws.SymbolLoader` 负责从 Hyperliquid API 定期加载 Symbol 元数据：

1. **初始化**：首次加载失败会导致服务启动失败
2. **后台重载**：每 2 小时自动更新一次
3. **容错处理**：重载失败不影响服务，继续使用旧数据

```go
symbolCache := cache.NewSymbolCache()
symbolLoader := ws.NewSymbolLoader(symbolCache, hyperliquid.MainnetAPIURL)
defer symbolLoader.Close()
symbolLoader.Start()

// 使用
symbol, ok := symbolCache.GetSpotSymbol("@123")  // "ETHUSDC"
coin, ok := symbolCache.GetSpotName("ETHUSDC")   // "@123"
```
```

**Step 3: 移除 SymbolConverter 相关描述**

删除所有提到 `SymbolConverter` 的部分。

**Step 4: 提交**

```bash
git add CLAUDE.md
git commit -m "docs: 更新文档说明 SymbolCache 和 SymbolLoader"
```

---

## Task 8: 添加单元测试

**文件:**
- 创建: `internal/cache/symbol_cache_test.go`
- 创建: `internal/ws/symbol_loader_test.go`

**Step 1: 创建 SymbolCache 测试**

```go
package cache

import (
    "sync"
    "testing"
)

func TestSymbolCache_BidirectionalMapping(t *testing.T) {
    cache := NewSymbolCache()

    // 设置现货映射
    cache.SetSpotSymbol("@123", "ETHUSDC")

    // 正向查询
    symbol, ok := cache.GetSpotSymbol("@123")
    if !ok || symbol != "ETHUSDC" {
        t.Fatalf("GetSpotSymbol failed: got %s, %v", symbol, ok)
    }

    // 反向查询
    coin, ok := cache.GetSpotName("ETHUSDC")
    if !ok || coin != "@123" {
        t.Fatalf("GetSpotName failed: got %s, %v", coin, ok)
    }
}

func TestSymbolCache_ConcurrentAccess(t *testing.T) {
    cache := NewSymbolCache()
    var wg sync.WaitGroup

    // 并发写入
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(n int) {
            defer wg.Done()
            cache.SetSpotSymbol("@123", "ETHUSDC")
            cache.GetSpotSymbol("@123")
        }(i)
    }

    wg.Wait()

    // 验证数据一致性
    symbol, ok := cache.GetSpotSymbol("@123")
    if !ok || symbol != "ETHUSDC" {
        t.Fatalf("concurrent access broke data: got %s, %v", symbol, ok)
    }
}

func TestSymbolCache_Stats(t *testing.T) {
    cache := NewSymbolCache()

    cache.SetSpotSymbol("@123", "ETHUSDC")
    cache.SetPerpSymbol("BTC", "BTCUSDC")

    stats := cache.Stats()

    spotCount, ok := stats["spot_count"].(int64)
    if !ok || spotCount != 1 {
        t.Fatalf("spot_count wrong: got %d, %v", spotCount, ok)
    }

    perpCount, ok := stats["perp_count"].(int64)
    if !ok || perpCount != 1 {
        t.Fatalf("perp_count wrong: got %d, %v", perpCount, ok)
    }
}
```

**Step 2: 运行测试**

Run: `go test ./internal/cache/symbol_cache_test.go -v`
Expected: 全部 PASS

**Step 3: 创建 SymbolLoader 测试**

```go
package ws

import (
    "testing"
    "time"

    "github.com/utrading/utrading-hl-monitor/internal/cache"
)

func TestSymbolLoader_NewSymbolLoader(t *testing.T) {
    symbolCache := cache.NewSymbolCache()

    // 使用测试 API URL（如果有的话）
    loader, err := NewSymbolLoader(symbolCache, "https://api.hyperliquid.xyz/info")
    if err != nil {
        t.Skipf("Skip test due to API error: %v", err)
    }
    defer loader.Close()

    // 验证缓存被填充
    stats := symbolCache.Stats()
    spotCount := stats["spot_count"].(int64)
    perpCount := stats["perp_count"].(int64)

    if spotCount == 0 {
        t.Error("spot cache should not be empty")
    }
    if perpCount == 0 {
        t.Error("perp cache should not be empty")
    }

    t.Logf("Loaded %d spot symbols, %d perp symbols", spotCount, perpCount)
}

func TestSymbolLoader_ReloadLoop(t *testing.T) {
    symbolCache := cache.NewSymbolCache()

    loader, err := NewSymbolLoader(symbolCache, "https://api.hyperliquid.xyz/info")
    if err != nil {
        t.Skipf("Skip test due to API error: %v", err)
    }
    defer loader.Close()

    // 启动重载循环
    loader.Start()

    // 等待一小段时间
    time.Sleep(100 * time.Millisecond)

    // 验证没有 panic 或死锁
    t.Log("reload loop started successfully")
}
```

**Step 4: 运行测试**

Run: `go test ./internal/ws/symbol_loader_test.go -v`
Expected: 全部 PASS（或因 API 不可用而 SKIP）

**Step 5: 提交**

```bash
git add internal/cache/symbol_cache_test.go internal/ws/symbol_loader_test.go
git commit -m "test: 添加 SymbolCache 和 SymbolLoader 单元测试"
```

---

## Task 9: 整体验证和清理

**Step 1: 完整编译测试**

Run: `go build ./...`
Expected: 无错误

**Step 2: 运行所有测试**

Run: `go test ./... -v`
Expected: 现有测试全部 PASS，新增测试 PASS

**Step 3: 检查未使用的导入**

Run: `goimports -l .`
如有未使用的导入，手动清理或运行：
```bash
goimports -w .
```

**Step 4: 验证 git status**

Run: `git status`
确认只有预期的文件被修改

**Step 5: 创建最终汇总提交**

```bash
git add -A
git commit -m "feat: 完成 SymbolCache 重构

- 拆分 SymbolConverter 为 SymbolCache 和 SymbolLoader
- SymbolCache 支持双向查询（coin ↔ symbol）
- 使用 concurrent.Map 实现无锁并发访问
- SymbolLoader 定期从 API 加载元数据
- 移除废弃的 SymbolCacheInterface"
```

---

## 验收标准

1. ✅ 所有代码编译通过
2. ✅ 所有单元测试通过
3. ✅ main.go 使用新的 SymbolLoader 和 SymbolCache
4. ✅ SymbolConverter 文件已删除
5. ✅ SymbolCacheInterface 接口已删除
6. ✅ 文档已更新（CLAUDE.md）
7. ✅ 支持双向查询（GetSpotName、GetPerpName）
8. ✅ 后台重载每 2 小时执行一次
9. ✅ 重载失败不影响服务运行
