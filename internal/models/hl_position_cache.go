package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// HlPositionCache Hyperliquid仓位缓存表
type HlPositionCache struct {
	ID      uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	Address string `gorm:"type:varchar(42);not null;uniqueIndex:uidx_address;comment:链上地址" json:"address"`

	// 现货数据（JSON 存储）
	SpotBalances string `gorm:"type:json;comment:现货余额JSON" json:"-"` // 内部使用
	SpotTotalUSD string `gorm:"type:varchar(32);not null;default:'0';comment:现货总价值USD" json:"spot_total_usd"`

	// 合约数据（JSON 存储）
	FuturesPositions string `gorm:"type:json;comment:合约仓位JSON" json:"-"` // 内部使用
	AccountValue     string `gorm:"type:varchar(32);not null;default:'0';comment:账户总价值" json:"account_value"`
	TotalMarginUsed  string `gorm:"type:varchar(32);not null;default:'0';comment:总保证金使用" json:"total_margin_used"`
	TotalNtlPos      string `gorm:"type:varchar(32);not null;default:'0';comment:总净仓位" json:"total_ntl_pos"`
	Withdrawable     string `gorm:"type:varchar(32);not null;default:'0';comment:可提取金额" json:"withdrawable"`

	// 缓存控制
	UpdatedAt time.Time `gorm:"not null;index:idx_updated;comment:更新时间" json:"updated_at"`
}

func (HlPositionCache) TableName() string {
	return "hl_position_cache"
}

// SpotBalancesData 现货余额数据结构
type SpotBalancesData []SpotBalanceItem

// SpotBalanceItem 现货余额项
type SpotBalanceItem struct {
	Coin     string `json:"coin"`
	Total    string `json:"total"`
	Hold     string `json:"hold"`
	EntryNtl string `json:"entry_ntl"`
}

// FuturesPositionsData 合约仓位数据结构
type FuturesPositionsData []PositionItem

// PositionItem 合约仓位项
type PositionItem struct {
	Coin           string       `json:"coin"`
	Szi            string       `json:"szi"`
	EntryPx        *string      `json:"entry_px"`
	UnrealizedPnl  string       `json:"unrealized_pnl"`
	Leverage       LeverageItem `json:"leverage"`
	MarginUsed     string       `json:"margin_used"`
	PositionValue  string       `json:"position_value"`
	ReturnOnEquity string       `json:"return_on_equity"`
}

// LeverageItem 杠杆信息
type LeverageItem struct {
	Type  string `json:"type"`
	Value int    `json:"value"`
}

// Scan 实现 sql.Scanner 接口（用于读取 JSON 字段）
func (s *SpotBalancesData) Scan(value interface{}) error {
	if value == nil {
		*s = make(SpotBalancesData, 0)
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, s)
}

// Value 实现 driver.Valuer 接口（用于写入 JSON 字段）
func (s SpotBalancesData) Value() (driver.Value, error) {
	if len(s) == 0 {
		return nil, nil
	}
	return json.Marshal(s)
}

// Scan 实现 sql.Scanner 接口
func (f *FuturesPositionsData) Scan(value interface{}) error {
	if value == nil {
		*f = make(FuturesPositionsData, 0)
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(bytes, f)
}

// Value 实现 driver.Valuer 接口
func (f FuturesPositionsData) Value() (driver.Value, error) {
	if len(f) == 0 {
		return nil, nil
	}
	return json.Marshal(f)
}
