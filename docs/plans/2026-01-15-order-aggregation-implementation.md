# 订单聚合功能实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**目标:** 实现订单聚合功能，将同一订单的多次撮合成交聚合成完整订单，支持状态完成触发和超时触发两种机制。

**架构:** 双订阅模式（orderFills + orderUpdates） + 订单聚合器（sync.Map 内存缓存） + 数据库持久化 + 超时扫描器。

**技术栈:** Go, gorm-gen, MySQL, NATS, Prometheus, Hyperliquid WebSocket SDK

---

## Task 1: 创建数据库表结构

**Files:**
- Create: `migrations/20260115_create_order_aggregation.sql`
- Create: `internal/models/order_aggregation.go`

### Step 1: 创建订单聚合表迁移文件

```bash
mkdir -p migrations
```

创建 `migrations/20260115_create_order_aggregation.sql`:

```sql
-- 订单聚合状态表
CREATE TABLE IF NOT EXISTS hl_order_aggregation (
    oid BIGINT PRIMARY KEY COMMENT '订单ID',
    address VARCHAR(42) NOT NULL COMMENT '监控地址',
    symbol VARCHAR(24) NOT NULL COMMENT '交易对',

    -- 聚合数据
    fills JSON NOT NULL COMMENT '所有 fill 数据',
    total_size DECIMAL(18,8) NOT NULL DEFAULT 0 COMMENT '总数量',
    weighted_avg_px DECIMAL(28,12) NOT NULL DEFAULT 0 COMMENT '加权平均价',

    -- 状态控制
    order_status VARCHAR(16) NOT NULL DEFAULT 'open' COMMENT '订单状态: open/filled/canceled',
    last_fill_time BIGINT NOT NULL COMMENT '最后 fill 时间戳',

    -- 处理标记
    signal_sent BOOLEAN NOT NULL DEFAULT FALSE COMMENT '信号是否已发送',

    -- 时间字段
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

    INDEX idx_address (address),
    INDEX idx_last_fill_time (last_fill_time),
    INDEX idx_signal_sent (signal_sent),
    INDEX idx_updated_at (updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订单聚合状态表';
```

### Step 2: 修改信号表，添加 oid 字段

创建 `migrations/20260115_alter_signal_add_oid.sql`:

```sql
-- 为信号表添加 oid 字段（用于关联订单）
ALTER TABLE hl_address_signal
ADD COLUMN IF NOT EXISTS oid BIGINT UNIQUE COMMENT '订单ID',
ADD INDEX IF NOT EXISTS idx_oid (oid);
```

### Step 3: 创建 OrderAggregation 模型

创建 `internal/models/order_aggregation.go`:

```go
package models

import (
	"time"

	"github.com/sonirico/go-hyperliquid"
)

// OrderAggregation 订单聚合状态
type OrderAggregation struct {
	Oid           int64                        `gorm:"column:oid;primaryKey" json:"oid"`
	Address       string                       `gorm:"column:address;not null;index:idx_address" json:"address"`
	Symbol        string                       `gorm:"column:symbol;not null" json:"symbol"`

	// 聚合数据
	Fills         []hyperliquid.WsOrderFill    `gorm:"column:fills;type:json;not null;serializer:json" json:"fills"`
	TotalSize     float64                      `gorm:"column:total_size;not null;default:0" json:"total_size"`
	WeightedAvgPx float64                      `gorm:"column:weighted_avg_px;not null;default:0" json:"weighted_avg_px"`

	// 状态控制
	OrderStatus   string                       `gorm:"column:order_status;not null;default:open" json:"order_status"`
	LastFillTime  int64                        `gorm:"column:last_fill_time;not null;index" json:"last_fill_time"`

	// 处理标记
	SignalSent    bool                         `gorm:"column:signal_sent;not null;default:false;index:idx_signal_sent" json:"signal_sent"`

	// 时间字段
	CreatedAt     time.Time                    `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt     time.Time                    `gorm:"column:updated_at;not null;index:idx_updated_at" json:"updated_at"`
}

