# utrading-hl-monitor 全面优化实施计划

**日期**: 2026-01-19  
**作者**: Claude Code  
**状态**: 实施计划制定中  
**目标**: 针对 100-1000 地址、1000-10000 消息/秒的中等规模场景进行性能、内存、代码结构三维度优化

---

## 1. 优化目标与指标

### 1.1 性能目标

| 指标 | 当前状态 | 目标状态 | 提升幅度 |
|------|---------|---------|---------|
| 消息吞吐能力 | ~5000 msg/s | 10000 msg/s | +100% |
| P99 延迟 | ~500ms | <200ms | -60% |
| 内存占用 | ~2GB | <1GB | -50% |
| 数据库写入频率 | 每条写入 | 批量写入 | -90% |

### 1.2 优化范围

- **缓存层重构**: 替换 sync.Map，引入智能淘汰和 TTL 管理
- **消息处理层重构**: 异步队列 + 批量写入
- **WebSocket 层优化**: 多连接负载均衡

---

## 2. 技术选型决策

### 2.1 缓存层技术选型

| 使用场景 | 技术选型 | 理由 |
|---------|---------|------|
| 订单去重器 | go-cache | 精确 TTL 过期，自动清理，30分钟窗口 |
| OID 映射缓存 | go-cache | TTL 管理，自动过期 |
| Symbol 转换缓存 | Ristretto | LFU 淘汰，成本控制，1000 symbol 规模 |
| 现货价格缓存 | **Ristretto** | **成本控制，避免无界增长，统一技术栈** |
| 合约价格缓存 | **Ristretto** | **成本控制，避免无界增长，统一技术栈** |

**统一使用 Ristretto 的优势**：
- **内存可控**：所有价格缓存都有 MaxCost 上限，避免长期运行内存泄漏
- **统一监控**：所有 Ristretto 缓存提供一致的命中率、淘汰统计
- **读写均衡**：Ristretto 的 Sharded 设计在高并发读写场景性能接近 sync.Map
- **LFU 淘汰**：自动保留热点价格，淘汰冷门数据

**依赖库引入**:
```bash
# 安装缓存库
go get github.com/dgraph-io/ristretto@latest
go get github.com/patrickmn/go-cache@latest
```

```go
import (
    "github.com/dgraph-io/ristretto"  // 高性能缓存
    "github.com/patrickmn/go-cache"   // 带 TTL 的缓存
)
```

### 2.2 DAO 层批量操作规范

- **必须使用 gorm-gen 生成的类型安全 API**
- 使用 `gen.Q.*` 查询构建器 + `clause.OnConflict` 实现 UPSERT
- 复杂查询使用 `UnderlyingDB()` 获取底层 GORM 连接

---

## 3. 分阶段实施计划

### 阶段 1: 缓存层重构 (预计 1-2 天)

#### 1.1 订单去重器优化 (P0)

**当前问题**:
- 使用 sync.Map，内存无界累积
- 依赖定时清理，高峰期可能内存膨胀
- cleanup 全量遍历性能开销大

**优化方案**:

```go
// internal/cache/dedup_cache.go

package cache

import (
    "github.com/patrickmn/go-cache"
    "time"
)

// DedupCache 订单去重缓存
type DedupCache struct {
    cache *cache.Cache // go-cache 内置 TTL 和自动清理
}

func NewDedupCache(ttl time.Duration) *DedupCache {
    return &DedupCache{
        cache: cache.New(ttl, ttl*2), // 清理间隔 = 2×TTL
    }
}

// IsSeen 检查是否已处理
func (c *DedupCache) IsSeen(address string, oid int64, direction string) bool {
    key := c.dedupKey(address, oid, direction)
    _, exists := c.cache.Get(key)
    return exists
}

// Mark 标记为已处理
func (c *DedupCache) Mark(address string, oid int64, direction string) {
    key := c.dedupKey(address, oid, direction)
    c.cache.Set(key, time.Now(), cache.DefaultExpiration)
}

// dedupKey 生成去重键
func (c *DedupCache) dedupKey(address string, oid int64, direction string) string {
    return fmt.Sprintf("%s-%d-%s", address, oid, direction)
}

// LoadFromDB 从数据库加载已发送订单
func (c *DedupCache) LoadFromDB(dao dao.OrderAggregationDAO) error {
    since := time.Now().Add(-cacheTTL)
    orders, err := dao.GetSentOrdersSince(since)
    if err != nil {
        return err
    }

    count := 0
    for _, order := range orders {
        c.Mark(order.Address, order.Oid, order.Direction)
        count++
    }

    logger.Info().Int("count", count).Msg("loaded sent orders from database")
    return nil
}

// Stats 获取统计信息
func (c *DedupCache) Stats() map[string]interface{} {
    return map[string]interface{}{
        "item_count": c.cache.ItemCount(),
        "ttl_minutes": cacheTTL.Minutes(),
    }
}
```

