# OrderProcessor 协程池优化设计

**日期**: 2026-01-21
**作者**: Claude Code
**状态**: 设计完成

## 概述

将 OrderProcessor 中的 `flushProcessor` 从单 goroutine 串行处理模式改为使用 ants.Pool 协程池并发处理，提升订单信号发送的吞吐量。

## 设计目标

1. **性能优化**: 复用 goroutine 减少创建/销毁开销
2. **资源控制**: 限制并发数，避免过多 goroutine 竞争资源
3. **代码一致性**: 与 Dispatcher 保持在同一个并发模型下

## 架构调整

### 变更范围

只修改 OrderProcessor，MessageQueue 和 PositionProcessor 保持不变。

| 组件 | 是否需要 Pool | 原因 |
|------|--------------|------|
| MessageQueue | ❌ | 通用组件，职责是排队分发，已有固定 workers |
| OrderProcessor | ✅ | `flushOrder` 有重 I/O 操作（NATS 发布、数据库写入），适合并发 |
| PositionProcessor | ❌ | 轻量操作（只转换+写入 BatchWriter），无需额外并发 |

### 数据流变化

**修改前**:
```
flushChan → flushProcessor (单 goroutine) → flushOrder (同步执行)
```

**修改后**:
```
flushChan → flushProcessor (单 goroutine) → pool.Submit(flushOrder) → 并发执行
```

### 与 Dispatcher 的对应关系

- **Dispatcher**: 每条 WebSocket 消息 → pool.Submit(回调)
- **OrderProcessor**: 每个 flush 请求 → pool.Submit(flushOrder)

## 核心实现

### 1. 结构体修改

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
    pool                 *ants.Pool        // 新增：协程池
    mu                   sync.RWMutex
}
```

### 2. 初始化修改

```go
func NewOrderProcessor(...) *OrderProcessor {
    // 创建 pool（固定 30 个 worker）
    pool, err := ants.NewPool(30)
    if err != nil {
        logger.Fatal().Err(err).Msg("create ants pool failed")
    }

    op := &OrderProcessor{
        // ... 其他字段
        flushChan: make(chan flushKey, 1000),
        done:      make(chan struct{}),
        pool:      pool,
    }

    op.wg.Add(2)
    go op.flushProcessor()
    go op.timeoutScanner()

    return op
}
```

### 3. flushProcessor 修改

```go
func (p *OrderProcessor) flushProcessor() {
    defer p.wg.Done()
    for {
        select {
        case req := <-p.flushChan:
            // 提交到 pool 并发执行
            _ = p.pool.Submit(func() {
                p.flushOrder(req.key, req.trigger, req.status)
            })
        case <-p.done:
            // 处理剩余消息
            for len(p.flushChan) > 0 {
                req := <-p.flushChan
                req := req  // 局部变量捕获
                _ = p.pool.Submit(func() {
                    p.flushOrder(req.key, req.trigger, req.status)
                })
            }
            return
        }
    }
}
```

**关键点**:
- 每个 flush 请求封装成闭包提交到 pool
- Pool 内部 30 个 worker 并发处理 flushOrder
- 闭包正确捕获变量（`req := req`）

### 4. Stop() 方法

```go
func (p *OrderProcessor) Stop() {
    close(p.done)        // 停止 flushProcessor 和 timeoutScanner
    p.wg.Wait()          // 等待两个 goroutine 退出
    p.pool.Release()     // 释放 pool（等待正在执行的 flushOrder 完成）
}
```

**关闭顺序**:
1. `close(done)` → `flushProcessor` 会处理完 `flushChan` 剩余消息后退出
2. `wg.Wait()` → 等待 `flushProcessor` 和 `timeoutScanner` 退出
3. `pool.Release()` → 等待所有正在执行的 `flushOrder` 完成

## 错误处理

```go
err := p.pool.Submit(func() {
    p.flushOrder(req.key, req.trigger, req.status)
})
if err != nil {
    // Pool 已关闭或满，记录错误
    logger.Error().Err(err).Str("key", req.key).Msg("pool submit failed")
}
```

## 边界情况

| 场景 | 行为 |
|------|------|
| flushChan 满 | triggerFlush 中的 default 分支会丢弃请求（已有逻辑） |
| pool.Submit 失败 | 记录日志，该 flush 请求丢失 |
| Stop() 时有正在执行的 flushOrder | pool.Release() 会等待完成 |
| flushOrder 并发执行 pendingOrders 操作 | concurrent.Map 保证线程安全 |

## 测试覆盖

1. **正常流程**: 订单成交 → flushChan → pool → flushOrder 完成
2. **并发测试**: 多个订单同时触发 flush，验证并发处理
3. **优雅关闭**: Stop() 时验证剩余消息处理完成
4. **边界测试**: pool 满、Submit 失败等场景

## 优势分析

1. **并发度提升**: 从串行处理提升到 30 并发
2. **资源可控**: 固定 pool 大小，避免 goroutine 泛滥
3. **复用开销**: goroutine 复用，减少创建/销毁成本
4. **代码一致**: 与 Dispatcher 保持相同的并发模型

## 权衡考虑

1. **消息顺序**: 多个 flush 并发执行，单个订单的处理顺序不变，但不同订单可能乱序（与原行为一致）
2. **复杂度**: 增加了 pool 管理的复杂度，但收益明显
3. **内存占用**: pool 维持 30 个 goroutine，但相比动态创建更可控