// TableName 指定表名
func (OrderAggregation) TableName() string {
	return "hl_order_aggregation"
}
```

### Step 4: 执行数据库迁移

```bash
mysql -u root -p utrading < migrations/20260115_create_order_aggregation.sql
mysql -u root -p utrading < migrations/20260115_alter_signal_add_oid.sql
```

### Step 5: 提交

```bash
git add migrations/ internal/models/order_aggregation.go
git commit -m "feat: 添加订单聚合表结构和模型"
```

---

## Task 2: 生成 gorm-gen 代码

**Files:**
- Modify: `cmd/gen/main.go`
- Run: `cd cmd/gen && go run main.go`

### Step 1: 运行代码生成器

```bash
cd cmd/gen && go run main.go
```

这将在 `internal/dal/gen/` 下生成类型安全的查询代码。

### Step 2: 验证生成的代码

检查 `internal/dal/gen/` 目录下是否生成了 `hl_order_aggregation` 相关的查询代码。

### Step 3: 提交

```bash
git add internal/dal/gen/
git commit -m "chore: 重新生成 gorm-gen 代码"
```

---

## Task 3: 创建 OrderAggregationDAO

**Files:**
- Create: `internal/dao/order_aggregation.go`

### Step 1: 创建 DAO 文件

创建 `internal/dao/order_aggregation.go`:

```go
package dao

import (
	"context"

	"github.com/utrading/utrading-hl-monitor/internal/dal"
	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"github.com/utrading/utrading-hl-monitor/internal/models"
)

type orderAggregationDAO struct {
	dao *gen.DO
}

// OrderAggregation 订单聚合 DAO
var _orderAggregation *orderAggregationDAO

// InitOrderAggregationDAO 初始化 DAO
func InitOrderAggregationDAO(db *dal.DB) {
	_orderAggregation = &orderAggregationDAO{
		dao: gen.Q.WithContext(context.Background()).HLOrderAggregation,
	}
}

// OrderAggregation 获取 DAO 实例
func OrderAggregation() *orderAggregationDAO {
	return _orderAggregation
}

// Create 创建订单聚合记录
func (d *orderAggregationDAO) Create(agg *models.OrderAggregation) error {
	return d.dao.Create(agg)
}

// Update 更新订单聚合记录
func (d *orderAggregationDAO) Update(agg *models.OrderAggregation) error {
	_, err := d.dao.Where(d.dao.Oid.Eq(agg.Oid)).Updates(agg)
	return err
}

// Get 根据 Oid 获取订单聚合
func (d *orderAggregationDAO) Get(oid int64) (*models.OrderAggregation, error) {
	return d.dao.Where(d.dao.Oid.Eq(oid)).First()
}

// GetPending 获取未发送信号的订单
func (d *orderAggregationDAO) GetPending() ([]*models.OrderAggregation, error) {
	return d.dao.Where(d.dao.SignalSent.Eq(false)).Find()
}

// GetTimeout 获取超时的订单
func (d *orderAggregationDAO) GetTimeout(beforeTimestamp int64) ([]*models.OrderAggregation, error) {
	return d.dao.Where(
		d.dao.SignalSent.Eq(false),
		d.dao.LastFillTime.Lt(beforeTimestamp),
	).Find()
}

// UpdateStatus 更新订单状态
func (d *orderAggregationDAO) UpdateStatus(oid int64, status string) error {
	_, err := d.dao.Where(d.dao.Oid.Eq(oid)).Update(
		d.dao.OrderStatus,
		status,
	)
	return err
}

// MarkSignalSent 标记信号已发送
func (d *orderAggregationDAO) MarkSignalSent(oid int64) error {
	_, err := d.dao.Where(d.dao.Oid.Eq(oid)).Update(
		d.dao.SignalSent,
		true,
	)
	return err
}

// DeleteOld 清理过期数据
func (d *orderAggregationDAO) DeleteOld(beforeTimestamp int64) error {
	_, err := d.dao.Where(
		d.dao.SignalSent.Eq(true),
		d.dao.UpdatedAt.Lt(beforeTimestamp),
	).Delete()
	return err
}
```

### Step 2: 在 main.go 中初始化 DAO

修改 `cmd/hl_monitor/main.go`，在 `dao.InitDAO()` 后添加:

```go
dao.InitOrderAggregationDAO(dal.MySQL())
```

### Step 3: 提交

```bash
git add internal/dao/order_aggregation.go cmd/hl_monitor/main.go
git commit -m "feat: 添加订单聚合 DAO"
```

---

## Task 4: 创建订单聚合器核心组件

**Files:**
- Create: `internal/ws/aggregator.go`

### Step 1: 创建聚合器文件

创建 `internal/ws/aggregator.go`:

```go
package ws

