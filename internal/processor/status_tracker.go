package processor

import (
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"
)

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

// statusTracker 状态追踪器实现
type statusTracker struct {
	cache *cache.Cache // key: "address-oid", value: status
}

// NewOrderStatusTracker 创建状态追踪器
// ttl: 记录过期时间，建议 10 分钟
func NewOrderStatusTracker(ttl time.Duration) OrderStatusTracker {
	return &statusTracker{
		cache: cache.New(ttl, 1*time.Minute), // 1分钟清理一次过期项
	}
}

// MarkStatus 记录订单状态
func (t *statusTracker) MarkStatus(address string, oid int64, status string) {
	key := fmt.Sprintf("%s-%d", address, oid)
	t.cache.Set(key, status, cache.DefaultExpiration)
}

// GetStatus 获取订单状态
func (t *statusTracker) GetStatus(address string, oid int64) (string, bool) {
	key := fmt.Sprintf("%s-%d", address, oid)
	if val, found := t.cache.Get(key); found {
		return val.(string), true
	}
	return "", false
}

// Remove 移除记录
func (t *statusTracker) Remove(address string, oid int64) {
	key := fmt.Sprintf("%s-%d", address, oid)
	t.cache.Delete(key)
}

// Clear 清空所有记录
func (t *statusTracker) Clear() {
	t.cache.Flush()
}
