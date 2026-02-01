package ws

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDispatcherDispatchWebData2(t *testing.T) {
	pm := NewPoolManager("wss://example.com/ws", 1, 10)

	// 手动添加模拟连接
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)
	pm.connections = append(pm.connections, wrapper)

	// 模拟订阅
	sub := Subscription{
		Channel: ChannelWebData2,
		User:    "0xabc",
	}

	callbackCalled := false
	var receivedMsg wsMessage

	handle, err := pm.Subscribe(sub, func(msg wsMessage) error {
		callbackCalled = true
		receivedMsg = msg
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}
	defer handle.Unsubscribe()

	// 创建测试消息
	data := map[string]interface{}{
		"user": "0xabc",
		"clearinghouseState": map[string]interface{}{
			"accountValue": "100000",
		},
	}

	dataBytes, _ := json.Marshal(data)

	msg := wsMessage{
		Channel: ChannelWebData2,
		Data:    json.RawMessage(dataBytes),
	}

	// 分发消息
	pm.dispatcher.Dispatch(msg)

	// 等待异步回调执行
	time.Sleep(10 * time.Millisecond)

	if !callbackCalled {
		t.Error("callback was not called")
	}

	if receivedMsg.Channel != ChannelWebData2 {
		t.Errorf("channel = %v, want webData2", receivedMsg.Channel)
	}
}

func TestDispatcherDispatchAllMids(t *testing.T) {
	pm := NewPoolManager("wss://example.com/ws", 1, 10)

	// 手动添加模拟连接
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)
	pm.connections = append(pm.connections, wrapper)

	sub := Subscription{
		Channel: ChannelAllMids,
	}

	callbackCalled := false

	handle, err := pm.Subscribe(sub, func(msg wsMessage) error {
		callbackCalled = true
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}
	defer handle.Unsubscribe()

	data := map[string]string{
		"BTC": "50000",
		"ETH": "3000",
	}

	dataBytes, _ := json.Marshal(data)

	msg := wsMessage{
		Channel: ChannelAllMids,
		Data:    json.RawMessage(dataBytes),
	}

	pm.dispatcher.Dispatch(msg)

	// 等待异步回调执行
	time.Sleep(10 * time.Millisecond)

	if !callbackCalled {
		t.Error("callback was not called")
	}
}
