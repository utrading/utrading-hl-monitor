package dao

import (
	"gorm.io/gorm/clause"

	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"github.com/utrading/utrading-hl-monitor/internal/models"
)

type PositionDAO struct{}

var _position = &PositionDAO{}

// Position 获取 PositionDAO 单例
func Position() *PositionDAO {
	return _position
}

// UpsertPositionCache 更新或插入仓位缓存
func (d *PositionDAO) UpsertPositionCache(cache *models.HlPositionCache) error {
	db := gen.HlPositionCache.UnderlyingDB()
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "address"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"spot_balances", "spot_total_usd",
			"futures_positions", "account_value",
			"total_margin_used", "total_ntl_pos", "withdrawable",
			"updated_at",
		}),
	}).Create(cache).Error
}

// BatchUpsertPositionCache 批量 upsert 仓位缓存
func (d *PositionDAO) BatchUpsertPositionCache(caches []*models.HlPositionCache) error {
	db := gen.HlPositionCache.UnderlyingDB()
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "address"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"spot_balances", "spot_total_usd", "futures_positions",
			"account_value", "total_margin_used", "total_ntl_pos",
			"withdrawable", "updated_at",
		}),
	}).Create(caches).Error
}

// GetPositionCache 获取指定地址的仓位缓存
func (d *PositionDAO) GetPositionCache(address string) (*models.HlPositionCache, error) {
	cache, err := gen.HlPositionCache.Where(gen.HlPositionCache.Address.Eq(address)).First()
	if err != nil {
		return nil, err
	}

	return cache, nil
}
