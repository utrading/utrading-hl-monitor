# CLAUDE.md

> 面向 Claude Code 的项目指南 - 开发规范、架构细节和设计文档

本文档为 Claude Code (claude.ai/code) 提供项目上下文，帮助理解代码结构、设计决策和开发规范。

## 项目概述

utrading-hl-monitor 是 Hyperliquid 仓位监控服务，通过 WebSocket 实时监听指定地址的仓位变化和订单成交事件，并将交易信号发布到 NATS 供下游服务使用。

**核心职责：**
- 实时监控链上大户地址的仓位变化
- 聚合订单成交数据，计算加权平均价格
- 发布开仓/平仓交易信号到 NATS
- 缓存仓位数据供下游查询

## 核心架构

### 领域模块划分

项目采用**领域驱动设计**的目录结构：

```
internal/
├── address/        # 地址加载器：定期从数据库加载监控地址
├── cache/          # 缓存层：订单去重、Symbol 转换、价格缓存、持仓余额
├── cleaner/        # 数据清理器：定期清理历史数据
├── dal/            # 数据库连接层：GORM 连接和 gorm-gen 生成器
├── dao/            # 数据访问对象层：统一数据库操作入口
├── manager/        # 管理器：Symbol Manager、Pool Manager
├── models/         # 数据模型定义
├── monitor/        # 健康检查和 Prometheus 指标
├── nats/           # NATS 消息发布
├── position/       # 仓位管理器：订阅并处理仓位数据变化
├── processor/      # 消息处理层：异步队列、批量写入、订单处理
└── ws/             # WebSocket 连接和订阅管理
```

### 数据流

```
AddressLoader → 定期从 hl_watch_addresses 加载地址
                ↓
    PoolManager → WebSocket 连接池管理
                ↓
    OrderAggregator → 双触发聚合（状态+超时）
                ↓
    MessageQueue → 异步消息处理（4 workers，背压保护）
                ↓
    OrderProcessor → 订单处理、信号构建、CloseRate 计算
                ↓
    BatchWriter → 批量写入数据库（100条/2秒，缓冲区去重）
                ↓
    ┌───────────┴──────────┐
    ↓                      ↓
NATS Publisher        PositionManager
(交易信号)             (仓位缓存更新)
    ↓                      ↓
下游消费              MySQL 持久化
```

### 组件依赖关系

```
main.go
├── config (配置管理)
├── logger (日志初始化)
├── dal.MySQL (数据库连接)
├── dao (DAO 层初始化)
├── cleaner.DataCleaner (数据清理)
├── nats.Publisher (NATS 发布)
├── manager.NewPoolManager (WebSocket 连接池)
├── symbol.NewManager (Symbol 元数据管理)
├── processor.NewBatchWriter (批量写入)
├── position.NewManager (仓位管理)
├── processor.NewOrderProcessor (订单处理)
└── monitor.NewHealthServer (健康检查)
```

## 组件详解

### WebSocket 层 (internal/ws/)

#### PoolManager (pool_manager.go)
**职责**：管理多个 WebSocket 连接，实现负载均衡和订阅管理

**关键特性**：
- 支持多连接（默认 5 个，可配置 10 个）
- 每连接最多 100 个订阅（防止单连接过载）
- 自动选择负载最少的连接
- 订阅去重（相同地址只订阅一次）

**重要方法**：
```go
func (pm *PoolManager) Subscribe(ctx context.Context, address string) error
func (pm *PoolManager) Unsubscribe(address string) error
func (pm *PoolManager) GetConnectionForSubscription() *ConnectionWrapper
```

#### ConnectionWrapper (connection_wrapper.go)
**职责**：封装单个 WebSocket 连接，处理自动重连

**重连策略**：
- 指数退避：1s → 2s → 4s → ... → 30s
- 最多重试 10 次
- 超时后触发 OnError 回调

#### OrderAggregator (subscription.go)
**职责**：聚合同一订单的多次 fill，双触发机制

**双触发机制**：
1. **状态触发**：订单状态变为 filled/canceled/rejected
2. **超时触发**：订单超过配置的超时时间（默认 5 分钟）

**反手订单处理**：
- 检测：`Long > Short` 或 `Short > Long`
- 拆分：平仓信号 + 开仓信号
- 方法：`splitReversedOrder()`

### 订单处理层 (internal/processor/)

#### OrderProcessor (order_processor.go)
**职责**：订单处理的核心业务逻辑

