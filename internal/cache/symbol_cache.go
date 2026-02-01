package cache

import (
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

// SymbolCache Symbol 转换缓存（支持双向映射）
type SymbolCache struct {
	spotNameToSymbol *concurrent.Map[string, string] // assetName (@123) -> symbol
	spotSymbolToName *concurrent.Map[string, string] // symbol -> assetName (@123)
	perpNameToSymbol *concurrent.Map[string, string] // assetName (BTC) -> symbol
	perpSymbolToName *concurrent.Map[string, string] // symbol -> assetName (BTC)
}

// NewSymbolCache 创建 Symbol 缓存
func NewSymbolCache() *SymbolCache {
	return &SymbolCache{
		spotNameToSymbol: &concurrent.Map[string, string]{},
		spotSymbolToName: &concurrent.Map[string, string]{},
		perpNameToSymbol: &concurrent.Map[string, string]{},
		perpSymbolToName: &concurrent.Map[string, string]{},
	}
}

// GetSpotSymbol 获取现货 symbol
// assetName: 如 "@123"
func (c *SymbolCache) GetSpotSymbol(assetName string) (string, bool) {
	return c.spotNameToSymbol.Load(assetName)
}

// GetSpotName 根据 symbol 获取现货 assetName
// symbol: 如 "@123-BTC"
func (c *SymbolCache) GetSpotName(symbol string) (string, bool) {
	return c.spotSymbolToName.Load(symbol)
}

// SetSpotSymbol 设置现货 symbol（同时维护正向和反向索引）
func (c *SymbolCache) SetSpotSymbol(assetName string, symbol string) {
	c.spotNameToSymbol.Store(assetName, symbol)
	c.spotSymbolToName.Store(symbol, assetName)
}

// GetPerpSymbol 获取合约 symbol
// assetName: 如 "BTC"
func (c *SymbolCache) GetPerpSymbol(assetName string) (string, bool) {
	return c.perpNameToSymbol.Load(assetName)
}

// GetPerpName 根据 symbol 获取合约 assetName
// symbol: 如 "BTC-USD"
func (c *SymbolCache) GetPerpName(symbol string) (string, bool) {
	return c.perpSymbolToName.Load(symbol)
}

// SetPerpSymbol 设置合约 symbol（同时维护正向和反向索引）
func (c *SymbolCache) SetPerpSymbol(assetName, symbol string) {
	c.perpNameToSymbol.Store(assetName, symbol)
	c.perpSymbolToName.Store(symbol, assetName)
}

// Stats 获取统计信息
func (c *SymbolCache) Stats() map[string]interface{} {
	return map[string]interface{}{
		"spot_name_to_symbol_count": c.spotNameToSymbol.Len(),
		"spot_symbol_to_name_count": c.spotSymbolToName.Len(),
		"perp_name_to_symbol_count": c.perpNameToSymbol.Len(),
		"perp_symbol_to_name_count": c.perpSymbolToName.Len(),
	}
}
