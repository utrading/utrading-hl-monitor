package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/utrading/utrading-hl-monitor/pkg/goplus"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// HealthServer HTTP 健康检查和指标服务器
type HealthServer struct {
	addr         string
	subManager   SubscriptionManagerRef
	pool         PoolRef
	publisher    PublisherRef
	server       *http.Server
	mu           sync.RWMutex
	healthy      bool
	healthySince time.Time
	startTime    time.Time
	metrics      *Metrics
}

// PoolRef WebSocket连接池引用接口
type PoolRef interface {
	IsConnected() bool
	IsReconnecting() bool
	GetStats() map[string]any
}

// PublisherRef NATS发布器引用接口
type PublisherRef interface {
	IsConnected() bool
}

// SubscriptionManagerRef 订阅管理器引用接口
type SubscriptionManagerRef interface {
	AddressCount() int
	GetStats() map[string]any
}

// NewHealthServer 创建健康检查服务器
func NewHealthServer(addr string, subManager SubscriptionManagerRef, pool PoolRef, publisher PublisherRef) *HealthServer {
	return &HealthServer{
		addr:         addr,
		subManager:   subManager,
		pool:         pool,
		publisher:    publisher,
		healthy:      true,
		healthySince: time.Now(),
		startTime:    time.Now(),
		metrics:      GetMetrics(),
	}
}

// Start 启动HTTP服务器
func (h *HealthServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// 健康检查端点
	mux.HandleFunc("/health", h.healthHandler)
	mux.HandleFunc("/health/ready", h.readyHandler)
	mux.HandleFunc("/health/live", h.liveHandler)

	// Prometheus指标端点
	mux.Handle("/metrics", promhttp.Handler())

	// 服务状态端点
	mux.HandleFunc("/status", h.statusHandler)

	h.server = &http.Server{
		Addr:         h.addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	logger.Info().Str("addr", h.addr).Msg("health server starting")

	goplus.Go(func() {
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error().Err(err).Msg("health server error")
		}
	})

	logger.Info().Str("addr", h.addr).Msg("health server started")

	return nil
}

// Stop 停止服务器
func (h *HealthServer) Stop(ctx context.Context) error {
	h.mu.Lock()
	h.healthy = false
	h.mu.Unlock()

	return h.server.Shutdown(ctx)
}