**核心流程**：
```go
1. ProcessOrderFill(fill)
   ↓
2. pendingOrders.LoadOrStore(key, order) // 原子操作
   ↓
3. pending.seenTids.LoadOrStore(tid, struct{}) // TID 去重
   ↓
4. pending.Aggregation.AddFill(fill)
   ↓
5. checkFlushConditions() // 检查是否触发
   ↓
6. buildSignal(agg) // 构建信号
   ↓
7. calculateCloseRate() // 计算平仓比例
   ↓
8. publishSignal(signal) // 发布
```

**PendingOrderCache**：
- 基于 `concurrent.Map`
- O(1) 时间复杂度
- 自动清理过期数据

**协程池优化**：
- 使用 `ants.Pool`（30 workers）
- 异步 flush 处理
- 避免阻塞主流程

#### OrderStatusTracker (status_tracker.go)
**职责**：解决消息乱序问题

**问题场景**：
- 先收到 orderUpdate（状态：filled）
- 后收到 orderFill（成交数据）

**解决方案**：
```go
// 收到终止状态时记录
tracker.Record(address, oid)

// 收到 fill 时检查
if tracker.IsTerminated(address, oid) {
    // 立即触发 flush
}
```

**配置**：
- 缓存：go-cache
- TTL：10 分钟
- Key 格式：`address-oid`

### 缓存层 (internal/cache/)

#### 缓存架构

| 缓存类型 | 实现库 | 用途 | TTL |
|---------|-------|------|-----|
| DedupCache | go-cache | 订单去重（address-oid-direction） | 30 分钟 |
| SymbolCache | concurrent.Map | Symbol 双向转换（coin↔symbol） | 持久 |
| PriceCache | concurrent.Map | 现货/合约价格缓存 | LRU |
| PositionBalanceCache | concurrent.Map | 仓位余额缓存 | 实时更新 |

#### PositionBalanceCache (position_cache.go)
**职责**：缓存持仓数据，支持 CloseRate 计算

**扩展设计**（2026-01-21）：
```go
type PositionBalanceCache struct {
    spotTotals      concurrent.Map[string, float64]
    accountValues   concurrent.Map[string, float64]
    spotBalances    concurrent.Map[string, *models.SpotBalancesData]     // 新增
    futuresPositions concurrent.Map[string, *models.FuturesPositionsData] // 新增
}
```

**新增方法**：
```go
func (c *PositionBalanceCache) GetSpotBalance(address string, coin string) (float64, bool)
func (c *PositionBalanceCache) GetFuturesPosition(address string, coin string) (float64, bool)
```

### Symbol 管理 (internal/symbol/)

#### Symbol Manager (manager.go)
**职责**：统一管理 Symbol 相关的缓存和数据加载

**组件**：
- `symbol.Loader`：定期从 Hyperliquid API 加载（每 2 小时）
- `cache.SymbolCache`：双向映射（coin ↔ symbol）
- `cache.PriceCache`：现货/合约价格缓存

**使用示例**：
```go
symbolManager, err := symbol.NewManager(hyperliquid.MainnetAPIURL)
if err != nil {
    logger.Fatal().Err(err).Msg("init symbol manager failed")
}
defer symbolManager.Close()

// Symbol 转换
symbol, ok := symbolManager.SymbolCache().GetSpotSymbol("@123")

// 价格查询
price, ok := symbolManager.PriceCache().GetSpotPrice("ETHUSDC")
```

### 消息处理层 (internal/processor/)

#### MessageQueue (message_queue.go)
**职责**：异步消息处理，提供背压保护

**架构**：
```go
type MessageQueue struct {
    queue   chan WsMessage // 缓冲队列（1000）
    workers int            // 4 个 worker
    handler MessageHandler // OrderProcessor
}
```

**背压保护**：
- 队列满时返回 `ErrQueueFull`
- 调用方自动降级为同步处理
- 避免内存溢出

**优雅关闭**：
- 超时：5 秒
- 处理完队列中所有消息
- 关闭 worker

#### BatchWriter (batch_writer.go)
**职责**：批量写入数据库，降低 IO 压力

**缓冲区去重机制**：
```go
type BatchItem interface {
    DedupKey() string // 去重键
}

// 相同键的数据会覆盖
func (w *BatchWriter[T]) Add(item T) {
    key := item.DedupKey()
    w.buffer.Store(key, item) // 覆盖旧值
}
```

