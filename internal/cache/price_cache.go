package cache

import (
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

// PriceCache 价格缓存（现货 + 合约）
// 使用 Ristretto 实现 LFU 淘汰和成本控制
type PriceCache struct {
	spotCache concurrent.Map[string, float64] // 现货价格  @1 -> 123.0
	perpCache concurrent.Map[string, float64] // 合约价格 BTC -> 123.0
}

// NewPriceCache 创建价格缓存
func NewPriceCache() *PriceCache {
	return &PriceCache{
		spotCache: concurrent.Map[string, float64]{},
		perpCache: concurrent.Map[string, float64]{},
	}
}

// GetSpotPrice 获取现货价格
func (c *PriceCache) GetSpotPrice(assetName string) (float64, bool) {
	return c.spotCache.Load(assetName)
}

// SetSpotPrice 设置现货价格
func (c *PriceCache) SetSpotPrice(assetName string, price float64) {
	c.spotCache.Store(assetName, price)
}

// GetPerpPrice 获取合约价格
func (c *PriceCache) GetPerpPrice(assetName string) (float64, bool) {
	return c.perpCache.Load(assetName)
}

// SetPerpPrice 设置合约价格
func (c *PriceCache) SetPerpPrice(assetName string, price float64) {
	c.perpCache.Store(assetName, price)
}

// Stats 获取统计信息
func (c *PriceCache) Stats() map[string]interface{} {
	return map[string]interface{}{
		"spot_count": c.spotCache.Len(),
		"perp_count": c.perpCache.Len(),
	}
}
