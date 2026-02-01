# PositionRate 计算功能设计

**日期**: 2026-01-19
**作者**: Claude Code
**状态**: 设计阶段

## 概述

将 `HlAddressSignal` 中的 `PositionSize` 字段（固定阈值分类）改为 `PositionRate` 字段（基于账户余额的动态比例），更准确地反映交易相对于账户规模的大小。

### 当前问题

- `PositionSize` 使用固定阈值分类（Small <10K, Medium <100K, Large ≥100K）
- 无法反映交易相对于账户实际规模的比例
- 不同规模的账户无法用同一标准衡量仓位大小

### 目标

- 计算 `(Price × Size) / 账户总价值 × 100%` 作为仓位比例
- 现货和合约分别计算不同的账户总价值
- 结果以百分比字符串表示（如 "15.50%", "8.33%"）

---

## 核心计算逻辑

### 现货 PositionRate

```
SpotTotalValue = AccountValue + Σ(Coin数量 × 价格)
PositionRate = (Price × Size) / SpotTotalValue × 100%
```

**账户总价值组成：**
- `AccountValue`: 账户基础余额
- `Σ(Coin数量 × 价格)`: 所有现货资产的 USD 价值

### 合约 PositionRate

```
WeightedAvgLeverage = Σ(杠杆 × PositionValue) / Σ(PositionValue)
FuturesTotalValue = Σ(PositionValue) + AccountValue × WeightedAvgLeverage
PositionRate = (Price × Size) / FuturesTotalValue × 100%
```

**账户总价值组成：**
- `Σ(PositionValue)`: 所有仓位的市值
- `AccountValue × WeightedAvgLeverage`: 余额 × 加权平均杠杆

### 加权平均杠杆

采用**价值加权平均**，而非算术平均：

```
WeightedAvgLeverage = Σ(各仓位杠杆 × 仓位价值) / 总仓位价值
```

**理由：** 大仓位对整体风险影响更大，应赋予更高权重。

---

## 架构设计

### 组件关系

```
┌─────────────────────────────────────────────────────────────────┐
│                         cmd/hl_monitor/main.go                  │
│                                                                  │
│  ┌──────────────────┐  ┌──────────────────┐  ┌────────────────┐ │
│  │ SpotAssetCtxs    │  │ SpotPriceCache   │  │ PositionManager│ │
│  │ 全局价格订阅     │─>│ 内存价格缓存     │  │ 仓位订阅        │ │
│  └──────────────────┘  └─────────┬────────┘  └────────┬───────┘ │
│                                          │                     │ │
│                                          ▼                     │ │
│                               ┌─────────────────────┐          │ │
│                               │ BalanceCalculator   │          │ │
│                               │ 余额计算器          │<─────────┘ │
│                               └──────────┬──────────┘             │
│                                          │                        │
│                                          ▼                        │
│                               ┌─────────────────────┐             │
│                               │ SubscriptionManager │             │
│                               └──────────┬──────────┘             │
│                                          │                        │
│                                          ▼                        │
│                               ┌─────────────────────┐             │
│                               │  OrderAggregator    │             │
│                               │  订单聚合 + 计算     │             │
│                               └──────────┬──────────┘             │
│                                          │                        │
│                                          ▼                        │
│                               ┌─────────────────────┐             │
│                               │  HlAddressSignal    │             │
│                               │  position_rate:     │             │
│                               │     "15.50%"        │             │
│                               └─────────────────────┘             │
└─────────────────────────────────────────────────────────────────┘
```

---

## 组件详细设计

### 1. 数据模型层 (`internal/nats/signal.go`)

```go
type HlAddressSignal struct {
    Address      string  `json:"address"`
    AssetType    string  `json:"asset_type"`
    Symbol       string  `json:"symbol"`
    Direction    string  `json:"direction"`
    Side         string  `json:"side"`
    PositionRate string  `json:"position_rate"`  // 原字段名: PositionSize
    Size         float64 `json:"size"`
    Price        float64 `json:"price"`
    Timestamp    int64   `json:"timestamp"`
}
```

**变更：** 字段名 `PositionSize` → `PositionRate`

---

### 2. 现货价格缓存 (`internal/ws/spot_price_cache.go`)

**功能：** 维护现货币种的实时价格（从 SpotAssetCtxs 订阅）

```go
// SpotPriceCache 现货价格缓存
type SpotPriceCache struct {
    prices map[string]*hyperliquid.SpotAssetCtx  // coin → SpotAssetCtx
    mu     sync.RWMutex
}

func NewSpotPriceCache() *SpotPriceCache

// Update 更新价格缓存（从 SpotAssetCtxs 订阅回调）
func (c *SpotPriceCache) Update(ctxs []hyperliquid.SpotAssetCtx)

// GetMidPx 获取指定币种的价格
// 优先使用 midPx，为 nil 时回退到 markPx
func (c *SpotPriceCache) GetMidPx(coin string) (float64, bool)
```

