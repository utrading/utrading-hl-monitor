# 反手订单处理设计

## 背景

在 git 提交 `ea747f44` 中，`subscription.go` 新增了处理**反手订单**（`Long > Short` / `Short > Long`）的逻辑。同一笔订单的 fills 可能同时包含平仓和开仓两个方向。

然而，新的 `OrderAggregator` 架构在 `aggregator.go:60` 标注了 `// TODO: 反手交易问题`，当前实现**未处理反手订单拆分**。

## 问题分析

### 反手订单定义

反手订单是指一笔订单同时完成平仓和开仓：

| 方向 | 说明 | 示例 |
|------|------|------|
| `Long > Short` | 多反空 | 平多仓 + 开空仓 |
| `Short > Long` | 空反多 | 平空仓 + 开多仓 |

### 当前架构问题

**旧实现（ea747f44）**：
- `subscription.go` 直接处理 fills
- 有 `addReversedFills()` 方法拆分反手订单
- 将反手订单拆分为平仓 + 开仓两个独立的信号

**新实现（当前）**：
- 引入 `OrderAggregator` 聚合同一订单的多个 fill
- `AddFill()` 直接聚合所有 fills，**没有处理反手订单拆分**
- `buildSignal()` 只看第一个 fill 的 `Dir`，无法处理混合方向
- **同一 Oid 的反手订单会互相覆盖**，导致信号丢失

### 核心问题

```go
// 当前：key = "address-oid"
key := orderKey(address, fill.Oid)  // ❌ 反手订单会覆盖
```

如果一笔反手订单包含 `Long > Short`：
1. 第一次调用 `AddFill(..., "Close Long")` → 创建聚合记录
2. 第二次调用 `AddFill(..., "Open Short")` → **覆盖**第一条记录
3. 结果：只保留了开仓部分，平仓信号丢失

## 解决方案：方案一 - 在 AddFill 前拆分

### 架构流程

```
WebSocket Fills
       ↓
SubscriptionManager.processOrderFills()
       ↓
   按 Oid 分组
       ↓
┌──────────────────────────────────────┐
│  splitReversedOrder() - 拆分逻辑     │
│  - 检测反手订单 (Long > Short)       │
│  - 拆分为 closeDir + openDir         │
└──────────────────────────────────────┘
       ↓
  为每个方向调用 AddFill()
       ↓
┌──────────────────────────────────────┐
│     OrderAggregator.AddFill()        │
│  - key = "address-oid-direction"     │
│  - 单方向聚合                        │
└──────────────────────────────────────┘
```

### 为什么选择方案一

1. **与 ea747f44 的设计一致**：旧代码已经在 `subscription.go` 实现了 `addReversedFills()`
2. **职责分离清晰**：SubscriptionManager 负责业务逻辑（拆分），OrderAggregator 负责聚合
3. **简化聚合器**：OrderAggregator 只处理单方向订单，保持简单

## 具体实现

### 1. OrderAggregator 修改

#### 修改聚合键函数

```go
// internal/ws/aggregator.go

// orderKey 生成订单缓存键（包含方向）
func orderKey(address string, oid int64, direction string) string {
    return cast.ToString(address) + "-" +
           cast.ToString(oid) + "-" +
           direction
}
```

#### 修改 AddFill 签名

```go
// AddFill 添加 fill 并更新聚合数据
func (a *OrderAggregator) AddFill(
    address string,
    fill hyperliquid.WsOrderFill,
    direction string,  // 新增参数：明确指定方向
) {
    key := orderKey(address, fill.Oid, direction)

    a.mu.Lock()
    defer a.mu.Unlock()

    // 加载或创建聚合记录
    if actual, loaded := a.orders.LoadOrStore(key, &models.OrderAggregation{
        Oid:          fill.Oid,
        Address:      address,
        Symbol:       fill.Coin,
        Direction:    direction,      // 新增字段
        OrderStatus:  "open",
        LastFillTime: time.Now().Unix(),
        CreatedAt:    time.Now(),
        UpdatedAt:    time.Now(),
    }); loaded {
        // 已存在，追加 fill
        agg := actual.(*models.OrderAggregation)
        agg.Fills = append(agg.Fills, fill)
        agg.TotalSize, agg.WeightedAvgPx = a.calculateWeightedAvg(agg.Fills)
        agg.LastFillTime = time.Now().Unix()
        agg.UpdatedAt = time.Now()
    } else {
        // 新创建，初始化 fills
        agg := actual.(*models.OrderAggregation)
        agg.Fills = []hyperliquid.WsOrderFill{fill}
        agg.TotalSize = cast.ToFloat64(fill.Sz)
        agg.WeightedAvgPx = cast.ToFloat64(fill.Px)
    }

    // 持久化到数据库
    a.persistOrder(key)

    // 更新活跃订单数
    a.updateActiveCount()

    // 记录 fill 数量
    if actual, ok := a.orders.Load(key); ok {
        agg := actual.(*models.OrderAggregation)
        monitor.ObserveFillsPerOrder(len(agg.Fills))
    }
}
```

