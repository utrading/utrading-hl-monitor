package cache

import (
	"fmt"
	"time"

	"github.com/patrickmn/go-cache"

	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// DedupCache 订单去重缓存，使用 go-cache 实现 TTL 自动过期
type DedupCache struct {
	cache *cache.Cache // go-cache 内置 TTL 和自动清理
	ttl   time.Duration
}

// NewDedupCache 创建订单去重缓存
// ttl: 订单保留时间（建议 30 分钟）
// 清理间隔自动设为 2×TTL
func NewDedupCache(ttl time.Duration) *DedupCache {
	return &DedupCache{
		cache: cache.New(ttl, ttl*2), // 清理间隔 = 2×TTL
		ttl:   ttl,
	}
}

// IsSeen 检查订单是否已处理
func (c *DedupCache) IsSeen(address string, oid int64, direction string) bool {
	key := c.dedupKey(address, oid, direction)
	_, exists := c.cache.Get(key)
	return exists
}

// Mark 标记订单为已处理
func (c *DedupCache) Mark(address string, oid int64, direction string) {
	key := c.dedupKey(address, oid, direction)
	c.cache.Set(key, time.Now(), cache.DefaultExpiration)
}

// dedupKey 生成去重键
// 格式: "address-oid-direction"
func (c *DedupCache) dedupKey(address string, oid int64, direction string) string {
	return fmt.Sprintf("%s-%d-%s", address, oid, direction)
}

type OrderAggregationDAO interface {
	GetSentOrdersSince(time.Time) ([]*models.OrderAggregation, error)
}

// LoadFromDB 从数据库加载已发送订单
// 用于服务启动时恢复去重状态
// 使用接口类型避免循环依赖
func (c *DedupCache) LoadFromDB(daoObj interface{}) error {
	if daoObj == nil {
		return fmt.Errorf("dao is nil")
	}

	dao, ok := daoObj.(OrderAggregationDAO)
	if !ok {
		return fmt.Errorf("invalid dao type")
	}

	since := time.Now().Add(-c.ttl)
	orders, err := dao.GetSentOrdersSince(since)
	if err != nil {
		return fmt.Errorf("get sent orders failed: %w", err)
	}

	count := 0
	for _, order := range orders {
		c.Mark(order.Address, order.Oid, order.Direction)
		count++
	}

	logger.Info().
		Int("count", count).
		Dur("window", c.ttl).
		Msg("loaded sent orders from database")

	return nil
}

// Stats 获取统计信息
func (c *DedupCache) Stats() map[string]interface{} {
	return map[string]interface{}{
		"item_count":  c.cache.ItemCount(),
		"ttl_minutes": c.ttl.Minutes(),
	}
}