**修改点**:
1. 创建 `internal/cache/dedup_cache.go`
2. 修改 `internal/ws/subscription.go` 中的 `OrderDeduper` 使用 `DedupCache`
3. 删除手动 cleanup 逻辑

**预期收益**:
- 内存占用降低 30-40%
- 代码简化 50+ 行
- 无性能损耗

---

#### 1.2 Symbol 转换缓存优化 (P0)

**当前问题**:
- 使用普通 map，缓存无限增长
- 无淘汰策略，可能导致 OOM

**优化方案**:

```go
// internal/cache/symbol_cache.go

package cache

import (
    "github.com/dgraph-io/ristretto"
    "strconv"
)

// SymbolCache Symbol 转换缓存
type SymbolCache struct {
    spotCache *ristretto.Cache // @123 → symbol
    perpCache *ristretto.Cache // BTC → symbol
}

func NewSymbolCache() (*SymbolCache, error) {
    // 1000 symbol 规模的配置
    spotConfig := &ristretto.Config{
        NumCounters: 1e4,              // 10 × 1000
        MaxCost:     128 * 1024,       // 128KB
        BufferItems: 64,
    }

    perpConfig := &ristretto.Config{
        NumCounters: 5e3,              // 5 × 1000
        MaxCost:     64 * 1024,        // 64KB
        BufferItems: 64,
    }

    spotCache, err := ristretto.NewCache(spotConfig)
    if err != nil {
        return nil, err
    }

    perpCache, err := ristretto.NewCache(perpConfig)
    if err != nil {
        return nil, err
    }

    return &SymbolCache{
        spotCache: spotCache,
        perpCache: perpCache,
    }, nil
}

// GetSpotSymbol 获取现货 symbol
func (c *SymbolCache) GetSpotSymbol(assetID int) (string, bool) {
    key := strconv.Itoa(assetID)
    if val, found := c.spotCache.Get(key); found {
        return val.(string), true
    }
    return "", false
}

// SetSpotSymbol 设置现货 symbol
func (c *SymbolCache) SetSpotSymbol(assetID int, symbol string) {
    key := strconv.Itoa(assetID)
    // 每个 symbol 约 20 字节
    c.spotCache.Set(key, symbol, 20)
}

// GetPerpSymbol 获取合约 symbol
func (c *SymbolCache) GetPerpSymbol(coin string) (string, bool) {
    if val, found := c.perpCache.Get(coin); found {
        return val.(string), true
    }
    return "", false
}

// SetPerpSymbol 设置合约 symbol
func (c *SymbolCache) SetPerpSymbol(coin, symbol string) {
    // 每个 symbol 约 15 字节
    c.perpCache.Set(coin, symbol, 15)
}

// Stats 获取统计信息
func (c *SymbolCache) Stats() map[string]interface{} {
    return map[string]interface{}{
        "spot_hits":      c.spotCache.Metrics.Hits(),
        "spot_misses":    c.spotCache.Metrics.Misses(),
        "spot_cost":      c.spotCache.Metrics.CostAdded(),
        "perp_hits":      c.perpCache.Metrics.Hits(),
        "perp_misses":    c.perpCache.Metrics.Misses(),
        "perp_cost":      c.perpCache.Metrics.CostAdded(),
    }
}

// Warmup预热Symbol缓存（从所有资产信息加载）
func (c *SymbolCache) Warmup(allAssets []hyperliquid.Asset) error {
    count := 0
    for _, asset := range allAssets {
        // 预热现货 symbol
        if asset.SpotSymbol != "" {
            c.SetSpotSymbol(asset.ID, asset.SpotSymbol)
            count++
        }

        // 预热合约 symbol
        if asset.PerpSymbol != "" {
            c.SetPerpSymbol(asset.Coin, asset.PerpSymbol)
            count++
        }
    }

    logger.Info().
        Int("total_assets", len(allAssets)).
        Int("warmed_items", count).
        Msg("symbol cache warmup completed")

    return nil
}
```

**启动时预热集成**：

```go
// internal/ws/subscription.go

func (m *SubscriptionManager) Start() error {
    // 1. 连接 WebSocket
    if err := m.poolManager.Connect(); err != nil {
        return err
    }

    // 2. 获取所有资产信息并预热缓存
    allAssets, err := m.poolManager.GetAllAssets()
    if err != nil {
        logger.Warn().Err(err).Msg("failed to get all assets for cache warmup")
    } else {
        if err := m.symbolConverter.Warmup(allAssets); err != nil {
            logger.Warn().Err(err).Msg("symbol cache warmup failed")
        }
    }

    // 3. 开始订阅地址
    // ...
}
```

**修改点**:
1. 创建 `internal/cache/symbol_cache.go`
2. 修改 `internal/position/manager.go` 中的 `SymbolConverter`
3. 删除手动 map 管理