#### 修改 UpdateStatus 签名

```go
// UpdateStatus 更新订单状态
func (a *OrderAggregator) UpdateStatus(
    address string,
    oid int64,
    status string,
    direction string,  // 新增参数
) {
    key := orderKey(address, oid, direction)

    a.mu.Lock()
    defer a.mu.Unlock()

    if actual, ok := a.orders.Load(key); ok {
        agg := actual.(*models.OrderAggregation)
        agg.OrderStatus = status
        agg.UpdatedAt = time.Now()

        // 如果订单完成，触发发送
        if status == "filled" || status == "canceled" {
            select {
            case a.flushChan <- flushRequest{key: key, trigger: "status"}:
            default:
                logger.Warn().Int64("oid", oid).Msg("flush channel full, drop flush request")
            }
        }
    }
}
```

#### 修改 TryFlush 签名

```go
// TryFlush 尝试发送订单信号
func (a *OrderAggregator) TryFlush(
    address string,
    oid int64,
    direction string,  // 新增参数
) bool {
    key := orderKey(address, oid, direction)

    a.mu.Lock()
    defer a.mu.Unlock()

    if actual, ok := a.orders.Load(key); ok {
        agg := actual.(*models.OrderAggregation)
        if agg.SignalSent {
            return false
        }

        select {
        case a.flushChan <- flushRequest{key: key, trigger: "manual"}:
            return true
        default:
            logger.Warn().Int64("oid", oid).Msg("flush channel full, drop manual flush request")
            return false
        }
    }

    return false
}
```

#### 修改 buildSignal

```go
// buildSignal 构建信号
func (a *OrderAggregator) buildSignal(agg *models.OrderAggregation) *nats.HlAddressSignal {
    if len(agg.Fills) == 0 {
        return nil
    }

    firstFill := agg.Fills[0]

    // 使用聚合记录中的 Direction 字段
    var direction, side string
    switch agg.Direction {
    case "Open Long":
        direction, side = "open", "LONG"
    case "Open Short":
        direction, side = "open", "SHORT"
    case "Close Long":
        direction, side = "close", "LONG"
    case "Close Short":
        direction, side = "close", "SHORT"
    case "Buy":
        direction, side = "open", "LONG"
    case "Sell":
        direction, side = "close", "LONG"
    default:
        logger.Warn().Str("dir", agg.Direction).Msg("unknown order direction, skip signal")
        return nil
    }

    assetType := "futures"
    if direction == "Buy" || direction == "Sell" {
        assetType = "spot"
    }

    return &nats.HlAddressSignal{
        Address:      agg.Address,
        Symbol:       agg.Symbol,
        AssetType:    assetType,
        Direction:    direction,
        Side:         side,
        PositionSize: classifyPositionSize(agg.TotalSize),
        Size:         agg.TotalSize,
        Price:        agg.WeightedAvgPx,
        Timestamp:    firstFill.Time,
    }
}
```

### 2. 模型修改

```go
// internal/models/order_aggregation.go

type OrderAggregation struct {
    ID            int64                      `json:"id" gorm:"primaryKey"`
    Oid           int64                      `json:"oid" gorm:"index"` // 订单 ID
    Address       string                     `json:"address" gorm:"index"`
    Symbol        string                     `json:"symbol"`
    Direction     string                     `json:"direction" gorm:"index"` // 新增：订单方向
    OrderStatus   string                     `json:"order_status"`
    Fills         []hyperliquid.WsOrderFill  `json:"fills" gorm:"serializer:json"`
    TotalSize     float64                    `json:"total_size"`
    WeightedAvgPx float64                    `json:"weighted_avg_px"`
    SignalSent    bool                       `json:"signal_sent" gorm:"index"`
    LastFillTime  int64                      `json:"last_fill_time" gorm:"index"`
    CreatedAt     time.Time                  `json:"created_at"`
    UpdatedAt     time.Time                  `json:"updated_at"`
}
```

### 3. SubscriptionManager 添加拆分逻辑