**价格获取优先级：**
1. `midPx` (最优价格)
2. `markPx` (标记价格，作为后备)
3. 均无效 → 返回 false

**设计理由：**
- 内存缓存，无数据库查询延迟
- 线程安全（读写锁）
- 优雅降级（midPx → markPx）

---

### 3. 余额计算器 (`internal/ws/balance_calculator.go`)

**功能：** 封装现货/合约余额的计算逻辑

```go
// BalanceCalculator 余额计算器
type BalanceCalculator struct {
    positionDAO    dao.PositionDAO
    priceCache *SpotPriceCache
}

func NewBalanceCalculator(positionDAO dao.PositionDAO, priceCache *SpotPriceCache) *BalanceCalculator

// CalculateSpotBalance 计算现货总价值
// 返回: AccountValue + Σ(币种数量 × 价格)
func (c *BalanceCalculator) CalculateSpotBalance(address string) (float64, error)

// CalculateFuturesBalance 计算合约总价值
// 返回: Σ(PositionValue) + AccountValue × 加权平均杠杆
func (c *BalanceCalculator) CalculateFuturesBalance(address string) (float64, error)
```

**现货计算逻辑：**
```go
func (c *BalanceCalculator) CalculateSpotBalance(address string) (float64, error) {
    cache, err := c.positionDAO.GetPositionCache(address)
    if err != nil || cache == nil {
        return 0, ErrPositionCacheNotFound
    }

    // 解析 AccountValue
    accountValue := cast.ToFloat64(cache.AccountValue)

    // 解析 SpotBalances JSON
    var balances models.SpotBalancesData
    json.Unmarshal([]byte(cache.SpotBalances), &balances)

    // 计算现货资产总价值
    spotAssetsValue := 0.0
    for _, balance := range balances {
        if balance.Total == "0" {
            continue  // 跳过空余额
        }

        total := cast.ToFloat64(balance.Total)
        midPx, ok := c.priceCache.GetMidPx(balance.Coin)
        if !ok {
            continue  // 价格不可用，跳过
        }

        spotAssetsValue += total * midPx
    }

    return accountValue + spotAssetsValue, nil
}
```

**合约计算逻辑：**
```go
func (c *BalanceCalculator) CalculateFuturesBalance(address string) (float64, error) {
    cache, err := c.positionDAO.GetPositionCache(address)
    if err != nil || cache == nil {
        return 0, ErrPositionCacheNotFound
    }

    accountValue := cast.ToFloat64(cache.AccountValue)

    // 解析 FuturesPositions JSON
    var positions models.FuturesPositionsData
    json.Unmarshal([]byte(cache.FuturesPositions), &positions)

    // 计算仓位总价值和加权杠杆
    totalPositionValue := 0.0
    weightedLeverageSum := 0.0

    for _, pos := range positions {
        positionValue := cast.ToFloat64(pos.PositionValue)
        if positionValue == 0 {
            continue
        }

        totalPositionValue += positionValue

        // 杠杆 × 仓位价值（价值加权）
        leverageValue := cast.ToFloat64(pos.Leverage.Value)
        weightedLeverageSum += leverageValue * positionValue
    }

    // 计算加权平均杠杆
    avgLeverage := 1.0  // 默认无杠杆
    if totalPositionValue > 0 {
        avgLeverage = weightedLeverageSum / totalPositionValue
    }

    return totalPositionValue + accountValue*avgLeverage, nil
}
```

---

### 4. OrderAggregator 更新 (`internal/ws/aggregator.go`)

**新增依赖：**

```go
type OrderAggregator struct {
    // ... 现有字段
    balanceCalc *BalanceCalculator  // 新增
    // ...
}

func NewOrderAggregator(
    publisher Publisher,
    timeout time.Duration,
    sc SymbolConverterInterface,
    balanceCalc *BalanceCalculator,  // 新增参数
) *OrderAggregator
```

**计算 PositionRate：**

```go
// calculatePositionRate 计算仓位比例
func (a *OrderAggregator) calculatePositionRate(
    address, assetType string,
    price, size float64,
) string {
    var totalBalance float64
    var err error

    if assetType == "spot" {
        totalBalance, err = a.balanceCalc.CalculateSpotBalance(address)
    } else {
        totalBalance, err = a.balanceCalc.CalculateFuturesBalance(address)
    }

    // 错误处理：返回 100%
    if err != nil || totalBalance <= 0 {
        logger.Debug().
            Str("address", address).
            Str("asset_type", assetType).
            Err(err).
            Msg("failed to calculate balance, using 100%")
        return "100.00%"
    }

    // 计算比例: (price × size) / totalBalance × 100
    rate := (price * size / totalBalance) * 100

    return fmt.Sprintf("%.2f%%", rate)
}
```

