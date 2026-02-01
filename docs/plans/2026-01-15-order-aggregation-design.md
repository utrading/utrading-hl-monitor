# 订单聚合功能设计文档

**日期**: 2026-01-15
**作者**: Claude Code
**状态**: 设计完成

---

## 1. 背景

### 1.1 问题分析

当前系统监听 `userFills` 数据流，用户提交的一笔订单会被交易所撮合系统拆分成多笔成交（fills）。这些 fills 可能在不同时间点到达，导致：

1. **数据不完整**：同一订单的后续 fills 被去重逻辑丢弃
2. **信号不准确**：发送的信号只包含部分成交数据，而非完整订单
3. **缺少状态判断**：无法准确判断订单何时真正完成

### 1.2 设计目标

- 将同一订单(Oid)的所有 fills 聚合成完整订单数据
- 支持通过订单状态和超时两种机制触发信号发送
- 保证数据持久化，服务重启后可恢复
- 保持高并发下的数据一致性

---

## 2. 核心架构

```
┌─────────────────────────────────────────────────────────────────┐
│                     SubscriptionManager                         │
│                  (统一管理 orderFills 和 orderUpdates)             │
└─────────────────────────────────────────────────────────────────┘
                              │
         ┌────────────────────┼────────────────────┐
         ▼                    ▼                    ▼
  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
  │orderFills   │    │orderUpdates │    │OrderAggregator│
  │订阅         │    │订阅         │    │(订单聚合器)   │
  └─────────────┘    └─────────────┘    └─────────────┘
         │                    │                    │
         └────────────────────┼────────────────────┘
                              ▼
                   ┌─────────────────────┐
                   │   OrderState        │
                   │  (sync.Map 缓存)    │
                   │  - Oid → Aggregation│
                   │  - Status           │
                   │  - LastUpdateTime   │
                   └─────────────────────┘
                              │
         ┌────────────────────┼────────────────────┐
         ▼                    ▼                    ▼
  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
  │状态完成触发  │    │ 5分钟超时   │    │  DAO Layer  │
  │status=filled│    │            │    │(持久化)      │
  └─────────────┘    └─────────────┘    └─────────────┘
         │                    │                    │
         └────────────────────┼────────────────────┘
                              ▼
                   ┌─────────────────────┐
                   │  1. Publish NATS    │
                   │  2. Save Signal DB  │
                   │  3. Mark Sent       │
                   └─────────────────────┘
```

---

## 3. 数据结构设计

### 3.1 内存数据结构

```go
// OrderAggregation 订单聚合状态（内存）
type OrderAggregation struct {
    Oid             int64
    Address         string
    Symbol          string

    // 聚合数据
    Fills           []hyperliquid.WsOrderFill
    TotalSize       float64
    WeightedAvgPx   float64

    // 状态控制
    OrderStatus     string    // open/filled/canceled
    LastFillTime    int64

    // 处理标记
    SignalSent      bool
}

// OrderAggregator 订单聚合器
type OrderAggregator struct {
    orders        sync.Map     // key: "address-oid" → *OrderAggregation
    timeout       time.Duration // 5分钟
    flushChan     chan int64
    publisher     Publisher
    dao           dao.OrderAggregationDAO
}
```

### 3.2 数据库表结构

**表1: hl_order_aggregation (订单聚合状态)**

