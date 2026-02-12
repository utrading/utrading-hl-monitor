package models

import "time"

// HlActiveAddress HL活跃地址表 (由 trading 服务同步)
type HlActiveAddress struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	ServerID  int       `gorm:"uniqueIndex:uidx_server_addr;not null;comment:服务实例ID" json:"server_id"`
	Address   string    `gorm:"type:varchar(64);uniqueIndex:uidx_server_addr;not null;comment:链上地址" json:"address"`
	CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

func (HlActiveAddress) TableName() string {
	return "hl_active_addresses"
}