**预期收益**:
- 内存可控（最大 192KB）
- 缓存命中率监控
- 自动淘汰热点数据

---

#### 1.3 价格缓存优化 (P0)

**当前问题**:
- 使用 sync.Map，缓存无界增长
- 长时间运行后内存持续累积
- 缺乏监控和淘汰策略

**优化方案**:

```go
// internal/cache/price_cache.go

package cache

import (
    "github.com/dgraph-io/ristretto"
)

// PriceCache 价格缓存（现货 + 合约）
type PriceCache struct {
    spotCache *ristretto.Cache // 现货价格
    perpCache *ristretto.Cache // 合约价格
}

func NewPriceCache() (*PriceCache, error) {
    // 1000 symbol，每个约 24 字节（symbol字符串 + float64）
    spotConfig := &ristretto.Config{
        NumCounters: 1e4,              // 10 × 1000
        MaxCost:     256 * 1024,       // 256KB
        BufferItems: 64,
    }

    perpConfig := &ristretto.Config{
        NumCounters: 1e4,              // 10 × 1000
        MaxCost:     256 * 1024,       // 256KB
        BufferItems: 64,
    }

    spotCache, err := ristretto.NewCache(spotConfig)
    if err != nil {
        return nil, err
    }

    perpCache, err := ristretto.NewCache(perpConfig)
    if err != nil {
        return nil, err
    }

    return &PriceCache{
        spotCache: spotCache,
        perpCache: perpCache,
    }, nil
}

// GetSpotPrice 获取现货价格
func (c *PriceCache) GetSpotPrice(symbol string) (float64, bool) {
    val, found := c.spotCache.Get(symbol)
    if !found {
        return 0, false
    }
    return val.(float64), true
}

// SetSpotPrice 设置现货价格
func (c *PriceCache) SetSpotPrice(symbol string, price float64) {
    // 每个 item 约 24 字节（symbol + float64 + overhead）
    c.spotCache.Set(symbol, price, 24)
}

// GetPerpPrice 获取合约价格
func (c *PriceCache) GetPerpPrice(symbol string) (float64, bool) {
    val, found := c.perpCache.Get(symbol)
    if !found {
        return 0, false
    }
    return val.(float64), true
}

// SetPerpPrice 设置合约价格
func (c *PriceCache) SetPerpPrice(symbol string, price float64) {
    c.perpCache.Set(symbol, price, 24)
}

// Stats 获取统计信息
func (c *PriceCache) Stats() map[string]interface{} {
    return map[string]interface{}{
        "spot_hits":       c.spotCache.Metrics.Hits(),
        "spot_misses":     c.spotCache.Metrics.Misses(),
        "spot_cost_added": c.spotCache.Metrics.CostAdded(),
        "spot_cost_evicted": c.spotCache.Metrics.CostEvicted(),
        "perp_hits":       c.perpCache.Metrics.Hits(),
        "perp_misses":     c.perpCache.Metrics.Misses(),
        "perp_cost_added": c.perpCache.Metrics.CostAdded(),
        "perp_cost_evicted": c.perpCache.Metrics.CostEvicted(),
    }
}
```

**修改点**:
1. 创建 `internal/cache/price_cache.go`
2. 修改 `internal/ws/` 或价格相关模块使用 `PriceCache`
3. 删除 sync.Map 价格缓存实现

**预期收益**:
- 内存可控（最大 512KB）
- 价格缓存命中率监控
- 自动淘汰冷门 symbol
- 与其他 Ristretto 缓存统一技术栈

---

#### 1.4 缓存接口抽象

**设计目标**: 统一缓存接口，便于测试和替换

```go
// internal/cache/interface.go

package cache

// SymbolCacheInterface Symbol 缓存接口
type SymbolCacheInterface interface {
    GetSpotSymbol(assetID int) (string, bool)
    SetSpotSymbol(assetID int, symbol string)
    GetPerpSymbol(coin string) (string, bool)
    SetPerpSymbol(coin, symbol string)
    Stats() map[string]interface{}
}

// DedupCacheInterface 去重缓存接口
type DedupCacheInterface interface {
    IsSeen(address string, oid int64, direction string) bool
    Mark(address string, oid int64, direction string)
    LoadFromDB(dao OrderAggregationDAO) error
    Stats() map[string]interface{}
}

// PriceCacheInterface 价格缓存接口
type PriceCacheInterface interface {
    GetSpotPrice(symbol string) (float64, bool)
    SetSpotPrice(symbol string, price float64)
    GetPerpPrice(symbol string) (float64, bool)
    SetPerpPrice(symbol string, price float64)
    Stats() map[string]interface{}
}
```

---

### 阶段 1 缓存层总结

**缓存层统一架构**：

