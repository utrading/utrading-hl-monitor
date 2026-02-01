package processor

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	hl "github.com/sonirico/go-hyperliquid"
	"github.com/spf13/cast"
	"github.com/utrading/utrading-hl-monitor/internal/cache"
	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/internal/monitor"
	"github.com/utrading/utrading-hl-monitor/internal/nats"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// Publisher NATS 发布接口
type Publisher interface {
	PublishAddressSignal(signal *nats.HlAddressSignal) error
}

// PendingOrderCache 待处理订单缓存
// 使用 concurrent.Map 实现线程安全的短期暂存
type PendingOrderCache struct {
	orders concurrent.Map[string, *PendingOrder]
}

// NewPendingOrderCache 创建缓存实例
func NewPendingOrderCache() *PendingOrderCache {
	return &PendingOrderCache{}
}

// Get 获取订单
func (c *PendingOrderCache) Get(key string) (*PendingOrder, bool) {
	return c.orders.Load(key)
}

// LoadOrStore 获取或存储订单
func (c *PendingOrderCache) LoadOrStore(key string, order *PendingOrder) (*PendingOrder, bool) {
	return c.orders.LoadOrStore(key, order)
}

// Set 存储订单
func (c *PendingOrderCache) Set(key string, order *PendingOrder) {
	c.orders.Store(key, order)
}

// Delete 删除订单
func (c *PendingOrderCache) Delete(key string) {
	c.orders.Delete(key)
}

// Len 返回订单数量
func (c *PendingOrderCache) Len() int64 {
	return c.orders.Len()
}

// Range 遍历所有订单
func (c *PendingOrderCache) Range(f func(key string, order *PendingOrder) bool) {
	c.orders.Range(f)
}

// Clear 清空所有订单
func (c *PendingOrderCache) Clear() {
	c.orders.Clear()
}

// PendingOrder 待处理订单
type PendingOrder struct {
	seenTids             concurrent.Map[int64, struct{}] // tid 去重
	Aggregation          *models.OrderAggregation
	FirstFillTime        time.Time
	SymbolCache          *cache.SymbolCache
	PositionBalanceCache *cache.PositionBalanceCache
}

// flushKey 发送键
type flushKey struct {
	key     string
	trigger string // "status", "timeout", "manual"
	status  string // order status "filled"
}

// OrderProcessor 订单处理器
type OrderProcessor struct {
	pendingOrders        *PendingOrderCache // key: "address-oid-direction"
	publisher            Publisher
	batchWriter          *BatchWriter
	deduper              cache.DedupCacheInterface
	symbolCache          *cache.SymbolCache
	positionBalanceCache *cache.PositionBalanceCache
	timeout              time.Duration
	flushChan            chan flushKey
	done                 chan struct{}
	wg                   sync.WaitGroup
	pool                 *ants.Pool   // 协程池
	statusTracker        OrderStatusTracker // 状态追踪器
	mu                   sync.RWMutex // 保留，待后续任务移除
}

// NewOrderProcessor 创建订单处理器
func NewOrderProcessor(
	publisher Publisher,
	batchWriter *BatchWriter,
	deduper cache.DedupCacheInterface,
	symbolCache *cache.SymbolCache,
	positionBalanceCache *cache.PositionBalanceCache,
) *OrderProcessor {
	if batchWriter == nil {
		logger.Warn().Msg("order processor created without batch writer, writes will be skipped")
	}

	// 创建协程池（固定 30 个 worker）
	pool, err := ants.NewPool(30)
	if err != nil {
		logger.Fatal().Err(err).Msg("create ants pool failed")
	}

	op := &OrderProcessor{
		pendingOrders:        NewPendingOrderCache(),
		publisher:            publisher,
		batchWriter:          batchWriter,
		deduper:              deduper,
		symbolCache:          symbolCache,
		positionBalanceCache: positionBalanceCache,
		timeout:              5 * time.Minute,
		flushChan:            make(chan flushKey, 1000),
		done:                 make(chan struct{}),
		pool:                 pool,
		statusTracker:        NewOrderStatusTracker(10 * time.Minute),
	}

	// 启动后台协程
	op.wg.Add(2)
	go op.flushProcessor()
	go op.timeoutScanner()

	return op
}

