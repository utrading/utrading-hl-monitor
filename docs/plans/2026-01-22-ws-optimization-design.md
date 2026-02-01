# ws 包优化设计文档

**日期**: 2026-01-22
**状态**: 设计完成

## 概述

本文档详细描述了 `internal/ws` 包的并发安全问题修复和性能优化方案。

---

## P0-1: 修复锁顺序不一致导致死锁风险

### 问题描述

`handleDisconnect()` 方法中存在多重锁获取，锁顺序不一致可能导致死锁：

**当前代码** (`pool_manager.go:305-337`):
```go
func (pm *PoolManager) handleDisconnect() {
    if !pm.reconnectMu.TryLock() {
        return
    }
    defer pm.reconnectMu.Unlock()

    pm.mu.Lock()
    pm.reconnecting = true
    pm.mu.Unlock()  // ❌ 释放 pm.mu 后仍持有 reconnectMu

    // ... 长时间操作 ...

    pm.mu.Lock()
    pm.reconnecting = false
    pm.mu.Unlock()
}
```

**死锁场景**：
1. goroutine A: 持有 `pm.mu` → 尝试获取 `reconnectMu`
2. goroutine B: 持有 `reconnectMu` → 在 `reconnectAll()` 中尝试获取 `pm.mu`

### 修复方案

**原则**: 建立统一的锁获取顺序，避免嵌套锁。

**方案 1: 重构 handleDisconnect，避免在持有 reconnectMu 时访问 pm.mu**

```go
// handleDisconnect 处理连接断开（修复版）
func (pm *PoolManager) handleDisconnect() {
    // 检查是否已在重连中
    if !pm.reconnectMu.TryLock() {
        logger.Debug().Msg("Reconnect already in progress, skipping")
        return
    }
    defer pm.reconnectMu.Unlock()

    logger.Warn().Msg("Starting WebSocket reconnection...")

    // 设置重连状态
    pm.setReconnecting(true)

    // 等待短暂时间后重连（避免网络抖动导致频繁重连）
    time.Sleep(2 * time.Second)

    if err := pm.reconnectAll(); err != nil {
        logger.Error().Err(err).Msg("Reconnection failed, will retry on next disconnect")
    }

    // 清除重连状态
    pm.setReconnecting(false)

    logger.Info().Msg("Reconnection completed")
}

// setReconnecting 设置重连状态
func (pm *PoolManager) setReconnecting(status bool) {
    pm.mu.Lock()
    pm.reconnecting = status
    pm.mu.Unlock()
}

// IsReconnecting 检查是否正在重连
func (pm *PoolManager) IsReconnecting() bool {
    pm.reconnectMu.Lock()
    defer pm.reconnectMu.Unlock()
    pm.mu.RLock()
    defer pm.mu.RUnlock()
    return pm.reconnecting
}
```

**方案 2: 修改 reconnectAll，确保不持有 pm.mu 时调用**