```go
// internal/ws/subscription.go

const (
    DirOpenLong    = "Open Long"
    DirOpenShort   = "Open Short"
    DirCloseLong   = "Close Long"
    DirCloseShort  = "Close Short"
    DirLongToShort = "Long > Short"
    DirShortToLong = "Short > Long"
    DirBuy         = "Buy"
    DirSell        = "Sell"
)

func (m *SubscriptionManager) processOrderFills(
    addr string,
    order hyperliquid.WsOrderFills,
) {
    // 按 Oid 分组 fills
    orderGroups := make(map[int64][]hyperliquid.WsOrderFill)
    for _, fill := range order.Fills {
        orderGroups[fill.Oid] = append(orderGroups[fill.Oid], fill)
    }

    // 处理每个订单组
    for oid, fills := range orderGroups {
        orderKey := fmt.Sprintf("oid-%d", oid)

        if isTradeProcessed(orderKey) {
            monitor.GetMetrics().IncTradeDeduped()
            continue
        }

        // 拆分反手订单
        splitOrders := m.splitReversedOrder(fills)

        // 为每个方向调用 AddFill
        for dir, dirFills := range splitOrders {
            for _, fill := range dirFills {
                m.aggregator.AddFill(addr, fill, dir)
            }
        }

        // 更新订单状态（使用原始 Dir）
        if len(fills) > 0 {
            firstFill := fills[0]
            m.aggregator.UpdateStatus(addr, oid, "open", firstFill.Dir)
        }

        markTradeProcessed(orderKey)
    }
}

// splitReversedOrder 拆分反手订单
func (m *SubscriptionManager) splitReversedOrder(
    fills []hyperliquid.WsOrderFill,
) map[string][]hyperliquid.WsOrderFill {
    grouped := make(map[string][]hyperliquid.WsOrderFill)

    for _, fill := range fills {
        switch fill.Dir {
        case DirLongToShort, DirShortToLong:
            m.addReversedFills(grouped, fill)
        default:
            grouped[fill.Dir] = append(grouped[fill.Dir], fill)
        }
    }

    return grouped
}

// addReversedFills 添加反手订单的平仓和开仓部分
func (m *SubscriptionManager) addReversedFills(
    grouped map[string][]hyperliquid.WsOrderFill,
    fill hyperliquid.WsOrderFill,
) {
    closeDir, openDir := m.getReverseDirections(fill.Dir)

    sz := cast.ToFloat64(fill.Sz)
    startPos := cast.ToFloat64(fill.StartPosition)
    closeSize := math.Abs(startPos)
    openSize := math.Max(sz-closeSize, 0)

    grouped[closeDir] = append(grouped[closeDir],
        m.cloneFillWithDirection(fill, closeDir, cast.ToString(closeSize)))
    grouped[openDir] = append(grouped[openDir],
        m.cloneFillWithDirection(fill, openDir, cast.ToString(openSize)))
}

// getReverseDirections 获取反手订单的平仓和开仓方向
func (m *SubscriptionManager) getReverseDirections(
    dir string,
) (closeDir, openDir string) {
    switch dir {
    case DirLongToShort:
        return DirCloseLong, DirOpenShort
    case DirShortToLong:
        return DirCloseShort, DirOpenLong
    default:
        return "", ""
    }
}

// cloneFillWithDirection 克隆订单成交并修改方向和数量
func (m *SubscriptionManager) cloneFillWithDirection(
    fill hyperliquid.WsOrderFill,
    dir string,
    sz string,
) hyperliquid.WsOrderFill {
    cloned := fill
    cloned.Dir = dir
    cloned.Sz = sz
    return cloned
}
```

### 4. 数据库迁移

#### 添加 Direction 字段

```sql
-- migrations/20260116_add_direction_to_order_aggregation.sql

ALTER TABLE hl_order_aggregation
ADD COLUMN direction VARCHAR(20) DEFAULT '' AFTER symbol,
ADD INDEX idx_direction (direction);

-- 清理旧数据（可选）
DELETE FROM hl_order_aggregation WHERE signal_sent = false;
```

#### 更新 gorm-gen 模型

```bash
cd cmd/gen
go run main.go
```

## 数据结构变化

### 聚合记录

同一 Oid 可能产生多条记录（每个方向一条）：

| ID | Oid | Address | Direction | Fills | SignalSent |
|----|-----|---------|-----------|-------|------------|
| 1 | 123 | 0xabc... | Close Long | 2 | true |
| 2 | 123 | 0xabc... | Open Short | 1 | false |

### 聚合键变化

| 组件 | 旧格式 | 新格式 |
|------|--------|--------|
| orderKey | `address-oid` | `address-oid-direction` |
| 数据库查询 | `WHERE oid = ?` | `WHERE oid = ? AND direction = ?` |

## 边界情况处理

### 1. 部分反手

场景：平仓部分已发送，开仓部分未发送

- 独立触发，互不影响
- 两个聚合记录独立管理

### 2. 同一方向多次 fill

场景：普通订单有多个 fill