// SetTimeout 设置超时时间
func (p *OrderProcessor) SetTimeout(timeout time.Duration) {
	p.timeout = timeout
}

// HandleMessage 处理消息（实现 MessageHandler 接口）
func (p *OrderProcessor) HandleMessage(msg Message) error {
	switch m := msg.(type) {
	case OrderFillMessage:
		return p.handleOrderFill(m)
	case OrderUpdateMessage:
		p.UpdateStatus(m.Address, m.Oid, m.Status, m.Direction)
		return nil
	case PositionUpdateMessage:
		return nil
	default:
		logger.Warn().Str("type", msg.Type()).Msg("unknown message type")
		return nil
	}
}

// handleOrderFill 处理订单成交
func (p *OrderProcessor) handleOrderFill(msg OrderFillMessage) error {
	fill, ok := msg.Fill.(hl.WsOrderFill)
	if !ok {
		return fmt.Errorf("invalid fill type")
	}

	key := p.orderKey(msg.Address, fill.Oid, msg.Direction)

	// 1. 检查去重缓存（已发送信号）
	if p.deduper != nil {
		if p.deduper.IsSeen(msg.Address, fill.Oid, msg.Direction) {
			logger.Debug().
				Int64("oid", fill.Oid).
				Str("direction", msg.Direction).
				Msg("order already sent via deduper, skipping fill")
			return nil
		}
	}

	// 2. 检查状态追踪器（是否已记录终止状态）
	shouldFlushImmediately := false
	preMarkedStatus := ""
	if status, found := p.statusTracker.GetStatus(msg.Address, fill.Oid); found {
		logger.Info().
			Int64("oid", fill.Oid).
			Str("direction", msg.Direction).
			Str("status", status).
			Msg("order status pre-marked, will flush immediately")
		shouldFlushImmediately = true
		preMarkedStatus = status
	}

	// 转换 symbol
	symbol, err := p.convertSymbol(fill.Coin, fill.Dir)
	if err != nil {
		logger.Warn().
			Str("coin", fill.Coin).
			Str("dir", fill.Dir).
			Err(err).
			Msg("symbol convert failed, using raw coin")
		symbol = fill.Coin
	}

	// 使用 LoadOrStore 原子操作获取或创建订单
	pending, loaded := p.pendingOrders.LoadOrStore(key, &PendingOrder{
		Aggregation: &models.OrderAggregation{
			Oid:           fill.Oid,
			Address:       msg.Address,
			Symbol:        symbol,
			Direction:     msg.Direction,
			OrderStatus:   "open",
			LastFillTime:  time.Now().Unix(),
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			Fills:         []hl.WsOrderFill{fill},
			TotalSize:     cast.ToFloat64(fill.Sz),
			WeightedAvgPx: cast.ToFloat64(fill.Px),
		},
		FirstFillTime:        time.Now(),
		SymbolCache:          p.symbolCache,
		PositionBalanceCache: p.positionBalanceCache,
	})

	if !loaded {
		// 新订单，更新监控指标
		monitor.SetOrderAggregationActive(int(p.pendingOrders.Len()))
		logger.Debug().
			Int64("oid", fill.Oid).
			Str("direction", msg.Direction).
			Msg("new order aggregation created")
	} else {
		// 检查订单是否已发送
		if pending.Aggregation.SignalSent {
			logger.Debug().
				Int64("oid", fill.Oid).
				Int64("tid", fill.Tid).
				Str("direction", msg.Direction).
				Msg("order already sent, skipping fill")
			return nil
		}

		// 已存在订单，使用 LoadOrStore 进行 tid 去重（O(1)）
		_, tidLoaded := pending.seenTids.LoadOrStore(fill.Tid, struct{}{})
		if tidLoaded {
			// 重复的 fill
			logger.Debug().
				Int64("oid", fill.Oid).
				Int64("tid", fill.Tid).
				Str("direction", msg.Direction).
				Msg("duplicate fill skipped")
			return nil
		}

		// 追加 fill
		pending.Aggregation.Fills = append(pending.Aggregation.Fills, fill)
		pending.Aggregation.TotalSize, pending.Aggregation.WeightedAvgPx = p.calculateWeightedAvg(pending.Aggregation.Fills)
		pending.Aggregation.LastFillTime = time.Now().Unix()
		pending.Aggregation.UpdatedAt = time.Now()

		// 记录 fill 数量
		monitor.ObserveFillsPerOrder(len(pending.Aggregation.Fills))
	}

	// 持久化到数据库
	p.persistOrder(pending.Aggregation)

	// 3. 如果状态追踪器有记录，立即 flush
	if shouldFlushImmediately && preMarkedStatus != "" {
		if !pending.Aggregation.SignalSent {
			p.triggerFlush(key, "status", preMarkedStatus)
			p.statusTracker.Remove(msg.Address, fill.Oid)
		}
	}

	return nil
}

