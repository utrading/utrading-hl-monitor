# 简化 SpotAssetCtxs 处理架构设计

**日期**: 2026-01-19
**作者**: Claude Code
**状态**: 设计阶段

## 概述

将现货价格数据（`SpotAssetCtxs`）的处理从独立的全局 WebSocket 订阅简化为直接在 `PositionManager.handleWebData2()` 中处理，消除冗余订阅并提升数据一致性。

### 当前问题

1. **额外的 WebSocket 订阅**：单独订阅 `SpotAssetCtxs` 增加资源消耗
2. **数据一致性风险**：价格缓存和仓位数据来自不同的订阅流，可能存在时序不一致
3. **代码分散**：价格订阅逻辑在 `main.go`，位置管理在 `position/manager.go`

### 目标

- 移除独立的 `SpotAssetCtxs` 全局订阅
- 在 `PositionManager.handleWebData2()` 中统一处理 `SpotAssetCtxs`
- 保持 `SpotPriceCache` 接口不变，对下游透明

---

## 当前架构分析

### 现有实现

```
┌──────────────────────────────────────────────────────┐
│                    main.go                          │
│                                                       │
│  ┌──────────────────────────────────────────────┐   │
│  │  SpotAssetCtxs 全局订阅                       │   │
│  │  hlClient.SpotAssetCtxs(callback)            │   │
│  └───────────────┬──────────────────────────────┘   │
│                  │                                   │
│                  ▼                                   │
│  ┌──────────────────────────────────────────────┐   │
│  │  priceCache.Update(ctxs)                 │   │
│  └──────────────────────────────────────────────┘   │
│                                                       │
│  ┌──────────────────────────────────────────────┐   │
│  │  posManager = NewPositionManager(client)     │   │
│  └──────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────┐
│             position/manager.go                      │
│                                                       │
│  ┌──────────────────────────────────────────────┐   │
│  │  WebData2 订阅                                │   │
│  │  client.WebData2(callback)                   │   │
│  └───────────────┬──────────────────────────────┘   │
│                  │                                   │
│                  ▼                                   │
│  ┌──────────────────────────────────────────────┐   │
│  │  handleWebData2(addr, webdata2)              │   │
│  │    └──> processPositionCache(webdata2)       │   │
│  │         (忽略 webdata2.SpotAssetCtxs)        │   │
│  └──────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────┘
```

**问题：**
- 两个独立的 WebSocket 订阅
- `SpotAssetCtxs` 在 `WebData2` 中存在但被忽略
- 价格更新与仓位更新分离

---

## 设计方案

### 新架构

```
┌──────────────────────────────────────────────────────┐
│                    main.go                          │
│                                                       │
│  ┌──────────────────────────────────────────────┐   │
│  │  priceCache = NewSpotPriceCache()        │   │
│  └───────────────┬──────────────────────────────┘   │
│                  │                                   │
│                  ▼                                   │
│  ┌──────────────────────────────────────────────┐   │
│  │  posManager = NewPositionManager(            │   │
│  │      client, priceCache)  // 注入缓存     │   │
│  └──────────────────────────────────────────────┘   │
│                                                       │
│  ┌──────────────────────────────────────────────┐   │
│  │  balanceCalc = NewBalanceCalculator(         │   │
│  │      dao.Position(), priceCache)        │   │
│  └──────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────┐
│             position/manager.go                      │
│                                                       │
│  ┌──────────────────────────────────────────────┐   │
│  │  WebData2 订阅                                │   │
│  │  client.WebData2(callback)                   │   │
│  └───────────────┬──────────────────────────────┘   │
│                  │                                   │
│                  ▼                                   │
│  ┌──────────────────────────────────────────────┐   │
│  │  handleWebData2(addr, webdata2)              │   │
│  │    ├──> processPositionCache(webdata2)       │   │
│  │    └──> updateSpotPriceCache(webdata2)       │   │
│  │         (新增：处理 SpotAssetCtxs)            │   │
│  └──────────────────────────────────────────────┘   │
│                  │                                   │
│                  ▼                                   │
│  ┌──────────────────────────────────────────────┐   │
│  │  priceCache.Update(                      │   │
│  │      webdata2.SpotAssetCtxs)                 │   │
│  └──────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────┘
```

---

## 组件详细设计

### 1. PositionManager 更新

