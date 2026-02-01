package dao

import (
	"sync"

	"gorm.io/gorm"

	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
)

type WatchAddressDAO struct {
	db *gorm.DB
}

var (
	_watchAddress     *WatchAddressDAO
	_watchAddressOnce sync.Once
)

// InitWatchAddressDAO 初始化 WatchAddressDAO
func InitWatchAddressDAO(db *gorm.DB) {
	_watchAddressOnce.Do(func() {
		_watchAddress = &WatchAddressDAO{
			db: db,
		}
	})
}

// WatchAddress 获取 WatchAddressDAO 单例
func WatchAddress() *WatchAddressDAO {
	return _watchAddress
}

// ListDistinctAddresses 获取去重后的地址列表（用于地址加载器）
func (d *WatchAddressDAO) ListDistinctAddresses() ([]string, error) {
	var addresses []string
	err := gen.HlWatchAddress.
		Select(gen.HlWatchAddress.Address).
		Distinct(gen.HlWatchAddress.Address).
		Scan(&addresses)
	return addresses, err
}
