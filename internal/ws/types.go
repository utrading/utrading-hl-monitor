// internal/ws/types.go
package ws

import (
	"encoding/json"
	"sync"
)

// 对象池：复用 wsMessage 结构体
var msgPool = sync.Pool{
	New: func() any {
		return &WsMessage{}
	},
}

// 对象池：复用字节缓冲区（用于 JSON 解析）
var bytePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 4096) // 预分配 4KB
		return &b
	},
}

// Channel Hyperliquid WebSocket 频道
type Channel string

const (
	ChannelWebData2      Channel = "webData2"
	ChannelUserFills     Channel = "userFills"
	ChannelOrderUpdates  Channel = "orderUpdates"
	ChannelAllMids       Channel = "allMids"
	ChannelL2Book        Channel = "l2Book"
	ChannelTrades        Channel = "trades"
	ChannelCandle        Channel = "candle"
	ChannelBbo           Channel = "bbo"
	ChannelSpotAssetCtxs Channel = "spotAssetCtxs"
)

// Subscription 订阅请求
type Subscription struct {
	Channel Channel `json:"type"`
	User    string  `json:"user,omitempty"`
	Coin    string  `json:"coin,omitempty"`
}

// Key 返回订阅的唯一键
func (s Subscription) Key() string {
	if s.User != "" {
		return string(s.Channel) + ":" + s.User
	}
	if s.Coin != "" {
		return string(s.Channel) + ":" + s.Coin
	}
	return string(s.Channel)
}

// WsMessage WebSocket 消息
type WsMessage struct {
	Channel Channel         `json:"channel"`
	Data    json.RawMessage `json:"data"`
}

// wsMessage 私有消息类型（内部使用）
type wsMessage = WsMessage

// Callback 消息回调函数
type Callback func(msg WsMessage) error
