package manager

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	hl "github.com/sonirico/go-hyperliquid"
	"github.com/spf13/cast"
	"github.com/utrading/utrading-hl-monitor/internal/address"
	"github.com/utrading/utrading-hl-monitor/internal/cache"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/internal/processor"
	"github.com/utrading/utrading-hl-monitor/internal/ws"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

var _ address.AddressSubscriber = (*PositionManager)(nil)

// PositionManager 仓位管理器 - 监听地址的仓位变化
type PositionManager struct {
	poolManager          *ws.PoolManager
	addresses            map[string]bool
	subs                 map[string]*ws.SubscriptionHandle
	priceCache           *cache.PriceCache           // 价格缓存引用
	symbolCache          *cache.SymbolCache          // Symbol 转换缓存
	positionBalanceCache *cache.PositionBalanceCache // 仓位余额缓存
	messageQueue         *processor.MessageQueue     // 消息队列
	messagesReceived     map[string]int64            // 每个地址接收的消息计数
	messagesFiltered     int64                       // 过滤掉的消息计数
	mu                   sync.RWMutex
}

// NewPositionManager 创建仓位管理器
func NewPositionManager(
	poolManager *ws.PoolManager,
	priceCache *cache.PriceCache,
	symbolCache *cache.SymbolCache,
	batchWriter *processor.BatchWriter,
) *PositionManager {

	// 创建消息队列
	messageQueue := processor.NewMessageQueue(1000, nil)

	// 创建仓位处理器
	positonProcessor := processor.NewPositionProcessor(batchWriter)

	// 设置处理器
	messageQueue.SetHandler(positonProcessor)

	messageQueue.Start()

	return &PositionManager{
		poolManager:          poolManager,
		addresses:            make(map[string]bool),
		subs:                 make(map[string]*ws.SubscriptionHandle),
		priceCache:           priceCache,
		symbolCache:          symbolCache,
		positionBalanceCache: cache.NewPositionBalanceCache(),
		messageQueue:         messageQueue,
		messagesReceived:     make(map[string]int64),
	}
}

// SubscribeAddress 订阅地址的仓位数据
func (m *PositionManager) SubscribeAddress(addr string) error {
	m.mu.Lock()
	if m.addresses[addr] {
		m.mu.Unlock()
		return nil
	}
	m.addresses[addr] = true
	m.mu.Unlock()
	return m.subscribeAddress(addr)
}

// UnsubscribeAddress 取消订阅地址
func (m *PositionManager) UnsubscribeAddress(addr string) error {
	m.mu.Lock()
	if !m.addresses[addr] {
		m.mu.Unlock()
		return nil
	}
	delete(m.addresses, addr)
	m.mu.Unlock()

	return m.unsubscribeAddress(addr)
}

func (m *PositionManager) subscribeAddress(addr string) error {
	if m.poolManager == nil {
		logger.Error().Msg("pool manager is nil")
		return nil
	}

	// 创建 ws 订阅
	sub := ws.Subscription{
		Channel: ws.ChannelWebData2,
		User:    addr,
	}

	// 订阅并设置回调
	handle, err := m.poolManager.Subscribe(sub, func(msg ws.WsMessage) error {
		// 解析 WebData2 消息
		var webdata2 hl.WebData2
		if err := json.Unmarshal(msg.Data, &webdata2); err != nil {
			logger.Error().Err(err).Str("address", addr).Msg("failed to unmarshal webdata2")
			return nil
		}

		// 验证消息中的 User 是否匹配订阅地址
		if webdata2.User != addr {
			m.mu.Lock()
			m.messagesFiltered++
			m.mu.Unlock()
			logger.Debug().
				Str("subscribed_address", addr).
				Str("message_user", webdata2.User).
				Msg("received webdata2 for different user, ignoring")
			return nil
		}

		// 记录有效消息
		m.mu.Lock()
		m.messagesReceived[addr]++
		m.mu.Unlock()

		m.handleWebData2(webdata2)
		return nil
	})

	if err != nil {
		logger.Error().Err(err).Str("address", addr).Msg("failed to subscribe webdata2")
		return err
	}

	m.mu.Lock()
	m.subs[addr] = handle
	m.mu.Unlock()

	logger.Info().Str("address", addr).Msg("subscribed position data")

	return nil
}

func (m *PositionManager) handleWebData2(webdata2 hl.WebData2) {
	// 处理现货价格（新增逻辑）
	if len(webdata2.SpotAssetCtxs) > 0 {
		for _, spotAssetCtx := range webdata2.SpotAssetCtxs {
			priceStr := spotAssetCtx.MarkPx
			if spotAssetCtx.MidPx != nil {
				priceStr = *spotAssetCtx.MidPx
			}

			price := cast.ToFloat64(priceStr)
			m.priceCache.SetSpotPrice(spotAssetCtx.Coin, price)
		}
	}

	// 处理仓位缓存（现有逻辑）
	m.processPositionCache(webdata2.User, &webdata2)
}

