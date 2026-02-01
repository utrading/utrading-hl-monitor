package manager

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	hl "github.com/sonirico/go-hyperliquid"
	"github.com/spf13/cast"
	"github.com/utrading/utrading-hl-monitor/internal/ws"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"

	"github.com/utrading/utrading-hl-monitor/internal/address"
	"github.com/utrading/utrading-hl-monitor/internal/cache"
	"github.com/utrading/utrading-hl-monitor/internal/monitor"
	"github.com/utrading/utrading-hl-monitor/internal/nats"
	"github.com/utrading/utrading-hl-monitor/internal/processor"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// 订单方向常量
const (
	DirOpenLong    = "Open Long"
	DirOpenShort   = "Open Short"
	DirCloseLong   = "Close Long"
	DirCloseShort  = "Close Short"
	DirLongToShort = "Long > Short"
	DirShortToLong = "Short > Long"
	DirBuy         = "Buy"
	DirSell        = "Sell"
)

// SubscriptionManager 订阅管理器 - 订阅地址的订单成交事件
type SubscriptionManager struct {
	poolManager          *ws.PoolManager
	publisher            Publisher
	addresses            concurrent.Map[string, struct{}]
	subs                 map[string]*ws.SubscriptionHandle // fills 和 updates 订阅句柄
	messageQueue         *processor.MessageQueue           // 消息队列
	orderProcessor       *processor.OrderProcessor         // 订单处理器
	deduper              *OrderDeduper                     // 订单去重器
	positionBalanceCache *cache.PositionBalanceCache       // 仓位余额缓存
	oidToAddress         concurrent.Map[int64, string]     // Oid 到地址的映射（用于 OrderUpdates 地址隔离）
	symbolCache          *cache.SymbolCache                // Symbol 缓存
	mu                   sync.RWMutex
	done                 chan struct{}
}

var _ address.AddressSubscriber = (*SubscriptionManager)(nil)

// Publisher 信号发布接口
type Publisher interface {
	PublishAddressSignal(signal *nats.HlAddressSignal) error
}

// NewSubscriptionManager 创建订阅管理器
func NewSubscriptionManager(
	poolManager *ws.PoolManager,
	publisher Publisher,
	symbolCache *cache.SymbolCache,
	positionBalanceCache *cache.PositionBalanceCache,
	batchWriter *processor.BatchWriter,
) *SubscriptionManager {
	deduper := NewOrderDeduper(30 * time.Minute) // 默认 30 分钟去重窗口

	// 创建消息队列
	messageQueue := processor.NewMessageQueue(10000, nil)

	// 创建订单处理器
	orderProcessor := processor.NewOrderProcessor(publisher, batchWriter, deduper, symbolCache, positionBalanceCache)

	// 设置处理器
	messageQueue.SetHandler(orderProcessor)

	// 启动消息队列
	messageQueue.Start()

	sm := &SubscriptionManager{
		poolManager:          poolManager,
		publisher:            publisher,
		addresses:            concurrent.Map[string, struct{}]{},
		subs:                 make(map[string]*ws.SubscriptionHandle),
		messageQueue:         messageQueue,
		orderProcessor:       orderProcessor,
		deduper:              deduper,
		positionBalanceCache: positionBalanceCache,
		oidToAddress:         concurrent.Map[int64, string]{},
		symbolCache:          symbolCache,
		done:                 make(chan struct{}),
	}

	return sm
}

// SetDeduper 设置去重器（可选，用于自定义去重窗口）
func (m *SubscriptionManager) SetDeduper(deduper *OrderDeduper) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deduper = deduper
}

// GetDeduper 获取去重器
func (m *SubscriptionManager) GetDeduper() *OrderDeduper {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.deduper
}

// SubscribeAddress 订阅地址
func (m *SubscriptionManager) SubscribeAddress(addr string) error {
	_, loaded := m.addresses.LoadOrStore(addr, struct{}{})
	if loaded {
		return nil
	}

	return m.subscribeAddress(addr)
}