import (
	"math"
	"sync"
	"time"

	"github.com/spf13/cast"
	"github.com/sonirico/go-hyperliquid"

	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/internal/nats"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// OrderAggregator 订单聚合器
type OrderAggregator struct {
	orders    sync.Map  // key: "address-oid" → *models.OrderAggregation
	timeout   time.Duration
	flushChan chan int64
	publisher Publisher
}

// NewOrderAggregator 创建订单聚合器
func NewOrderAggregator(publisher Publisher, timeout time.Duration) *OrderAggregator {
	a := &OrderAggregator{
		orders:    sync.Map{},
		timeout:   timeout,
		flushChan: make(chan int64, 1000),
		publisher: publisher,
	}

	// 启动处理器
	go a.flushProcessor()
	go a.timeoutScanner()

	return a
}

// orderKey 生成订单缓存键
func orderKey(address string, oid int64) string {
	return cast.ToString(address) + "-" + cast.ToString(oid)
}

// AddFill 添加 fill 并更新聚合数据
func (a *OrderAggregator) AddFill(address string, fill hyperliquid.WsOrderFill) {
	key := orderKey(address, fill.Oid)

	// 加载或创建聚合记录
	if actual, loaded := a.orders.LoadOrStore(key, &models.OrderAggregation{
		Oid:          fill.Oid,
		Address:      address,
		Symbol:       fill.Coin,
		OrderStatus:  "open",
		LastFillTime: time.Now().Unix(),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}); loaded {
		// 已存在，追加 fill
		agg := actual.(*models.OrderAggregation)
		agg.Fills = append(agg.Fills, fill)
		agg.TotalSize, agg.WeightedAvgPx = a.calculateWeightedAvg(agg.Fills)
		agg.LastFillTime = time.Now().Unix()
		agg.UpdatedAt = time.Now()
	} else {
		// 新创建，初始化 fills
		agg := actual.(*models.OrderAggregation)
		agg.Fills = []hyperliquid.WsOrderFill{fill}
		agg.TotalSize = cast.ToFloat64(fill.Sz)
		agg.WeightedAvgPx = cast.ToFloat64(fill.Px)
	}

	// 持久化到数据库
	a.persistOrder(key)
}

// calculateWeightedAvg 计算加权平均价
func (a *OrderAggregator) calculateWeightedAvg(fills []hyperliquid.WsOrderFill) (totalSize, avgPx float64) {
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
func (a *OrderAggregator) persistOrder(key string) {
	if actual, ok := a.orders.Load(key); ok {
		agg := actual.(*models.OrderAggregation)
		if err := dao.OrderAggregation().Update(agg); err != nil {
			logger.Error().Err(err).Int64("oid", agg.Oid).Msg("persist order failed")
		}
	}
}

// UpdateStatus 更新订单状态
func (a *OrderAggregator) UpdateStatus(address string, oid int64, status string) {
	key := orderKey(address, oid)
	if actual, ok := a.orders.Load(key); ok {
		agg := actual.(*models.OrderAggregation)
		agg.OrderStatus = status
		agg.UpdatedAt = time.Now()

		// 如果订单完成，触发发送
		if status == "filled" || status == "canceled" {
			a.flushChan <- oid
		}
	}
}

// TryFlush 尝试发送订单信号
func (a *OrderAggregator) TryFlush(address string, oid int64) bool {
	key := orderKey(address, oid)

	if actual, ok := a.orders.Load(key); ok {
		agg := actual.(*models.OrderAggregation)
		if agg.SignalSent {
			return false
		}

		// 触发发送
		a.flushChan <- oid
		return true
	}

	return false
}

// flushProcessor 处理发送队列
func (a *OrderAggregator) flushProcessor() {
	for oid := range a.flushChan {
		a.flushOrder(oid)
	}
}

// flushOrder 发送订单信号
func (a *OrderAggregator) flushOrder(oid int64) {
	// 查找订单
	var key string
	var agg *models.OrderAggregation

	a.orders.Range(func(k, v any) bool {
		if orderAgg := v.(*models.OrderAggregation); orderAr.Oid == oid {
			key = k.(string)
			agg = orderAgg
			return false
		}
		return true
	})

	if agg == nil || agg.SignalSent {
		return
	}

	// 构建信号
	signal := a.buildSignal(agg)

	// 1. 先发布到 NATS
	if err := a.publisher.PublishAddressSignal(signal); err != nil {
		logger.Error().Err(err).Int64("oid", oid).Msg("publish signal failed")
		return
	}

	// 2. 再保存到数据库
	if err := dao.Signal().Create(signal); err != nil {
		logger.Error().Err(err).Int64("oid", oid).Msg("save signal failed")
	}

	// 3. 标记已发送
	agg.SignalSent = true
	dao.OrderAggregation().Update(agg)

	logger.Info().
		Int64("oid", oid).
		Str("symbol", signal.Symbol).
		Float64("size", signal.Size).
		Msg("order signal sent")
}

// buildSignal 构建信号
func (a *OrderAggregator) buildSignal(agg *models.OrderAggregation) *nats.HlAddressSignal {
	if len(agg.Fills) == 0 {
		return nil
	}

	firstFill := agg.Fills[0]

	// 判断方向
	var direction, side string
	switch firstFill.Dir {
	case "Open Long":
		direction, side = "open", "LONG"
	case "Open Short":
		direction, side = "open", "SHORT"
	case "Close Long":
		direction, side = "close", "LONG"
	case "Close Short":
		direction, side = "close", "SHORT"
	case "Buy":
		direction, side = "open", "LONG"
	case "Sell":
		direction, side = "close", "LONG"
	}

	return &nats.HlAddressSignal{
		Oid:          agg.Oid,
		Address:      agg.Address,
		Symbol:       agg.Symbol,
		AssetType:    "futures", // TODO: 根据 fill 类型判断
		Direction:    direction,
		Side:         side,
		PositionSize: classifyPositionSize(agg.TotalSize),
		Size:         agg.TotalSize,
		Price:        agg.WeightedAvgPx,
		Timestamp:    firstFill.Time,
	}
}

// classifyPositionSize 分类仓位大小
func classifyPositionSize(size float64) string {
	if size < 10000 {
		return "Small"
	} else if size < 100000 {
		return "Medium"
	}
	return "Large"
}

// timeoutScanner 超时扫描器
func (a *OrderAggregator) timeoutScanner() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		a.scanTimeoutOrders()
	}
}

