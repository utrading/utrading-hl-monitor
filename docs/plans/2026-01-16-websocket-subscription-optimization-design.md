# WebSocket 订阅管理优化设计

## 背景问题

当前 `SubscriptionManager` 存在以下问题需要解决：

1. **订阅数量限制**：单个 WebSocket 连接不能订阅太多地址，需要多连接负载均衡
2. **取消订阅未实现**：取消订阅时只关闭了本地 Subscription，未发送 WebSocket 取消订阅消息
3. **历史数据干扰**：订阅地址时会收到历史 fills，需要过滤已处理的订单
4. **重启后重复处理**：服务重启时，WebSocket 可能会重新发送已处理过的 fills

## 核心设计原则

- **问题3和4的关联性**：两者都是"过滤已处理的订单"，使用统一的去重机制
- **时间窗口**：只处理 30 分钟内的订单，超时订单自动过期
- **内存去重**：使用内存集合缓存已发送的订单，避免频繁数据库查询

---

## 解决方案总览

| 问题 | 解决方案 | 优先级 |
|------|---------|--------|
| 1. 订阅数量限制 | 多连接负载均衡 + 可配置的每连接最大订阅数 | P0 |
| 2. 取消订阅 | 发送 WebSocket 取消订阅消息 | P1 |
| 3. 历史数据干扰 | 内存去重集合 + 启动时预加载 | P0 |
| 4. 重启后重复 | 与问题3共用去重机制 | P0 |

---

## 架构设计

### 1. 多连接负载均衡

#### 数据结构变更

```go
type SubscriptionManager struct {
    pool       *PoolManager  // 改为连接池管理器（原 *Pool）
    publisher  Publisher
    addresses  map[string]bool
    subs       map[string]*hyperliquid.Subscription
    aggregator *OrderAggregator
    deduper    *OrderDeduper  // 新增：订单去重器
    mu         sync.RWMutex
    done       chan struct{}

    // 新增：负载均衡配置
    maxAddressesPerConnection int  // 每个连接最大订阅地址数
    connections               []*ConnectionWrapper  // 连接包装器列表
}

type ConnectionWrapper struct {
    client    *hyperliquid.WebsocketClient
    addresses map[string]bool  // 该连接上的地址
    subs      map[string]*hyperliquid.Subscription
    mu        sync.RWMutex
}

type PoolManager struct {
    connections []*ConnectionWrapper
    maxPerConn  int
    mu          sync.RWMutex
}
```

#### 订阅分配策略

```go
// SubscribeAddress 选择负载最小的连接订阅
func (m *SubscriptionManager) SubscribeAddress(addr string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 检查去重
    if m.addresses[addr] {
        return nil
    }

    // 选择负载最小的连接
    conn := m.selectLeastLoadedConnection()

    // 在该连接上订阅
    if err := m.subscribeOnConnection(conn, addr); err != nil {
        return err
    }

    m.addresses[addr] = true
    return nil
}

func (m *SubscriptionManager) selectLeastLoadedConnection() *ConnectionWrapper {
    var minConn *ConnectionWrapper
    minCount := int(math.MaxInt32)

    for _, conn := range m.connections {
        conn.mu.RLock()
        count := len(conn.addresses)
        conn.mu.RUnlock()

        if count < minCount {
            minCount = count
            minConn = conn
        }

        // 如果有空余容量，直接返回
        if minCount < m.maxAddressesPerConnection {
            break
        }
    }

    // 如果所有连接都满了，创建新连接
    if minCount >= m.maxAddressesPerConnection {
        newConn := m.createNewConnection()
        m.connections = append(m.connections, newConn)
        return newConn
    }

    return minConn
}
```

---

### 2. 取消订阅优化

#### 当前问题

```go
// 当前实现：只关闭本地 Subscription
func (m *SubscriptionManager) UnsubscribeAddress(addr string) error {
    if sub, ok := m.subs[addr+"-fills"]; ok {
        sub.Close()  // ❌ 只关闭本地，未发送取消订阅消息
        delete(m.subs, addr+"-fills")
    }
    return nil
}
```

#### 优化方案

需要检查 `go-hyperliquid` 库是否支持取消订阅：

```go
// 方案1：如果库支持取消订阅
func (m *SubscriptionManager) UnsubscribeAddress(addr string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    if !m.addresses[addr] {
        return nil
    }

    // 找到该地址所在的连接
    conn := m.findConnectionByAddress(addr)
    if conn == nil {
        return nil
    }

    // 发送取消订阅消息
    if sub, ok := conn.subs[addr+"-fills"]; ok {
        if err := sub.Unsubscribe(); err != nil {
            logger.Warn().Err(err).Str("address", addr).Msg("unsubscribe failed")
        }
        sub.Close()
        delete(conn.subs, addr+"-fills")
    }

    // 更新连接地址集合
    delete(conn.addresses, addr)
    delete(m.addresses, addr)

    monitor.GetMetrics().SetAddressesCount(m.AddressCount())
    return nil
}

// 方案2：如果库不支持，记录需要后续处理
// - 可以在 issue 中请求库支持
// - 或者关闭并重建连接（不推荐）
```