// UnsubscribeAddress 取消订阅地址
func (m *SubscriptionManager) UnsubscribeAddress(addr string) error {
	if _, exists := m.addresses.LoadAndDelete(addr); !exists {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Unsubscribe both subscriptions using ws
	if sub, ok := m.subs[addr+"-fills"]; ok {
		_ = sub.Unsubscribe()
		delete(m.subs, addr+"-fills")
	}
	if sub, ok := m.subs[addr+"-updates"]; ok {
		_ = sub.Unsubscribe()
		delete(m.subs, addr+"-updates")
	}

	// 清理该地址的 Oid 映射
	m.oidToAddress.Range(func(oid int64, addr string) bool {
		m.oidToAddress.Delete(oid)
		return true
	})

	logger.Info().Str("address", addr).Msg("unsubscribed order fills and updates")

	monitor.GetMetrics().SetAddressesCount(m.AddressCount())
	return nil
}

func (m *SubscriptionManager) subscribeAddress(addr string) error {
	if m.poolManager == nil {
		return fmt.Errorf("pool manager is nil")
	}

	// 1. 订阅 userFills
	fillsSub := ws.Subscription{
		Channel: ws.ChannelUserFills,
		User:    addr,
	}

	fillsHandle, err := m.poolManager.Subscribe(fillsSub, func(msg ws.WsMessage) error {
		// 解析 order fills 消息
		var fills hl.WsOrderFills
		if err := json.Unmarshal(msg.Data, &fills); err != nil {
			logger.Error().Err(err).Str("address", addr).Msg("failed to unmarshal order fills")
			return nil
		}

		if addr != fills.User {
			logger.Debug().Str("address", addr).
				Str("user", fills.User).
				Msg("order fills user mismatch, ignoring")
			return nil
		}

		// 转换为 hyperliquid.WsOrderFills 格式（复用现有逻辑）
		m.handleWsOrderFills(fills)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to subscribe userFills: %w", err)
	}

	// 2. 订阅 orderUpdates
	updatesSub := ws.Subscription{
		Channel: ws.ChannelOrderUpdates,
		User:    addr,
	}

	updatesHandle, err := m.poolManager.Subscribe(updatesSub, func(msg ws.WsMessage) error {
		// 解析 order updates 消息（数组格式）
		var orders []hl.WsOrder
		if err := json.Unmarshal(msg.Data, &orders); err != nil {
			logger.Error().Err(err).Str("address", addr).Msg("failed to unmarshal order updates")
			return nil
		}

		m.handleWsOrderUpdates(addr, orders)
		return nil
	})
	if err != nil {
		_ = fillsHandle.Unsubscribe()
		return fmt.Errorf("failed to subscribe orderUpdates: %w", err)
	}

	m.mu.Lock()
	m.subs[addr+"-fills"] = fillsHandle
	m.subs[addr+"-updates"] = updatesHandle
	m.mu.Unlock()

	logger.Info().
		Str("address", addr).
		Msg("subscribed order fills and updates")

	monitor.GetMetrics().SetAddressesCount(m.AddressCount())

	return nil
}

// handleWsOrderUpdates 处理 ws 格式的订单更新
func (m *SubscriptionManager) handleWsOrderUpdates(user string, orders []hl.WsOrder) {
	processedCount := 0
	skippedCount := 0

	for _, wsOrder := range orders {
		order := wsOrder.Order

		// 通过 Oid 查找地址（从 OrderFills 中建立的映射）
		addr, ok := m.oidToAddress.Load(order.Oid)
		if !ok {
			// 未找到映射，可能是订阅启动前的订单，跳过
			logger.Debug().
				Int64("oid", order.Oid).
				Str("status", string(wsOrder.Status)).
				Msg("order update: oid not found in address map, skipping")
			skippedCount++
			continue
		}

		if user != addr {
			logger.Debug().Str("address", addr).
				Str("user", user).
				Str("status", string(wsOrder.Status)).
				Msg("order update: user not found in address map, skipping")
			skippedCount++
			continue
		}

		logger.Info().Str("address", addr).
			Str("user", user).
			Int64("oid", order.Oid).
			Str("status", string(wsOrder.Status)).
			Msg("order update: processing order")

		if wsOrder.Status == "open" || wsOrder.Status == "triggered" {
			logger.Debug().
				Int64("oid", order.Oid).
				Str("status", string(wsOrder.Status)).
				Msg("order update: status not terminal, skipping")
			skippedCount++
			continue
		}

		// 验证地址是否在订阅列表中
		_, isSubscribed := m.addresses.Load(addr)

		if !isSubscribed {
			logger.Debug().
				Str("address", addr).
				Int64("oid", order.Oid).
				Msg("order update: address not subscribed, skipping")
			skippedCount++
			continue
		}

		// 更新订单处理器中的订单状态
		// 注意：orderUpdates 不包含 direction 信息，暂时使用空字符串
		// 实际业务中，反手订单的两个方向会共享同一个状态
		// 将状态更新放入消息队列，保证与 fills 按顺序处理
		msg := processor.OrderUpdateMessage{
			Address:   addr,
			Oid:       order.Oid,
			Status:    string(wsOrder.Status),
			Direction: "",
		}
		if err := m.messageQueue.Enqueue(msg); err != nil {
			logger.Error().Err(err).
				Str("address", addr).
				Int64("oid", order.Oid).
				Msg("failed to enqueue order update")
		}
		m.oidToAddress.Delete(order.Oid)
		processedCount++
	}

	if processedCount > 0 || skippedCount > 0 {
		logger.Debug().
			Int("processed", processedCount).
			Int("skipped", skippedCount).
			Msg("processed order updates")
	}
}

// handleWsOrderFills 处理 ws 格式的订单成交
func (m *SubscriptionManager) handleWsOrderFills(orders hl.WsOrderFills) {
	user := orders.User
	logger.Info().Str("address", user).
		Int("fills_count", len(orders.Fills)).
		Msg("received order fills")

	now := time.Now().UnixMilli()

	// 按 Oid 分组 fills
	orderGroups := make(map[int64][]hl.WsOrderFill)
	for _, fill := range orders.Fills {
		// 超过 30分钟就不处理了
		if now-fill.Time > 30*60*1000 {
			logger.Info().
				Str("address", user).
				Int64("order_id", fill.Oid).
				Msg("timeout skipping fill")
			continue
		}
		orderGroups[fill.Oid] = append(orderGroups[fill.Oid], fill)
	}

	// 处理每个订单组
	for oid, fills := range orderGroups {
		// 建立 Oid → Address 映射（用于 OrderUpdates 地址隔离）
		m.oidToAddress.Store(oid, user)

		// 拆分反手订单
		splitOrders := m.splitReversedOrder(fills)

		// 为每个方向调用 AddFill
		for dir, dirFills := range splitOrders {
			for _, fill := range dirFills {
				// 检查是否已处理（去重）
				if m.deduper.IsSeen(user, fill.Oid, dir) {
					logger.Debug().
						Str("address", user).
						Int64("oid", fill.Oid).
						Str("direction", dir).
						Int64("tid", fill.Tid).
						Msg("skip already processed order")
					continue
				}

				// 转换为 processor.OrderFillMessage 格式
				// 由于 processor 期望 hyperliquid.WsOrderFill，我们需要适配
				msg := m.convertToOrderFillMessage(user, fill, dir)

				if err := m.messageQueue.Enqueue(msg); err != nil {
					logger.Error().Err(err).
						Str("address", user).
						Int64("oid", fill.Oid).
						Str("direction", dir).
						Int64("tid", fill.Tid).
						Msg("failed to enqueue order fill")
				}
			}
		}
	}
}

// convertToOrderFillMessage 转换 hl.WsOrderFill 到 OrderFillMessage
func (m *SubscriptionManager) convertToOrderFillMessage(user string, fill hl.WsOrderFill, dir string) processor.OrderFillMessage {
	// 由于 processor.OrderFillMessage.Fill 期望 hyperliquid.WsOrderFill 类型
	// 我们需要创建一个适配器或修改 processor 的接口
	// 这里我们使用一个简化的方法，直接传递 hl.WsOrderFill 的字段
	return processor.OrderFillMessage{
		Address:   user,
		Fill:      fill, // 需要 processor 包支持 hl.WsOrderFill
		Direction: dir,
	}
}

// Addresses 获取所有订阅的地址
func (m *SubscriptionManager) Addresses() []string {
	result := make([]string, 0, int(m.addresses.Len()))
	m.addresses.Range(func(addr string, _ struct{}) bool {
		result = append(result, addr)
		return true
	})
	return result
}

// AddressCount 获取订阅地址数量
func (m *SubscriptionManager) AddressCount() int {
	return int(m.addresses.Len())
}

// GetStats 获取统计信息
func (m *SubscriptionManager) GetStats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]any{
		"address_count": m.AddressCount(),
	}

	// 添加去重器统计
	if m.deduper != nil {
		deduperStats := m.deduper.GetStats()
		for k, v := range deduperStats {
			stats["deduper_"+k] = v
		}
	}

	// ws.PoolManager 暂不提供统计信息

	return stats
}

