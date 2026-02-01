package processor

// Message 消息接口
type Message interface {
	Type() string
}

// OrderFillMessage 订单成交消息
type OrderFillMessage struct {
	Address   string
	Fill      interface{} // hl.WsOrderFill
	Direction string      // "Open Long", "Close Short" 等
}

func (m OrderFillMessage) Type() string { return "order_fill" }

// OrderUpdateMessage 订单状态更新消息
type OrderUpdateMessage struct {
	Address   string
	Oid       int64
	Status    string
	Direction string // 可选，为空时遍历所有方向
}

func (m OrderUpdateMessage) Type() string { return "order_update" }

// PositionUpdateMessage 仓位更新消息
type PositionUpdateMessage struct {
	Address string
	Data    interface{} // hl.WsPositionData
}

func (m PositionUpdateMessage) Type() string { return "position_update" }
