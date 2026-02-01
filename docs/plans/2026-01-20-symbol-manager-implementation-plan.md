# SymbolManager 组件实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**目标:** 创建 SymbolManager 纯容器组件，统一管理 SymbolCache、SymbolLoader 和 PriceCache，确保 Symbol 数据在服务启动时加载完成。

**架构:** 纯容器模式 - Manager 封装三个缓存组件的生命周期，SymbolLoader 立即加载数据（fail-fast），提供访问器方法获取各缓存实例。

**技术栈:** Go 1.23+, concurrent.Map, hyperliquid-go SDK

---

## Task 1: 创建 symbol 包目录

**Files:**
- Create: `internal/symbol/`

**Step 1: 创建目录**

Run: `mkdir -p internal/symbol`

Expected: 目录创建成功

**Step 2: 验证**

Run: `ls -la internal/symbol`
Expected: 空目录

**Step 3: 提交**

```bash
git add internal/symbol/
git commit -m "feat(symbol): 创建 symbol 包目录"
```

---

## Task 2: 移动 SymbolLoader 到 symbol 包

**Files:**
- Move: `internal/ws/symbol_loader.go` → `internal/symbol/loader.go`
- Move: `internal/ws/symbol_loader_test.go` → `internal/symbol/loader_test.go`

**Step 1: 使用 git mv 移动文件**

```bash
git mv internal/ws/symbol_loader.go internal/symbol/loader.go
git mv internal/ws/symbol_loader_test.go internal/symbol/loader_test.go
```

**Step 2: 验证文件已移动**

Run: `ls -la internal/symbol/`
Expected: 看到 loader.go 和 loader_test.go

**Step 3: 提交**

```bash
git add internal/symbol/
git commit -m "refactor(symbol): 移动 SymbolLoader 到 symbol 包"
```

---

## Task 3: 修改 loader.go 的 package 和类型名

**Files:**
- Modify: `internal/symbol/loader.go`

**Step 1: 修改 package 声明**

```go
// 原代码:
package ws

// 新代码:
package symbol
```

**Step 2: 修改类型名 SymbolLoader → Loader**

全文替换 `SymbolLoader` 为 `Loader`：
```go
// 原代码:
type SymbolLoader struct {
// ...
func NewSymbolLoader(

// 新代码:
type Loader struct {
// ...
func NewLoader(
```

**Step 3: 修改导入路径（如果需要）

确保导入路径正确：
```go
import (
    "context"
    "strings"
    "time"

    "github.com/sonirico/go-hyperliquid"
    "github.com/utrading/utrading-hl-monitor/internal/cache"
    "github.com/utrading/utrading-hl-monitor/pkg/logger"
)
```

**Step 4: 验证编译**

Run: `go build ./internal/symbol/...`
Expected: 无错误

**Step 5: 提交**

```bash
git add internal/symbol/loader.go
git commit -m "refactor(symbol): 重命名 SymbolLoader 为 Loader"
```

---

## Task 4: 修改 loader_test.go

**Files:**
- Modify: `internal/symbol/loader_test.go`

**Step 1: 修改 package 声明**

```go
// 原代码:
package ws

// 新代码:
package symbol
```

**Step 2: 更新所有测试中的类型引用**

将 `SymbolLoader` 改为 `Loader`：
```go
// 原代码:
func TestSymbolLoader_NewSymbolLoader(t *testing.T) {
    loader, err := NewSymbolLoader(

// 新代码:
func TestLoader_NewLoader(t *testing.T) {
    loader, err := NewLoader(
```

同样更新其他测试函数名称和类型引用。

**Step 3: 运行测试**

Run: `go test ./internal/symbol/... -short -v`
Expected: 测试通过（或因 API 不可用而 SKIP）

**Step 4: 提交**

```bash
git add internal/symbol/loader_test.go
git commit -m "refactor(symbol): 更新测试使用新的类型名"
```

---

## Task 5: 创建 manager.go

**Files:**
- Create: `internal/symbol/manager.go`