**去重键设计**：
```go
// 仓位缓存：按 address 去重
func (i PositionCacheItem) DedupKey() string {
    return "pc:" + i.Address
}

// 订单聚合：按 oid+address+direction 去重
func (i OrderAggregationItem) DedupKey() string {
    return fmt.Sprintf("oa:%d:%s:%s", i.Oid, i.Address, i.Direction)
}
```

**配置参数**：
```toml
[optimization]
enabled = true
batch_size = 100          # 批量大小
flush_interval_ms = 2000  # 刷新间隔（毫秒）
```

### 数据清理器 (internal/cleaner/)

#### DataCleaner (cleaner.go)
**职责**：定期清理历史数据，防止数据库膨胀

**清理策略**：
| 表名 | 保留时间 | 清理间隔 |
|------|----------|----------|
| hl_order_aggregation | 2 小时 | 1 小时 |
| hl_address_signal | 7 天 | 1 小时 |

**实现方式**：
- 使用 DAO 层批量删除
- 分批删除（每次 1000 条）
- 避免长事务

### 数据访问层 (internal/dao/)

**重要规范**：所有数据库操作必须通过 DAO 层，禁止在业务逻辑中直接使用 `dal.MySQL()`。

```go
// ✅ 正确：通过 DAO 访问
dao.Position().UpsertPositionCache(cache)
dao.WatchAddress().ListDistinctAddresses()

// ❌ 错误：直接使用 dal
dal.MySQL().Where(...).First(...)
```

#### DAO 单例模式
```go
// 初始化（在 main.go 中已完成）
dao.InitDAO(dal.MySQL())

// 使用
dao.Position().UpsertPositionCache(cache)
dao.WatchAddress().ListDistinctAddresses()
dao.OrderAggregation().FindByOid(oid)
```

#### 添加新数据访问操作
1. 在 `internal/dao/` 对应的 DAO 文件中添加方法
2. 使用 gorm-gen 提供的类型安全查询 API（`gen.Q.*`）
3. 复杂查询可使用 `UnderlyingDB()` 获取底层 GORM 连接

## 关键设计模式

### 订单聚合机制

**双触发机制**：
```
OrderFills + OrderUpdates
         ↓
   OrderAggregator
         ↓
┌────────────────┐
│ 状态触发检查    │ → filled/canceled/rejected → 立即发送
└────────────────┘
         ↓
┌────────────────┐
│ 超时触发检查    │ → 超过 5 分钟 → 发送
└────────────────┘
```

### 去重策略

**三层去重机制**：

1. **OrderDeduper**（ws/deduper.go）
   - 范围：address-oid-direction
   - 目的：防止重复发送已处理的订单
   - TTL：30 分钟
   - 启动时从数据库加载已发送订单

2. **PendingOrderCache.seenTids**（processor/order_processor.go）
   - 范围：单个订单内的 tid
   - 目的：防止同一 fill 重复处理
   - 实现：concurrent.Map

3. **BatchWriter 缓冲区去重**（processor/batch_writer.go）
   - 范围：缓冲区内的相同去重键
   - 目的：保留最新数据
   - 实现：concurrent.Map.Store() 覆盖

### 批量写入优化

**DAO 层批量 Upsert**：
```go
func (d *PositionDAO) BatchUpsertPositionCache(caches []*models.HlPositionCache) error {
    return d.dao.UnderlyingDB().
        Clauses(clause.OnConflict{
            Columns:   []clause.Column{{Name: "address"}},
            DoUpdates: clause.AssignmentColumns([]string{...}),
        }).
        Create(&caches).Error
}
```

**批量删除**：
```go
func (d *CleanerDAO) CleanExpiredSignals(before time.Time, limit int) (int64, error) {
    return d.dao.UnderlyingDB().
        Where("created_at < ?", before).
        Limit(limit).
        Delete(&models.HlAddressSignal{}).RowsAffected
}
```

### 并发控制

**协程池使用**：

1. **OrderProcessor**（30 workers）
   ```go
   pool, _ := ants.NewPool(30)
   defer pool.Submit(func() { ... })
   ```

2. **BatchWriter**（异步 flush）
   ```go
   go w.flushLoop() // 独立 goroutine
   ```

3. **并发安全容器**
   - `concurrent.Map`：支持 Len() 方法的 sync.Map
   - `go-cache`：带 TTL 的内存缓存

## 开发规范

### DAO 层使用规范

**基本原则**：
- 所有数据库操作通过 DAO 层
- 使用 gorm-gen 提供的类型安全查询
- 复杂查询使用 `UnderlyingDB()` 获取底层 GORM

