package models

import "time"

// HlAddressSignal HL地址信号表
type HlAddressSignal struct {
	ID uint `gorm:"primaryKey;autoIncrement" json:"id"`

	// 地址信息
	Address      string  `gorm:"type:varchar(42);not null;index:idx_address;comment:监控地址" json:"address"`
	PositionRate float64 `gorm:"type:decimal(18,3);not null;comment:仓位比例: 百分比，如 0.155 表示 15.5%" json:"position_rate"`
	CloseRate    float64 `gorm:"type:decimal(18,3);not null;default:0;comment:平仓比例: 平仓数量/当前仓位" json:"close_rate"`

	// 交易信息
	Symbol    string  `gorm:"type:varchar(24);not null;index;comment:交易对" json:"symbol"`
	CoinType  string  `gorm:"type:varchar(8);not null" json:"coin_type"`
	AssetType string  `gorm:"type:varchar(24);not null;index;comment:资产类型: spot/futures" json:"asset_type"`
	Direction string  `gorm:"type:varchar(8);not null;comment:仓位方向 open/close" json:"direction"`
	Side      string  `gorm:"type:varchar(8);not null;comment:方向: LONG/SHORT" json:"side"`
	Price     float64 `gorm:"type:decimal(28,12);not null;comment:价格" json:"price"`
	Size      float64 `gorm:"type:decimal(18,8);not null;comment:数量" json:"size"`

	// 时间字段
	CreatedAt time.Time `gorm:"autoCreateTime;index:idx_created;comment:创建时间" json:"created_at"`
	ExpiredAt time.Time `gorm:"not null;index;comment:过期时间(7天后)" json:"expired_at"`
}

// TableName 指定表名
func (HlAddressSignal) TableName() string {
	return "hl_address_signals"
}
