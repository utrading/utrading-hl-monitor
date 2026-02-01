package ws

import (
	"sync"
)

// ConnectionWrapper 连接包装器
type ConnectionWrapper struct {
	client        *Client
	subscriptions map[string]Subscription
	mu            sync.RWMutex
}

// NewConnectionWrapper 创建连接包装器
func NewConnectionWrapper(client *Client) *ConnectionWrapper {
	return &ConnectionWrapper{
		client:        client,
		subscriptions: make(map[string]Subscription),
	}
}

// Client 获取底层客户端
func (cw *ConnectionWrapper) Client() *Client {
	return cw.client
}

// SubscriptionCount 获取订阅数量
func (cw *ConnectionWrapper) SubscriptionCount() int {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	return len(cw.subscriptions)
}

// HasSubscription 检查是否有指定订阅
func (cw *ConnectionWrapper) HasSubscription(key string) bool {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	_, exists := cw.subscriptions[key]
	return exists
}

// GetSubscription 获取单个订阅详情 (新增方法)
func (cw *ConnectionWrapper) GetSubscription(key string) (Subscription, bool) {
	cw.mu.RLock()
	defer cw.mu.RUnlock()
	sub, exists := cw.subscriptions[key]
	return sub, exists
}

// AddSubscription 添加订阅（支持两种调用方式）
func (cw *ConnectionWrapper) AddSubscription(key string, sub ...Subscription) {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if len(sub) > 0 {
		// 新方式：传入订阅对象
		cw.subscriptions[key] = sub[0]
	} else {
		// 旧方式：仅传入 key（用于测试），创建空订阅
		cw.subscriptions[key] = Subscription{}
	}
}

// RemoveSubscription 移除订阅
func (cw *ConnectionWrapper) RemoveSubscription(key string) {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	delete(cw.subscriptions, key)
}

// GetSubscriptionKeys 获取所有订阅的 key (重命名以更准确)
func (cw *ConnectionWrapper) GetSubscriptionKeys() []string {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	// 预分配容量，提升性能
	keys := make([]string, 0, len(cw.subscriptions))
	for key := range cw.subscriptions {
		keys = append(keys, key)
	}
	return keys
}

// GetAllSubscriptions 获取所有订阅的副本 (新增方法，用于连接迁移)
// 在重连或负载均衡时，可以直接拿到这个 map 进行批量操作，而不需要回 PoolManager 查找
func (cw *ConnectionWrapper) GetAllSubscriptions() map[string]Subscription {
	cw.mu.RLock()
	defer cw.mu.RUnlock()

	// 必须返回副本，防止外部修改影响内部状态，或并发读写 panic
	copyMap := make(map[string]Subscription, len(cw.subscriptions))
	for k, v := range cw.subscriptions {
		copyMap[k] = v
	}
	return copyMap
}
