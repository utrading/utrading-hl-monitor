# 代码简化与优化设计

**日期**: 2026-01-19
**目标**: 移除冗余代码，统一架构，不需要向后兼容
**状态**: 设计完成，待实施

---

## 1. 优化目标

### 1.1 移除的组件

| 组件 | 文件 | 原因 |
|------|------|------|
| OrderAggregator | `internal/ws/aggregator.go` | 被 MessageQueue + OrderProcessor 替代 |
| Optimization 配置 | `config/config.go` | 不需要配置开关，优化始终启用 |
| 灰度控制 | 设计文档中 | 项目未上线，无需灰度 |

### 1.2 新增的组件

| 组件 | 文件 | 职责 |
|------|------|------|
| OrderProcessor | `internal/processor/order_processor.go` | 订单聚合、NATS 发布、数据库写入 |
| OrderFillMessage | `internal/processor/message.go` | 订单成交消息类型 |

---

## 2. 架构变更

### 2.1 当前架构

```
WebSocket → SubscriptionManager → OrderAggregator (sync.Map) → NATS
                                                    ↓
                                              DAO 直接写入
```

### 2.2 新架构

```
WebSocket → SubscriptionManager → MessageQueue (异步) → OrderProcessor → BatchWriter → DAO
                                                              ↓
                                                         NATS 发布
```

### 2.3 核心变化

1. **移除 OrderAggregator**：删除 `internal/ws/aggregator.go`（~450 行）
2. **新增 OrderProcessor**：在 `internal/processor/order_processor.go` 实现订单聚合逻辑
3. **MessageQueue 集成**：SubscriptionManager 发送消息到队列，由 OrderProcessor 处理
4. **统一写入**：所有数据库操作通过 BatchWriter 批量写入

---

## 3. OrderProcessor 设计

### 3.1 数据结构

```go
type OrderProcessor struct {
    pendingOrders map[string]*PendingOrder  // key: "address-oid-direction"
    mu            sync.RWMutex
    publisher     Publisher
    batchWriter   *BatchWriter
    deduper       *cache.DedupCache
    timeout       time.Duration
    flushChan     chan flushKey
}

type PendingOrder struct {
    Aggregation   *models.OrderAggregation
    FirstFillTime time.Time
}
```

### 3.2 处理流程

1. **接收消息**：从 MessageQueue 收到 OrderFillMessage
2. **聚合 Fills**：按 "address-oid-direction" 聚合 fills
   - 同一 tid 去重
   - 计算加权平均价
3. **触发发送**：
   - 订单状态变为 filled/canceled
   - 超时（默认 5 分钟无新 fill）
4. **发送信号**：构建 HlAddressSignal，发布到 NATS
5. **持久化**：通过 BatchWriter 批量写入数据库
6. **去重标记**：发送成功后标记到 DedupCache

### 3.3 后台协程

```go
func (p *OrderProcessor) timeoutScanner() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        p.scanTimeoutOrders()
    }
}
```

---

## 4. 消息类型

```go
type OrderFillMessage struct {
    Address  string
    Fill     hyperliquid.WsOrderFill
    Direction string  // "Open Long", "Close Short" 等
}

func (m OrderFillMessage) Type() string {
    return "order_fill"
}
```

---

## 5. SubscriptionManager 变更

### 5.1 字段变更

**移除**：
- `aggregator *OrderAggregator`

**新增**：
- `messageQueue *processor.MessageQueue`

### 5.2 方法简化

```go
func (m *SubscriptionManager) handleOrderFills(addr string, fills hyperliquid.WsOrderFills) {
    for _, fill := range fills.Data {
        direction := m.determineDirection(fill)
        msg := processor.OrderFillMessage{
            Address:   addr,
            Fill:      fill,
            Direction: direction,
        }
        m.messageQueue.Enqueue(msg)  // 入队列即可
    }
}
```

### 5.3 初始化变更

```go
func NewSubscriptionManager(...) *SubscriptionManager {
    mq := processor.NewMessageQueue(10000, 4, nil)
    mq.Start()

    orderProc := processor.NewOrderProcessor(publisher, batchWriter, deduper)
    mq.SetHandler(orderProc)

    return &SubscriptionManager{
        messageQueue: mq,
        // ...
    }
}
```

---

## 6. 错误处理与优雅关闭

### 6.1 错误处理策略

1. **消息处理失败**：
   - NATS 发布失败：记录错误，重试 3 次（指数退避）
   - 数据库写入失败：依赖 BatchWriter 的队列满策略
   - 聚合逻辑失败：记录错误，跳过该 fill，继续处理

2. **背压降级**：
   - MessageQueue 满时自动降级为同步处理
   - 保证不丢失消息，但会阻塞 WebSocket 接收

### 6.2 优雅关闭

```go
func (m *SubscriptionManager) Shutdown() error {
    // 1. 停止接收新消息
    close(m.done)

    // 2. 停止消息队列（等待处理完成）
    m.messageQueue.Stop()

    // 3. 刷新批量写入器（强制写入缓冲区）
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    m.batchWriter.GracefulShutdown(ctx)

    // 4. 关闭连接池
    m.poolManager.Close()

    return nil
}
```

---

## 7. 配置清理

### 7.1 删除 Optimization 配置段

```toml
# 移除以下配置
[optimization]
enabled = true
batch_size = 100
flush_interval_ms = 100
```

### 7.2 硬编码默认值

| 配置 | 默认值 |
|------|--------|
| MessageQueue 大小 | 10000 |
| MessageQueue workers | 4 |
| BatchWriter batch | 100 |
| BatchWriter flush | 100ms |

---

## 8. 实施步骤

### 步骤 1：创建 OrderProcessor

- 新建 `internal/processor/order_processor.go`
- 实现 `PendingOrder` 结构和聚合逻辑
- 实现 `timeoutScanner` 后台协程
- 单元测试：聚合、超时、去重逻辑

### 步骤 2：修改 SubscriptionManager

- 移除 `aggregator` 字段
- 添加 `messageQueue` 字段
- 简化 `handleOrderFills` 方法
- 实现 `Shutdown` 方法
- 集成测试：消息流端到端

### 步骤 3：清理配置

- 修改 `config/config.go`，删除 `Optimization` 结构体
- 更新 `cfg.toml` 示例

### 步骤 4：删除旧代码

- 删除 `internal/ws/aggregator.go`
- 删除相关测试文件
- 更新文档

### 步骤 5：验证

- 运行所有测试：`go test ./...`
- 编译检查：`go build ./...`
- 集成测试：WebSocket → MessageQueue → OrderProcessor → NATS/DB

---

## 9. 测试策略

### 9.1 单元测试

- `OrderProcessor` 聚合逻辑
- `tid` 去重验证
- 超时触发测试
- 加权平均价计算

### 9.2 集成测试

- 完整消息流：WebSocket → MessageQueue → OrderProcessor
- NATS 发布验证
- 数据库写入验证
- 去重器集成测试

### 9.3 压力测试

- 10000 msg/s 吞吐量验证
- 内存泄漏检测
- 背压降级验证

---

## 10. 预期收益

| 指标 | 变化 |
|------|------|
| 代码行数 | -450 行（aggregator.go） |
| sync.Map 使用 | 0 处（统一使用 Ristretto） |
| 配置复杂度 | 简化（移除 Optimization 段） |
| 架构清晰度 | 提升（统一消息队列模式） |
| 性能 | 持平或提升（异步 + 批量） |

---

**设计完成**: 2026-01-19
**状态**: 待实施