```sql
CREATE TABLE hl_order_aggregation (
    oid BIGINT PRIMARY KEY COMMENT '订单ID',
    address VARCHAR(42) NOT NULL COMMENT '监控地址',
    symbol VARCHAR(24) NOT NULL COMMENT '交易对',

    -- 聚合数据
    fills JSON NOT NULL COMMENT '所有 fill 数据',
    total_size DECIMAL(18,8) NOT NULL DEFAULT 0 COMMENT '总数量',
    weighted_avg_px DECIMAL(28,12) NOT NULL DEFAULT 0 COMMENT '加权平均价',

    -- 状态控制
    order_status VARCHAR(16) NOT NULL DEFAULT 'open' COMMENT '订单状态',
    last_fill_time BIGINT NOT NULL COMMENT '最后 fill 时间戳',

    -- 处理标记
    signal_sent BOOLEAN NOT NULL DEFAULT FALSE COMMENT '信号是否已发送',

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_address (address),
    INDEX idx_last_fill_time (last_fill_time),
    INDEX idx_signal_sent (signal_sent)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

**表2: hl_address_signal (交易信号)**

```sql
CREATE TABLE hl_address_signal (
    id BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    oid BIGINT NOT NULL UNIQUE COMMENT '订单ID',

    address VARCHAR(42) NOT NULL COMMENT '监控地址',
    position_size VARCHAR(16) NOT NULL COMMENT '仓位大小',
    symbol VARCHAR(24) NOT NULL COMMENT '交易对',
    coin_type VARCHAR(8) NOT NULL,
    asset_type VARCHAR(24) NOT NULL COMMENT 'spot/futures',
    direction VARCHAR(8) NOT NULL COMMENT 'open/close',
    side VARCHAR(8) NOT NULL COMMENT 'LONG/SHORT',
    price DECIMAL(28,12) NOT NULL,
    size DECIMAL(18,8) NOT NULL,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expired_at TIMESTAMP NOT NULL COMMENT '7天后过期',

    INDEX idx_address (address),
    INDEX idx_symbol (symbol),
    INDEX idx_created_at (created_at),
    INDEX idx_expired_at (expired_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

---

## 4. 核心流程设计

### 4.1 orderFills 处理流程

```
收到 orderFills 消息
    │
    ▼
遍历每个 Fill
    │
    ├─► AddFill(oid, fill)
    │       │
    │       ├─ 1. 从 sync.Map 加载/创建 OrderAggregation
    │       ├─ 2. 追加 Fill 到数组
    │       ├─ 3. 重新计算 TotalSize 和 WeightedAvgPx
    │       ├─ 4. 更新 LastFillTime
    │       └─ 5. 持久化到数据库
    │
    └─► 完成循环
```

### 4.2 orderUpdates 处理流程

```
收到 orderUpdates 消息
    │
    ▼
遍历每个 Order
    │
    ├─► 检查 Order.Status
    │       │
    │       ├─ filled / canceled ──► TryFlush(oid, "status_completed")
    │       │
    │       └─ open ──► 跳过
    │
    └─► 完成循环
```

### 4.3 超时扫描流程

```
定时器 (每30秒)
    │
    ▼
扫描 sync.Map 中所有订单
    │
    ├─► 筛选条件：
    │   - signal_sent = false
    │   - now - last_fill_time > 5分钟
    │
    ▼
批量发送到 flushChan
    │
    ▼
flushOrder(oid)
    │
    ├─► 1. Publish NATS
    ├─► 2. Save Signal DB
    └─► 3. Mark SignalSent = true
```

### 4.4 信号发送流程（重要：先 NATS 后 DB）

```go
func (a *OrderAggregator) flushOrder(oid int64) error {
    // 1. 获取聚合数据
    agg := a.getOrder(oid)
    if agg.SignalSent {
        return nil
    }

    // 2. 构建信号
    signal := buildSignal(agg)

    // 3. 先发布到 NATS（优先保证实时性）
    if err := a.publisher.Publish(signal); err != nil {
        return err
    }

    // 4. 再保存到数据库（持久化）
    if err := a.dao.CreateSignal(signal); err != nil {
        logger.Error().Err(err).Int64("oid", oid).Msg("save signal failed")
        // NATS 已发送，DB 失败不阻断
    }

    // 5. 标记已发送
    agg.SignalSent = true
    return a.dao.UpdateAggregation(agg)
}
```

---

## 5. 错误处理与重试

### 5.1 NATS 发布重试

```go
func (p *SignalPublisher) PublishWithRetry(signal *nats.HlAddressSignal) error {
    for attempt := 0; attempt <= maxRetry; attempt++ {
        // 1. 先 NATS
        if err := p.publishToNATS(signal); err != nil {
            time.Sleep(retryDelay)
            continue
        }

        // 2. 后 DB
        if err := p.dao.CreateSignal(signal); err != nil {
            logger.Error().Err(err).Msg("save signal failed")
            // NATS 已成功，DB 失败不重试
        }

        return nil
    }
    return ErrMaxRetryExceeded
}
```

### 5.2 服务启动恢复

```go
func (a *OrderAggregator) RestorePendingOrders() error {
    // 从数据库加载 signal_sent = false 的订单
    pending, _ := a.dao.GetPendingOrders()

    for _, agg := range pending {
        if isTimeout(agg) {
            // 超时订单立即发送
            a.flushChan <- agg.Oid
        } else {
            // 未超时订单重新加载到缓存
            key := orderKey(agg.Address, agg.Oid)
            a.orders.Store(key, agg)
        }
    }
    return nil
}
```

---

## 6. 监控指标

### 6.1 Prometheus 指标

| 指标名称 | 类型 | 标签 | 说明 |
|---------|------|------|------|
| `hl_order_aggregation_active` | Gauge | - | 当前聚合中的订单数 |
| `hl_order_aggregation_total` | Counter | - | 订单聚合总数 |
| `hl_order_flush_total` | Counter | trigger | 发送触发原因 (status/timeout) |
| `hl_order_flush_duration_seconds` | Histogram | - | 发送耗时分布 |
| `hl_order_fills_per_order` | Histogram | - | 每个订单的 fill 数量 |
| `hl_signal_db_save_duration_seconds` | Histogram | - | 信号保存数据库耗时 |
| `hl_order_fills_received_total` | Counter | - | orderFills 接收总数 |
| `hl_order_updates_received_total` | Counter | - | orderUpdates 接收总数 |

---

## 7. 配置参数

```toml
[order_aggregation]
# 超时时间：订单多久无新 fill 后自动发送
timeout = "5m"

# 扫描间隔：多久扫描一次超时订单
scan_interval = "30s"

# 重试配置
max_retry = 3
retry_delay = "1s"

# 数据清理：清理多少天前的已完成订单
retention_days = 7
```

---

## 8. 实施计划

### Phase 1: 数据层 (优先级: 高)
- [ ] 创建数据库表结构
- [ ] 生成 gorm-gen 代码
- [ ] 实现 OrderAggregationDAO
- [ ] 扩展 SignalDAO

### Phase 2: 核心组件 (优先级: 高)
- [ ] 实现 OrderAggregator
- [ ] 修改 SubscriptionManager 支持双订阅
- [ ] 实现超时扫描器
- [ ] 实现信号发送逻辑

### Phase 3: 集成与测试 (优先级: 中)
- [ ] 集成到 main.go
- [ ] 单元测试
- [ ] 集成测试
- [ ] 监控指标验证

### Phase 4: 优化与文档 (优先级: 低)
- [ ] 性能优化
- [ ] 补充文档
- [ ] 清理废弃代码

---

## 9. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 内存溢出（订单积压） | 高 | 限制最大缓存数，定期清理 |
| NATS 发送失败 | 中 | 重试机制 + 数据库持久化 |
| 数据库连接池耗尽 | 中 | 连接池监控，批量操作 |
| 重复发送信号 | 高 | 原子检查 + SignalSent 标记 |
| 时序问题（fill 先于 updates） | 低 | 以最后到达的为准 |

---

## 10. 附录

### 10.1 订单状态枚举

```go
const (
    OrderStatusValueOpen              OrderStatusValue = "open"
    OrderStatusValueFilled            OrderStatusValue = "filled"
    OrderStatusValueCanceled          OrderStatusValue = "canceled"
    OrderStatusValueRejected          OrderStatusValue = "rejected"
    OrderStatusValueExpired           OrderStatusValue = "expired"
    // ... 更多状态
)
```

### 10.2 关键计算公式

**加权平均价：**
```
WeightedAvgPx = Σ(Sz_i × Px_i) / Σ(Sz_i)
```

**超时判断：**
```
isTimeout = (now - LastFillTime) > 5min
```