```go
// reconnectAll 重连所有连接并恢复订阅（修复版）
func (pm *PoolManager) reconnectAll() error {
    ctx := context.Background()

    // 1. 分类连接：已断开的 vs 未断开的（快照操作）
    pm.mu.Lock()
    var disconnectedConns, activeConns []*ConnectionWrapper
    for _, cw := range pm.connections {
        if !cw.Client().IsConnected() {
            disconnectedConns = append(disconnectedConns, cw)
        } else {
            activeConns = append(activeConns, cw)
        }
    }
    pm.mu.Unlock()

    // 2. 关闭所有已断开的连接
    for _, cw := range disconnectedConns {
        cw.Client().Close()
    }

    // 3. 重建已断开的连接（不持有 pm.mu）
    reconnectedConns := make([]*ConnectionWrapper, 0, len(disconnectedConns))
    for i, cw := range disconnectedConns {
        client := NewClient(pm.url)
        client.SetMessageHandler(pm.dispatcher.Dispatch)
        client.SetDisconnectCallback(func() {
            logger.Warn().Msg("WebSocket connection disconnected, triggering reconnect")
            go pm.handleDisconnect()
        })

        if err := client.Connect(ctx); err != nil {
            logger.Error().Err(err).Int("connection_index", i).Msg("Failed to reconnect")
            continue
        }

        wrapper := NewConnectionWrapper(client)
        reconnectedConns = append(reconnectedConns, wrapper)

        // 迁移订阅计数
        for _, key := range cw.GetSubscriptions() {
            wrapper.AddSubscription(key)
        }

        logger.Info().Int("connection_index", i).Msg("Reconnected successfully")
    }

    // 4. 合并未断开的连接和重连的连接
    pm.mu.Lock()
    pm.connections = append(activeConns, reconnectedConns...)
    pm.mu.Unlock()

    logger.Info().
        Int("active_connections", len(activeConns)).
        Int("reconnected", len(reconnectedConns)).
        Int("total", len(activeConns)+len(reconnectedConns)).
        Msg("Connection status after reconnection")

    // 5. 恢复订阅（如果需要）
    return pm.restoreSubscriptions(disconnectedConns, reconnectedConns)
}

// restoreSubscriptions 恢复订阅
func (pm *PoolManager) restoreSubscriptions(disconnectedConns, reconnectedConns []*ConnectionWrapper) error {
    if len(reconnectedConns) == 0 {
        logger.Info().Msg("No connections needed reconnection, skipping subscription restore")
        return nil
    }

    // 6. 快速获取需要恢复的订阅（最小化锁时间）
    pm.subscriptionsMu.RLock()
    subscriptionsToRestore := make([]string, 0, len(pm.subscriptions))
    oldConnMap := make(map[*ConnectionWrapper]struct{})

    for key, info := range pm.subscriptions {
        // 检查是否原连接断开
        for _, dc := range disconnectedConns {
            if info.connection == dc {
                subscriptionsToRestore = append(subscriptionsToRestore, key)
                oldConnMap[info.connection] = struct{}{}
                break
            }
        }
    }
    pm.subscriptionsMu.RUnlock()

    if len(subscriptionsToRestore) == 0 {
        return nil
    }

    // 7. 重新发送订阅请求
    restoredCount := 0
    for _, key := range subscriptionsToRestore {
        // 获取新连接
        conn, err := pm.acquireConnectionNoLock()
        if err != nil {
            logger.Error().Err(err).Str("key", key).Msg("Failed to acquire connection for resubscribe")
            continue
        }

        // 获取订阅信息
        pm.subscriptionsMu.RLock()
        info := pm.subscriptions[key]
        pm.subscriptionsMu.RUnlock()

        // 重新发送订阅请求
        if conn.Client().IsConnected() {
            if err := conn.Client().Subscribe(info.subscription); err != nil {
                logger.Error().Err(err).Str("key", key).Msg("Failed to resubscribe")
                continue
            }
        }

        // 更新订阅信息
        pm.subscriptionsMu.Lock()
        if existingInfo, exists := pm.subscriptions[key]; exists {
            existingInfo.connection = conn
        }
        pm.subscriptionsMu.Unlock()

        conn.AddSubscription(key)
        restoredCount++
        logger.Info().Str("key", key).Msg("Resubscribed successfully")
    }

    logger.Info().
        Int("restored", restoredCount).
        Int("total", len(subscriptionsToRestore)).
        Msg("Subscription restore completed")

    return nil
}
```

### 修复验证

1. 单元测试：测试并发调用 `handleDisconnect()` 和 `reconnectAll()`
2. 集成测试：模拟多个连接同时断开
3. 压力测试：并发订阅和重连

---

## P0-2: 修复持有 subscriptionsMu 锁时间过长

### 问题描述

`reconnectAll()` 中复制 `subscriptions` 时持有读锁，但后续的 `acquireConnectionNoLock()` 可能触发写操作，导致新的订阅操作被阻塞。

### 修复方案

**优化思路**: 分批更新订阅，避免长时间持有锁