```
┌─────────────────────────────────────────────┐
│              缓存层架构                       │
├─────────────────────────────────────────────┤
│                                             │
│  go-cache (TTL管理)                          │
│  ├─ 订单去重器 (30分钟TTL)                   │
│  └─ OID 映射 (30分钟TTL)                     │
│                                             │
│  Ristretto (成本控制)                        │
│  ├─ Symbol 转换缓存 (1000 symbol, 192KB)    │
│  ├─ 现货价格缓存 (1000 symbol, 256KB)        │
│  └─ 合约价格缓存 (1000 symbol, 256KB)        │
│                                             │
│  总内存预算: ~704KB                          │
│                                             │
└─────────────────────────────────────────────┘
```

**关键收益**：
- 所有有界缓存统一使用 Ristretto，内存可控
- go-cache 处理 TTL 场景，自动清理
- 统一的监控接口和指标
- 避免长期运行的内存泄漏风险

---

### 阶段 2: 消息处理层重构 (预计 2-3 天)

#### 2.1 异步消息队列 (P0)

**设计目标**: 解耦消息接收和处理，提升吞吐量

```go
// internal/processor/message_queue.go

package processor

import (
    "sync"
)

// Message 消息接口
type Message interface {
    Type() string
}

// OrderFillMessage 订单成交消息
type OrderFillMessage struct {
    Address string
    Fill    hyperliquid.WsOrderFill
}

func (m OrderFillMessage) Type() string { return "order_fill" }

// PositionUpdateMessage 仓位更新消息
type PositionUpdateMessage struct {
    Address string
    Data    hyperliquid.WsPositionData
}

func (m PositionUpdateMessage) Type() string { return "position_update" }

// MessageQueue 异步消息队列
type MessageQueue struct {
    queue    chan Message
    workers  int
    wg       sync.WaitGroup
    handler  MessageHandler
    done     chan struct{}
}

// MessageHandler 消息处理器接口
type MessageHandler interface {
    HandleMessage(msg Message) error
}

func NewMessageQueue(size int, workers int, handler MessageHandler) *MessageQueue {
    return &MessageQueue{
        queue:   make(chan Message, size),
        workers: workers,
        handler: handler,
        done:    make(chan struct{}),
    }
}

// Start 启动工作协程
func (q *MessageQueue) Start() {
    for i := 0; i < q.workers; i++ {
        q.wg.Add(1)
        go q.worker()
    }
}

func (q *MessageQueue) worker() {
    defer q.wg.Done()
    for {
        select {
        case msg := <-q.queue:
            if err := q.handler.HandleMessage(msg); err != nil {
                logger.Error().Err(err).Str("type", msg.Type()).Msg("handle message failed")
            }
        case <-q.done:
            return
        }
    }
}

// Enqueue 发送消息（带背压策略）
func (q *MessageQueue) Enqueue(msg Message) error {
    select {
    case q.queue <- msg:
        return nil
    default:
        // 队列满，启用同步降级策略
        monitor.QueueFullTotal.Inc()
        logger.Warn().
            Str("type", msg.Type()).
            Int("queue_size", len(q.queue)).
            Msg("message queue full, falling back to sync processing")

        // 同步处理消息（阻塞调用）
        return q.handler.HandleMessage(msg)
    }
}

// Stop 停止队列
func (q *MessageQueue) Stop() {
    close(q.done)
    q.wg.Wait()
}
```

---

#### 2.2 批量写入器 (P0)

**设计目标**: 降低数据库写入频率，减少 IO 压力

