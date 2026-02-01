package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNewClient(t *testing.T) {
	client := NewClient("wss://example.com/ws")

	if client == nil {
		t.Fatal("NewClient() returned nil")
	}

	if client.url != "wss://example.com/ws" {
		t.Errorf("url = %v, want %v", client.url, "wss://example.com/ws")
	}

	if client.done == nil {
		t.Error("done channel not initialized")
	}
}

func TestNewClientEmptyURL(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewClient with empty URL should panic")
		}
	}()

	NewClient("")
}

func TestClientClose(t *testing.T) {
	client := NewClient("wss://example.com/ws")

	// 第一次关闭应该成功
	err := client.Close()
	if err != nil {
		t.Errorf("first Close() failed: %v", err)
	}

	// 验证 done channel 已关闭
	select {
	case <-client.done:
		// 正常，channel 已关闭
	default:
		t.Error("done channel should be closed after Close()")
	}

	// 第二次关闭应该是安全的（幂等）
	err = client.Close()
	if err != nil {
		t.Errorf("second Close() should be safe: %v", err)
	}
}

func TestClientCloseIdempotent(t *testing.T) {
	client := NewClient("wss://example.com/ws")

	// 多次关闭应该是安全的
	for i := 0; i < 5; i++ {
		if err := client.Close(); err != nil {
			t.Errorf("Close() iteration %d failed: %v", i, err)
		}
	}
}

func TestClientConnect(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("server upgrade failed: %v", err)
		}
		defer conn.Close()

		// 保持连接直到测试结束
		<-time.After(5 * time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	client := NewClient(wsURL)
	ctx := context.Background()

	err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}

	if !client.IsConnected() {
		t.Error("IsConnected() = false, want true")
	}

	client.Close()
}

func TestClientSubscribe(t *testing.T) {
	var receivedMsg map[string]interface{}

	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("server upgrade failed: %v", err)
		}
		defer conn.Close()

		// 读取客户端发送的消息
		err = conn.ReadJSON(&receivedMsg)
		if err != nil {
			t.Fatalf("server read failed: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	client := NewClient(wsURL)
	ctx := context.Background()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer client.Close()

	sub := Subscription{
		Channel: ChannelWebData2,
		User:    "0x123",
	}

	if err := client.Subscribe(sub); err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	// 等待服务器接收消息
	time.Sleep(100 * time.Millisecond)

	if receivedMsg["method"] != "subscribe" {
		t.Errorf("method = %v, want subscribe", receivedMsg["method"])
	}

	subData, ok := receivedMsg["subscription"].(map[string]interface{})
	if !ok {
		t.Fatal("subscription field missing or not a map")
	}

	if subData["type"] != "webData2" {
		t.Errorf("type = %v, want webData2", subData["type"])
	}

	if subData["user"] != "0x123" {
		t.Errorf("user = %v, want 0x123", subData["user"])
	}
}

func TestClientMessageDispatch(t *testing.T) {
	upgrader := websocket.Upgrader{}
	messageReceived := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("server upgrade failed: %v", err)
		}
		defer conn.Close()

		// 发送测试消息
		testMsg := map[string]interface{}{
			"channel": "allMids",
			"data":    map[string]string{"BTC": "50000"},
		}

		time.Sleep(100 * time.Millisecond)
		if err := conn.WriteJSON(testMsg); err != nil {
			t.Fatalf("server write failed: %v", err)
		}

		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	client := NewClient(wsURL)
	client.SetMessageHandler(func(msg wsMessage) error {
		messageReceived = true

		if msg.Channel != ChannelAllMids {
			t.Errorf("channel = %v, want allMids", msg.Channel)
		}

		var data map[string]string
		if err := json.Unmarshal(msg.Data, &data); err != nil {
			t.Errorf("failed to unmarshal data: %v", err)
		}

		if data["BTC"] != "50000" {
			t.Errorf("BTC price = %v, want 50000", data["BTC"])
		}

		return nil
	})

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}
	defer client.Close()

	// 等待消息处理
	time.Sleep(1 * time.Second)

	if !messageReceived {
		t.Error("message handler was not called")
	}
}