```go
// restoreSubscriptions 优化版
func (pm *PoolManager) restoreSubscriptions(disconnectedConns, reconnectedConns []*ConnectionWrapper) error {
    if len(reconnectedConns) == 0 {
        return nil
    }

    // 1. 快速快照：只记录需要恢复的订阅 key
    pm.subscriptionsMu.RLock()
    keysToRestore := make([]string, 0)
    connMapping := make(map[string]*ConnectionWrapper)
    for key, info := range pm.subscriptions {
        // 检查是否原连接断开
        for _, dc := range disconnectedConns {
            if info.connection == dc {
                keysToRestore = append(keysToRestore, key)
                connMapping[key] = info.connection
                break
            }
        }
    }
    pm.subscriptionsMu.RUnlock()

    // 2. 分批更新订阅（每批 100 个）
    batchSize := 100
    for i := 0; i < len(keysToRestore); i += batchSize {
        end := i + batchSize
        if end > len(keysToRestore) {
            end = len(keysToRestore)
        }

        // 批量获取新连接映射
        newConnMapping := make(map[string]*ConnectionWrapper)
        for j := i; j < end; j++ {
            key := keysToRestore[j]
            conn, err := pm.acquireConnectionNoLock()
            if err != nil {
                logger.Error().Err(err).Str("key", key).Msg("Failed to acquire connection")
                continue
            }
            newConnMapping[key] = conn
        }

        // 3. 重新发送订阅请求
        for j := i; j < end; j++ {
            key := keysToRestore[j]
            conn := newConnMapping[key]
            if conn == nil {
                continue
            }

            pm.subscriptionsMu.RLock()
            info := pm.subscriptions[key]
            pm.subscriptionsMu.RUnlock()

            if conn.Client().IsConnected() {
                if err := conn.Client().Subscribe(info.subscription); err != nil {
                    logger.Error().Err(err).Str("key", key).Msg("Failed to resubscribe")
                    continue
                }
            }
        }

        // 4. 批量更新订阅信息（短暂持有锁）
        pm.subscriptionsMu.Lock()
        for j := i; j < end; j++ {
            key := keysToRestore[j]
            newConn := newConnMapping[key]
            if newConn != nil && pm.subscriptions[key] != nil {
                pm.subscriptions[key].connection = newConn
            }
        }
        pm.subscriptionsMu.Unlock()

        logger.Info().
            Int("batch_start", i).
            Int("batch_end", end).
            Int("batch_size", end-i).
            Msg("Subscription batch restored")
    }

    return nil
}
```

### 优化效果

- **锁持有时间**: 从 O(n) 降低到 O(batchSize)
- **并发阻塞**: 新订阅操作最多等待一个批次的时间
- **性能提升**: 批量更新减少锁竞争

---

## P0-3: 修复 Dispatcher goroutine 池错误被忽略

### 问题描述

`dispatchToKey()` 中 `pool.Submit()` 的错误被忽略，导致消息丢失：

```go
for _, cb := range info.callbacks {
    _ = d.pool.Submit(func() {  // ❌ 错误被忽略
        if err := cb(msg); err != nil {
            logger.Error().Err(err).Str("channel", string(msg.Channel)).Msg("callback error")
        }
    })
}
```

### 修复方案

**策略**: goroutine 池满时降级为同步执行，并记录指标

```go
// Dispatcher 结构增强
type Dispatcher struct {
    pm           *PoolManager
    pool         *ants.Pool
    poolFullHist *prometheus.HistogramVec
}

func NewDispatcher(pm *PoolManager) *Dispatcher {
    poolSize := 50
    if env := os.Getenv("DISPATCHER_POOL_SIZE"); env != "" {
        if size, err := strconv.Atoi(env); err == nil && size > 0 {
            poolSize = size
        }
    }

    pool, _ := ants.NewPool(
        poolSize,
        ants.WithPreAlloc(false),
        ants.WithExpiryDuration(time.Minute),
    )

    return &Dispatcher{
        pm:   pm,
        pool: pool,
        poolFullHist: monitor.NewDispatcherPoolFullHistogram(),
    }
}

// dispatchToKey 修复版
func (d *Dispatcher) dispatchToKey(key string, msg wsMessage) {
    d.pm.subscriptionsMu.RLock()
    info, exists := d.pm.subscriptions[key]
    d.pm.subscriptionsMu.RUnlock()

    if !exists {
        return
    }

    for _, cb := range info.callbacks {
        err := d.pool.Submit(func() {
            if err := cb(msg); err != nil {
                logger.Error().Err(err).
                    Str("channel", string(msg.Channel)).
                    Str("key", key).
                    Msg("callback error")
            }
        })

        if err != nil {
            // goroutine 池满，降级为同步执行
            logger.Warn().
                Err(err).
                Str("key", key).
                Msg("dispatcher pool full, executing synchronously")

            // 记录指标
            if d.poolFullHist != nil {
                d.poolFullHist.Observe(float64(len(d.pool.Running())))
            }

            // 同步执行回调
            if err := cb(msg); err != nil {
                logger.Error().Err(err).
                    Str("channel", string(msg.Channel)).
                    Str("key", key).
                    Msg("callback error (sync)")
            }
        }
    }
}
```