```go
// internal/processor/batch_writer.go

package processor

import (
    "sync"
    "time"
    "gorm.io/gorm"
    "gorm.io/gorm/clause"
)

// BatchItem 批量写入项
type BatchItem interface {
    TableName() string
}

// PositionCacheItem 仓位缓存项
type PositionCacheItem struct {
    Address string
    Cache   *models.HlPositionCache
}

func (i PositionCacheItem) TableName() string { return "hl_position_cache" }

// BatchWriterConfig 批量写入配置
type BatchWriterConfig struct {
    BatchSize    int           // 批量大小（默认 100）
    FlushInterval time.Duration // 刷新间隔（默认 100ms）
    MaxQueueSize int           // 最大队列大小（默认 10000）
}

// BatchWriter 批量写入器
type BatchWriter struct {
    config    BatchWriterConfig
    queue     chan BatchItem
    buffers   map[string][]BatchItem // 按 table 分组
    mu        sync.RWMutex
    db        *gorm.DB
    flushTick *time.Ticker
    done      chan struct{}
    wg        sync.WaitGroup
}

func NewBatchWriter(db *gorm.DB, config BatchWriterConfig) *BatchWriter {
    if config.BatchSize == 0 {
        config.BatchSize = 100
    }
    if config.FlushInterval == 0 {
        config.FlushInterval = 100 * time.Millisecond
    }
    if config.MaxQueueSize == 0 {
        config.MaxQueueSize = 10000
    }

    return &BatchWriter{
        config:  config,
        queue:   make(chan BatchItem, config.MaxQueueSize),
        buffers: make(map[string][]BatchItem),
        db:      db,
        done:    make(chan struct{}),
    }
}

// Start 启动批量写入器
func (w *BatchWriter) Start() {
    w.flushTick = time.NewTicker(w.config.FlushInterval)
    
    // 启动接收协程
    w.wg.Add(1)
    go w.receiveLoop()

    // 启动刷新协程
    w.wg.Add(1)
    go w.flushLoop()
}

func (w *BatchWriter) receiveLoop() {
    defer w.wg.Done()
    for {
        select {
        case item := <-w.queue:
            w.mu.Lock()
            table := item.TableName()
            w.buffers[table] = append(w.buffers[table], item)
            
            // 检查是否达到批量大小
            if len(w.buffers[table]) >= w.config.BatchSize {
                tables := []string{table}
                w.mu.Unlock()
                w.flush(tables...)
            } else {
                w.mu.Unlock()
            }
        case <-w.done:
            return
        }
    }
}

func (w *BatchWriter) flushLoop() {
    defer w.wg.Done()
    for {
        select {
        case <-w.flushTick.C:
            w.flushAll()
        case <-w.done:
            w.flushAll()
            return
        }
    }
}

// flush 刷新指定表
func (w *BatchWriter) flush(tables ...string) {
    w.mu.Lock()
    defer w.mu.Unlock()

    for _, table := range tables {
        items := w.buffers[table]
        if len(items) == 0 {
            continue
        }

        // 执行批量 upsert
        if err := w.batchUpsert(table, items); err != nil {
            logger.Error().Err(err).Str("table", table).Msg("batch upsert failed")
        } else {
            logger.Debug().Str("table", table).Int("count", len(items)).Msg("batch upsert success")
        }

        // 清空缓冲
        w.buffers[table] = nil
    }
}

// flushAll 刷新所有表
func (w *BatchWriter) flushAll() {
    w.mu.Lock()
    tables := make([]string, 0, len(w.buffers))
    for table := range w.buffers {
        tables = append(tables, table)
    }
    w.mu.Unlock()

    w.flush(tables...)
}

// batchUpsert 使用 gorm-gen 执行批量 upsert
func (w *BatchWriter) batchUpsert(table string, items []BatchItem) error {
    switch table {
    case "hl_position_cache":
        return w.batchUpsertPositions(items)
    default:
        return ErrUnsupportedTable
    }
}

// batchUpsertPositions 批量 upsert 仓位缓存
func (w *BatchWriter) batchUpsertPositions(items []BatchItem) error {
    caches := make([]*models.HlPositionCache, 0, len(items))
    for _, item := range items {
        if pos, ok := item.(PositionCacheItem); ok {
            caches = append(caches, pos.Cache)
        }
    }

    // 使用 gorm-gen 生成的查询 API
    // gen.Q.HlPositionCache 是全局查询对象
    return gen.Q.HlPositionCache.
        UnderlyingDB().
        Claused(clause.OnConflict{
            Columns:   []clause.Column{{Name: "address"}},
            DoUpdates: clause.AssignmentColumns([]string{
                "spot_balances", "spot_total_usd", "futures_positions",
                "account_value", "total_margin_used", "total_ntl_pos",
                "withdrawable", "updated_at",
            }),
        }).
        Create(&caches).
        Error
}

// Add 添加写入项
func (w *BatchWriter) Add(item BatchItem) error {
    select {
    case w.queue <- item:
        return nil
    default:
        return ErrQueueFull
    }
}

// Stop 停止写入器
func (w *BatchWriter) Stop() {
    close(w.done)
    w.wg.Wait()
    w.flushTick.Stop()

    // 优雅关闭：等待缓冲区数据写入完成
    w.flushAll()
}

// GracefulShutdown 优雅关闭，带超时控制
func (w *BatchWriter) GracefulShutdown(timeout time.Duration) error {
    done := make(chan struct{})
    go func() {
        w.Stop()
        close(done)
    }()

    select {
    case <-done:
        return nil
    case <-time.After(timeout):
        logger.Warn().Dur("timeout", timeout).Msg("batch writer shutdown timeout, forcing flush")
        w.flushAll() // 强制刷新
        return ErrShutdownTimeout
    }
}
```

**优雅关闭集成到 main.go**：

```go
func main() {
    // ... 初始化代码

    // 监听退出信号
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    <-sigCh
    logger.Info().Msg("shutting down gracefully...")

    // 优雅关闭批量写入器（最多等待 5 秒）
    if err := container.GetBatchWriter().GracefulShutdown(5 * time.Second); err != nil {
        logger.Error().Err(err).Msg("batch writer shutdown error")
    }

    logger.Info().Msg("shutdown complete")
}
```
```

---

#### 2.3 消息处理器实现

```go
// internal/processor/position_processor.go

package processor

