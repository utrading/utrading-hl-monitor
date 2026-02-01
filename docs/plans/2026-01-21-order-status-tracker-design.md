# OrderStatusTracker 状态追踪器设计

**日期**: 2026-01-21
**作者**: Claude Code
**状态**: 设计完成

## 概述

解决 WebSocket 消息乱序导致的两个问题：
1. **重复发送信号**：OrderUpdate 先到触发 flush，后续 OrderFill 重复创建订单导致超时重复发送
2. **延迟发送信号**：OrderUpdate 先到但未找到订单，后续 OrderFill 只能等超时扫描器触发

## 问题场景

### 场景 1：重复发送信号
```
t1: OrderUpdate(status=filled) → 找到 PendingOrder → flush → 信号发送 → 订单移除
t2: OrderFill（乱序到达）→ 创建新 PendingOrder → 无后续触发 → 5分钟后超时重复发送
```
**问题**：同一订单信号发送了**两次**

### 场景 2：延迟发送信号
```
t1: OrderUpdate(status=filled) → 未找到 PendingOrder（Fill 还没到）→ 什么都不做
t2: OrderFill 到达 → 创建 PendingOrder → 没有 OrderUpdate 来触发 → 5分钟后超时才发送
```
**问题**：订单实际已完成，但延迟了 **5 分钟** 才发送信号

## 解决方案

引入 **OrderStatusTracker** 状态追踪器，记录已收到终止状态但还未匹配到 PendingOrder 的订单。

### 核心机制

1. **OrderUpdate 到达时**：
   - 如果找到 PendingOrder → 立即 flush
   - 如果未找到 → 记录到 statusTracker

2. **OrderFill 到达时**：
   - 检查 statusTracker，如果已标记为终止状态 → 立即 flush
   - 检查 deduper，如果已发送信号 → 跳过

3. **flush 执行后**：
   - 从 statusTracker 移除记录

## 架构设计

### OrderStatusTracker 接口

```go
// OrderStatusTracker 订单状态追踪器
// 用于记录已收到终止状态但还未匹配到 PendingOrder 的订单
type OrderStatusTracker interface {
    // MarkStatus 记录订单状态（filled, canceled 等）
    MarkStatus(address string, oid int64, status string)

    // GetStatus 获取订单状态，返回是否已记录和具体状态
    GetStatus(address string, oid int64) (string, bool)

    // Remove 移除记录（flush 后调用）
    Remove(address string, oid int64)

    // Clear 清空所有记录（测试用）
    Clear()
}
```

### 实现结构

```go
type statusTracker struct {
    cache *cache.Cache // key: "address-oid", value: status
}

func NewOrderStatusTracker(ttl time.Duration) OrderStatusTracker {
    return &statusTracker{
        cache: cache.New(ttl, 1*time.Minute), // 1分钟清理一次过期项
    }
}

func (t *statusTracker) MarkStatus(address string, oid int64, status string) {
    key := fmt.Sprintf("%s-%d", address, oid)
    t.cache.Set(key, status, cache.DefaultExpiration)
}

func (t *statusTracker) GetStatus(address string, oid int64) (string, bool) {
    key := fmt.Sprintf("%s-%d", address, oid)
    if val, found := t.cache.Get(key); found {
        return val.(string), true
    }
    return "", false
}

func (t *statusTracker) Remove(address string, oid int64) {
    key := fmt.Sprintf("%s-%d", address, oid)
    t.cache.Delete(key)
}

func (t *statusTracker) Clear() {
    t.cache.Flush()
}
```

### 配置参数

| 参数 | 值 | 说明 |
|------|---|------|
| TTL | 10 分钟 | 自动过期时间，超过此时长未匹配的记录自动清理 |
| 清理间隔 | 1 分钟 | go-cache 内部过期清理间隔 |

## OrderProcessor 集成

### 结构体修改

```go
type OrderProcessor struct {
    pendingOrders        *PendingOrderCache
    publisher            Publisher
    batchWriter          *BatchWriter
    deduper              cache.DedupCacheInterface
    symbolCache          *cache.SymbolCache
    positionBalanceCache *cache.PositionBalanceCache
    timeout              time.Duration
    flushChan            chan flushKey
    done                 chan struct{}
    wg                   sync.WaitGroup
    pool                 *ants.Pool
    statusTracker        OrderStatusTracker // 新增：状态追踪器
    mu                   sync.RWMutex
}
```

### 初始化修改

```go
func NewOrderProcessor(...) *OrderProcessor {
    // ... 现有代码

    op := &OrderProcessor{
        // ... 现有字段
        statusTracker: NewOrderStatusTracker(10 * time.Minute), // 10分钟TTL
    }

    return op
}
```