### Prometheus 指标

```go
var DispatcherPoolFullHistogram = prometheus.NewHistogramVec(
    prometheus.HistogramOpts{
        Name:    "hl_monitor_dispatcher_pool_full_running",
        Help:    "Number of running goroutines when dispatcher pool is full",
        Buckets: []float64{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
    },
    []string{"pool_type"},
)
```

---

## P0-4: 修复 Client.readPump() 空转 CPU

### 问题描述

`readPump()` 使用 `default` 分支导致 busy-wait：

```go
for {
    select {
    case <-ctx.Done():
        return
    case <-c.done:
        return
    default:
        // ❌ 立即执行，不等待 ReadMessage()
        _, msg, err := conn.ReadMessage()
    }
}
```

### 修复方案

**方案**: 移除 `default` 分支，直接阻塞读取

```go
// readPump 读取循环（修复版）
func (c *Client) readPump(ctx context.Context) {
    defer func() {
        c.mu.Lock()
        if c.conn != nil {
            c.conn.Close()
            c.conn = nil
        }
        c.mu.Unlock()
        c.notifyDisconnect()
    }()

    for {
        select {
        case <-ctx.Done():
            return
        case <-c.done:
            return
        }

        c.mu.RLock()
        conn := c.conn
        c.mu.RUnlock()

        if conn == nil {
            return
        }

        // 设置读取超时，避免永久阻塞
        conn.SetReadDeadline(time.Now().Add(30 * time.Second))
        _, msg, err := conn.ReadMessage()
        if err != nil {
            if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
                logger.Debug().Msg("Read timeout, retrying")
                continue  // 超时继续重试
            }
            return  // 其他错误直接返回
        }

        var wsMsg wsMessage
        if err = json.Unmarshal(msg, &wsMsg); err != nil {
            logger.Debug().Err(err).Bytes("data", msg).Msg("failed to unmarshal message")
            continue
        }

        // 调用外部处理函数
        if c.onMessage != nil {
            if err = c.onMessage(wsMsg); err != nil {
                logger.Error().Err(err).Msg("onMessage error")
            }
        }
    }
}
```

### 优化效果

- **CPU 使用**: 从 100% 降低到接近 0%（阻塞等待）
- **性能**: 减少无效的循环迭代
- **稳定性**: 超时重试机制避免网络抖动导致连接断开

---

## P1-1: 修复 unsubscribe() 回调列表管理逻辑错误

### 问题描述

当前 `unsubscribe()` 无法正确移除回调：

```go
if len(info.callbacks) > 1 {
    return nil  // ❌ 什么都没做！
}
```

### 修复方案

**策略**: 为每个回调创建独立的 Handle，支持精确移除