// PositionProcessor 仓位消息处理器
type PositionProcessor struct {
    batchWriter *BatchWriter
    posManager  *position.Manager
}

func NewPositionProcessor(bw *BatchWriter, pm *position.Manager) *PositionProcessor {
    return &PositionProcessor{
        batchWriter: bw,
        posManager:  pm,
    }
}

func (p *PositionProcessor) HandleMessage(msg Message) error {
    switch m := msg.(type) {
    case PositionUpdateMessage:
        return p.handlePositionUpdate(m)
    default:
        return nil
    }
}

func (p *PositionProcessor) handlePositionUpdate(msg PositionUpdateMessage) error {
    // 处理仓位更新
    cache := p.posManager.ProcessPositionData(msg.Address, msg.Data)

    // 添加到批量写入队列
    return p.batchWriter.Add(PositionCacheItem{
        Address: msg.Address,
        Cache:   cache,
    })
}
```

---

### 阶段 3: WebSocket 层优化 (预计 1-2 天)

#### 3.1 多连接负载均衡 (P0)

详见 `2026-01-16-websocket-subscription-optimization-design.md`

核心改进：
- 每个 WebSocket 连接最多订阅 100 个地址
- 超过限制自动创建新连接
- 负载均衡选择最少负载的连接

---

#### 3.2 取消订阅实现 (P1)

**前提条件**: 检查 `go-hyperliquid` 库是否支持 `Unsubscribe()`

如果支持：
```go
func (m *SubscriptionManager) UnsubscribeAddress(addr string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    conn := m.findConnectionByAddress(addr)
    if conn == nil {
        return nil
    }

    key := addr + "-fills"
    if sub, ok := conn.subs[key]; ok {
        // 发送取消订阅消息
        if err := sub.Unsubscribe(); err != nil {
            logger.Warn().Err(err).Str("address", addr).Msg("unsubscribe failed")
        }
        sub.Close()
        delete(conn.subs, key)
    }

    delete(conn.addresses, addr)
    delete(m.addresses, addr)
    monitor.GetMetrics().SetAddressesCount(m.AddressCount())
    return nil
}
```

---

## 4. 灰度发布策略

### 4.1 按地址维度的灰度实现

**设计方案**：使用地址哈希决定是否启用优化，保证同一地址始终走同一路径。

```go
// internal/optimization/grayscale.go

package optimization

import (
    "hash/fnv"
    "math"
)

type GrayscaleController struct {
    ratio int // 0-100
}

func NewGrayscaleController(ratio int) *GrayscaleController {
    return &GrayscaleController{ratio: ratio}
}

// IsEnabled 判断指定地址是否启用优化
func (c *GrayscaleController) IsEnabled(address string) bool {
    if c.ratio >= 100 {
        return true // 全量启用
    }
    if c.ratio <= 0 {
        return false // 全部禁用
    }

    // 使用 FNV 哈希计算地址的分桶
    h := fnv.New32a()
    h.Write([]byte(address))
    hash := h.Sum32()

    // 哈希值模 100，结果小于 ratio 则启用
    return (hash % 100) < uint32(c.ratio)
}

// SetRatio 动态调整灰度比例
func (c *GrayscaleController) SetRatio(ratio int) {
    if ratio < 0 {
        ratio = 0
    }
    if ratio > 100 {
        ratio = 100
    }
    c.ratio = ratio
}

func (c *GrayscaleController) GetRatio() int {
    return c.ratio
}
```

**集成到 SubscriptionManager**：

```go
// internal/ws/subscription.go

type SubscriptionManager struct {
    // ... 现有字段
    grayscale *optimization.GrayscaleController
}

func (m *SubscriptionManager) handleOrderFills(addr string, order hyperliquid.WsOrderFills) {
    // 检查该地址是否启用优化
    if !m.grayscale.IsEnabled(addr) {
        // 使用旧逻辑（同步处理）
        m.handleOrderFillsLegacy(addr, order)
        return
    }

    // 使用新逻辑（异步队列 + 批量写入）
    m.handleOrderFillsOptimized(addr, order)
}
```

### 4.2 配置开关

```toml
[optimization]
# 是否启用优化（灰度开关）
enabled = true

# 优化功能开关
enable_cache_layer = true      # 缓存层优化
enable_async_queue = true      # 异步队列
enable_batch_writer = true     # 批量写入
enable_multi_connection = true # 多连接负载均衡

# 灰度比例（0-100，0=禁用，100=全量）
# 10% 地址启用优化
graylist_ratio = 10

### 4.2 灰度流程

```
1. 部署新版本（graylist_ratio = 0）
       ↓
2. 配置 graylist_ratio = 10（10% 地址启用）
       ↓
3. 监控关键指标 24 小时
       ↓
4. 指标正常 → 扩大到 50%
       ↓
5. 监控关键指标 24 小时
       ↓
6. 指标正常 → 扩大到 100%
       ↓
7. 稳定运行 1 周后移除开关
```