---

### 3. 订单去重器（OrderDeduper）

#### 设计目标

- **过滤历史 fills**：订阅时收到的历史订单
- **防止重复处理**：服务重启后的重复数据
- **时间窗口**：只保留 30 分钟内的订单记录
- **性能**：内存操作，O(1) 查询

#### 数据结构

```go
type OrderDeduper struct {
    // 去重集合：key = "address-oid-direction", value = 过期时间戳
    seen sync.Map

    // 配置
    ttl time.Duration  // 订单保留时间（30分钟）

    // 清理
    done    chan struct{}
    cleanup *time.Ticker
}

func NewOrderDeduper(ttl time.Duration) *OrderDeduper {
    d := &OrderDeduper{
        seen:    sync.Map{},
        ttl:     ttl,
        done:    make(chan struct{}),
        cleanup: time.NewTicker(5 * time.Minute),  // 每5分钟清理一次
    }

    go d.cleanupLoop()
    return d
}

// dedupKey 生成去重键
func (d *OrderDeduper) dedupKey(address string, oid int64, direction string) string {
    return fmt.Sprintf("%s-%d-%s", address, oid, direction)
}
```

#### 核心方法

```go
// IsSeen 检查订单是否已处理
func (d *OrderDeduper) IsSeen(address string, oid int64, direction string) bool {
    key := d.dedupKey(address, oid, direction)

    if _, exists := d.seen.Load(key); exists {
        return true
    }
    return false
}

// Mark 标记订单为已处理
func (d *OrderDeduper) Mark(address string, oid int64, direction string) {
    key := d.dedupKey(address, oid, direction)
    expiry := time.Now().Add(d.ttl)
    d.seen.Store(key, expiry.Unix())
}

// LoadFromDB 从数据库加载已发送的订单
func (d *OrderDeduper) LoadFromDB(dao dao.OrderAggregationDAO) error {
    // 加载 30 分钟内 signal_sent=true 的订单
    since := time.Now().Add(-d.ttl)

    orders, err := dao.GetSentOrdersSince(since)
    if err != nil {
        return err
    }

    count := 0
    for _, order := range orders {
        d.Mark(order.Address, order.Oid, order.Direction)
        count++
    }

    logger.Info().
        Int("count", count).
        Dur("window", d.ttl).
        Msg("loaded sent orders from database")

    return nil
}

// cleanupLoop 定期清理过期记录
func (d *OrderDeduper) cleanupLoop() {
    for {
        select {
        case <-d.done:
            return
        case <-d.cleanup.C:
            d.cleanup()
        }
    }
}

func (d *OrderDeduper) cleanup() {
    now := time.Now().Unix()
    expired := 0

    d.seen.Range(func(key, value any) bool {
        expiry := value.(int64)
        if expiry < now {
            d.seen.Delete(key)
            expired++
        }
        return true
    })

    if expired > 0 {
        logger.Debug().
            Int("expired", expired).
            Msg("cleaned up expired dedup entries")
    }
}
```

---

### 4. 集成到 SubscriptionManager

#### 修改 handleOrderFills

```go
func (m *SubscriptionManager) handleOrderFills(addr string, order hyperliquid.WsOrderFills) {
    logger.Info().Str("address", addr).Int("fills_count", len(order.Fills)).Msg("received order fills")

    // 按 Oid 分组 fills
    orderGroups := make(map[int64][]hyperliquid.WsOrderFill)
    for _, fill := range order.Fills {
        orderGroups[fill.Oid] = append(orderGroups[fill.Oid], fill)
    }

    // 处理每个订单组
    for _, fills := range orderGroups {
        // 拆分反手订单
        splitOrders := m.splitReversedOrder(fills)

        // 为每个方向调用 AddFill
        for dir, dirFills := range splitOrders {
            for _, fill := range dirFills {
                // 检查是否已处理（去重）
                if m.deduper.IsSeen(addr, fill.Oid, dir) {
                    logger.Debug().
                        Str("address", addr).
                        Int64("oid", fill.Oid).
                        Str("direction", dir).
                        Msg("skip already processed order")
                    continue
                }

                m.aggregator.AddFill(addr, fill, dir)
            }
        }
    }
}
```

#### 修改信号发送流程

```go
// 在 flushOrder 发送信号后，标记为已处理
func (a *OrderAggregator) flushOrder(key string, trigger string) {
    // ... 发送信号逻辑 ...

    // 标记为已发送
    agg.SignalSent = true
    if dao.OrderAggregation() != nil {
        if err := dao.OrderAggregation().Update(agg); err != nil {
            logger.Error().Err(err).Int64("oid", agg.Oid).Msg("update order aggregation failed")
        }
    }

    // 新增：标记到去重器
    if SubscriptionManager.deduper != nil {
        SubscriptionManager.deduper.Mark(agg.Address, agg.Oid, agg.Direction)
    }

    // ...
}
```

---