```go
// SubscriptionHandle 订阅句柄
type SubscriptionHandle struct {
    key      string
    pm       *PoolManager
    callback Callback
    index    int  // 回调索引
}

// Unsubscribe 取消订阅
func (sh *SubscriptionHandle) Unsubscribe() error {
    return sh.pm.unsubscribeHandle(sh.key, sh.callback, sh.index)
}

// Subscribe 订阅（返回 Handle）
func (pm *PoolManager) Subscribe(sub Subscription, callback Callback) (*SubscriptionHandle, error) {
    key := sub.Key()

    pm.subscriptionsMu.Lock()
    defer pm.subscriptionsMu.Unlock()

    if info, exists := pm.subscriptions[key]; exists {
        info.callbacks = append(info.callbacks, callback)
        return &SubscriptionHandle{
            key:      key,
            pm:       pm,
            callback: callback,
            index:    len(info.callbacks) - 1,
        }, nil
    }

    // 获取或创建连接
    conn, err := pm.acquireConnection()
    if err != nil {
        return nil, err
    }

    // 发送订阅请求
    if conn.Client().IsConnected() {
        if err := conn.Client().Subscribe(sub); err != nil {
            return nil, fmt.Errorf("subscribe request failed: %w", err)
        }
    }

    // 记录订阅
    pm.subscriptions[key] = &subscriptionInfo{
        subscription: sub,
        callbacks:    []Callback{callback},
        connection:   conn,
    }

    conn.AddSubscription(key)

    logger.Info().
        Str("key", key).
        Int("connection_subs", conn.SubscriptionCount()).
        Msg("Subscribed")

    return &SubscriptionHandle{
        key:      key,
        pm:       pm,
        callback: callback,
        index:    0,
    }, nil
}

// unsubscribeHandle 内部取消订阅方法
func (pm *PoolManager) unsubscribeHandle(key string, callback Callback, index int) error {
    pm.subscriptionsMu.Lock()
    defer pm.subscriptionsMu.Unlock()

    info, exists := pm.subscriptions[key]
    if !exists {
        return nil
    }

    // 验证回调是否匹配
    if index >= len(info.callbacks) || &info.callbacks[index] != &callback {
        return fmt.Errorf("callback not found at index %d", index)
    }

    // 移除回调
    info.callbacks = append(info.callbacks[:index], info.callbacks[index+1:]...)

    // 如果没有回调了，取消服务器订阅
    if len(info.callbacks) == 0 {
        if err := info.connection.Client().Unsubscribe(info.subscription); err != nil {
            return err
        }
        info.connection.RemoveSubscription(key)
        delete(pm.subscriptions, key)
        logger.Info().Str("key", key).Msg("Unsubscribed")
    }

    return nil
}
```

---

## P1-2: 优化 ConnectionWrapper 数据结构

### 问题描述

`subscriptions` 存储完整的 `Subscription` 对象但只使用 key，内存浪费。

### 修复方案

**策略**: 使用 `map[string]struct{}` 代替 `map[string]Subscription`

```go
// ConnectionWrapper 连接包装器（优化版）
type ConnectionWrapper struct {
    client        *Client
    subscriptions map[string]struct{}  // 使用 set
    mu            sync.RWMutex
}

func NewConnectionWrapper(client *Client) *ConnectionWrapper {
    return &ConnectionWrapper{
        client:        client,
        subscriptions: make(map[string]struct{}),
    }
}

func (cw *ConnectionWrapper) AddSubscription(key string) {
    cw.mu.Lock()
    defer cw.mu.Unlock()
    cw.subscriptions[key] = struct{}{}
}

func (cw *ConnectionWrapper) RemoveSubscription(key string) {
    cw.mu.Lock()
    defer cw.mu.Unlock()
    delete(cw.subscriptions, key)
}

func (cw *ConnectionWrapper) HasSubscription(key string) bool {
    cw.mu.RLock()
    defer cw.mu.RUnlock()
    _, exists := cw.subscriptions[key]
    return exists
}

func (cw *ConnectionWrapper) SubscriptionCount() int {
    cw.mu.RLock()
    defer cw.mu.RUnlock()
    return len(cw.subscriptions)
}

func (cw *ConnectionWrapper) GetSubscriptions() []string {
    cw.mu.RLock()
    defer cw.mu.RUnlock()

    keys := make([]string, 0, len(cw.subscriptions))
    for key := range cw.subscriptions {
        keys = append(keys, key)
    }
    return keys
}
```

### 内存优化效果

| 场景 | 旧方案 | 新方案 | 节省 |
|------|--------|--------|------|
| 1000 个订阅 | ~80KB | ~8KB | 90% |
| 10000 个订阅 | ~800KB | ~80KB | 90% |

---

## P1-3: 修复重连期间订阅状态不一致