**Step 1: 创建文件并编写代码**

```go
package symbol

import (
    "github.com/utrading/utrading-hl-monitor/internal/cache"
)

// Manager Symbol 管理器（纯容器）
// 统一管理 SymbolCache、Loader 和 PriceCache 的生命周期
type Manager struct {
    symbolCache *cache.SymbolCache
    loader      *Loader
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

// SymbolCache 返回 Symbol 缓存
func (m *Manager) SymbolCache() *cache.SymbolCache {
    return m.symbolCache
}

// PriceCache 返回价格缓存
func (m *Manager) PriceCache() *cache.PriceCache {
    return m.priceCache
}
```

**Step 2: 验证编译**

Run: `go build ./internal/symbol/...`
Expected: 无错误

**Step 3: 提交**

```bash
git add internal/symbol/manager.go
git commit -m "feat(symbol): 创建 SymbolManager 纯容器"
```

---

## Task 6: 更新 ws.SubscriptionManager 导入

**Files:**
- Modify: `internal/ws/subscription.go`

**Step 1: 添加 symbol 包导入**

在 import 区块添加：
```go
import (
    // ... 其他导入 ...
    "github.com/utrading/utrading-hl-monitor/internal/symbol"
    // ...
)
```

**Step 2: 验证编译**

Run: `go build ./internal/ws/...`
Expected: 无错误

**Step 3: 提交**

```bash
git add internal/ws/subscription.go
git commit -m "refactor(ws): 添加 symbol 包导入"
```

---

## Task 7: 更新 processor.OrderProcessor 导入（如果需要）

**Files:**
- Modify: `internal/processor/order_processor.go`

**Step 1: 检查是否需要导入**

Run: `grep -n "symbol" internal/processor/order_processor.go`
Expected: 确认是否需要导入

**Step 2: 如果需要，添加导入**

在 import 区块添加：
```go
import (
    // ... 其他导入 ...
    "github.com/utrading/utrading-hl-monitor/internal/symbol"
    // ...
)
```

**Step 3: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 无错误

**Step 4: 提交（如果有修改）**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor(processor): 添加 symbol 包导入"
```

---

## Task 8: 更新 position.PositionManager 导入（如果需要）

**Files:**
- Modify: `internal/position/manager.go`

**Step 1: 检查是否需要导入**

Run: `grep -n "symbol" internal/position/manager.go`
Expected: 确认是否需要导入

**Step 2: 如果需要，添加导入**

在 import 区块添加：
```go
import (
    // ... 其他导入 ...
    "github.com/utrading/utrading-hl-monitor/internal/symbol"
    // ...
)
```

**Step 3: 验证编译**

Run: `go build ./internal/position/...`
Expected: 无错误

**Step 4: 提交（如果有修改）**

```bash
git add internal/position/manager.go
git commit -m "refactor(position): 添加 symbol 包导入"
```

---

## Task 9: 修改 main.go 使用 SymbolManager

**Files:**
- Modify: `cmd/hl_monitor/main.go`

**Step 1: 添加 symbol 包导入**

```go
import (
    // ... 其他导入 ...
    "github.com/utrading/utrading-hl-monitor/internal/symbol"
    // ...
)
```

**Step 2: 替换 Symbol 缓存初始化代码**

找到以下代码：
```go
// 创建 Symbol 转换缓存
symbolCache := cache.NewSymbolCache()