// scanTimeoutOrders 扫描超时订单
func (a *OrderAggregator) scanTimeoutOrders() {
	now := time.Now().Unix()
	timeoutThreshold := now - int64(a.timeout.Seconds())

	a.orders.Range(func(key, value any) bool {
		agg := value.(*models.OrderAggregation)

		// 未发送且超时
		if !agg.SignalSent && agg.LastFillTime < timeoutThreshold {
			a.flushChan <- agg.Oid
		}
		return true
	})
}

// Close 关闭聚合器
func (a *OrderAggregator) Close() error {
	close(a.flushChan)
	return nil
}
```

**注意：** 上面的代码有一个 bug，`orderAgg` 应该是 `agg`。修复：

```go
a.orders.Range(func(k, v any) bool {
    if agg := v.(*models.OrderAggregation); agg.Oid == oid {
        key = k.(string)
        return false
    }
    return true
})
```

### Step 2: 提交

```bash
git add internal/ws/aggregator.go
git commit -m "feat: 添加订单聚合器核心组件"
```

---

## Task 5: 修改 SubscriptionManager 支持双订阅

**Files:**
- Modify: `internal/ws/subscription.go`

### Step 1: 添加聚合器字段和 orderUpdates 订阅

修改 `internal/ws/subscription.go` 中的 `SubscriptionManager` 结构体：

```go
type SubscriptionManager struct {
	pool       *Pool
	publisher  Publisher
	addresses  map[string]bool
	subs       map[string]*hyperliquid.Subscription
	aggregator *OrderAggregator  // 新增
	mu         sync.RWMutex
	done       chan struct{}
}
```

### Step 2: 修改构造函数

```go
func NewSubscriptionManager(pool *Pool, publisher Publisher) *SubscriptionManager {
	return &SubscriptionManager{
		pool:       pool,
		publisher:  publisher,
		addresses:  make(map[string]bool),
		subs:       make(map[string]*hyperliquid.Subscription),
		aggregator: NewOrderAggregator(publisher, 5*time.Minute),  // 新增
		done:       make(chan struct{}),
	}
}
```

### Step 3: 修改 subscribeAddress 添加 orderUpdates 订阅

```go
func (m *SubscriptionManager) subscribeAddress(addr string) error {
	client := m.pool.Client()
	if client == nil {
		return fmt.Errorf("websocket client is nil")
	}

	// 1. 订阅 orderFills
	fillsSub, err := client.OrderFills(
		hyperliquid.OrderFillsSubscriptionParams{User: addr},
		func(order hyperliquid.WsOrderFills, err error) {
			if err != nil {
				logger.Error().Err(err).Str("address", addr).Msg("order fills callback error")
				return
			}
			m.handleOrderFills(addr, order)
		},
	)
	if err != nil {
		return err
	}

	// 2. 订阅 orderUpdates
	updatesSub, err := client.OrderUpdates(
		hyperliquid.OrderUpdatesSubscriptionParams{User: addr},
		func(orders []hyperliquid.WsOrder, err error) {
			if err != nil {
				logger.Error().Err(err).Str("address", addr).Msg("order updates callback error")
				return
			}
			m.handleOrderUpdates(addr, orders)
		},
	)
	if err != nil {
		fillsSub.Close()
		return err
	}

	m.mu.Lock()
	m.subs[addr+"-fills"] = fillsSub
	m.subs[addr+"-updates"] = updatesSub
	m.mu.Unlock()

	logger.Info().Str("address", addr).Msg("subscribed order fills and updates")

	monitor.GetMetrics().SetAddressesCount(m.AddressCount())

	return nil
}
```

### Step 4: 添加 handleOrderUpdates 方法

```go
func (m *SubscriptionManager) handleOrderUpdates(addr string, orders []hyperliquid.WsOrder) {
	logger.Info().Str("address", addr).Int("count", len(orders)).Msg("received order updates")

	for _, order := range orders {
		// 更新聚合器中的订单状态
		m.aggregator.UpdateStatus(addr, order.Order.Oid, string(order.Status))
	}
}
```

### Step 5: 修改 handleOrderFills 使用聚合器

```go
func (m *SubscriptionManager) handleOrderFills(addr string, order hyperliquid.WsOrderFills) {
	logger.Info().Str("address", addr).Interface("order", order).Msg("received order fills")

	for _, fill := range order.Fills {
		// 添加到聚合器
		m.aggregator.AddFill(addr, fill)
	}
}
```

### Step 6: 修改 Close 方法

```go
func (m *SubscriptionManager) Close() error {
	close(m.done)
	m.aggregator.Close()  // 新增

	m.mu.Lock()
	for _, sub := range m.subs {
		sub.Close()
	}
	m.subs = make(map[string]*hyperliquid.Subscription)
	m.mu.Unlock()

	return m.pool.Close()
}
```

### Step 7: 提交

```bash
git add internal/ws/subscription.go
git commit -m "feat: 支持双订阅模式和订单聚合"
```

---

## Task 6: 扩展 SignalDAO 支持 Create 方法

**Files:**
- Modify: `internal/dao/signal.go`

### Step 1: 添加 Create 方法

在 `internal/dao/signal.go` 中的 `signalDAO` 结构体添加：

```go
// Create 创建信号记录
func (d *signalDAO) Create(signal *nats.HlAddressSignal) error {
	return d.dao.Create(signal)
}
```

### Step 2: 提交

```bash
git add internal/dao/signal.go
git commit -m "feat: 添加信号创建方法"
```

---

## Task 7: 添加监控指标

**Files:**
- Modify: `internal/metrics/metrics.go`

### Step 1: 添加订单聚合相关指标

```go
var (
	// 订单聚合相关
	orderAggregationActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "hl_order_aggregation_active",
		Help: "当前聚合中的订单数量",
	})

	orderFlushTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "hl_order_flush_total",
		Help: "订单发送总数（按触发原因）",
	}, []string{"trigger"})

	orderFillsPerOrder = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "hl_order_fills_per_order",
		Help:    "每个订单的 fill 数量分布",
		Buckets: []float64{1, 2, 3, 5, 10, 20, 50},
	})

	orderUpdatesReceived = promauto.NewCounter(prometheus.CounterOpts{
		Name: "hl_order_updates_received_total",
		Help: "接收到的 orderUpdates 消息总数",
	})
)
```

### Step 2: 添加指标辅助方法

```go
// IncOrderFlush 增加订单发送计数
func IncOrderFlush(trigger string) {
	orderFlushTotal.WithLabelValues(trigger).Inc()
}