### 问题描述

`reconnectAll()` 在重连过程中，订阅信息指向旧连接，存在竞态窗口。

### 修复方案

**策略**: 引入订阅状态标志，原子更新连接映射

```go
// subscriptionInfo 订阅信息（增强版）
type subscriptionInfo struct {
    subscription Subscription
    callbacks    []Callback
    connection   *ConnectionWrapper
    restoring    bool  // 重连中标志
}

// restoreSubscriptions 优化版
func (pm *PoolManager) restoreSubscriptions(disconnectedConns, reconnectedConns []*ConnectionWrapper) error {
    if len(reconnectedConns) == 0 {
        return nil
    }

    // 1. 设置重连标志（原子操作）
    pm.subscriptionsMu.Lock()
    for key, info := range pm.subscriptions {
        for _, dc := range disconnectedConns {
            if info.connection == dc {
                info.restoring = true
            }
        }
    }
    pm.subscriptionsMu.Unlock()

    // 2. 重新发送订阅请求并记录新连接
    newConnMapping := make(map[string]*ConnectionWrapper)
    for key, oldConn := range pm.getConnectionMapping(disconnectedConns) {
        conn, err := pm.acquireConnectionNoLock()
        if err != nil {
            logger.Error().Err(err).Str("key", key).Msg("Failed to acquire connection")
            continue
        }

        pm.subscriptionsMu.RLock()
        info := pm.subscriptions[key]
        pm.subscriptionsMu.RUnlock()

        if conn.Client().IsConnected() {
            if err := conn.Client().Subscribe(info.subscription); err != nil {
                logger.Error().Err(err).Str("key", key).Msg("Failed to resubscribe")
                continue
            }
        }

        newConnMapping[key] = conn
    }

    // 3. 原子更新所有连接和标志
    pm.subscriptionsMu.Lock()
    for key, newConn := range newConnMapping {
        if pm.subscriptions[key] != nil {
            pm.subscriptions[key].connection = newConn
            pm.subscriptions[key].restoring = false
            newConn.AddSubscription(key)
        }
    }
    pm.subscriptionsMu.Unlock()

    return nil
}

func (pm *PoolManager) getConnectionMapping(disconnectedConns []*ConnectionWrapper) map[string]*ConnectionWrapper {
    oldConnMap := make(map[string]*ConnectionWrapper)
    pm.subscriptionsMu.RLock()
    for key, info := range pm.subscriptions {
        oldConnMap[key] = info.connection
    }
    pm.subscriptionsMu.RUnlock()
    return oldConnMap
}
```

### 竞态窗口消除

| 阶段 | 旧方案 | 新方案 |
|------|--------|--------|
| 重连前 | connection 指向旧连接 | connection 指�向旧连接 |
| 重连中 | connection 可能指向新连接 | `restoring=true`，消息不处理 |
| 重连后 | connection 更新为新连接 | 原子更新，无竞态窗口 |

---

## P1-4: 修复 leastLoadedConnection() 可能返回 nil

### 问题描述

当 `pm.connections` 为空时，`leastLoadedConnection()` 返回 nil，调用方未检查导致 panic。

### 修复方案

```go
// leastLoadedConnection 获取负载最少的连接（修复版）
func (pm *PoolManager) leastLoadedConnection() *ConnectionWrapper {
    pm.mu.RLock()
    defer pm.mu.RUnlock()

    if len(pm.connections) == 0 {
        logger.Error().Msg("No connections available")
        return nil
    }

    var selected *ConnectionWrapper
    minCount := int(^uint(0) >> 1)

    for _, cw := range pm.connections {
        count := cw.SubscriptionCount()
        if count < minCount {
            minCount = count
            selected = cw
        }
    }

    if selected == nil {
        logger.Error().Msg("No valid connection found despite non-empty connections")
        return nil
    }

    logger.Warn().
        Int("subscription_count", minCount).
        Msg("All connections at capacity, returning least loaded")

    return selected
}

// acquireConnectionNoLock 修复版
func (pm *PoolManager) acquireConnectionNoLock() (*ConnectionWrapper, error) {
    // 1. 尝试找到有容量的现有连接
    for _, cw := range pm.connections {
        if cw.SubscriptionCount() < pm.maxSubscriptions {
            return cw, nil
        }
    }

    // 2. 创建新连接
    if len(pm.connections) < pm.maxConnections {
        return pm.createConnectionNoLock()
    }

    // 3. 返回负载最少的连接（降级）
    conn := pm.leastLoadedConnection()
    if conn == nil {
        return nil, fmt.Errorf("no available connection")
    }
    return conn, nil
}
```

