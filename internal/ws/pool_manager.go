package ws

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// subscriptionInfo 订阅信息
type subscriptionInfo struct {
	subscription Subscription
	callbacks    map[int64]Callback
	connection   *ConnectionWrapper
}

// PoolManager 连接池管理器
type PoolManager struct {
	url              string
	connections      []*ConnectionWrapper
	mu               sync.RWMutex // 保护 connections 切片
	maxConnections   int
	maxSubscriptions int
	subscriptions    map[string]*subscriptionInfo
	subscriptionsMu  sync.RWMutex // 保护 subscriptions map
	dispatcher       *Dispatcher
	started          atomic.Bool // 使用原子操作替代 bool + 锁
	// 用于生成回调 ID
	callbackIDSeq int64

	reconnectMu      sync.Mutex    // 保证同一时间只有一个重连过程在跑
	reconnectBackoff time.Duration // 当前退避时间
}

// SubscriptionHandle 订阅句柄
type SubscriptionHandle struct {
	id  int64  // 唯一回调 ID
	key string // 订阅 Key
	pm  *PoolManager
}

func NewPoolManager(url string, maxConns, maxSubs int) *PoolManager {
	pm := &PoolManager{
		url:              url,
		connections:      make([]*ConnectionWrapper, 0, maxConns),
		maxConnections:   maxConns,
		maxSubscriptions: maxSubs,
		subscriptions:    make(map[string]*subscriptionInfo),
	}
	pm.dispatcher = NewDispatcher(pm, 100)
	return pm
}

func (pm *PoolManager) Start(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.started.Load() {
		return nil
	}

	// 创建初始连接
	wrapper, err := pm.createConnectionLocked(ctx)
	if err != nil {
		return err
	}
	pm.connections = append(pm.connections, wrapper)

	pm.started.Store(true)
	logger.Info().Msg("PoolManager started")
	return nil
}

func (pm *PoolManager) Close() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.dispatcher != nil {
		pm.dispatcher.Close()
	}

	for _, cw := range pm.connections {
		cw.Client().Close()
	}
	pm.connections = nil
	pm.started.Store(false)
	return nil
}

// MaxConnections 获取最大连接数
func (pm *PoolManager) MaxConnections() int {
	return pm.maxConnections
}

// MaxSubscriptions 获取每连接最大订阅数
func (pm *PoolManager) MaxSubscriptions() int {
	return pm.maxSubscriptions
}

// ConnectionCount 获取当前连接数
func (pm *PoolManager) ConnectionCount() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.connections)
}

// IsConnected 检查是否有活动连接
func (pm *PoolManager) IsConnected() bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, cw := range pm.connections {
		if cw.Client().IsConnected() {
			return true
		}
	}
	return false
}

// IsReconnecting 检查是否正在重连
func (pm *PoolManager) IsReconnecting() bool {
	// 简化实现：检查 reconnectMu 是否被持有
	// 这里可以返回 false，因为重连是内部管理的
	return false
}

// GetStats 获取连接池统计信息
func (pm *PoolManager) GetStats() map[string]any {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	subCount := 0
	for _, cw := range pm.connections {
		subCount += cw.SubscriptionCount()
	}

	return map[string]any{
		"connection_count":   len(pm.connections),
		"subscription_count": subCount,
		"started":            pm.started.Load(),
	}
}

// SubscriptionCount 获取订阅总数
func (pm *PoolManager) SubscriptionCount() int {
	pm.subscriptionsMu.RLock()
	defer pm.subscriptionsMu.RUnlock()
	return len(pm.subscriptions)
}