// ObserveFillsPerOrder 观察 fill 数量
func ObserveFillsPerOrder(count int) {
	orderFillsPerOrder.Observe(float64(count))
}

// IncOrderUpdates 增加订单更新计数
func IncOrderUpdates(count float64) {
	orderUpdatesReceived.Add(count)
}
```

### Step 3: 提交

```bash
git add internal/metrics/metrics.go
git commit -m "feat: 添加订单聚合监控指标"
```

---

## Task 8: 集成监控指标到聚合器

**Files:**
- Modify: `internal/ws/aggregator.go`

### Step 1: 在 AddFill 中添加指标

```go
func (a *OrderAggregator) AddFill(address string, fill hyperliquid.WsOrderFill) {
	// ... 原有代码 ...

	// 更新活跃订单数
	a.updateActiveCount()

	// 记录 fill 数量
	if actual, ok := a.orders.Load(key); ok {
		agg := actual.(*models.OrderAggregation)
		monitor.ObserveFillsPerOrder(len(agg.Fills))
	}
}

func (a *OrderAggregator) updateActiveCount() {
	count := 0
	a.orders.Range(func(_, v any) bool {
		agg := v.(*models.OrderAggregation)
		if !agg.SignalSent {
			count++
		}
		return true
	})
	monitor.GetMetrics().SetOrderAggregationActive(count)
}
```

### Step 2: 在 flushOrder 中添加指标

```go
func (a *OrderAggregator) flushOrder(oid int64) {
	// ... 发送逻辑后 ...

	// 记录发送指标
	monitor.IncOrderFlush("auto") // 或根据实际触发原因传入
}
```

### Step 3: 提交

```bash
git add internal/ws/aggregator.go
git commit -m "feat: 集成监控指标到订单聚合器"
```

---

## Task 9: 添加配置参数

**Files:**
- Modify: `config/config.go`
- Modify: `cfg.toml`

### Step 1: 添加配置结构

在 `config/config.go` 的 `Config` 结构体中添加：

```go
type Config struct {
	// ... 原有字段 ...

	OrderAggregation OrderAggregationConfig `toml:"order_aggregation"`
}