// convertSymbol 转换 coin 为标准 symbol 格式
func (p *OrderProcessor) convertSymbol(coin string, dir string) (string, error) {
	// 判断现货/合约
	if p.isSpotDir(dir) {
		symbol, ok := p.symbolCache.GetSpotSymbol(coin)
		if !ok {
			return "", fmt.Errorf("spot coin not found: %s", coin)
		}
		return symbol, nil
	}

	// 合约处理
	cleanCoin := coin
	if strings.Contains(coin, ":") {
		parts := strings.Split(coin, ":")
		if len(parts) == 2 && parts[0] == "xyz" {
			cleanCoin = parts[1]
		}
	}

	symbol, ok := p.symbolCache.GetPerpSymbol(cleanCoin)
	if !ok {
		return "", fmt.Errorf("perp coin not found: %s", cleanCoin)
	}
	return symbol, nil
}

// isSpotDir 判断是否为现货方向
func (p *OrderProcessor) isSpotDir(dir string) bool {
	return dir == "Buy" || dir == "Sell"
}

// UpdateStatus 更新订单状态
func (p *OrderProcessor) UpdateStatus(address string, oid int64, status string, direction string) {
	if status == "open" || status == "triggered" {
		return
	}

	// 先记录状态到 tracker（无论是否找到 PendingOrder）
	p.statusTracker.MarkStatus(address, oid, status)

	if direction == "" {
		// 查找所有方向的订单（通过遍历）
		for _, dir := range []string{"Open Long", "Open Short", "Close Long", "Close Short", "Buy", "Sell"} {
			key := p.orderKey(address, oid, dir)
			if _, exist := p.pendingOrders.Get(key); !exist {
				continue
			}
			p.triggerFlush(key, "status", status)
			p.statusTracker.Remove(address, oid) // 从 tracker 移除
		}
	} else {
		key := p.orderKey(address, oid, direction)
		if _, exists := p.pendingOrders.Get(key); exists {
			p.triggerFlush(key, "status", status)
			p.statusTracker.Remove(address, oid) // 从 tracker 移除
		}
	}
}

// orderKey 生成订单键
func (p *OrderProcessor) orderKey(address string, oid int64, direction string) string {
	return fmt.Sprintf("%s-%d-%s", address, oid, direction)
}

// calculateWeightedAvg 计算加权平均价
func (p *OrderProcessor) calculateWeightedAvg(fills []hl.WsOrderFill) (totalSize, avgPx float64) {
	var totalValue float64
	for _, f := range fills {
		sz := cast.ToFloat64(f.Sz)
		px := cast.ToFloat64(f.Px)
		totalSize += sz
		totalValue += sz * px
	}
	if totalSize == 0 {
		return 0, 0
	}
	return totalSize, totalValue / totalSize
}

