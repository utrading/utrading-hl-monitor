package dao

import (
	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
)

type ActiveAddressDAO struct{}

var _activeAddress = &ActiveAddressDAO{}

// ActiveAddress 获取 ActiveAddressDAO 单例
func ActiveAddress() *ActiveAddressDAO {
	return _activeAddress
}

// ListDistinctAddresses 获取去重后的活跃地址列表
func (d *ActiveAddressDAO) ListDistinctAddresses() ([]string, error) {
	var addresses []string
	err := gen.HlActiveAddress.
		Select(gen.HlActiveAddress.Address).
		Distinct(gen.HlActiveAddress.Address).
		Scan(&addresses)
	return addresses, err
}
