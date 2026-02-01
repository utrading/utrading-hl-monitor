package nats

import (
	"encoding/json"

	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

const TopicHLAddressSignal = "hl_address_signal"

// HlAddressSignal 地址信号消息
type HlAddressSignal struct {
	Address      string  `json:"address"`       // 监控地址
	AssetType    string  `json:"asset_type"`    // spot/futures
	Symbol       string  `json:"symbol"`        // 交易对
	Direction    string  `json:"direction"`     // open/close
	Side         string  `json:"side"`          // LONG/SHORT
	PositionRate float64 `json:"position_rate"` // 仓位比例: 百分比，如 15.50%
	CloseRate    float64 `json:"close_rate"`    // 平仓比例: 平仓数量/当前仓位
	Size         float64 `json:"size"`          // 数量
	Price        float64 `json:"price"`         // 价格
	Timestamp    int64   `json:"timestamp"`     // 时间戳
}

// Marshal 序列化信号
func (s *HlAddressSignal) Marshal() ([]byte, error) {
	data, err := json.Marshal(s)
	if err != nil {
		logger.Error().Err(err).Msg("marshal signal failed")
		return nil, err
	}
	return data, nil
}
