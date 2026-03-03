package models

// PairConfig 交易对配置表
type PairConfig struct {
	ID       uint   `gorm:"primaryKey;column:id;unsigned"`
	Symbol   string `gorm:"size:32;column:symbol;not null;uniqueIndex:symbol_idx"`
	Platform string `gorm:"size:32;column:platform;uniqueIndex:symbol_idx"`

	Category uint8 `gorm:"default:4;column:category"` // 交易对分类：1,2,3,4,5
}

// TableName 指定表名
func (PairConfig) TableName() string {
	return "pair_configs"
}
