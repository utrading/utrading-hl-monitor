package dao

import (
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/internal/nats"
)

type SignalDAO struct{}

var _signal = &SignalDAO{}

// Signal 获取 SignalDAO 单例
func Signal() *SignalDAO {
	return _signal
}

// Create 保存信号到数据库
// 将 NATS 的 HlAddressSignal 转换为数据库模型并保存
func (d *SignalDAO) Create(natsSignal *nats.HlAddressSignal) error {
	// 计算 7 天后过期时间
	expiredAt := time.Now().AddDate(0, 0, 7)

	dbSignal := &models.HlAddressSignal{
		Address:      natsSignal.Address,
		PositionRate: natsSignal.PositionRate,
		CloseRate:    natsSignal.CloseRate,
		Symbol:       natsSignal.Symbol,
		AssetType:    natsSignal.AssetType,
		Direction:    natsSignal.Direction,
		Side:         natsSignal.Side,
		Price:        natsSignal.Price,
		Size:         natsSignal.Size,
		ExpiredAt:    expiredAt,
	}

	return gen.HlAddressSignal.Create(dbSignal)
}

// DeleteOld 清理过期数据（早于指定时间的记录）
func (d *SignalDAO) DeleteOld(before time.Time) (int64, error) {
	result, err := gen.HlAddressSignal.Where(
		gen.HlAddressSignal.CreatedAt.Lt(before),
	).Delete()

	if err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}

// Count 统计信号总数
func (d *SignalDAO) Count() (int64, error) {
	return gen.HlAddressSignal.Count()
}

// DeleteOldest 删除最旧的 N 条记录
func (d *SignalDAO) DeleteOldest(limit int64) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}

	// 获取最旧记录的 ID 范围
	var oldestID uint
	err := gen.HlAddressSignal.Order(gen.HlAddressSignal.ID).
		Limit(1).
		Select(gen.HlAddressSignal.ID).
		Scan(&oldestID)
	if err != nil {
		return 0, err
	}

	// 计算截止 ID
	var cutoffID uint
	err = gen.HlAddressSignal.Where(
		gen.HlAddressSignal.ID.Gte(oldestID),
	).Order(gen.HlAddressSignal.ID).
		Limit(1).
		Offset(int(limit - 1)).
		Select(gen.HlAddressSignal.ID).
		Scan(&cutoffID)
	if err != nil {
		return 0, err
	}

	// 删除 ID <= cutoffID 的记录
	result, err := gen.HlAddressSignal.Where(
		gen.HlAddressSignal.ID.Lte(cutoffID),
	).Delete()
	if err != nil {
		return 0, err
	}

	return result.RowsAffected, nil
}