### 4.3 关键监控指标

| 指标 | 告警阈值 | 说明 |
|------|---------|------|
| 内存占用 | >1.5GB | 内存泄漏风险 |
| P99 延迟 | >300ms | 性能退化 |
| 消息积压 | >10000 | 队列阻塞 |
| 缓存命中率 | <80% | 缓存配置问题 |
| 数据库连接池 | >80% | 连接池耗尽 |

---

## 5. 风险控制与回滚预案

### 5.1 风险识别

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| Ristretto 内存泄漏 | 低 | 高 | 监控指标，保留 sync.Map 回退路径 |
| 批量写入数据丢失 | 中 | 高 | 定期刷新 + 优雅关闭 |
| 异步队列阻塞 | 中 | 中 | 队列满告警 + 降级策略 |
| 多连接稳定性 | 低 | 中 | 连接健康检查 + 自动重连 |

### 5.2 回滚预案

**触发条件**:
1. 核心指标 P0 级别告警
2. 数据不一致率 >1%
3. 服务不可用

**回滚步骤**:
1. 关闭灰度开关
2. 重启服务使用旧代码路径
3. 验证数据一致性
4. 分析问题根因

**回滚命令**:
```bash
# 1. 更新配置
sed -i 's/enable_cache_layer = true/enable_cache_layer = false/' cfg.toml

# 2. 重启服务
make restart

# 3. 验证
make logs | grep "optimization disabled"
```

---

## 6. 依赖注入容器设计

### 6.1 Registry 组件

```go
// internal/registry/registry.go

package registry

import (
    "sync"
)

// Container 依赖注入容器
type Container struct {
    // 缓存层
    symbolCache  cache.SymbolCacheInterface
    priceCache   cache.PriceCacheInterface
    dedupCache   cache.DedupCacheInterface
    
    // 处理层
    messageQueue *processor.MessageQueue
    batchWriter  *processor.BatchWriter
    
    // 业务层
    posManager   *position.Manager
    subManager   *ws.SubscriptionManager
    
    mu sync.RWMutex
}

func NewContainer() *Container {
    return &Container{}
}

// Initialize 初始化所有组件
func (c *Container) Initialize(db *gorm.DB, cfg *config.Config) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // 1. 初始化缓存层
    symbolCache, err := cache.NewSymbolCache()
    if err != nil {
        return err
    }
    c.symbolCache = symbolCache

    priceCache, err := cache.NewPriceCache()
    if err != nil {
        return err
    }
    c.priceCache = priceCache

    dedupCache := cache.NewDedupCache(30 * time.Minute)
    if err := dedupCache.LoadFromDB(dao.OrderAggregation()); err != nil {
        logger.Warn().Err(err).Msg("load dedup cache failed")
    }
    c.dedupCache = dedupCache

    // 2. 初始化处理层
    bw := processor.NewBatchWriter(db, processor.BatchWriterConfig{
        BatchSize:    100,
        FlushInterval: 100 * time.Millisecond,
        MaxQueueSize: 10000,
    })
    bw.Start()
    c.batchWriter = bw

    mq := processor.NewMessageQueue(10000, 4, nil)
    mq.Start()
    c.messageQueue = mq

    // 3. 初始化业务层
    c.posManager = position.NewManager(symbolCache, bw)
    c.subManager = ws.NewSubscriptionManager(pool, publisher, dedupCache)

    return nil
}

// Shutdown 优雅关闭
func (c *Container) Shutdown() error {
    c.mu.Lock()
    defer c.mu.Unlock()

    // 停止消息队列
    if c.messageQueue != nil {
        c.messageQueue.Stop()
    }

    // 停止批量写入器
    if c.batchWriter != nil {
        c.batchWriter.Stop()
    }

    return nil
}

// Getters
func (c *Container) SymbolCache() cache.SymbolCacheInterface {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.symbolCache
}

func (c *Container) DedupCache() cache.DedupCacheInterface {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.dedupCache
}

func (c *Container) PriceCache() cache.PriceCacheInterface {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.priceCache
}

// ... 其他 Getters
```

---

## 7. 实施时间表

| 阶段 | 任务 | 预计时间 | 依赖 |
|------|------|---------|------|
| 1.1 | 订单去重器优化 | 0.5 天 | - |
| 1.2 | Symbol 缓存优化 | 0.5 天 | - |
| 1.3 | 价格缓存优化 | 0.5 天 | - |
| 1.4 | 缓存接口抽象 | 0.5 天 | 1.1, 1.2, 1.3 |
| 2.1 | 异步消息队列 | 1 天 | 1.4 |
| 2.2 | 批量写入器 | 1.5 天 | 2.1 |
| 2.3 | 消息处理器实现 | 0.5 天 | 2.2 |
| 3.1 | 多连接负载均衡 | 1 天 | - |
| 3.2 | 取消订阅实现 | 0.5 天 | 3.1 |
| 4 | 依赖注入容器 | 0.5 天 | 全部 |
| 5 | 单元测试 | 1 天 | 全部 |
| 6 | 集成测试 | 1 天 | 全部 |
| 7 | 灰度发布 | 3 天 | 全部 |

