package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestE2EMultipleAddresses(t *testing.T) {
	upgrader := websocket.Upgrader{}

	// 记录接收到的订阅
	var subscriptions []string
	var subsMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}

			if msg["method"] == "subscribe" {
				sub := msg["subscription"].(map[string]interface{})
				subsMu.Lock()
				subscriptions = append(subscriptions, sub["type"].(string)+":"+sub["user"].(string))
				subsMu.Unlock()

				// 发送确认消息
				response := map[string]interface{}{
					"channel": sub["type"], // response 仍然使用 channel 字段
					"data": map[string]interface{}{
						"user": sub["user"],
					},
				}
				conn.WriteJSON(response)
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	// 创建连接池
	pool := NewPoolManager(wsURL, 2, 2)
	ctx := context.Background()

	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer pool.Close()

	// 订阅多个地址
	addresses := []string{
		"0x399965e15d4e61ec3529cc98b7f7ebb93b733336",
		"0x1234567890abcdef1234567890abcdef12345678",
		"0xabcdefabcdefabcdefabcdefabcdefabcdefab",
	}

	var wg sync.WaitGroup
	messagesReceived := make(map[string]int)
	var msgsMu sync.Mutex

	for _, addr := range addresses {
		wg.Add(1)
		go func(address string) {
			defer wg.Done()

			sub := Subscription{
				Channel: ChannelWebData2,
				User:    address,
			}

			_, err := pool.Subscribe(sub, func(msg wsMessage) error {
				msgsMu.Lock()
				messagesReceived[address]++
				msgsMu.Unlock()
				return nil
			})

			if err != nil {
				t.Errorf("Subscribe(%s) failed: %v", address, err)
			}
		}(addr)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	// 验证订阅请求
	subsMu.Lock()
	if len(subscriptions) != 3 {
		t.Errorf("expected 3 subscriptions, got %d", len(subscriptions))
	}
	subsMu.Unlock()
}

func TestE2ESubscriptionDeduplication(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var subscribeCount int
	var countMu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}

			if msg["method"] == "subscribe" {
				countMu.Lock()
				subscribeCount++
				countMu.Unlock()
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	pool := NewPoolManager(wsURL, 1, 10)
	ctx := context.Background()

	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer pool.Close()

	addr := "0x399965e15d4e61ec3529cc98b7f7ebb93b733336"
	sub := Subscription{
		Channel: ChannelWebData2,
		User:    addr,
	}

	// 同一个订阅，三个回调
	for i := 0; i < 3; i++ {
		_, err := pool.Subscribe(sub, func(msg wsMessage) error {
			return nil
		})
		if err != nil {
			t.Fatalf("Subscribe() failed: %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// 应该只发送一次订阅请求
	if subscribeCount != 1 {
		t.Errorf("expected 1 subscribe request, got %d", subscribeCount)
	}
}