// healthHandler 健康检查处理器
func (h *HealthServer) healthHandler(w http.ResponseWriter, r *http.Request) {
	status := h.getHealthStatus()
	code := http.StatusOK
	if !status.Healthy {
		code = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(status)
}

// readyHandler 就绪检查处理器
func (h *HealthServer) readyHandler(w http.ResponseWriter, r *http.Request) {
	ready := h.isReady()
	if !ready {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// liveHandler 存活检查处理器
func (h *HealthServer) liveHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// statusHandler 服务状态处理器
func (h *HealthServer) statusHandler(w http.ResponseWriter, r *http.Request) {
	status := h.getHealthStatus()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// isReady 检查服务是否就绪
func (h *HealthServer) isReady() bool {
	h.mu.RLock()
	healthy := h.healthy
	h.mu.RUnlock()

	if !healthy {
		return false
	}

	// 检查WebSocket连接
	if h.pool != nil && !h.pool.IsConnected() {
		return false
	}

	return true
}

// getHealthStatus 获取健康状态
func (h *HealthServer) getHealthStatus() HealthStatus {
	h.mu.RLock()
	healthy := h.healthy
	healthySince := h.healthySince
	h.mu.RUnlock()

	wsConnected := false
	wsReconnecting := false
	if h.pool != nil {
		wsConnected = h.pool.IsConnected()
		wsReconnecting = h.pool.IsReconnecting()
	}

	natsConnected := false
	if h.publisher != nil {
		natsConnected = h.publisher.IsConnected()
	}

	addressCount := 0
	if h.subManager != nil {
		addressCount = h.subManager.AddressCount()
	}

	return HealthStatus{
		Healthy:      healthy,
		HealthySince: healthySince.Format(time.RFC3339),
		Uptime:       time.Since(h.startTime).String(),
		WebSocket: WebSocketStatus{
			Connected:    wsConnected,
			Reconnecting: wsReconnecting,
		},
		NATS: NATSStatus{
			Connected: natsConnected,
		},
		Addresses: AddressStatus{
			Count: addressCount,
		},
	}
}

// HealthStatus 健康状态结构
type HealthStatus struct {
	Healthy      bool            `json:"healthy"`
	HealthySince string          `json:"healthy_since"`
	Uptime       string          `json:"uptime"`
	WebSocket    WebSocketStatus `json:"websocket"`
	NATS         NATSStatus      `json:"nats"`
	Addresses    AddressStatus   `json:"addresses"`
}

// WebSocketStatus WebSocket连接状态
type WebSocketStatus struct {
	Connected    bool `json:"connected"`
	Reconnecting bool `json:"reconnecting"`
}

// NATSStatus NATS连接状态
type NATSStatus struct {
	Connected bool `json:"connected"`
}

// AddressStatus 地址状态
type AddressStatus struct {
	Count int `json:"count"`
}

// Metrics 指标收集器
type Metrics struct {
	signalsPublished   *prometheus.CounterVec
	signalErrors       *prometheus.CounterVec
	addressesCount     prometheus.Gauge
	websocketConnected prometheus.Gauge
	natsConnected      prometheus.Gauge
	tradeDeduped       prometheus.Counter
	tradeProcessed     *prometheus.CounterVec
	positionsTotal     prometheus.Gauge
	positionUpdates    *prometheus.CounterVec
	marginUpdates      *prometheus.CounterVec
	// 订单聚合相关
	orderAggregationActive prometheus.Gauge
	orderFlushTotal        *prometheus.CounterVec
	orderFillsPerOrder     prometheus.Histogram
	orderUpdatesReceived   prometheus.Counter
	// 连接池管理相关
	poolManagerConnectionCount prometheus.Gauge
	// 缓存相关 (T041)
	cacheHitTotal   *prometheus.CounterVec
	cacheMissTotal  *prometheus.CounterVec
	// 消息队列相关 (T042)
	messageQueueSize      prometheus.Gauge
	messageQueueFullTotal prometheus.Counter
	// 批量写入器相关 (T043)
	batchWriteSize         prometheus.Histogram
	batchWriteDurationSecs prometheus.Histogram
	batchDedupCacheHit     *prometheus.CounterVec
}

// NewMetrics 创建指标收集器
func NewMetrics(namespace string) *Metrics {
	m := &Metrics{
		signalsPublished: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "signals_published_total",
				Help:      "Total number of signals published to NATS",
			},
			[]string{"side", "symbol"},
		),
		signalErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "signal_errors_total",
				Help:      "Total number of signal publish errors",
			},
			[]string{"type"},
		),
		addressesCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "addresses_subscribed",
				Help:      "Current number of subscribed addresses",
			},
		),
		websocketConnected: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "websocket_connected",
				Help:      "WebSocket connection status (1=connected, 0=disconnected)",
			},
		),
		natsConnected: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "nats_connected",
				Help:      "NATS connection status (1=connected, 0=disconnected)",
			},
		),
		tradeDeduped: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "trades_deduplicated_total",
				Help:      "Total number of trades deduplicated",
			},
		),
		tradeProcessed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "trades_processed_total",
				Help:      "Total number of trades processed",
			},
			[]string{"type"}, // opening, closing, ignored
		),
		positionsTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "positions_total",
				Help:      "Total number of positions monitored",
			},
		),
		positionUpdates: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "position_updates_total",
				Help:      "Total number of position updates",
			},
			[]string{"status"}, // success, error
		),
		marginUpdates: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "margin_updates_total",
				Help:      "Total number of account margin updates",
			},
			[]string{"status"}, // success, error
		),
		orderAggregationActive: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "order_aggregation_active",
				Help:      "当前聚合中的订单数量",
			},
		),
		orderFlushTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "order_flush_total",
				Help:      "订单发送总数（按触发原因）",
			},
			[]string{"trigger"},
		),
		orderFillsPerOrder: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "order_fills_per_order",
				Help:      "每个订单的 fill 数量分布",
				Buckets:   []float64{1, 2, 3, 5, 10, 20, 50},
			},
		),
		orderUpdatesReceived: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "order_updates_received_total",
				Help:      "接收到的 orderUpdates 消息总数",
			},
		),
		poolManagerConnectionCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "pool_manager_connection_count",
				Help:      "WebSocket 连接池管理器的当前连接数",
			},
		),
		// 缓存相关 (T041)
		cacheHitTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "cache_hit_total",
				Help:      "缓存命中总数（按缓存类型）",
			},
			[]string{"cache_type"}, // dedup, symbol, price
		),
		cacheMissTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "cache_miss_total",
				Help:      "缓存未命中总数（按缓存类型）",
			},
			[]string{"cache_type"}, // dedup, symbol, price
		),
		// 消息队列相关 (T042)
		messageQueueSize: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "message_queue_size",
				Help:      "消息队列当前大小",
			},
		),
		messageQueueFullTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "message_queue_full_total",
				Help:      "消息队列满事件总数",
			},
		),
		// 批量写入器相关 (T043)
		batchWriteSize: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "batch_write_size",
				Help:      "批量写入大小分布",
				Buckets:   []float64{1, 10, 25, 50, 100, 200, 500},
			},
		),
		batchWriteDurationSecs: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "batch_write_duration_seconds",
				Help:      "批量写入耗时分布（秒）",
				Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
			},
		),
		batchDedupCacheHit: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "batch_dedup_cache_hit_total",
				Help:      "Total number of batch deduplication cache hits",
			},
			[]string{"table"},
		),
	}

	prometheus.MustRegister(
		m.signalsPublished,
		m.signalErrors,
		m.addressesCount,
		m.websocketConnected,
		m.natsConnected,
		m.tradeDeduped,
		m.tradeProcessed,
		m.positionsTotal,
		m.positionUpdates,
		m.marginUpdates,
		m.orderAggregationActive,
		m.orderFlushTotal,
		m.orderFillsPerOrder,
		m.orderUpdatesReceived,
		m.poolManagerConnectionCount,
		// 缓存相关 (T041)
		m.cacheHitTotal,
		m.cacheMissTotal,
		// 消息队列相关 (T042)
		m.messageQueueSize,
		m.messageQueueFullTotal,
		// 批量写入器相关 (T043)
		m.batchWriteSize,
		m.batchWriteDurationSecs,
		m.batchDedupCacheHit,
	)

	return m
}