### UpdateStatus 方法修改

```go
func (p *OrderProcessor) UpdateStatus(address string, oid int64, status string, direction string) {
    if status == "open" || status == "triggered" {
        return
    }

    // 先记录状态到 tracker（无论是否找到 PendingOrder）
    p.statusTracker.MarkStatus(address, oid, status)

    if direction == "" {
        for _, dir := range []string{"Open Long", "Open Short", "Close Long", "Close Short", "Buy", "Sell"} {
            key := p.orderKey(address, oid, dir)
            if _, exists := p.pendingOrders.Get(key); exists {
                p.triggerFlush(key, "status", status)
                p.statusTracker.Remove(address, oid) // 从 tracker 移除
            }
        }
    } else {
        key := p.orderKey(address, oid, direction)
        if _, exists := p.pendingOrders.Get(key); exists {
            p.triggerFlush(key, "status", status)
            p.statusTracker.Remove(address, oid) // 从 tracker 移除
        }
    }
}
```

### handleOrderFill 方法修改

```go
func (p *OrderProcessor) handleOrderFill(msg OrderFillMessage) error {
    fill, ok := msg.Fill.(hl.WsOrderFill)
    if !ok {
        return fmt.Errorf("invalid fill type")
    }

    key := p.orderKey(msg.Address, fill.Oid, msg.Direction)

    // 1. 检查去重缓存（已发送信号）
    if p.deduper != nil {
        if p.deduper.IsSeen(msg.Address, fill.Oid, msg.Direction) {
            logger.Debug().
                Int64("oid", fill.Oid).
                Str("direction", msg.Direction).
                Msg("order already sent via deduper, skipping fill")
            return nil
        }
    }

    // 2. 检查状态追踪器（是否已记录终止状态）
    if status, found := p.statusTracker.GetStatus(msg.Address, fill.Oid); found {
        logger.Info().
            Int64("oid", fill.Oid).
            Str("direction", msg.Direction).
            Str("status", status).
            Msg("order status pre-marked, will flush immediately")

        // 标记需要在创建/更新后立即 flush
        defer func() {
            if pending, exists := p.pendingOrders.Get(key); exists {
                if !pending.Aggregation.SignalSent {
                    p.triggerFlush(key, "status", status)
                    p.statusTracker.Remove(msg.Address, fill.Oid)
                }
            }
        }()
    }

    // 3. 正常的 OrderFill 处理逻辑
    // ... 转换 symbol、LoadOrStore 等现有逻辑保持不变

    return nil
}
```

## 完整消息流

### 场景 1：正常顺序
```
OrderFill → 创建 PendingOrder
OrderUpdate(filled) → 找到订单 → 立即 flush → statusTracker.Remove()
```

### 场景 2：乱序 - Update 先到
```
OrderUpdate(filled) → 未找到订单 → 记录到 statusTracker
OrderFill → 创建订单 → 检查 statusTracker → 立即 flush → statusTracker.Remove()
```

### 场景 3：乱序 - 填充后收到重复 Fill
```
OrderUpdate(filled) → flush → 信号发送 → deduper.Mark()
OrderFill(重复) → deduper 检查 → 跳过
```

### 场景 4：已发送信号的订单再次收到 Update
```
OrderUpdate(filled) → 找到订单 → flush → SignalSent=true
OrderUpdate(filled 重复) → SignalSent 检查 → 跳过
```

## 边界情况处理

| 情况 | 处理方式 |
|------|----------|
| statusTracker TTL 过期 | 自动清理，不影响正常流程 |
| 同一订单多个 direction | statusTracker 用 address-oid，不区分 direction |
| flush 失败 | 重试机制由 flushChan 缓冲，不阻塞 |
| deduper 和 statusTracker 都有记录 | deduper 优先级更高（已发送信号） |

## 优势分析

1. **解决重复发送**：deduper 二次防护，已发送信号的订单不会重复创建
2. **解决延迟发送**：statusTracker 立即触发，从 5 分钟延迟降至实时
3. **自动过期清理**：10 分钟 TTL，无需手动扫描清理
4. **线程安全**：go-cache 原生支持并发访问
5. **性能影响小**：内存缓存操作，O(1) 时间复杂度
6. **兼容现有逻辑**：最小化修改，向后兼容

## 权衡考虑

1. **内存占用**：每个订单记录约 100 bytes，10 分钟内预计 < 1MB
2. **TTL 设置**：10 分钟是经验值，可根据实际网络延迟调整
3. **defer 开销**：只在 statusTracker 命中时使用，性能影响可忽略
4. **direction 处理**：statusTracker 不区分 direction，简化逻辑但可能误触发（概率极低）