## 启动流程

### 初始化顺序

```go
func main() {
    // 1. 初始化 DAO
    dao.InitDAO(db)

    // 2. 创建去重器
    deduper := ws.NewOrderDeduper(30 * time.Minute)

    // 3. 从数据库加载已发送的订单
    if err := deduper.LoadFromDB(dao.OrderAggregation()); err != nil {
        logger.Warn().Err(err).Msg("failed to load sent orders, continuing anyway")
    }

    // 4. 创建连接池（支持多连接）
    poolManager := ws.NewPoolManager(url, maxAddressesPerConnection)

    // 5. 创建订阅管理器
    subManager := ws.NewSubscriptionManager(poolManager, publisher, deduper)

    // 6. 启动服务
    // ...
}
```

---

## 数据库查询优化

### DAO 方法

```go
// internal/dao/order_aggregation.go

func (d *OrderAggregationDAO) GetSentOrdersSince(since time.Time) ([]*models.OrderAggregation, error) {
    var orders []*models.OrderAggregation

    err := d.UnderlyingDB().
        Where(d.SignalSent.Eq(true)).
        Where(d.LastFillTime.Gte(since.Unix())).
        Find(&orders).
        Error

    return orders, err
}
```

---

## 配置参数

### 新增配置项

```toml
[hl_monitor]
# 每个连接最大订阅地址数
max_addresses_per_connection = 100

# 订单去重时间窗口
order_dedup_window = "30m"

# 订单去重清理间隔
order_dedup_cleanup_interval = "5m"
```

---

## 监控指标

### 新增 Prometheus 指标

```go
// 去重器指标
func (d *OrderDeduper) GetMetrics() map[string]interface{} {
    count := 0
    d.seen.Range(func(_, _ any) bool {
        count++
        return true
    })

    return map[string]interface{}{
        "dedup_entries":    count,
        "dedup_ttl_minutes": d.ttl.Minutes(),
    }
}

// 订阅管理器指标
func (m *SubscriptionManager) GetStats() map[string]any {
    connStats := make([]map[string]any, len(m.connections))

    for i, conn := range m.connections {
        conn.mu.RLock()
        connStats[i] = map[string]any{
            "address_count": len(conn.addresses),
            "subscription_count": len(conn.subs),
        }
        conn.mu.RUnlock()
    }

    return map[string]any{
        "address_count":         m.AddressCount(),
        "connection_count":      len(m.connections),
        "connections":           connStats,
        "dedup_entries":         m.deduper.GetMetrics()["dedup_entries"],
    }
}
```

---

## 边界情况处理

### 1. 重连时的去重

- 重连后，WebSocket 会重新推送所有 fills（包括历史的）
- 去重器会自动过滤已处理的订单

### 2. 去重器内存溢出

- TTL 机制确保 30 分钟后自动过期
- 定期清理任务防止内存泄漏

### 3. 并发安全

- `sync.Map` 保证去重集合的并发安全
- `ConnectionWrapper` 使用独立的锁

### 4. 数据库连接失败

- 启动时如果数据库不可用，只记录警告，不阻塞启动
- 去重器从空集开始运行

---

## 实施步骤

### 阶段 1：订单去重器（P0）

1. 创建 `internal/ws/deduper.go`
2. 实现 `OrderDeduper` 结构和方法
3. 添加 DAO 查询方法 `GetSentOrdersSince()`
4. 在 `SubscriptionManager` 中集成去重器
5. 修改 `handleOrderFills` 添加去重检查
6. 单元测试和集成测试

### 阶段 2：多连接负载均衡（P0）

1. 创建 `ConnectionWrapper` 结构
2. 修改 `Pool` → `PoolManager`
3. 实现连接选择逻辑
4. 实现动态连接创建
5. 更新监控指标
6. 测试多连接场景

### 阶段 3：取消订阅优化（P1）

1. 检查 `go-hyperliquid` 是否支持 `Unsubscribe()`
2. 如果支持，实现取消订阅逻辑
3. 如果不支持，提交 issue 或寻找替代方案
4. 测试取消订阅

### 阶段 4：监控和优化（P2）

1. 添加 Prometheus 指标
2. 添加性能监控
3. 压力测试
4. 文档更新

---

## 影响范围

### 修改文件

- `internal/ws/subscription.go` - 核心订阅管理逻辑
- `internal/ws/pool.go` - 改为 PoolManager
- `internal/ws/deduper.go` - 新增去重器
- `internal/dao/order_aggregation.go` - 新增查询方法
- `cfg.toml` - 新增配置项

### 测试文件

- `internal/ws/subscription_test.go`
- `internal/ws/deduper_test.go`

---

## 验证清单

- [ ] 去重器正确过滤历史订单
- [ ] 去重器正确过滤重启后的重复订单
- [ ] 去重器正确清理过期记录
- [ ] 多连接负载均衡正常工作
- [ ] 取消订阅正确发送消息
- [ ] 监控指标正确上报
- [ ] 所有单元测试通过
- [ ] 集成测试通过