// Subscribe 订阅
func (pm *PoolManager) Subscribe(sub Subscription, callback Callback) (*SubscriptionHandle, error) {
	key := sub.Key()
	handleID := atomic.AddInt64(&pm.callbackIDSeq, 1)

	// 1. 快速路径：已有订阅
	pm.subscriptionsMu.Lock()
	if info, exists := pm.subscriptions[key]; exists {
		info.callbacks[handleID] = callback
		pm.subscriptionsMu.Unlock()
		return &SubscriptionHandle{id: handleID, key: key, pm: pm}, nil
	}
	pm.subscriptionsMu.Unlock()

	// 2. 获取连接（锁外）
	conn, err := pm.acquireConnection()
	if err != nil {
		return nil, err
	}

	// 3. 创建 Pending 订阅（原子）
	pm.subscriptionsMu.Lock()
	if info, exists := pm.subscriptions[key]; exists {
		info.callbacks[handleID] = callback
		pm.subscriptionsMu.Unlock()
		return &SubscriptionHandle{id: handleID, key: key, pm: pm}, nil
	}

	info := &subscriptionInfo{
		subscription: sub,
		callbacks:    map[int64]Callback{handleID: callback},
		connection:   conn,
	}
	pm.subscriptions[key] = info
	pm.subscriptionsMu.Unlock()

	// 4. 锁外执行网络 Subscribe
	if conn.Client().IsConnected() {
		if err = conn.Client().Subscribe(sub); err != nil {
			// 回滚
			pm.subscriptionsMu.Lock()
			delete(pm.subscriptions, key)
			pm.subscriptionsMu.Unlock()
			return nil, err
		}
	}

	conn.AddSubscription(key, sub)

	return &SubscriptionHandle{id: handleID, key: key, pm: pm}, nil
}

// Unsubscribe 句柄取消
func (sh *SubscriptionHandle) Unsubscribe() error {
	return sh.pm.unsubscribe(sh.key, sh.id)
}

// unsubscribe 内部取消逻辑
func (pm *PoolManager) unsubscribe(key string, handleID int64) error {
	var (
		conn *ConnectionWrapper
		sub  Subscription
	)

	pm.subscriptionsMu.Lock()
	info, exists := pm.subscriptions[key]
	if !exists {
		pm.subscriptionsMu.Unlock()
		return nil
	}

	delete(info.callbacks, handleID)

	// 标记状态
	conn = info.connection
	sub = info.subscription
	delete(pm.subscriptions, key)
	pm.subscriptionsMu.Unlock()

	// 锁外执行网络 IO
	if conn != nil && conn.Client().IsConnected() {
		_ = conn.Client().Unsubscribe(sub)
		conn.RemoveSubscription(key)
	}

	logger.Info().Str("key", key).Msg("Unsubscribed completely")
	return nil
}

func (pm *PoolManager) acquireConnection() (*ConnectionWrapper, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 1. 查找现有未满的连接
	for _, cw := range pm.connections {
		if cw.Client().IsConnected() && cw.SubscriptionCount() < pm.maxSubscriptions {
			return cw, nil
		}
	}

	// 2. 创建新连接
	if len(pm.connections) < pm.maxConnections {
		return pm.createConnectionLocked(context.Background())
	}

	// 3. 降级：返回负载最小的（即使已满，或者返回错误由调用方决定，这里保持原逻辑）
	return pm.leastLoadedConnection(), nil
}

// createConnectionLocked 必须在持有 mu 时调用
func (pm *PoolManager) createConnectionLocked(ctx context.Context) (*ConnectionWrapper, error) {
	client := NewClient(pm.url)
	client.SetMessageHandler(pm.dispatcher.Dispatch)

	// 设置断开回调
	// 注意：回调在一个单独的 goroutine 中执行
	client.SetDisconnectCallback(func() {
		logger.Warn().Msg("Connection disconnected, triggering handler")
		go pm.handleDisconnect()
	})

	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}

	wrapper := NewConnectionWrapper(client)
	// 这里不 append，由调用者 append，因为 reconnect 时行为不同
	return wrapper, nil
}

func (pm *PoolManager) leastLoadedConnection() *ConnectionWrapper {
	// 假设 connections 不为空
	if len(pm.connections) == 0 {
		return nil
	}

	var selected *ConnectionWrapper
	minCount := int(^uint(0) >> 1)

	for _, cw := range pm.connections {
		// 优先选择活跃连接
		if !cw.Client().IsConnected() {
			continue
		}
		count := cw.SubscriptionCount()
		if count < minCount {
			minCount = count
			selected = cw
		}
	}

	// 如果所有连接都断了，返回第一个
	if selected == nil && len(pm.connections) > 0 {
		return pm.connections[0]
	}
	return selected
}