**buildSignal 更新：**

```go
func (a *OrderAggregator) buildSignal(agg *models.OrderAggregation) *nats.HlAddressSignal {
    // ... 现有逻辑 ...

    // 计算 PositionRate（替换 classifyPositionSize）
    positionRate := a.calculatePositionRate(
        agg.Address,
        assetType,
        agg.WeightedAvgPx,
        agg.TotalSize,
    )

    return &nats.HlAddressSignal{
        Address:      agg.Address,
        Symbol:       agg.Symbol,
        AssetType:    assetType,
        Direction:    direction,
        Side:         side,
        PositionRate: positionRate,  // 新字段
        Size:         agg.TotalSize,
        Price:        agg.WeightedAvgPx,
        Timestamp:    firstFill.Time,
    }
}
```

**移除：** `classifyPositionSize()` 函数（不再需要）

---

### 5. 主程序集成 (`cmd/hl_monitor/main.go`)

```go
func main() {
    // ... 现有初始化 ...

    // 1. 创建现货价格缓存
    priceCache := ws.NewSpotPriceCache()

    // 2. 订阅全局 SpotAssetCtxs（独立于用户订阅）
    spotSub, err := poolManager.DefaultConnection().SpotAssetCtxs(
        func(ctxs []hyperliquid.SpotAssetCtx, err error) {
            if err != nil {
                logger.Error().Err(err).Msg("spot asset ctxs error")
                return
            }
            priceCache.Update(ctxs)
        },
    )
    if err != nil {
        logger.Fatal().Err(err).Msg("failed to subscribe spot asset ctxs")
    }
    defer spotSub.Close()

    // 3. 创建余额计算器
    balanceCalc := ws.NewBalanceCalculator(dao.Position(), priceCache)

    // 4. 创建订阅管理器（传入余额计算器）
    subscriptionManager := ws.NewSubscriptionManager(
        poolManager,
        natsPublisher,
        symbolConverter,
        balanceCalc,  // 新增参数
    )

    // ... 现有逻辑 ...
}
```

---

## 数据流

### 价格数据流

```
SpotAssetCtxs WebSocket (全局订阅)
         │
         ▼
SpotPriceCache.Update()
         │
         ├──> prices[coin] = SpotAssetCtx
         │
         ▼
BalanceCalculator.CalculateSpotBalance()
         │
         ├──> priceCache.GetMidPx(coin)
         │    ├──> midPx != nil ? midPx : markPx
         │
         ▼
totalBalance = AccountValue + Σ(Coin数量 × 价格)
```

### 信号生成流

```
orderFills WebSocket 事件
         │
         ▼
handleOrderFills()
         │
         ▼
OrderAggregator.AddFill()
         │
         ▼
OrderAggregator.flushOrder()
         │
         ▼
buildSignal()
         │
         ├──> calculatePositionRate()
         │    ├──> BalanceCalculator.Calculate*Balance()
         │    └──> 格式化为 "XX.XX%"
         │
         ▼
HlAddressSignal {
    PositionRate: "15.50%",  // 新字段
    ...
}
         │
         ▼
Publish to NATS
```

---

## 错误处理策略

| 场景 | 处理方式 | 返回值 |
|------|----------|--------|
| `position_cache` 中无该地址 | 使用占位符 | `"100.00%"` |
| `SpotAssetCtx` 中无该币种价格 | 跳过该币种，继续计算 | 部分计算 |
| `midPx` 为 nil | 回退到 `markPx` | markPx 值 |
| `midPx` 和 `markPx` 均无效 | 跳过该币种 | 部分计算 |
| 解析 JSON 失败 | 记录警告，使用默认值 | `0` |
| `totalBalance <= 0` | 返回占位符 | `"100.00%"` |
| 计算结果 > 100% | 仍返回实际值 | 实际百分比 |

---

## 边界条件

| 场景 | 预期结果 |
|------|---------|
| 余额为 0 | `"100.00%"` |
| size 为 0 | `"0.00%"` |
| price 为 0 | `"0.00%"` |
| 比例 > 100% | 返回实际值（如 `"150.00%"`） |
| SpotBalances 为空 | 仅使用 AccountValue |
| FuturesPositions 为空 | 仅使用 AccountValue |
| 所有币种价格均不可用 | 仅使用 AccountValue |
| SpotAssetCtx 无该币种 | 跳过该币种 |