**示例**：
```go
// 简单查询：使用 gorm-gen
func (d *WatchAddressDAO) ListDistinctAddresses() ([]string, error) {
    var addresses []string
    err := d.dao.Q.HlWatchAddress.
        Where(dao.HlWatchAddress.IsActive.Eq(1)).
        Select(dao.HlWatchAddress.Address).
        Scan(&addresses).Error
    return addresses, err
}

// 复杂查询：使用 UnderlyingDB
func (d *OrderAggregationDAO) CleanExpiredSignals(before time.Time, limit int) (int64, error) {
    return d.dao.UnderlyingDB().
        Where("created_at < ?", before).
        Limit(limit).
        Delete(&models.HlAddressSignal{}).RowsAffected
}
```

### 错误处理模式

**日志记录**：
```go
if err != nil {
    logger.Error().
        Err(err).
        Str("address", address).
        Msg("failed to subscribe")
    return err
}
```

**错误传递**：
```go
// 直接返回
return err

// 包装错误
return fmt.Errorf("failed to connect: %w", err)

// 降级处理
if err != nil {
    logger.Warn().Err(err).Msg("cache miss, using fallback")
    return fallbackValue
}
```

### 日志记录规范

**日志级别**：
- `Debug`：详细的调试信息（生产环境关闭）
- `Info`：关键业务流程（订单发送、订阅成功）
- `Warn`：可恢复的错误（缓存未命中、重试）
- `Error`：需要关注的错误（连接失败、数据错误）

**结构化日志**：
```go
logger.Info().
    Str("address", address).
    Str("symbol", symbol).
    Float64("size", size).
    Msg("order signal sent")

logger.Error().
    Err(err).
    Str("oid", fmt.Sprintf("%d", oid)).
    Msg("failed to process order")
```

### 测试规范

**测试文件命名**：`xxx_test.go`

**表格驱动测试**：
```go
func TestCalculateCloseRate(t *testing.T) {
    tests := []struct {
        name     string
        direction string
        size     float64
        expected float64
    }{
        {"open order", "open", 1.0, 0},
        {"close order", "close", 0.5, 0.5},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := calculateCloseRate(tt.direction, tt.size)
            if result != tt.expected {
                t.Errorf("got %v, want %v", result, tt.expected)
            }
        })
    }
}
```

## 配置系统

### 完整配置项 (cfg.toml)

```toml
[hl_monitor]
hyperliquid_ws_url = "wss://api.hyperliquid.xyz/ws"
health_server_addr = "0.0.0.0:8080"
address_reload_interval = "5m"
max_connections = 5
max_subscriptions_per_connection = 100

[mysql]
dsn = "root:password@tcp(localhost:3306)/utrading?charset=utf8mb4&parseTime=True&loc=Local"
max_idle_connections = 16
max_open_connections = 64
set_conn_max_lifetime = 7200

[nats]
endpoint = "nats://localhost:4222"

[log]
level = "info"
max_size = 50
max_backups = 60
max_age = 15
compress = false
console = false

[order_aggregation]
timeout = "5m"
scan_interval = "30s"
max_retry = 3
retry_delay = "1s"

[optimization]
enabled = true
batch_size = 100
flush_interval_ms = 2000

[data_cleaner]
enabled = true
order_aggregation_retention_hours = 2
signal_retention_days = 7
cleanup_interval_hours = 1
```

### 环境变量

支持通过环境变量覆盖配置：
```bash
export HL_MONITOR_MYSQL_DSN="user:pass@tcp(host:3306)/db"
export HL_MONITOR_NATS_ENDPOINT="nats://localhost:4222"
```

## 设计文档索引

### 按日期索引

| 日期 | 标题 | 文件 |
|------|------|------|
| 2026-01-15 | 订单聚合器双触发机制 | [order-aggregation-design](docs/plans/2026-01-15-order-aggregation-design.md) |
| 2026-01-16 | 反手订单处理优化 | [reversed-order-handling-design](docs/plans/2026-01-16-reversed-order-handling-design.md) |
| 2026-01-19 | 仓位比例计算功能 | [position-rate-calculation-design](docs/plans/2026-01-19-position-rate-calculation-design.md) |
| 2026-01-20 | Symbol Manager 实现 | [symbol-manager-design](docs/plans/2026-01-20-symbol-manager-design.md) |
| 2026-01-20 | BatchWriter 去重优化 | [batchwriter-dedup-plan](docs/plans/2026-01-20-batchwriter-dedup-plan.md) |
| 2026-01-21 | OrderProcessor 协程池优化 | [orderprocessor-pool-design](docs/plans/2026-01-21-orderprocessor-pool-design.md) |
| 2026-01-21 | OrderStatusTracker 状态追踪 | [order-status-tracker-design](docs/plans/2026-01-21-order-status-tracker-design.md) |
| 2026-01-21 | PositionBalanceCache 扩展 | [position-cache-extension-design](docs/plans/2026-01-21-position-cache-extension-design.md) |

