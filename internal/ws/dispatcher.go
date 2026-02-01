package ws

import (
	"github.com/panjf2000/ants/v2"
	"github.com/tidwall/gjson"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// Dispatcher 消息分发器
type Dispatcher struct {
	pm   *PoolManager
	pool *ants.Pool
}

// NewDispatcher 创建分发器
func NewDispatcher(pm *PoolManager, poolSize int) *Dispatcher {
	if poolSize <= 0 {
		poolSize = 1000 // 默认值调大
	}
	pool, _ := ants.NewPool(poolSize)
	return &Dispatcher{
		pm:   pm,
		pool: pool,
	}
}

// Dispatch 处理收到的消息
func (d *Dispatcher) Dispatch(msg wsMessage) error {
	channel := string(msg.Channel)

	// 根据不同频道类型处理
	switch channel {
	case string(ChannelWebData2):
		d.dispatchWebData2(msg)
	case string(ChannelUserFills):
		d.dispatchUserFills(msg)
	case string(ChannelOrderUpdates):
		d.broadcastToChannel(ChannelOrderUpdates, msg)
	default:
		d.dispatchGeneric(msg)
	}

	return nil
}

// dispatchWebData2
func (d *Dispatcher) dispatchWebData2(msg wsMessage) {
	// 假设 msg.Data 是 []byte
	jsonStr := string(msg.Data)
	user := gjson.Get(jsonStr, "user").String()

	if user == "" {
		// 无法解析 User，广播到所有 WebData2 订阅
		d.broadcastToChannel(ChannelWebData2, msg)
		return
	}

	key := string(ChannelWebData2) + ":" + user
	d.dispatchToKey(key, msg)
}

// dispatchUserFills
func (d *Dispatcher) dispatchUserFills(msg wsMessage) {
	jsonStr := string(msg.Data)
	user := gjson.Get(jsonStr, "user").String()

	if user == "" {
		d.broadcastToChannel(ChannelUserFills, msg)
		return
	}

	key := string(ChannelUserFills) + ":" + user
	d.dispatchToKey(key, msg)
}

// dispatchGeneric 通用频道分发
func (d *Dispatcher) dispatchGeneric(msg wsMessage) {
	key := string(msg.Channel)
	d.dispatchToKey(key, msg)
}

// dispatchToKey 分发到指定键的订阅 (核心优化：缩小锁粒度)
func (d *Dispatcher) dispatchToKey(key string, msg wsMessage) {
	// 1. 快速获取回调列表（持有锁时间极短）
	callbacks := d.getCallbacksByKey(key)
	if len(callbacks) == 0 {
		return
	}

	// 2. 在锁外执行分发
	d.executeCallbacks(callbacks, msg, key)
}

// broadcastToChannel 广播 (优化：缩小锁粒度)
func (d *Dispatcher) broadcastToChannel(channel Channel, msg wsMessage) {
	prefix := string(channel) + ":"

	// 1. 收集所有需要执行的回调
	var allCallbacks []Callback

	d.pm.subscriptionsMu.RLock()
	for key, info := range d.pm.subscriptions {
		// 前缀匹配
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			for _, cb := range info.callbacks {
				allCallbacks = append(allCallbacks, cb)
			}
		}
	}
	d.pm.subscriptionsMu.RUnlock()

	if len(allCallbacks) == 0 {
		return
	}

	// 2. 锁外执行
	d.executeCallbacks(allCallbacks, msg, string(channel)+"(broadcast)")
}

// getCallbacksByKey 辅助方法：安全获取回调副本
func (d *Dispatcher) getCallbacksByKey(key string) []Callback {
	d.pm.subscriptionsMu.RLock()
	defer d.pm.subscriptionsMu.RUnlock()

	info, exists := d.pm.subscriptions[key]
	if !exists || len(info.callbacks) == 0 {
		return nil
	}

	// 复制切片，避免外部执行时并发读写 map
	cbs := make([]Callback, 0, len(info.callbacks))
	for _, cb := range info.callbacks {
		cbs = append(cbs, cb)
	}
	return cbs
}

// executeCallbacks 统一执行逻辑
func (d *Dispatcher) executeCallbacks(cbs []Callback, msg wsMessage, logKey string) {
	for _, cb := range cbs {
		// 显式捕获变量
		callback := cb

		err := d.pool.Submit(func() {
			if err := callback(msg); err != nil {
				logger.Error().Err(err).
					Str("channel", string(msg.Channel)).
					Str("key", logKey).
					Msg("callback error")
			}
		})

		if err != nil {
			// ants.ErrPoolOverload
			// 降级：同步执行
			// 注意：此时我们没有持有任何锁，同步执行是安全的，只会阻塞当前的 readPump，不会死锁
			logger.Warn().
				Err(err).
				Str("key", logKey).
				Msg("dispatcher pool full, executing synchronously")

			if err = callback(msg); err != nil {
				logger.Error().Err(err).Str("key", logKey).Msg("callback error (sync)")
			}
		}
	}
}

// Close 关闭分发器
func (d *Dispatcher) Close() {
	if d.pool != nil {
		d.pool.Release()
	}
}