// splitReversedOrder 拆分反手订单（ws 版本）
func (m *SubscriptionManager) splitReversedOrder(
	fills []hl.WsOrderFill,
) map[string][]hl.WsOrderFill {
	grouped := make(map[string][]hl.WsOrderFill)

	for _, fill := range fills {
		switch fill.Dir {
		case DirLongToShort, DirShortToLong:
			m.addReversedFills(grouped, fill)
		default:
			grouped[fill.Dir] = append(grouped[fill.Dir], fill)
		}
	}

	return grouped
}

// addReversedFills 添加反手订单的平仓和开仓部分（ws 版本）
func (m *SubscriptionManager) addReversedFills(
	grouped map[string][]hl.WsOrderFill,
	fill hl.WsOrderFill,
) {
	closeDir, openDir := m.getReverseDirections(fill.Dir)

	sz := cast.ToFloat64(fill.Sz)
	startPos := cast.ToFloat64(fill.StartPosition)
	closeSize := math.Abs(startPos)
	openSize := math.Max(sz-closeSize, 0)

	grouped[closeDir] = append(grouped[closeDir],
		m.cloneFillWithDirection(fill, closeDir, cast.ToString(closeSize)))
	grouped[openDir] = append(grouped[openDir],
		m.cloneFillWithDirection(fill, openDir, cast.ToString(openSize)))
}