- 正常聚合到同一 key
- 加权平均价计算正确

### 3. 现货交易

场景：`Buy` / `Sell` 方向

- 无反手概念，保持不变
- `Direction = "Buy"` 或 `"Sell"`

### 4. 订单状态更新

场景：`orderUpdates` 推送状态变化

- 需要同时更新所有方向的聚合记录
- 或使用原始 Dir 作为状态

## 测试策略

### 单元测试

```go
// internal/ws/subscription_test.go

func TestSplitReversedOrder(t *testing.T) {
    fills := []hyperliquid.WsOrderFill{
        {Oid: 123, Dir: "Long > Short", Sz: "1000", StartPosition: "500"},
    }

    split := splitReversedOrder(fills)

    assert.Contains(t, split, "Close Long")
    assert.Contains(t, split, "Open Short")
    assert.Equal(t, "500", split["Close Long"][0].Sz)
    assert.Equal(t, "500", split["Open Short"][0].Sz)
}

func TestGetReverseDirections(t *testing.T) {
    closeDir, openDir := getReverseDirections("Long > Short")
    assert.Equal(t, "Close Long", closeDir)
    assert.Equal(t, "Open Short", openDir)

    closeDir, openDir = getReverseDirections("Short > Long")
    assert.Equal(t, "Close Short", closeDir)
    assert.Equal(t, "Open Long", openDir)
}
```

### 集成测试

```go
// internal/ws/aggregator_test.go

func TestReversedOrderE2E(t *testing.T) {
    aggregator := NewOrderAggregator(mockPublisher, 5*time.Minute)
    addr := "test-address"

    // 模拟反手订单
    fill := hyperliquid.WsOrderFill{
        Oid:           123,
        Dir:          "Long > Short",
        Sz:           "1000",
        StartPosition: "500",
        Coin:         "BTC",
        Px:           "50000",
    }

    // 拆分后的 fills
    closeFill := cloneFillWithDirection(fill, "Close Long", "500")
    openFill := cloneFillWithDirection(fill, "Open Short", "500")

    // 添加到聚合器
    aggregator.AddFill(addr, closeFill, "Close Long")
    aggregator.AddFill(addr, openFill, "Open Short")

    // 验证：两个独立的聚合记录
    closeKey := orderKey(addr, 123, "Close Long")
    openKey := orderKey(addr, 123, "Open Short")

    closeAgg, ok := aggregator.orders.Load(closeKey)
    assert.True(t, ok)
    assert.Equal(t, "Close Long", closeAgg.(*models.OrderAggregation).Direction)

    openAgg, ok := aggregator.orders.Load(openKey)
    assert.True(t, ok)
    assert.Equal(t, "Open Short", openAgg.(*models.OrderAggregation).Direction)
}
```

## 迁移步骤

### 阶段 1：OrderAggregator 修改

1. 修改 `orderKey()` 函数，添加 `direction` 参数
2. 修改 `AddFill()` 签名，添加 `direction` 参数
3. 修改 `UpdateStatus()` 签名，添加 `direction` 参数
4. 修改 `TryFlush()` 签名，添加 `direction` 参数
5. 修改 `buildSignal()`，使用 `agg.Direction`

### 阶段 2：SubscriptionManager 添加拆分逻辑

1. 添加 `splitReversedOrder()` 方法
2. 添加 `addReversedFills()` 方法
3. 添加 `getReverseDirections()` 方法
4. 添加 `cloneFillWithDirection()` 方法
5. 修改 `processOrderFills()`，调用拆分逻辑

### 阶段 3：数据库迁移

1. 创建迁移文件，添加 `direction` 字段
2. 运行迁移
3. 重新生成 gorm-gen 模型

### 阶段 4：测试验证

1. 运行单元测试
2. 运行集成测试
3. 本地验证反手订单处理

### 阶段 5：部署验证

1. 部署到测试环境
2. 监控日志和指标
3. 验证反手订单信号正确发送

## 影响范围

### 修改文件

- `internal/ws/aggregator.go` - 聚合键和签名修改
- `internal/ws/subscription.go` - 添加拆分逻辑
- `internal/models/order_aggregation.go` - 添加 Direction 字段
- `migrations/20260116_add_direction_to_order_aggregation.sql` - 数据库迁移

### 无需修改

- 业务逻辑调用方式（外部 API 不变）
- 其他 DAO 层
- 配置文件

## 验证清单

- [ ] 编译通过
- [ ] 单元测试通过
- [ ] 反手订单拆分正确
- [ ] 普通订单正常聚合
- [ ] 订单状态更新正确
- [ ] 信号发送独立触发
- [ ] 数据库记录正确
- [ ] Prometheus 指标正确
