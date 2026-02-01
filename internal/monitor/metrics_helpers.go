package monitor

// 便捷函数供外部调用，无需访问 Metrics 实例

// SetOrderAggregationActive 设置聚合中的订单数量
func SetOrderAggregationActive(count int) {
	GetMetrics().SetOrderAggregationActive(count)
}

// IncOrderFlush 增加订单发送计数
func IncOrderFlush(trigger string) {
	GetMetrics().IncOrderFlush(trigger)
}

// ObserveFillsPerOrder 观察 fill 数量
func ObserveFillsPerOrder(count int) {
	GetMetrics().ObserveFillsPerOrder(count)
}

// SetPoolManagerConnectionCount 设置连接池管理器的连接数
func SetPoolManagerConnectionCount(count int) {
	GetMetrics().SetPoolManagerConnectionCount(count)
}

// IncCacheHit 增加缓存命中计数 (T041)
func IncCacheHit(cacheType string) {
	GetMetrics().IncCacheHit(cacheType)
}

// IncCacheMiss 增加缓存未命中计数 (T041)
func IncCacheMiss(cacheType string) {
	GetMetrics().IncCacheMiss(cacheType)
}

// SetMessageQueueSize 设置消息队列大小 (T042)
func SetMessageQueueSize(size int) {
	GetMetrics().SetMessageQueueSize(size)
}

// IncMessageQueueFull 增加消息队列满事件计数 (T042)
func IncMessageQueueFull() {
	GetMetrics().IncMessageQueueFull()
}

// ObserveBatchWriteSize 观察批量写入大小 (T043)
func ObserveBatchWriteSize(size int) {
	GetMetrics().ObserveBatchWriteSize(size)
}

// ObserveBatchWriteDuration 观察批量写入耗时 (T043)
func ObserveBatchWriteDuration(duration float64) {
	GetMetrics().ObserveBatchWriteDuration(duration)
}

// IncBatchDedupCacheHit 增加批量写入去重缓存命中计数
func IncBatchDedupCacheHit(table string) {
	GetMetrics().IncBatchDedupCacheHit(table)
}
