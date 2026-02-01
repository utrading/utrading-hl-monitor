package symbol

import (
	"github.com/sonirico/go-hyperliquid"
	"github.com/utrading/utrading-hl-monitor/internal/cache"
)

// Manager Symbol 管理器（纯容器）
// 统一管理 SymbolCache、Loader 和 PriceCache 的生命周期
type Manager struct {
	symbolCache *cache.SymbolCache
	loader      *Loader
	priceCache  *cache.PriceCache
}

// NewManager 创建 Symbol 管理器
// 首次加载失败会返回错误，确保服务启动时 Symbol 数据可用
func NewManager() (*Manager, error) {
	// 1. 创建缓存
	symbolCache := cache.NewSymbolCache()
	priceCache := cache.NewPriceCache()

	// 2. 创建加载器（立即加载，失败返回错误）
	loader, err := NewLoader(symbolCache, hyperliquid.MainnetAPIURL)
	if err != nil {
		return nil, err
	}

	// 3. 启动后台重载
	loader.Start()

	return &Manager{
		symbolCache: symbolCache,
		loader:      loader,
		priceCache:  priceCache,
	}, nil
}

// Close 关闭管理器，停止后台重载
func (m *Manager) Close() error {
	m.loader.Close()
	return nil
}

// SymbolCache 返回 Symbol 缓存
func (m *Manager) SymbolCache() *cache.SymbolCache {
	return m.symbolCache
}

// PriceCache 返回价格缓存
func (m *Manager) PriceCache() *cache.PriceCache {
	return m.priceCache
}