// persistOrder 持久化订单到数据库
func (p *OrderProcessor) persistOrder(agg *models.OrderAggregation) {
	if p.batchWriter == nil {
		return
	}

	// 将 OrderAggregation 转换为 OrderAggregationItem 写入
	if err := p.batchWriter.Add(OrderAggregationItem{agg}); err != nil {
		logger.Error().Err(err).
			Str("addr", agg.Address).
			Int64("oid", agg.Oid).
			Str("direction", agg.Direction).
			Msg("failed to persist order")
		return
	}
}

// triggerFlush 触发发送
func (p *OrderProcessor) triggerFlush(key string, trigger, status string) {
	select {
	case p.flushChan <- flushKey{key: key, trigger: trigger, status: status}:
	default:
		logger.Warn().Str("key", key).Msg("flush channel full, drop flush request")
	}
}

// flushProcessor 处理发送队列
func (p *OrderProcessor) flushProcessor() {
	defer p.wg.Done()
	for {
		select {
		case req := <-p.flushChan:
			// 提交到协程池并发执行
			key := req.key
			trigger := req.trigger
			status := req.status
			_ = p.pool.Submit(func() {
				p.flushOrder(key, trigger, status)
			})
		case <-p.done:
			// 处理剩余消息
			for len(p.flushChan) > 0 {
				req := <-p.flushChan
				key := req.key
				trigger := req.trigger
				status := req.status
				_ = p.pool.Submit(func() {
					p.flushOrder(key, trigger, status)
				})
			}
			return
		}
	}
}

// flushOrder 发送订单信号
func (p *OrderProcessor) flushOrder(key string, trigger, status string) {
	pending, exists := p.pendingOrders.Get(key)
	if !exists {
		return
	}

	if pending.Aggregation.SignalSent {
		return
	}

	// 构建信号
	signal := p.buildSignal(pending.Aggregation)
	if signal == nil {
		return
	}

	// 1. 发布到 NATS
	if err := p.publisher.PublishAddressSignal(signal); err != nil {
		logger.Error().Err(err).Int64("oid", pending.Aggregation.Oid).Msg("publish signal failed")
		return
	}

	// 2. 标记已发送
	pending.Aggregation.SignalSent = true
	pending.Aggregation.OrderStatus = status
	pending.Aggregation.UpdatedAt = time.Now()

	// 3. 标记到去重器
	if p.deduper != nil {
		p.deduper.Mark(pending.Aggregation.Address, pending.Aggregation.Oid, pending.Aggregation.Direction)
	}

	// 持久化到数据库
	p.persistOrder(pending.Aggregation)

	// 4. 从待处理列表移除
	p.pendingOrders.Delete(key)
	monitor.SetOrderAggregationActive(int(p.pendingOrders.Len()))

	// 5. 清理 seenTids（防止内存泄漏）
	pending.seenTids.Clear()

	// 6. 记录发送指标
	monitor.IncOrderFlush(trigger)

	if err := dao.Signal().Create(signal); err != nil {
		logger.Error().
			Err(err).
			Int64("oid", pending.Aggregation.Oid).
			Msg("persist signal to hl_address_signals failed")
		// 信号持久化失败不阻塞主流程，订单已发送到 NATSƒ
	}

	logger.Info().
		Int64("oid", pending.Aggregation.Oid).
		Str("symbol", signal.Symbol).
		Float64("size", signal.Size).
		Str("trigger", trigger).
		Msg("order signal sent")
}

