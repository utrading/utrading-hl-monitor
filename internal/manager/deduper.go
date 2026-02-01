package manager

import (
	"fmt"
	"time"

	"github.com/spf13/cast"

	"github.com/utrading/utrading-hl-monitor/internal/cache"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// OrderDeduper 订单去重器
// 使用 cache.DedupCache 实现 TTL 自动过期
type OrderDeduper struct {
	cache *cache.DedupCache
	ttl   time.Duration
}

// NewOrderDeduper 创建订单去重器
func NewOrderDeduper(ttl time.Duration) *OrderDeduper {
	if ttl <= 0 {
		ttl = 30 * time.Minute // 默认 30 分钟
	}

	return &OrderDeduper{
		cache: cache.NewDedupCache(ttl),
		ttl:   ttl,
	}
}

// dedupKey 生成去重键
func (d *OrderDeduper) dedupKey(address string, oid int64, direction string) string {
	return fmt.Sprintf("%s-%d-%s", address, oid, direction)
}

// IsSeen 检查订单是否已处理
func (d *OrderDeduper) IsSeen(address string, oid int64, direction string) bool {
	return d.cache.IsSeen(address, oid, direction)
}

// Mark 标记订单为已处理
func (d *OrderDeduper) Mark(address string, oid int64, direction string) {
	d.cache.Mark(address, oid, direction)
}

// MarkFromAggregation 从 OrderAggregation 记录标记为已处理
func (d *OrderDeduper) MarkFromAggregation(agg *models.OrderAggregation) {
	d.Mark(agg.Address, agg.Oid, agg.Direction)
}

// LoadFromDB 从数据库加载已发送的订单
// 加载 signal_sent=true 且在时间窗口内的订单
func (d *OrderDeduper) LoadFromDB(daoOrder interface{}) error {
	// 使用接口类型，避免直接依赖 dao 包
	if daoOrder == nil {
		return fmt.Errorf("dao not initialized")
	}

	return d.cache.LoadFromDB(daoOrder)
}

// Close 关闭去重器
// go-cache 不需要显式关闭，此方法为兼容性保留
func (d *OrderDeduper) Close() error {
	logger.Debug().Msg("order deduper closed (no-op for go-cache)")
	return nil
}

// GetStats 获取去重器统计信息
func (d *OrderDeduper) GetStats() map[string]any {
	stats := d.cache.Stats()
	return map[string]any{
		"entries":     stats["item_count"],
		"ttl_minutes": cast.ToInt64(d.ttl.Minutes()),
	}
}

// Stats 获取去重器统计信息（实现 cache.DedupCacheInterface）
func (d *OrderDeduper) Stats() map[string]interface{} {
	stats := d.cache.Stats()
	return map[string]interface{}{
		"item_count":  stats["item_count"],
		"ttl_minutes": stats["ttl_minutes"],
	}
}