func (m *PositionManager) processPositionCache(addr string, webdata2 *hl.WebData2) {
	// 现货总价值 = Σ(币种数量 × 价格)
	spotTotalUSD := 0.0

	// 解析现货余额
	var spotBalances models.SpotBalancesData
	if webdata2.SpotState != nil {
		spotBalances = make(models.SpotBalancesData, 0, len(webdata2.SpotState.Balances))
		for _, balance := range webdata2.SpotState.Balances {
			if cast.ToFloat64(balance.Total) == 0 {
				continue
			}
			coin := hl.MainnetToAlias(balance.Coin)
			spotBalances = append(spotBalances, models.SpotBalanceItem{
				Coin:     coin, // BTC
				Total:    balance.Total,
				Hold:     balance.Hold,
				EntryNtl: balance.EntryNtl,
			})

			total := cast.ToFloat64(balance.Total)

			if isStableCoin(coin) {
				// 稳定币默认价格为 1
				spotTotalUSD += total * 1.0
				continue
			}

			for _, base := range []string{"USDC", "USDT", "USDH"} {
				symbol := coin + base
				assetName, exists := m.symbolCache.GetSpotName(symbol)
				if !exists {
					logger.Error().Str("symbol", symbol).Msg("symbol not found in cache, skip position cache")
					continue
				}
				midPx, ok := m.priceCache.GetSpotPrice(assetName)
				if ok {
					spotTotalUSD += total * midPx
				}
				break
			}
		}
	}
	spotBalancesJSON, _ := json.Marshal(spotBalances)

	// 解析合约仓位
	var futuresPositions models.FuturesPositionsData

	state := webdata2.ClearinghouseState

	marginSummary := &hl.MarginSummary{}
	if state.CrossMarginSummary != nil {
		marginSummary = state.CrossMarginSummary
	} else if state.MarginSummary != nil {
		marginSummary = state.MarginSummary
	}

	if webdata2.ClearinghouseState != nil {
		futuresPositions = make(models.FuturesPositionsData, 0, len(state.AssetPositions))
		for _, assetPos := range state.AssetPositions {
			if assetPos.Position.Coin == "" {
				continue
			}

			var entryPx *string
			if assetPos.Position.EntryPx != nil {
				entryPx = assetPos.Position.EntryPx
			}

			// 转换合约 coin 为统一格式 (BTC -> BTCUSDC)
			coin := assetPos.Position.Coin
			if strings.Contains(coin, ":") {
				parts := strings.Split(coin, ":")
				if len(parts) != 2 || parts[0] != "xyz" {
					continue
				}
				coin = parts[1]
			}

			coin = hl.MainnetToAlias(coin)

			if m.symbolCache != nil {
				if converted, ok := m.symbolCache.GetPerpSymbol(coin); ok {
					coin = converted
				}
			} else {
				coin = coin + "USDC"
			}

			position := models.PositionItem{
				Coin:          coin,
				Szi:           assetPos.Position.Szi,
				EntryPx:       entryPx,
				UnrealizedPnl: assetPos.Position.UnrealizedPnl,
				Leverage: models.LeverageItem{
					Type:  assetPos.Position.Leverage.Type,
					Value: assetPos.Position.Leverage.Value,
				},
				MarginUsed:     assetPos.Position.MarginUsed,
				PositionValue:  assetPos.Position.PositionValue,
				ReturnOnEquity: assetPos.Position.ReturnOnEquity,
			}

			futuresPositions = append(futuresPositions, position)
		}
	}
	futuresPositionsJSON, _ := json.Marshal(futuresPositions)

	logger.Debug().
		Str("address", addr).
		Int("futures_count", len(futuresPositions)).
		Msg("serialized futures positions")

	logger.Debug().
		Str("address", addr).
		Float64("spot_total_usd", spotTotalUSD).
		Int("spot_count", len(spotBalances)).
		Msg("calculated spot total value")

	// 写入数据库队列
	message := processor.NewPositionCacheMessage(addr, &models.HlPositionCache{
		Address:          addr,
		SpotBalances:     string(spotBalancesJSON),
		SpotTotalUSD:     fmt.Sprintf("%.6f", spotTotalUSD),
		FuturesPositions: string(futuresPositionsJSON),
		AccountValue:     marginSummary.AccountValue,
		TotalMarginUsed:  marginSummary.TotalMarginUsed,
		TotalNtlPos:      marginSummary.TotalNtlPos,
		Withdrawable:     state.Withdrawable,
		UpdatedAt:        time.Now(),
	})
	if err := m.messageQueue.Enqueue(message); err != nil {
		logger.Error().Err(err).
			Str("address", addr).
			Msg("failed to enqueue position manager")
	}

	// 同时更新内存缓存（包括持仓数据）
	accountValue := cast.ToFloat64(marginSummary.AccountValue)
	m.positionBalanceCache.Set(addr, spotTotalUSD, accountValue, &spotBalances, &futuresPositions)
}

func (m *PositionManager) unsubscribeAddress(addr string) error {
	m.mu.Lock()
	handle, ok := m.subs[addr]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.subs, addr)
	m.mu.Unlock()

	if handle != nil {
		_ = handle.Unsubscribe()
	}

	// 清理缓存
	m.positionBalanceCache.Delete(addr)

	logger.Info().Str("address", addr).Msg("unsubscribed position data")

	return nil
}

// Addresses 获取所有监控的地址
func (m *PositionManager) Addresses() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]string, 0, len(m.addresses))
	for addr := range m.addresses {
		result = append(result, addr)
	}
	return result
}

// PositionBalanceCache 获取仓位余额缓存
func (m *PositionManager) PositionBalanceCache() *cache.PositionBalanceCache {
	return m.positionBalanceCache
}

// GetStats 获取统计信息
func (m *PositionManager) GetStats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := map[string]any{
		"monitored_addresses":  len(m.addresses),
		"messages_filtered":    m.messagesFiltered,
		"messages_per_address": make(map[string]int64),
	}
	for addr, count := range m.messagesReceived {
		stats["messages_per_address"].(map[string]int64)[addr] = count
	}
	return stats
}

// Close 关闭管理器
func (m *PositionManager) Close() error {
	// 停止消息队列
	if m.messageQueue != nil {
		m.messageQueue.Stop()
	}

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

// isStableCoin 判断是否为稳定币
func isStableCoin(coin string) bool {
	return coin == "USDC" || coin == "USDH" || coin == "USDT"
}
