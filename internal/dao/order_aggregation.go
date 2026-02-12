package dao

import (
	"time"

	"gorm.io/gorm/clause"

	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"github.com/utrading/utrading-hl-monitor/internal/models"
)

type OrderAggregationDAO struct{}

var _orderAggregation = &OrderAggregationDAO{}

func OrderAggregation() *OrderAggregationDAO {
	return _orderAggregation
}

// Create 创建订单聚合记录
func (d *OrderAggregationDAO) Create(agg *models.OrderAggregation) error {
	return gen.OrderAggregation.Create(agg)
}

// Update 更新订单聚合记录
func (d *OrderAggregationDAO) Update(agg *models.OrderAggregation) error {
	_, err := gen.OrderAggregation.Where(
		gen.OrderAggregation.Oid.Eq(agg.Oid),
	).Updates(agg)
	return err
}

// Get 根据 Oid 获取订单聚合
func (d *OrderAggregationDAO) Get(oid int64) (*models.OrderAggregation, error) {
	return gen.OrderAggregation.Where(
		gen.OrderAggregation.Oid.Eq(oid),
	).First()
}

// GetPending 获取未发送信号的订单
func (d *OrderAggregationDAO) GetPending() ([]*models.OrderAggregation, error) {
	return gen.OrderAggregation.Where(
		gen.OrderAggregation.SignalSent.Is(false),
	).Find()
}

// GetTimeout 获取超时的订单
func (d *OrderAggregationDAO) GetTimeout(beforeTimestamp int64) ([]*models.OrderAggregation, error) {
	return gen.OrderAggregation.Where(
		gen.OrderAggregation.SignalSent.Is(false),
		gen.OrderAggregation.LastFillTime.Lt(beforeTimestamp),
	).Find()
}

// UpdateStatus 更新订单状态
func (d *OrderAggregationDAO) UpdateStatus(oid int64, status string) error {
	_, err := gen.OrderAggregation.Where(
		gen.OrderAggregation.Oid.Eq(oid),
	).Update(
		gen.OrderAggregation.OrderStatus,
		status,
	)
	return err
}

// MarkSignalSent 标记信号已发送
func (d *OrderAggregationDAO) MarkSignalSent(oid int64) error {
	_, err := gen.OrderAggregation.Where(
		gen.OrderAggregation.Oid.Eq(oid),
	).Update(
		gen.OrderAggregation.SignalSent,
		true,
	)
	return err
}

// DeleteOld 清理过期数据（删除所有早于指定时间的记录）
func (d *OrderAggregationDAO) DeleteOld(beforeTimestamp int64) (int64, error) {
	result, err := gen.OrderAggregation.Where(
		gen.OrderAggregation.UpdatedAt.Lt(time.Unix(beforeTimestamp, 0)),
	).Delete()

	if err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

// GetSentOrdersSince 获取指定时间之后已发送信号的订单
func (d *OrderAggregationDAO) GetSentOrdersSince(since time.Time) ([]*models.OrderAggregation, error) {
	return gen.OrderAggregation.Where(
		gen.OrderAggregation.SignalSent.Is(true),
		gen.OrderAggregation.LastFillTime.Gte(since.Unix()),
	).Find()
}

// BatchUpsert 批量 upsert 订单聚合
// 按 Oid+Address+Direction 复合键冲突处理
func (d *OrderAggregationDAO) BatchUpsert(aggs []*models.OrderAggregation) error {
	db := gen.OrderAggregation.UnderlyingDB()
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "oid"},
			{Name: "address"},
			{Name: "direction"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"symbol", "fills", "total_size", "weighted_avg_px",
			"order_status", "last_fill_time", "updated_at", "signal_sent",
		}),
	}).Create(aggs).Error
}
