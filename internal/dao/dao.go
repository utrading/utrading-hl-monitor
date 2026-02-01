package dao

import (
	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"gorm.io/gorm"
)

// InitDAO 初始化所有 DAO（应用启动时调用）
func InitDAO(db *gorm.DB) {
	gen.SetDefault(db)
	InitPositionDAO(db)
	InitWatchAddressDAO(db)
	InitOrderAggregationDAO(db)
	InitSignalDAO(db)
}