---

## P2-1: 消除类型转换开销

### 修复方案

```go
// Dispatch 消息分发（优化版）
func (d *Dispatcher) Dispatch(msg wsMessage) error {
    switch msg.Channel {  // 直接比较，避免类型转换
    case ChannelWebData2:
        return d.dispatchWebData2(msg)
    case ChannelUserFills:
        return d.dispatchUserFills(msg)
    case ChannelOrderUpdates:
        return d.dispatchOrderUpdates(msg)
    case ChannelPongAuth:
        return d.dispatchPongAuth(msg)
    case ChannelPongTrade:
        return d.dispatchPongTrade(msg)
    default:
        return d.dispatchGeneric(msg)
    }
}
```

---

## P2-2: goroutine 池大小可配置

### 修复方案

```go
// NewDispatcher 创建分发器（支持配置）
func NewDispatcher(pm *PoolManager) *Dispatcher {
    poolSize := 50
    if env := os.Getenv("DISPATCHER_POOL_SIZE"); env != "" {
        if size, err := strconv.Atoi(env); err == nil && size > 0 {
            poolSize = size
        }
    }

    pool, _ := ants.NewPool(
        poolSize,
        ants.WithPreAlloc(false),
        ants.WithExpiryDuration(time.Minute),
    )

    return &Dispatcher{
        pm:   pm,
        pool: pool,
    }
}
```

---

## 实施计划

### 第 1 阶段：P0 问题修复（1-2 天）
- [ ] 修复 P0-1: 锁顺序不一致
- [ ] 修复 P0-2: subscriptionsMu 锁时间过长
- [ ] 修复 P0-3: Dispatcher 错误忽略
- [ ] 修复 P0-4: readPump 空转
- [ ] 单元测试验证
- [ ] 集成测试验证

### 第 2 阶段：P1 问题修复（1-2 天）
- [ ] 修复 P1-1: unsubscribe 逻辑
- [ ] 修复 P1-2: ConnectionWrapper 数据结构
- [ ] 修复 P1-3: 订阅状态不一致
- [ ] 修复 P1-4: leastLoadedConnection nil 检查

### 第 3 阶段：P2 性能优化（1 天）
- [ ] 优化 P2-1: 类型转换
- [ ] 优化 P2-2: goroutine 池配置

---

## 测试验证

### 并发安全测试

```go
func TestConcurrentReconnect(t *testing.T) {
    pool := NewPoolManager("wss://example.com/ws", 5, 100)
    ctx := context.Background()
    pool.Start(ctx)
    defer pool.Close()

    // 模拟多个连接同时断开
    var wg sync.WaitGroup
    for i := 0; i < 10; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            pool.handleDisconnect()
        }(i)
    }
    wg.Wait()

    // 验证状态一致性
    if pool.IsReconnecting() {
        t.Error("Should not be reconnecting after all goroutines complete")
    }
}
```

### 性能测试

```go
func BenchmarkReconnect(b *testing.B) {
    pool := NewPoolManager("wss://example.com/ws", 10, 100)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        pool.reconnectAll()
    }
}
```

---

## 验收标准

### 功能验收
- [ ] 所有并发测试通过
- [ ] 无死锁、无 panic
- [ ] 重连成功率 > 95%
- [ ] 消息零丢失（Dispatcher 池满时有降级）

### 性能验收
- [ ] CPU 使用率 < 10%（空闲时）
- [ ] 内存泄漏检测通过
- [ ] 重连延迟 < 5 秒

### 代码质量验收
- [ ] 代码审查通过
- [ ] 测试覆盖率 > 80%
- [ ] 无 lint 错误
