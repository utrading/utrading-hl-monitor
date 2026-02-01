package processor

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// BatchItem 批量写入项接口
type BatchItem interface {
	TableName() string
	DedupKey() string // 返回去重键
}

// PositionCacheItem 仓位缓存项
type PositionCacheItem struct {
	Address string
	Cache   *models.HlPositionCache
}

func (i PositionCacheItem) TableName() string {
	return "hl_position_cache"
}

// DedupKey 返回去重键（基于 address）
func (i PositionCacheItem) DedupKey() string {
	return "pc:" + i.Address // pc = position cache
}

// OrderAggregationItem 订单聚合项
type OrderAggregationItem struct {
	Aggregation *models.OrderAggregation
}

func (i OrderAggregationItem) TableName() string {
	return "hl_order_aggregation"
}

func (i OrderAggregationItem) DedupKey() string {
	return fmt.Sprintf("oa:%d:%s:%s",
		i.Aggregation.Oid,
		i.Aggregation.Address,
		i.Aggregation.Direction)
}

// BatchWriterConfig 批量写入配置
type BatchWriterConfig struct {
	BatchSize     int           // 批量大小（默认 100）
	FlushInterval time.Duration // 刷新间隔（默认 100ms）
	MaxQueueSize  int           // 最大队列大小（默认 10000）
}

// BatchWriter 批量写入器
// 将数据库写入操作批量执行，降低 IO 压力
type BatchWriter struct {
	config    *BatchWriterConfig
	queue     chan BatchItem
	buffers   concurrent.Map[string, BatchItem] // 按 dedupKey 分组（去重）
	flushTick *time.Ticker
	done      chan struct{}
	wg        sync.WaitGroup
}

// NewBatchWriter 创建批量写入器
func NewBatchWriter(config *BatchWriterConfig) *BatchWriter {
	if config == nil {
		config = &BatchWriterConfig{}
	}

	if config.BatchSize <= 0 {
		config.BatchSize = 100
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = 2 * time.Second
	}
	if config.MaxQueueSize <= 0 {
		config.MaxQueueSize = 10000
	}

	return &BatchWriter{
		config:  config,
		queue:   make(chan BatchItem, config.MaxQueueSize),
		buffers: concurrent.Map[string, BatchItem]{},
		done:    make(chan struct{}),
	}
}

// Start 启动批量写入器
func (w *BatchWriter) Start() {
	w.flushTick = time.NewTicker(w.config.FlushInterval)

	// 启动接收协程
	w.wg.Add(1)
	go w.receiveLoop()

	// 启动刷新协程
	w.wg.Add(1)
	go w.flushLoop()
}

func (w *BatchWriter) receiveLoop() {
	defer w.wg.Done()
	for {
		select {
		case item := <-w.queue:
			key := item.DedupKey()
			w.buffers.Store(key, item) // 直接覆盖，Len() 自动维护

			// 检查是否达到批量大小
			if w.buffers.Len() >= int64(w.config.BatchSize) {
				w.flushAll()
			}
		case <-w.done:
			// 处理队列中剩余的数据
			for len(w.queue) > 0 {
				item := <-w.queue
				key := item.DedupKey()
				w.buffers.Store(key, item)
			}
			return
		}
	}
}

func (w *BatchWriter) flushLoop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.flushTick.C:
			w.flushAll()
		case <-w.done:
			w.flushAll()
			return
		}
	}
}

// flush 刷新指定表
func (w *BatchWriter) flush(tables ...string) {
	if len(tables) == 0 {
		return
	}

	// 按 table 分组收集数据
	grouped := make(map[string][]BatchItem)
	var keysToDelete []string

	w.buffers.Range(func(key string, item BatchItem) bool {
		table := item.TableName()

		// 检查是否需要刷新此表
		for _, t := range tables {
			if t == table {
				grouped[table] = append(grouped[table], item)
				keysToDelete = append(keysToDelete, key)
				break
			}
		}
		return true
	})

	// 执行批量 upsert
	for table, items := range grouped {
		if err := w.batchUpsert(table, items); err != nil {
			logger.Error().Err(err).Str("table", table).Int("count", len(items)).Msg("batch upsert failed")
		} else {
			logger.Debug().Str("table", table).Int("count", len(items)).Msg("batch upsert success")
		}
	}

	// 删除已刷新的数据
	for _, key := range keysToDelete {
		w.buffers.Delete(key)
	}
}

// flushAll 刷新所有表
func (w *BatchWriter) flushAll() {
	//tables := make(map[string]bool)
	//w.buffers.Range(func(key string, item BatchItem) bool {
	//	tables[item.TableName()] = true
	//	return true
	//})
	//
	//tableList := make([]string, 0, len(tables))
	//for table := range tables {
	//	tableList = append(tableList, table)
	//}

	tableList := []string{
		"hl_position_cache",
		"hl_order_aggregation",
	}

	w.flush(tableList...)
}

// batchUpsert 使用 gorm-gen 执行批量 upsert
func (w *BatchWriter) batchUpsert(table string, items []BatchItem) error {
	switch table {
	case "hl_position_cache":
		return w.batchUpsertPositions(items)
	case "hl_order_aggregation":
		return w.batchUpsertOrderAggregations(items)
	default:
		logger.Warn().Str("table", table).Msg("unsupported table for batch upsert")
		return nil // 不阻塞未知表
	}
}

// batchUpsertPositions 批量 upsert 仓位缓存
func (w *BatchWriter) batchUpsertPositions(items []BatchItem) error {
	caches := make([]*models.HlPositionCache, 0, len(items))
	for _, item := range items {
		if pos, ok := item.(PositionCacheItem); ok {
			caches = append(caches, pos.Cache)
		}
	}

	if len(caches) == 0 {
		return nil
	}

	// 调用 DAO 层批量 upsert
	return dao.Position().BatchUpsertPositionCache(caches)
}

// batchUpsertOrderAggregations 批量 upsert 订单聚合
func (w *BatchWriter) batchUpsertOrderAggregations(items []BatchItem) error {
	aggs := make([]*models.OrderAggregation, 0, len(items))
	for _, item := range items {
		if agg, ok := item.(OrderAggregationItem); ok {
			aggs = append(aggs, agg.Aggregation)
		}
	}

	if len(aggs) == 0 {
		return nil
	}

	// 调用 DAO 层批量 upsert
	return dao.OrderAggregation().BatchUpsert(aggs)
}

// Add 添加写入项
func (w *BatchWriter) Add(item BatchItem) error {
	select {
	case w.queue <- item:
		return nil
	default:
		return ErrQueueFull
	}
}

// Stop 停止写入器
func (w *BatchWriter) Stop() {
	// 1. 通知协程退出
	close(w.done)

	// 2. 等待协程处理完队列数据并退出
	w.wg.Wait()

	// 3. 刷新所有缓冲数据
	w.flushAll()

	// 4. 停止定时器
	if w.flushTick != nil {
		w.flushTick.Stop()
	}
}

// GracefulShutdown 优雅关闭，带超时控制
func (w *BatchWriter) GracefulShutdown(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		logger.Warn().Dur("timeout", timeout).Msg("batch writer shutdown timeout, forcing flush")
		w.flushAll()
		return ErrShutdownTimeout
	}
}

// ErrQueueFull 队列满错误
var ErrQueueFull = errors.New("message queue full")

// ErrShutdownTimeout 关闭超时错误
var ErrShutdownTimeout = errors.New("shutdown timeout")