### 按组件索引

#### WebSocket 层
- [WebSocket 订阅优化设计](docs/plans/2026-01-16-websocket-subscription-optimization-design.md)
- [自定义 WebSocket 实现](docs/plans/2026-01-21-custom-websocket-implementation.md)

#### 订单处理
- [订单聚合器优化](docs/plans/2026-01-16-order-aggregator-optimization-design.md)
- [PendingOrderCache 重构](docs/plans/2026-01-20-pending-order-cache-refactor.md)
- [数据清理器设计](docs/plans/2026-01-21-data-cleanup-design.md)

#### Symbol 管理
- [Symbol Manager 设计](docs/plans/2026-01-20-symbol-manager-design.md)
- [Symbol Cache 重构](docs/plans/2026-01-20-symbol-cache-refactor-design.md)

#### 综合
- [综合优化实现](docs/plans/2026-01-19-comprehensive-optimization-implementation.md)
- [代码简化设计](docs/plans/2026-01-19-code-simplification-design.md)

## 故障排查

### 常见问题

**Q: WebSocket 连接频繁断开**
- 检查网络连接
- 查看重连策略日志
- 确认 Hyperliquid API 状态

**Q: 订单信号重复发送**
- 检查 OrderDeduper 是否正常初始化
- 查看 DAO 层去重逻辑
- 确认数据库中 signal_sent 字段

**Q: 内存占用持续增长**
- 检查 PendingOrderCache 清理机制
- 查看缓存 TTL 配置
- 使用 pprof 分析内存泄漏

**Q: 数据库连接池耗尽**
- 调整 `max_open_connections` 配置
- 检查是否有连接泄漏
- 查看慢查询日志

### 调试技巧

**启用 Debug 日志**：
```toml
[log]
level = "debug"
```

**查看 Prometheus 指标**：
```bash
curl http://localhost:8080/metrics
```

**检查健康状态**：
```bash
curl http://localhost:8080/health
curl http://localhost:8080/status
```

**追踪订单处理**：
```go
logger.Debug().
    Str("oid", fmt.Sprintf("%d", oid)).
    Str("address", address).
    Msg("processing order fill")
```

## 附录

### gorm-gen 使用

**生成查询代码**：
```bash
cd cmd/gen
go run main.go
```

**使用生成的查询 API**：
```go
// 使用 Q 构建器
var addresses []string
err := dao.Q.HlWatchAddress.
    Where(dao.HlWatchAddress.IsActive.Eq(1)).
    Select(dao.HlWatchAddress.Address).
    Scan(&addresses).Error

// 使用条件表达式
err := dao.Q.HlOrderAggregation.
    Where(
        dao.HlOrderAggregation.Address.Eq(address),
        dao.HlOrderAggregation.LastFillTime.Gt(startTime),
    ).
    Find(&aggregations).Error
```

### Prometheus 指标

#### 缓存指标
```
hl_monitor_cache_hit_total{cache_type="dedup|symbol|price"}
hl_monitor_cache_miss_total{cache_type="dedup|symbol|price"}
```

#### 消息队列指标
```
hl_monitor_message_queue_size
hl_monitor_message_queue_full_total
```

#### 批量写入指标
```
hl_monitor_batch_write_size
hl_monitor_batch_write_duration_seconds_bucket
```

#### 订单聚合指标
```
hl_monitor_order_aggregation_active
hl_monitor_order_flush_total{trigger="status|timeout"}
hl_monitor_order_fills_per_order_bucket
```

### 符号说明

**Hyperliquid Symbol 格式**：
- 现货：`COINUSDC`（如 `ETHUSDC`）
- 合约：直接使用 coin 名称（如 `ETH`）
- Meta Symbol：`@数字`（如 `@123`）

**方向枚举**：
- `open`：开仓
- `close`：平仓

**Side 枚举**：
- `LONG`：做多
- `SHORT`：做空

**仓位大小**：
- `Small`：小仓位
- `Medium`：中等仓位
- `Large`：大仓位