// SetAddressesCount 设置地址数量
func (m *Metrics) SetAddressesCount(count int) {
	m.addressesCount.Set(float64(count))
}

// SetWebSocketConnected 设置WebSocket连接状态
func (m *Metrics) SetWebSocketConnected(connected bool) {
	if connected {
		m.websocketConnected.Set(1)
	} else {
		m.websocketConnected.Set(0)
	}
}

// SetNATSConnected 设置NATS连接状态
func (m *Metrics) SetNATSConnected(connected bool) {
	if connected {
		m.natsConnected.Set(1)
	} else {
		m.natsConnected.Set(0)
	}
}

// IncSignalsPublished 增加发布的信号计数
func (m *Metrics) IncSignalsPublished(side, symbol string) {
	m.signalsPublished.WithLabelValues(side, symbol).Inc()
}

// IncSignalErrors 增加信号错误计数
func (m *Metrics) IncSignalErrors(errType string) {
	m.signalErrors.WithLabelValues(errType).Inc()
}

// IncTradeDeduped 增加去重交易计数
func (m *Metrics) IncTradeDeduped() {
	m.tradeDeduped.Inc()
}

// IncTradeProcessed 增加处理的交易计数
func (m *Metrics) IncTradeProcessed(tradeType string) {
	m.tradeProcessed.WithLabelValues(tradeType).Inc()
}

// SetPositionsTotal 设置总持仓数
func (m *Metrics) SetPositionsTotal(count int) {
	m.positionsTotal.Set(float64(count))
}

// IncPositionUpdates 增加仓位更新计数
func (m *Metrics) IncPositionUpdates(status string) {
	m.positionUpdates.WithLabelValues(status).Inc()
}

// IncMarginUpdates 增加保证金更新计数
func (m *Metrics) IncMarginUpdates(status string) {
	m.marginUpdates.WithLabelValues(status).Inc()
}

// SetOrderAggregationActive 设置聚合中的订单数量
func (m *Metrics) SetOrderAggregationActive(count int) {
	m.orderAggregationActive.Set(float64(count))
}

// IncOrderFlush 增加订单发送计数
func (m *Metrics) IncOrderFlush(trigger string) {
	m.orderFlushTotal.WithLabelValues(trigger).Inc()
}

// ObserveFillsPerOrder 观察 fill 数量
func (m *Metrics) ObserveFillsPerOrder(count int) {
	m.orderFillsPerOrder.Observe(float64(count))
}

// IncOrderUpdates 增加订单更新计数
func (m *Metrics) IncOrderUpdates(count float64) {
	m.orderUpdatesReceived.Add(count)
}