#### 结构体变更

```go
// PositionManager 仓位管理器
type PositionManager struct {
    pool           *hyperliquid.WebsocketClient
    addresses      map[string]bool
    subs           map[string]*hyperliquid.Subscription
    priceCache *ws.SpotPriceCache  // 新增：价格缓存引用
    mu             sync.RWMutex
}
```

#### 构造函数变更

```go
// NewPositionManager 创建仓位管理器
func NewPositionManager(
    pool *hyperliquid.WebsocketClient,
    priceCache *ws.SpotPriceCache,  // 新增参数
) *PositionManager {
    return &PositionManager{
        pool:           pool,
        addresses:      make(map[string]bool),
        subs:           make(map[string]*hyperliquid.Subscription),
        priceCache: priceCache,  // 保存引用
    }
}
```

#### handleWebData2 增强

```go
func (m *PositionManager) handleWebData2(addr string, webdata2 hyperliquid.WebData2) {
    // 处理仓位缓存（现有逻辑）
    if webdata2.ClearinghouseState != nil {
        m.processPositionCache(addr, &webdata2)
    }

    // 处理现货价格（新增逻辑）
    if len(webdata2.SpotAssetCtxs) > 0 {
        m.priceCache.Update(webdata2.SpotAssetCtxs)
        logger.Debug().
            Str("address", addr).
            Int("price_count", len(webdata2.SpotAssetCtxs)).
            Msg("updated spot prices from webdata2")
    }
}
```

---

### 2. main.go 简化

#### 移除的代码

```go
// --- 删除以下代码 ---

// 创建现货价格缓存
priceCache := ws.NewSpotPriceCache()

// 订阅全局 SpotAssetCtxs（获取现货币种价格）
hlClient := poolManager.Client()
if hlClient == nil {
    logger.Fatal().Msg("websocket client is nil")
}
spotSub, err := hlClient.SpotAssetCtxs(
    func(data hyperliquid.SpotAssetCtxs, err error) {
        if err != nil {
            logger.Error().Err(err).Msg("spot asset ctxs error")
            return
        }
        priceCache.Update(data)
    },
)
if err != nil {
    logger.Fatal().Err(err).Msg("failed to subscribe spot asset ctxs")
}
defer spotSub.Close()
```

#### 新增/修改的代码

```go
// 创建现货价格缓存
priceCache := ws.NewSpotPriceCache()

// 初始化仓位管理器（注入价格缓存）
posManager := position.NewPositionManager(
    poolManager.Client(),
    priceCache,  // 注入价格缓存
)

// 创建余额计算器
balanceCalc := ws.NewBalanceCalculator(dao.Position(), priceCache)

// 初始化订阅管理器（监听订单成交）
subManager := ws.NewSubscriptionManager(
    poolManager,
    publisher,
    symbolConverter,
    balanceCalc,
)
```

---

## WebData2 数据结构确认

根据 `pkg/go-hyperliquid/ws_types.go`：

```go
type WebData2 struct {
    ClearinghouseState *ClearinghouseState `json:"clearinghouseState,omitempty"`
    // ... 其他字段
    SpotAssetCtxs      []SpotAssetCtx      `json:"spotAssetCtxs,omitempty"`  // 确认存在
    // ... 其他字段
}
```

**结论：** `SpotAssetCtxs` 字段已存在于 `WebData2` 中，无需额外订阅。

---

## 数据流对比

### 当前方案

```
Hyperliquid Server
      │
      ├──> SpotAssetCtxs WebSocket (独立订阅)
      │         └──> main.go: priceCache.Update()
      │
      └──> WebData2 WebSocket (按地址订阅)
                └──> PositionManager: processPositionCache()
```

### 新方案

```
Hyperliquid Server
      │
      └──> WebData2 WebSocket (按地址订阅)
                ├──> PositionManager: processPositionCache()
                └──> PositionManager: priceCache.Update()
```

---

## 优势分析

| 维度 | 当前方案 | 新方案 | 改进 |
|------|---------|--------|------|
| WebSocket 订阅 | 2 个（全局 + 按地址） | 1 个（按地址） | 减少 50% |
| 代码位置 | 分散（main + position） | 集中（position） | 更清晰 |
| 数据一致性 | 独立订阅，可能不一致 | 同一来源，完全一致 | 更可靠 |
| 资源消耗 | 额外订阅处理 | 无额外开销 | 更高效 |
| 维护成本 | 两处逻辑 | 一处逻辑 | 更易维护 |