**总计**: 约 12-13 天

---

## 8. 测试计划

### 8.1 单元测试

```go
// internal/cache/dedup_cache_test.go

func TestDedupCache_IsSeen(t *testing.T) {
    cache := NewDedupCache(30 * time.Second)

    // 测试首次查询
    assert.False(t, cache.IsSeen("addr1", 123, "open"))

    // 测试标记
    cache.Mark("addr1", 123, "open")
    assert.True(t, cache.IsSeen("addr1", 123, "open"))

    // 测试不同方向
    assert.False(t, cache.IsSeen("addr1", 123, "close"))
}

func TestDedupCache_TTL(t *testing.T) {
    cache := NewDedupCache(100 * time.Millisecond)

    cache.Mark("addr1", 123, "open")
    assert.True(t, cache.IsSeen("addr1", 123, "open"))

    // 等待过期
    time.Sleep(150 * time.Millisecond)
    assert.False(t, cache.IsSeen("addr1", 123, "open"))
}
```

### 8.2 压力测试

```bash
# 模拟 10000 消息/秒
go test -bench=. -benchtime=10s ./internal/processor/

# 内存泄漏检测
go test -memprofile=mem.prof ./internal/cache/
go tool pprof mem.prof
```

### 8.3 集成测试

- 端到端消息流测试
- 服务重启恢复测试
- 多并发场景测试

---

## 9. 监控指标扩展

### 9.1 新增 Prometheus 指标

```go
// internal/monitor/metrics.go

var (
    // 缓存指标
    cacheHitTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hl_cache_hit_total",
            Help: "Cache hit total",
        },
        []string{"cache_type"},
    )

    cacheMissTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hl_cache_miss_total",
            Help: "Cache miss total",
        },
        []string{"cache_type"},
    )

    // 队列指标
    queueSize = promauto.NewGauge(
        prometheus.GaugeOpts{
            Name: "hl_message_queue_size",
            Help: "Message queue size",
        },
    )

    queueFullTotal = promauto.NewCounter(
        prometheus.CounterOpts{
            Name: "hl_message_queue_full_total",
            Help: "Message queue full count",
        },
    )

    // 批量写入指标
    batchWriteSize = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "hl_batch_write_size",
            Help:    "Batch write size",
            Buckets: prometheus.LinearBuckets(10, 10, 100),
        },
    )

    batchWriteDuration = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "hl_batch_write_duration_seconds",
            Help:    "Batch write duration",
            Buckets: prometheus.DefBuckets,
        },
    )
)
```

---

## 10. 配置文件更新

```toml
# 新增优化配置段
[optimization]
# 总开关
enabled = true

# 功能开关
enable_cache_layer = true
enable_async_queue = true
enable_batch_writer = true
enable_multi_connection = true

# 灰度配置
graylist_ratio = 10  # 0-100

[cache.symbol]
# Symbol 缓存配置
max_symbols = 1000
num_counters = 10000
max_cost_spot = 131072   # 128KB
max_cost_perp = 65536    # 64KB

[cache.price]
# 价格缓存配置
max_symbols = 1000
num_counters = 10000
max_cost_spot = 262144   # 256KB
max_cost_perp = 262144   # 256KB

[cache.dedup]
# 去重缓存配置
ttl = "30m"

[queue]
# 消息队列配置
queue_size = 10000
workers = 4

[batch_writer]
# 批量写入配置
batch_size = 100
flush_interval = "100ms"
max_queue_size = 10000

[websocket]
# WebSocket 配置
max_addresses_per_connection = 100
```

---

## 11. 文档更新清单

- [ ] CLAUDE.md - 更新缓存层和处理器说明
- [ ] README.md - 更新性能指标和配置说明
- [ ] docs/architecture.md - 更新架构图
- [ ] docs/deployment.md - 更新灰度发布流程
- [ ] internal/cache/README.md - 新增缓存模块文档
- [ ] internal/processor/README.md - 新增处理器模块文档

---

## 12. 总结

本实施计划通过 **缓存层重构**、**消息处理层重构** 和 **WebSocket 层优化** 三个维度，系统性地提升 utrading-hl-monitor 的性能和可维护性。

**关键成果**:
- 消息吞吐提升 100%（5000 → 10000 msg/s）
- 内存占用降低 50%（2GB → 1GB）
- P99 延迟降低 60%（500ms → 200ms）
- 数据库写入频率降低 90%

**风险控制**:
- 灰度发布策略
- 完善的监控指标
- 快速回滚预案

**下一步**:
1. 评审本实施计划
2. 开始阶段 1 开发
3. 持续监控和调整