type OrderAggregationConfig struct {
	Timeout      time.Duration `toml:"timeout"`
	ScanInterval time.Duration `toml:"scan_interval"`
	MaxRetry     int           `toml:"max_retry"`
	RetryDelay   time.Duration `toml:"retry_delay"`
}
```

### Step 2: 添加默认值

```go
func Default() *Config {
	return &Config{
		// ... 原有默认值 ...

		OrderAggregation: OrderAggregationConfig{
			Timeout:      5 * time.Minute,
			ScanInterval: 30 * time.Second,
			MaxRetry:     3,
			RetryDelay:   1 * time.Second,
		},
	}
}
```

### Step 3: 在 cfg.toml 中添加配置

```toml
[order_aggregation]
timeout = "5m"
scan_interval = "30s"
max_retry = 3
retry_delay = "1s"
```

### Step 4: 修改 NewOrderAggregator 使用配置

```go
aggregator: NewOrderAggregator(publisher, config.OrderAggregation.Timeout)
```

### Step 5: 提交

```bash
git add config/config.go cfg.toml
git commit -m "feat: 添加订单聚合配置参数"
```

---

## Task 10: 编写单元测试

**Files:**
- Create: `internal/ws/aggregator_test.go`

### Step 1: 创建测试文件

```go
package ws

import (
	"testing"
	"time"

	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/assert"
)

func TestOrderAggregator_AddFill(t *testing.T) {
	publisher := &mockPublisher{}
	aggregator := NewOrderAggregator(publisher, 5*time.Minute)

	address := "0x123"
	fill := hyperliquid.WsOrderFill{
		Oid:  12345,
		Coin: "BTC",
		Sz:   "1.5",
		Px:   "50000",
		Dir:  "Open Long",
		Time: time.Now().Unix(),
	}

	aggregator.AddFill(address, fill)

	// 验证缓存
	key := orderKey(address, fill.Oid)
	actual, ok := aggregator.orders.Load(key)
	assert.True(t, ok)

	agg := actual.(*models.OrderAggregation)
	assert.Equal(t, int64(12345), agg.Oid)
	assert.Equal(t, "BTC", agg.Symbol)
	assert.Equal(t, 1.5, agg.TotalSize)
}