---

## 测试策略

### 单元测试

#### `SpotPriceCache` 测试

- `TestSpotPriceCache_UpdateAndGet` - 基本更新和获取
- `TestSpotPriceCache_GetMidPx_UsesMarkPxAsFallback` - midPx 为 nil 时使用 markPx
- `TestSpotPriceCache_GetMidPx_MidPxTakesPrecedence` - midPx 优先于 markPx
- `TestSpotPriceCache_ConcurrentUpdate` - 并发更新安全性

#### `BalanceCalculator` 测试

- `TestBalanceCalculator_CalculateSpotBalance` - 现货余额计算
- `TestBalanceCalculator_CalculateFuturesBalance` - 合约余额计算
- `TestBalanceCalculator_CalculateFuturesBalance_WeightedLeverage` - 加权杠杆计算
- `TestBalanceCalculator_NoCache_ReturnsError` - 缓存不存在
- `TestBalanceCalculator_EmptySpotBalances` - 空现货余额
- `TestBalanceCalculator_EmptyFuturesPositions` - 空合约仓位

#### `OrderAggregator` 集成测试

- `TestOrderAggregator_CalculatePositionRate_Spot` - 现货 PositionRate
- `TestOrderAggregator_CalculatePositionRate_Futures` - 合约 PositionRate
- `TestOrderAggregator_CalculatePositionRate_NoCache_Returns100` - 缓存不存在返回 100%
- `TestOrderAggregator_CalculatePositionRate_ZeroSize_Returns0` - size 为 0

### 测试覆盖率目标

| 组件 | 目标 | 重点 |
|------|------|------|
| `SpotPriceCache` | 90%+ | 并发、价格获取、nil 处理 |
| `BalanceCalculator` | 85%+ | 计算、空数据、异常值 |
| `OrderAggregator` | 80%+ | PositionRate、边界条件 |

---

## 依赖变更

### 新增文件

- `internal/ws/spot_price_cache.go`
- `internal/ws/spot_price_cache_test.go`
- `internal/ws/balance_calculator.go`
- `internal/ws/balance_calculator_test.go`

### 修改文件

- `internal/nats/signal.go` - PositionSize → PositionRate
- `internal/ws/aggregator.go` - 集成 BalanceCalculator
- `internal/ws/aggregator_test.go` - 更新测试
- `internal/ws/subscription.go` - 传递 BalanceCalculator
- `cmd/hl_monitor/main.go` - 初始化组件

---

## 数据库变更

无需变更。使用现有的 `hl_position_cache` 表：

- `SpotBalances` - 现货余额 JSON
- `FuturesPositions` - 合约仓位 JSON
- `AccountValue` - 账户基础余额

---

## 向后兼容性

### 影响

- **下游服务：** `HlAddressSignal` JSON 字段名变更 (`position_size` → `position_rate`)
- **数据库：** 无变更
- **API：** 无变更

### 迁移建议

下游服务需要：
1. 更新 JSON 字段解析：`position_size` → `position_rate`
2. 数据类型保持 `string`
3. 数值范围变化：`Small/Medium/Large` → `"0.00%" ~ "100.00%+"`

---

## 性能考虑

### 缓存策略

- `SpotPriceCache`: 内存缓存，WebSocket 实时更新
- `PositionCache`: 数据库查询，由 PositionManager 维护

### 计算开销

- 现货计算：O(n)，n 为 SpotBalances 币种数量
- 合约计算：O(m)，m 为 FuturesPositions 数量
- 通常 n < 20, m < 50，开销可忽略

### 并发安全

- `SpotPriceCache`: 读写锁保护
- `BalanceCalculator`: 无状态，线程安全
- `OrderAggregator`: 现有的 `mu` 锁保护

---

## 后续优化

1. **现货总价值计算：** 当前 SpotTotalUSD 为 0，可由 PositionManager 计算
2. **价格缓存持久化：** 可考虑定期持久化价格快照
3. **计算结果缓存：** 对同一地址的短期重复计算可缓存结果
4. **监控指标：** 添加 PositionRate 分布的 Prometheus 指标

---

## 总结

本设计通过引入 `SpotPriceCache` 和 `BalanceCalculator`，将 `PositionSize` 从固定阈值分类升级为基于账户实际规模的动态比例计算。核心改进：

1. **准确性提升：** 反映交易相对于账户的真实比例
2. **差异化计算：** 现货和合约分别计算账户价值
3. **优雅降级：** 价格、余额不可用时返回 100%
4. **性能优化：** 内存价格缓存，无额外数据库查询

测试覆盖率和边界条件处理确保功能可靠性。
