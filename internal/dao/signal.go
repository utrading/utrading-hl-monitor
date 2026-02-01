package dao

import (
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/internal/nats"
)

type SignalDAO struct {
	db *gorm.DB
}

var (
	_signal     *SignalDAO
	_signalOnce sync.Once
)

// InitSignalDAO 初始化 SignalDAO
func InitSignalDAO(db *gorm.DB) {
	_signalOnce.Do(func() {
		_signal = &SignalDAO{
			db: db,
		}
	})
}

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