// 创建现货价格缓存
priceCache := cache.NewPriceCache()
```

替换为：
```go
// 创建 Symbol 管理器（内部会加载 Symbol 数据）
symbolManager, err := symbol.NewManager(hyperliquid.MainnetAPIURL)
if err != nil {
    logger.Fatal().Err(err).Msg("init symbol manager failed")
}
defer symbolManager.Close()
```

**Step 3: 修改 SubscriptionManager 初始化**

找到：
```go
subManager := ws.NewSubscriptionManager(poolManager, publisher, symbolCache, balanceCalc)
```

改为：
```go
subManager := ws.NewSubscriptionManager(poolManager, publisher, symbolManager.SymbolCache(), balanceCalc)
```

**Step 4: 修改 PositionManager 初始化**

找到：
```go
posManager := position.NewPositionManager(poolManager.Client(), priceCache, symbolCache)
```

改为：
```go
posManager := position.NewPositionManager(
    poolManager.Client(),
    symbolManager.PriceCache(),
    symbolManager.SymbolCache(),
)
```

**Step 5: 删除未使用的 cache 导入（如果现在未使用）

检查 `internal/cache` 是否还被使用，如果没有则从导入中移除。

**Step 6: 验证编译**

Run: `go build ./cmd/hl_monitor/`
Expected: 无错误

**Step 7: 提交**

```bash
git add cmd/hl_monitor/main.go
git commit -m "refactor(main): 使用 SymbolManager 替换直接初始化缓存"
```

---

## Task 10: 删除旧的 symbol_loader.go 引用

**Files:**
- Modify: `internal/ws/` (检查是否有残留引用)

**Step 1: 检查残留引用**

Run: `grep -r "symbol_loader" internal/ws/`
Expected: 无输出（如果有的话需要清理）

**Step 2: 检查 SymbolLoader 引用**

Run: `grep -r "SymbolLoader" internal/ws/`
Expected: 无输出（如果有的话需要清理）

**Step 3: 验证编译**

Run: `go build ./...`
Expected: 无错误

**Step 4: 提交（如果有修改）**

```bash
git add -A
git commit -m "refactor: 清理旧的 SymbolLoader 引用"
```

---

## Task 11: 运行测试验证

**Step 1: 运行所有测试**

Run: `go test ./... -short -v 2>&1 | grep -E "(PASS|FAIL|ok)"`
Expected: 所有测试 PASS

**Step 2: 完整编译验证**

Run: `go build ./...`
Expected: 无错误

**Step 3: 检查 git status**

Run: `git status`
Expected: 无未提交的修改

**Step 4: 查看提交历史**

Run: `git log --oneline -5`
Expected: 看到所有相关提交

---

## Task 12: 更新文档

**Files:**
- Modify: `CLAUDE.md`

**Step 1: 更新 Symbol 元数据管理章节**

找到 Symbol 元数据管理章节，更新为：
```markdown
### Symbol 元数据管理

`symbol.Manager` 统一管理 Symbol 相关的缓存和数据加载：

```go
import "github.com/utrading/utrading-hl-monitor/internal/symbol"

// 创建管理器（会立即加载 Symbol 数据）
symbolManager, err := symbol.NewManager(hyperliquid.MainnetAPIURL)
if err != nil {
    logger.Fatal().Err(err).Msg("init symbol manager failed")
}
defer symbolManager.Close()

// 使用缓存
symbol, ok := symbolManager.SymbolCache().GetSpotSymbol("@123")
price, ok := symbolManager.PriceCache().GetSpotPrice("ETHUSDC")
```

**组件：**
- `symbol.Loader`: 定期从 Hyperliquid API 加载元数据（每 2 小时）
- `cache.SymbolCache`: 双向映射（coin ↔ symbol），基于 concurrent.Map
- `cache.PriceCache`: 现货/合约价格缓存
```

**Step 2: 移除旧的 SymbolConverter 描述

确保没有残留的 `SymbolConverter` 引用。

**Step 3: 验证**

Run: `go build ./...`
Expected: 无错误

**Step 4: 提交**

```bash
git add CLAUDE.md
git commit -m "docs: 更新文档说明 SymbolManager"
```

---

## 验收标准

1. ✅ `internal/symbol` 包已创建
2. ✅ `SymbolLoader` 已移动并重命名为 `Loader`
3. ✅ `Manager` 纯容器已创建
4. ✅ `main.go` 使用 `symbol.NewManager()`
5. ✅ Symbol 数据在启动时加载完成
6. ✅ 所有代码编译通过
7. ✅ 所有测试通过
8. ✅ 文档已更新
9. ✅ 无残留的 `SymbolLoader` 引用