// buildSignal 构建信号
func (p *OrderProcessor) buildSignal(agg *models.OrderAggregation) *nats.HlAddressSignal {
	if len(agg.Fills) == 0 {
		return nil
	}

	firstFill := agg.Fills[0]

	// 方向映射
	var direction, side, assetType string
	switch agg.Direction {
	case "Open Long":
		direction, side, assetType = "open", "LONG", "futures"
	case "Open Short":
		direction, side, assetType = "open", "SHORT", "futures"
	case "Close Long":
		direction, side, assetType = "close", "LONG", "futures"
	case "Close Short":
		direction, side, assetType = "close", "SHORT", "futures"
	case "Buy":
		direction, side, assetType = "open", "LONG", "spot"
	case "Sell":
		direction, side, assetType = "close", "LONG", "spot"
	default:
		logger.Warn().Str("dir", agg.Direction).Msg("unknown order direction, skip signal")
		return nil
	}

	// 计算 PositionRate
	positionRate := p.calculatePositionRate(agg.Address, assetType, agg.WeightedAvgPx, agg.TotalSize)

	// 计算 CloseRate（平仓比例）
	closeRate := p.calculateCloseRate(direction, assetType, agg.Address, agg.Symbol, agg.TotalSize)

	return &nats.HlAddressSignal{
		Address:      agg.Address,
		Symbol:       agg.Symbol,
		AssetType:    assetType,
		Direction:    direction,
		Side:         side,
		PositionRate: positionRate,
		CloseRate:    closeRate,
		Size:         agg.TotalSize,
		Price:        agg.WeightedAvgPx,
		Timestamp:    firstFill.Time,
	}
}

// calculatePositionRate 计算仓位比例
func (p *OrderProcessor) calculatePositionRate(address, assetType string, price, size float64) float64 {
	if p.positionBalanceCache == nil {
		return 100.00
	}

	var totalBalance float64
	var ok bool

	if assetType == "spot" {
		totalBalance, ok = p.positionBalanceCache.GetSpotTotal(address)
	} else {
		totalBalance, ok = p.positionBalanceCache.GetAccountValue(address)
	}

	// 错误处理：返回 100%
	if !ok || totalBalance <= 0 {
		logger.Debug().
			Str("address", address).
			Str("asset_type", assetType).
			Msg("balance cache not found or invalid, using 100%")
		return 100.00
	}

	// 计算比例: (price × size) / totalBalance × 100
	rate := (price * size / totalBalance) * 100
	return rate
}

// calculateCloseRate 计算平仓比例
func (p *OrderProcessor) calculateCloseRate(direction string, assetType string, address string, symbol string, size float64) float64 {
	// 只有平仓订单才计算平仓比例
	if direction != "close" {
		return 0
	}

	if p.positionBalanceCache == nil {
		return 0
	}

	var currentPosition float64
	var ok bool

	// 根据 assetType 判断现货还是合约
	if assetType == "spot" {
		// 去掉 symbol 尾部的 USDC，获取原始 coin 名称
		coin := strings.TrimSuffix(symbol, "USDC")
		currentPosition, ok = p.positionBalanceCache.GetSpotBalance(address, coin)
	} else { // futures
		currentPosition, ok = p.positionBalanceCache.GetFuturesPosition(address, symbol)
	}

	if !ok || currentPosition <= 0 {
		// 无法获取当前持仓，返回 0
		return 0
	}

	// 计算平仓比例: 平仓数量 / 当前持仓数量
	if size > currentPosition {
		// 防止超过 100%
		return 1.0
	}

	return size / currentPosition
}

// timeoutScanner 超时扫描器
func (p *OrderProcessor) timeoutScanner() {
	defer p.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.scanTimeoutOrders()
		case <-p.done:
			return
		}
	}
}

// scanTimeoutOrders 扫描超时订单
func (p *OrderProcessor) scanTimeoutOrders() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	timeoutThreshold := now.Add(-p.timeout)

	p.pendingOrders.Range(func(key string, pending *PendingOrder) bool {
		// 未发送且超时
		if !pending.Aggregation.SignalSent && pending.FirstFillTime.Before(timeoutThreshold) {
			p.triggerFlush(key, "timeout", "filled")
		}
		return true
	})
}

// Stop 停止处理器
func (p *OrderProcessor) Stop() {
	close(p.done)
	p.wg.Wait()
	p.pool.Release()
}

// ActiveCount 返回活跃订单数
func (p *OrderProcessor) ActiveCount() int {
	return int(p.pendingOrders.Len())
}