func TestOrderAggregator_CalculateWeightedAvg(t *testing.T) {
	aggregator := &OrderAggregator{}

	fills := []hyperliquid.WsOrderFill{
		{Sz: "1.0", Px: "50000"},
		{Sz: "2.0", Px: "51000"},
	}

	totalSize, avgPx := aggregator.calculateWeightedAvg(fills)
	assert.Equal(t, 3.0, totalSize)
	assert.InDelta(t, 50666.67, avgPx, 0.01)
}

type mockPublisher struct{}

func (m *mockPublisher) PublishAddressSignal(signal *nats.HlAddressSignal) error {
	return nil
}
```

### Step 2: 运行测试

```bash
go test ./internal/ws/... -v
```

### Step 3: 提交

```bash
git add internal/ws/aggregator_test.go
git commit -m "test: 添加订单聚合器单元测试"
```

---

## Task 11: 本地测试验证

### Step 1: 构建项目

```bash
make build
```

### Step 2: 使用本地配置启动

```bash
./hl_monitor -config cfg.local.toml
```

### Step 3: 观察日志

检查以下日志：
- "subscribed order fills and updates"
- "received order fills"
- "received order updates"
- "order signal sent"

### Step 4: 验证数据库

```sql
SELECT * FROM hl_order_aggregation;
SELECT * FROM hl_address_signal ORDER BY created_at DESC LIMIT 10;
```

### Step 5: 验证 Prometheus 指标

访问 `http://localhost:9090/metrics`，检查：
- `hl_order_aggregation_active`
- `hl_order_flush_total`
- `hl_order_fills_per_order`

---

## Task 12: 更新文档

**Files:**
- Modify: `README.md`

### Step 1: 添加订单聚合功能说明

在 README.md 的 "核心功能" 部分添加：

```markdown
### 订单聚合

- **双订阅模式**：同时订阅 orderFills 和 orderUpdates
- **智能聚合**：将同一订单的多次撮合聚合成完整订单
- **双触发机制**：
  - 订单状态完成触发（filled/canceled）
  - 5 分钟超时自动触发
- **数据持久化**：订单聚合状态保存到数据库，服务重启可恢复
- **优先级**：先发布 NATS 信号，后保存数据库
```

### Step 2: 添加监控指标说明

```markdown
### 监控指标

| 指标名称 | 说明 |
|---------|------|
| hl_order_aggregation_active | 当前聚合中的订单数 |
| hl_order_flush_total | 订单发送总数（按触发原因） |
| hl_order_fills_per_order | 每个订单的 fill 数量分布 |
```

### Step 3: 提交

```bash
git add README.md
git commit -m "docs: 更新订单聚合功能说明"
```

---

## Task 13: 代码审查与优化

### Step 1: 运行代码检查

```bash
go vet ./...
go fmt ./...
golangci-lint run
```

### Step 2: 性能优化检查

- [ ] 检查 sync.Map 性能是否满足需求
- [ ] 检查数据库连接池配置
- [ ] 检查 flushChan 缓冲大小

### Step 3: 错误处理检查

- [ ] 所有关键路径都有错误处理
- [ ] 日志级别合理
- [ ] 监控覆盖完整

---

## Task 14: 最终提交与合并

### Step 1: 查看所有变更

```bash
git status
git diff
```

### Step 2: 创建最终提交

```bash
git add .
git commit -m "feat: 实现订单聚合功能

- 支持双订阅模式 (orderFills + orderUpdates)
- 实现订单聚合器，聚合多次撮合成交
- 支持状态完成触发和超时触发
- 数据持久化，服务重启可恢复
- 添加监控指标和配置参数
- 先发布 NATS，后保存数据库
"
```

### Step 3: 推送到远程

```bash
git push origin master
```

---

## 测试清单

- [ ] 数据库表创建成功
- [ ] gorm-gen 代码生成正确
- [ ] DAO 单元测试通过
- [ ] 聚合器单元测试通过
- [ ] 本地启动无错误
- [ ] orderFills 订阅正常
- [ ] orderUpdates 订阅正常
- [ ] 订单聚合逻辑正确
- [ ] 状态完成触发正确
- [ ] 超时触发正确
- [ ] NATS 发布成功
- [ ] 数据库保存成功
- [ ] 监控指标正确
- [ ] 配置加载正确
- [ ] 服务重启恢复正确

---

## 完成标准

1. 所有单元测试通过
2. 本地启动运行正常
3. 日志输出符合预期
4. 数据库记录正确
5. NATS 信号正确发送
6. 监控指标正常采集
7. 文档完整更新