// SetPoolManagerConnectionCount 设置连接池管理器的连接数
func (m *Metrics) SetPoolManagerConnectionCount(count int) {
	m.poolManagerConnectionCount.Set(float64(count))
}

// IncCacheHit 增加缓存命中计数 (T041)
func (m *Metrics) IncCacheHit(cacheType string) {
	m.cacheHitTotal.WithLabelValues(cacheType).Inc()
}

// IncCacheMiss 增加缓存未命中计数 (T041)
func (m *Metrics) IncCacheMiss(cacheType string) {
	m.cacheMissTotal.WithLabelValues(cacheType).Inc()
}

// SetMessageQueueSize 设置消息队列大小 (T042)
func (m *Metrics) SetMessageQueueSize(size int) {
	m.messageQueueSize.Set(float64(size))
}

// IncMessageQueueFull 增加消息队列满事件计数 (T042)
func (m *Metrics) IncMessageQueueFull() {
	m.messageQueueFullTotal.Inc()
}

// ObserveBatchWriteSize 观察批量写入大小 (T043)
func (m *Metrics) ObserveBatchWriteSize(size int) {
	m.batchWriteSize.Observe(float64(size))
}

// ObserveBatchWriteDuration 观察批量写入耗时 (T043)
func (m *Metrics) ObserveBatchWriteDuration(duration float64) {
	m.batchWriteDurationSecs.Observe(duration)
}

// IncBatchDedupCacheHit 增加批量写入去重缓存命中计数
func (m *Metrics) IncBatchDedupCacheHit(table string) {
	m.batchDedupCacheHit.WithLabelValues(table).Inc()
}

var globalMetrics *Metrics
var metricsMu sync.Once

// GetMetrics 获取全局指标收集器
func GetMetrics() *Metrics {
	metricsMu.Do(func() {
		globalMetrics = NewMetrics("hl_monitor")
	})
	return globalMetrics
}

// InitMetrics 初始化指标收集器（供main使用）
func InitMetrics() {
	GetMetrics()
}

// PoolWrapper WebSocket连接池包装器
type PoolWrapper struct {
	pool interface {
		IsConnected() bool
		IsReconnecting() bool
		GetStats() map[string]any
	}
}

// NewPoolWrapper 创建连接池包装器
func NewPoolWrapper(pool interface {
	IsConnected() bool
	IsReconnecting() bool
	GetStats() map[string]any
}) *PoolWrapper {
	return &PoolWrapper{pool: pool}
}

// IsConnected 检查是否已连接
func (w *PoolWrapper) IsConnected() bool {
	return w.pool.IsConnected()
}

// IsReconnecting 检查是否正在重连
func (w *PoolWrapper) IsReconnecting() bool {
	return w.pool.IsReconnecting()
}

// GetStats 获取连接统计
func (w *PoolWrapper) GetStats() map[string]any {
	return w.pool.GetStats()
}

// PublisherWrapper NATS发布器包装器
type PublisherWrapper struct {
	publisher interface {
		IsConnected() bool
	}
}

// NewPublisherWrapper 创建发布器包装器
func NewPublisherWrapper(publisher interface {
	IsConnected() bool
}) *PublisherWrapper {
	return &PublisherWrapper{publisher: publisher}
}

// IsConnected 检查是否已连接
func (w *PublisherWrapper) IsConnected() bool {
	return w.publisher.IsConnected()
}

// WebSocketPoolRef 接口用于ws.pool
type WebSocketPoolRef interface {
	IsConnected() bool
	IsReconnecting() bool
	GetStats() map[string]any
}

// NATSPublisherRef 接口用于nats.publisher
type NATSPublisherRef interface {
	IsConnected() bool
}

// GetHealthServer 创建健康检查服务器
func GetHealthServer(addr string, subManager SubscriptionManagerRef, pool PoolRef, publisher PublisherRef) *HealthServer {
	return NewHealthServer(addr, subManager, pool, publisher)
}

// NewPoolRef 从*ws.Pool创建PoolRef
func NewPoolRef(pool interface {
	IsConnected() bool
	IsReconnecting() bool
	GetStats() map[string]any
}) PoolRef {
	return pool
}

// NewPublisherRef 从*nats.Publisher创建PublisherRef
func NewPublisherRef(publisher interface {
	IsConnected() bool
}) PublisherRef {
	return publisher
}

var _ = fmt.Println // 避免未使用的导入
