# SymbolManager 组件设计

## 目标

创建 `SymbolManager` 纯容器组件，统一管理 `SymbolCache`、`SymbolLoader` 和 `PriceCache`，简化 main.go 初始化逻辑，确保 Symbol 数据在服务启动时加载完成。

## 架构

### 组件职责

`internal/symbol.Manager` 是纯容器，负责：
1. **统一初始化**：创建并管理三个缓存组件的生命周期
2. **封装启动逻辑**：SymbolLoader 立即加载数据，失败则服务拒绝启动
3. **优雅关闭**：停止 SymbolLoader 的后台重载循环
4. **访问器方法**：提供 getter 方法获取各个缓存实例

### 依赖关系

```
SymbolManager (internal/symbol)
├── SymbolCache (cache.SymbolCache)
│   ├── spotNameToSymbol  (coin → symbol)
│   ├── spotSymbolToName  (symbol → coin)
│   ├── perpNameToSymbol  (coin → symbol)
│   └── perpSymbolToName  (symbol → coin)
├── Loader (symbol.Loader，原 SymbolLoader)
│   └── 定期从 Hyperliquid API 加载元数据
└── PriceCache (cache.PriceCache)
    ├── spotCache (symbol → price)
    └── perpCache (symbol → price)
```

### 文件组织

```
internal/symbol/
├── manager.go       # SymbolManager 纯容器
├── loader.go        # SymbolLoader（从 internal/ws 移动并重命名）
└── loader_test.go   # 测试文件（从 internal/ws 移动）
```

## 数据结构

### Manager 结构

```go
package symbol

import (
    "github.com/utrading/utrading-hl-monitor/internal/cache"
)

// Manager Symbol 管理器（纯容器）
type Manager struct {
    symbolCache *cache.SymbolCache
    loader      *Loader  // 重命名为 Loader（原 SymbolLoader）
    priceCache  *cache.PriceCache
}

// NewManager 创建 Symbol 管理器
// 首次加载失败会返回错误，确保服务启动时 Symbol 数据可用
func NewManager(httpURL string) (*Manager, error) {
    // 1. 创建缓存
    symbolCache := cache.NewSymbolCache()
    priceCache := cache.NewPriceCache()

    // 2. 创建加载器（立即加载，失败返回错误）
    loader, err := NewLoader(symbolCache, httpURL)
    if err != nil {
        return nil, err
    }

    // 3. 启动后台重载
    loader.Start()

    return &Manager{
        symbolCache: symbolCache,
        loader:      loader,
        priceCache:  priceCache,
    }, nil
}

// Close 关闭管理器，停止后台重载
func (m *Manager) Close() error {
    m.loader.Close()
    return nil
}

// 访问器方法
func (m *Manager) SymbolCache() *cache.SymbolCache { return m.symbolCache }
func (m *Manager) PriceCache() *cache.PriceCache  { return m.priceCache }
```

### Loader 重命名

原 `ws.SymbolLoader` 重命名为 `symbol.Loader`：

```go
// Loader Symbol 元数据加载器（原 SymbolLoader）
type Loader struct {
    cache          *cache.SymbolCache
    client         *hyperliquid.Info
    httpURL        string
    reloadInterval time.Duration
    done           chan struct{}
}

// NewLoader 创建加载器（首加载失败返回错误）
func NewLoader(symbolCache *cache.SymbolCache, httpURL string) (*Loader, error)

// Start 启动后台重载
func (sl *Loader) Start()

// Close 停止重载
func (sl *Loader) Close()
```

## main.go 集成

### 修改前（当前状态 - 有 Bug）

```go
// 创建 Symbol 转换缓存
symbolCache := cache.NewSymbolCache()

// 创建现货价格缓存
priceCache := cache.NewPriceCache()

// ... symbolCache 是空的，Symbol 查询会失败！
```

### 修改后

```go
// 创建 Symbol 管理器（内部会加载 Symbol 数据）
symbolManager, err := symbol.NewManager(hyperliquid.MainnetAPIURL)
if err != nil {
    logger.Fatal().Err(err).Msg("init symbol manager failed")
}
defer symbolManager.Close()

// 使用访问器获取缓存
subManager := ws.NewSubscriptionManager(
    poolManager,
    publisher,
    symbolManager.SymbolCache(),  // 替代原来的 symbolCache
    balanceCalc,
)

posManager := position.NewPositionManager(
    poolManager.Client(),
    symbolManager.PriceCache(),   // 替代原来的 priceCache
    symbolManager.SymbolCache(),
)
```

### 改进点

1. **一行代码完成初始化**：`symbol.NewManager()` 封装所有逻辑
2. **Fail-fast**：Symbol 数据加载失败时服务拒绝启动
3. **自动清理**：`defer symbolManager.Close()` 确保资源释放
4. **清晰的接口**：通过访问器方法获取缓存，依赖关系明确

## 实施计划

### 步骤 1：创建 symbol 包并移动文件

```bash
mkdir -p internal/symbol
git mv internal/ws/symbol_loader.go internal/symbol/loader.go
git mv internal/ws/symbol_loader_test.go internal/symbol/loader_test.go
```

### 步骤 2：修改 package 和类型名

**loader.go：**
- `package ws` → `package symbol`
- `type SymbolLoader` → `type Loader`
- `func NewSymbolLoader` → `func NewLoader`

**loader_test.go：**
- 更新所有引用

### 步骤 3：创建 manager.go

纯容器实现，包含：
- `NewManager(httpURL string) (*Manager, error)`
- `Close() error`
- `SymbolCache() *cache.SymbolCache`
- `PriceCache() *cache.PriceCache`

### 步骤 4：更新引用处

| 文件 | 修改内容 |
|------|----------|
| `internal/ws/subscription.go` | 导入 `internal/symbol`，参数类型保持 `*cache.SymbolCache` |
| `internal/processor/order_processor.go` | 导入 `internal/symbol`（如果需要） |
| `internal/position/manager.go` | 导入 `internal/symbol`（如果需要） |
| `cmd/hl_monitor/main.go` | 使用 `symbol.NewManager()` |

### 步骤 5：验证

```bash
go build ./...
go test ./...
```

### 步骤 6：更新文档

更新 `CLAUDE.md` 中的 Symbol 元数据管理章节。

## 设计原则

1. **纯容器模式**：Manager 只管理生命周期，不提供额外功能
2. **Fail-fast**：初始化失败立即返回错误，不启动服务
3. **单一职责**：每个组件职责明确
4. **依赖最小化**：其他包只需导入 `internal/symbol`
5. **向后兼容**：保持 `*cache.SymbolCache` 类型，不影响现有代码

## 预期效果

- ✅ Symbol 数据在服务启动时加载完成
- ✅ main.go 代码更简洁
- ✅ 统一的生命周期管理
- ✅ 更好的可测试性
