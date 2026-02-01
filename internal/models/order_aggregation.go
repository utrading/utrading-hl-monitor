package models

import (
	"time"

	"github.com/sonirico/go-hyperliquid"
)

// OrderAggregation 订单聚合状态
type OrderAggregation struct {
	ID        int64  `gorm:"column:id;primaryKey" json:"id"`
	Oid       int64  `gorm:"column:oid;not null;uniqueIndex:uidx_addr_oid_dir;" json:"oid"`
	Address   string `gorm:"column:address;type:varchar(42);not null;uniqueIndex:uidx_addr_oid_dir" json:"address"`
	Direction string `gorm:"column:direction;type:varchar(16);not null;uniqueIndex:uidx_addr_oid_dir;" json:"direction"` // 订单方向
	Symbol    string `gorm:"column:symbol;type:varchar(24);not null" json:"symbol"`

	// 聚合数据
	Fills         []hyperliquid.WsOrderFill `gorm:"column:fills;type:json;not null;serializer:json" json:"fills"`
	TotalSize     float64                   `gorm:"column:total_size;not null;default:0" json:"total_size"`
	WeightedAvgPx float64                   `gorm:"column:weighted_avg_px;not null;default:0" json:"weighted_avg_px"`

	// 状态控制
	OrderStatus  string `gorm:"column:order_status;type:varchar(64);not null;default:open" json:"order_status"`
	LastFillTime int64  `gorm:"column:last_fill_time;not null;index" json:"last_fill_time"`

	// 处理标记
	SignalSent bool `gorm:"column:signal_sent;not null;default:false;index:idx_signal_sent" json:"signal_sent"`

	// 时间字段
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName 指定表名
func (OrderAggregation) TableName() string {
	return "hl_order_aggregation"
}
