package models

import (
	"time"
	"gorm.io/gorm"
)

type HlWatchAddress struct {
	ID       uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	PlayerID uint   `gorm:"not null;uniqueIndex:uidx_player_addr;index:idx_player;comment:玩家ID" json:"player_id"`
	Address  string `gorm:"type:varchar(42);not null;uniqueIndex:uidx_player_addr;comment:链上地址" json:"address"`

	// 用户自定义信息
	Nickname string `gorm:"type:varchar(64);default:'';comment:自定义昵称" json:"nickname"`
	IsSystem bool   `gorm:"type:tinyint(1);not null;default:0;comment:是否系统地址池" json:"is_system"`

	CreatedAt time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (HlWatchAddress) TableName() string {
	return "hl_watch_addresses"
}