---

## 注意事项

### 1. SpotAssetCtxs 的全局性

- `SpotAssetCtxs` 是**全局市场数据**，不依赖于特定地址
- 任何用户的 `WebData2` 推送都包含完整的 `SpotAssetCtxs`
- 多次更新是**幂等的**：`SpotPriceCache.Update()` 直接覆盖，无副作用

### 2. 更新频率

- 每次 `WebData2` 推送都会更新价格缓存
- 通常在用户交易时触发，频率适中
- 如果监控多个地址，价格会频繁更新（但这是正常的）

### 3. 启动时机

- 问题：服务启动时，第一个 `WebData2` 推送到达前，价格缓存为空
- 影响：此期间的订单 `PositionRate` 计算会返回 `"100.00%"`
- 方案：可接受（短暂窗口），或可选地保留首次初始化订阅

### 4. 向后兼容

- `SpotPriceCache` 接口**完全不变**
- `BalanceCalculator` 逻辑**完全不变**
- 对下游（`OrderAggregator`）**透明**

---

## 测试策略

### 单元测试

```go
// TestPositionManager_handleWebData2_UpdatesSpotPrices
func TestPositionManager_handleWebData2_UpdatesSpotPrices(t *testing.T) {
    cache := ws.NewSpotPriceCache()
    pm := position.NewPositionManager(nil, cache)

    webdata2 := hyperliquid.WebData2{
        SpotAssetCtxs: []hyperliquid.SpotAssetCtx{
            {Coin: "BTC", MidPx: strPtr("50000")},
        },
    }

    pm.handleWebData2("test_addr", webdata2)

    px, ok := cache.GetMidPx("BTC")
    assert.True(t, ok)
    assert.Equal(t, 50000.0, px)
}
```

### 集成测试

- 验证 `WebData2` 推送后价格缓存更新
- 验证 `BalanceCalculator` 能正确读取价格
- 验证 `OrderAggregator` 能正确计算 `PositionRate`

---

## 迁移计划

### 阶段 1：代码变更

1. 修改 `PositionManager` 结构体和构造函数
2. 增强 `handleWebData2` 方法
3. 简化 `main.go`

### 阶段 2：验证

1. 单元测试：`PositionManager`
2. 集成测试：价格缓存更新
3. 手动测试：监控服务运行

### 阶段 3：部署

1. 灰度发布（可选）
2. 监控日志：观察价格更新频率
3. 完全切换

---

## 回滚方案

如果新方案出现问题：

1. **快速回滚**：恢复 `main.go` 中的独立订阅
2. **条件回滚**：添加配置项控制是否使用新方案
3. **数据验证**：对比新旧方案的价格缓存内容

---

## 性能影响

### 资源节省

- **网络流量**：减少 1 个 WebSocket 订阅
- **内存占用**：减少 1 个订阅对象
- **CPU 开销**：减少 1 个回调处理

### 潜在成本

- **更新频率**：如果监控多个地址，价格缓存更新更频繁
- **锁竞争**：多地址同时更新时的锁竞争（`SpotPriceCache` 已有读写锁）

### 评估

- **收益 > 成本**：节省 1 个订阅带来的资源节省 > 频繁更新的微小开销
- **实际影响**：`SpotPriceCache` 是轻量级操作，实际影响可忽略

---

## 后续优化

1. **监控指标**：添加价格缓存更新的 Prometheus 指标
2. **去重优化**：如果多个地址同时推送，可考虑短时间窗口去重
3. **初始化优化**：服务启动时可选地执行一次初始化查询

---

## 总结

本设计通过将 `SpotAssetCtxs` 处理集中到 `PositionManager.handleWebData2()` 中：

1. **简化架构**：移除独立的 WebSocket 订阅
2. **提升一致性**：价格和仓位数据来自同一来源
3. **降低复杂度**：代码集中，逻辑清晰
4. **保持兼容**：对下游组件完全透明

**关键变更：**
- `PositionManager` 新增 `priceCache` 引用
- `handleWebData2` 新增 `SpotAssetCtxs` 处理逻辑
- `main.go` 移除独立订阅，简化初始化