// cloneFillWithDirection 克隆订单成交并修改方向和数量（ws 版本）
func (m *SubscriptionManager) cloneFillWithDirection(
	fill hl.WsOrderFill,
	dir string,
	sz string,
) hl.WsOrderFill {
	cloned := fill
	cloned.Dir = dir
	cloned.Sz = sz
	return cloned
}

// getReverseDirections 获取反手订单的平仓和开仓方向
func (m *SubscriptionManager) getReverseDirections(
	dir string,
) (closeDir, openDir string) {
	switch dir {
	case DirLongToShort:
		return DirCloseLong, DirOpenShort
	case DirShortToLong:
		return DirCloseShort, DirOpenLong
	default:
		return "", ""
	}
}

// isSpotDir 判断是否为现货方向
func (m *SubscriptionManager) isSpotDir(dir string) bool {
	return dir == "Buy" || dir == "Sell"
}

// getSpotSymbol 获取现货 symbol
func (m *SubscriptionManager) getSpotSymbol(coin string) (string, error) {
	symbol, ok := m.symbolCache.GetSpotSymbol(coin)
	if !ok {
		return "", fmt.Errorf("spot coin not found: %s", coin)
	}
	return symbol, nil
}

// getPerpSymbol 获取合约 symbol
func (m *SubscriptionManager) getPerpSymbol(coin string) (string, error) {
	// 处理 xyz:BTC 格式
	cleanCoin := coin
	if strings.Contains(coin, ":") {
		parts := strings.Split(coin, ":")
		if len(parts) == 2 && parts[0] == "xyz" {
			cleanCoin = parts[1]
		}
	}

	symbol, ok := m.symbolCache.GetPerpSymbol(cleanCoin)
	if !ok {
		return "", fmt.Errorf("perp coin not found: %s", cleanCoin)
	}
	return symbol, nil
}

// Close 关闭订阅管理器
func (m *SubscriptionManager) Close() error {
	close(m.done)

	// 停止消息队列
	if m.messageQueue != nil {
		m.messageQueue.Stop()
	}

	// 停止订单处理器
	if m.orderProcessor != nil {
		m.orderProcessor.Stop()
	}

	m.deduper.Close() // 关闭去重器

	m.mu.Lock()
	// 取消所有订阅
	for _, handle := range m.subs {
		_ = handle.Unsubscribe()
	}
	m.subs = make(map[string]*ws.SubscriptionHandle)
	m.mu.Unlock()

	// 不关闭 poolManager，因为它由外部管理

	return nil
}