func (pm *PoolManager) handleDisconnect() {
	// 使用 TryLock 防止并发重连风暴
	if !pm.reconnectMu.TryLock() {
		return
	}
	defer pm.reconnectMu.Unlock()

	// 指数退避重连算法
	const (
		initialBackoff = 1 * time.Second
		maxBackoff     = 300 * time.Second
	)

	// 初始化或使用当前退避时间
	if pm.reconnectBackoff == 0 {
		pm.reconnectBackoff = initialBackoff
	}

	for {
		// 加入随机扰动（抖动），避免惊群效应
		// 范围：[0.5 * backoff, 1.5 * backoff]
		jitter := time.Duration(float64(pm.reconnectBackoff) * (0.5 + rand.Float64()))
		logger.Warn().Dur("backoff", jitter).Msg("Reconnecting with exponential backoff")

		time.Sleep(jitter)
		if err := pm.repairConnections(); err == nil {
			break
		}

		pm.reconnectBackoff *= 2
		if pm.reconnectBackoff > maxBackoff {
			pm.reconnectBackoff = maxBackoff
		}
	}

	// 重连成功，重置退避时间
	pm.reconnectBackoff = initialBackoff
	logger.Info().Msg("Reconnected successfully, backoff reset")
}

// repairConnections 修复断开的连接
func (pm *PoolManager) repairConnections() error {
	pm.mu.Lock()
	// 复制一份快照，避免遍历时长时间持有锁
	connsSnapshot := make([]*ConnectionWrapper, len(pm.connections))
	copy(connsSnapshot, pm.connections)
	pm.mu.Unlock()

	for i, cw := range connsSnapshot {
		if cw.Client().IsConnected() {
			continue
		}

		logger.Warn().Int("index", i).Msg("Repairing connection")

		// 1. 关闭旧连接资源
		cw.Client().Close()

		// 2. 创建新连接
		// 注意：这里我们不调用 createConnectionLocked，因为我们是在替换特定位置
		// 且不希望 append 到切片尾部
		newClient := NewClient(pm.url)
		newClient.SetMessageHandler(pm.dispatcher.Dispatch)
		newClient.SetDisconnectCallback(func() {
			go pm.handleDisconnect()
		})

		if err := newClient.Connect(context.Background()); err != nil {
			logger.Error().Err(err).Int("index", i).Msg("Failed to reconnect specific client")
			return err
		}

		newWrapper := NewConnectionWrapper(newClient)

		// 3. 更新连接池切片 (需要加锁)
		pm.mu.Lock()
		// 再次检查越界（虽然极少发生）
		if i < len(pm.connections) {
			pm.connections[i] = newWrapper
		}
		pm.mu.Unlock()

		// 4. 迁移并恢复订阅
		// 这一步比较耗时，放在锁外
		pm.migrateAndResubscribe(cw, newWrapper)
	}

	return nil
}

// migrateAndResubscribe 将旧连接的订阅迁移到新连接
func (pm *PoolManager) migrateAndResubscribe(oldConn, newConn *ConnectionWrapper) {
	// 获取旧连接负责的所有 Key
	// 注意：GetSubscriptions 应该是 ConnectionWrapper 的线程安全方法
	subKeys := oldConn.GetSubscriptionKeys()

	if len(subKeys) == 0 {
		return
	}

	logger.Info().Int("count", len(subKeys)).Msg("Migrating subscriptions to new connection")

	for _, key := range subKeys {
		pm.subscriptionsMu.Lock()
		info, exists := pm.subscriptions[key]

		// 如果订阅已经不存在，或者已经迁移走了，则跳过
		if !exists || info.connection != oldConn {
			pm.subscriptionsMu.Unlock()
			continue
		}

		// 更新指向新连接
		info.connection = newConn
		pm.subscriptionsMu.Unlock()

		// 在新连接上添加记录
		newConn.AddSubscription(key, info.subscription)

		// 发送订阅指令
		// 此时 info.subscription 依然有效
		if err := newConn.Client().Subscribe(info.subscription); err != nil {
			logger.Error().Err(err).Str("key", key).Msg("Resubscribe failed during migration")
			// 可以在这里做重试逻辑
		}
	}
}
