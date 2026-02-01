package ws

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestNewConnectionWrapper(t *testing.T) {
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)

	if wrapper == nil {
		t.Fatal("NewConnectionWrapper() returned nil")
	}

	if wrapper.Client() != client {
		t.Error("Client() returns wrong client")
	}

	if wrapper.SubscriptionCount() != 0 {
		t.Errorf("SubscriptionCount() = %d, want 0", wrapper.SubscriptionCount())
	}
}

func TestConnectionWrapperAddSubscription(t *testing.T) {
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)

	wrapper.AddSubscription("key1")
	wrapper.AddSubscription("key2")

	if wrapper.SubscriptionCount() != 2 {
		t.Errorf("SubscriptionCount() = %d, want 2", wrapper.SubscriptionCount())
	}

	if !wrapper.HasSubscription("key1") {
		t.Error("HasSubscription(key1) = false, want true")
	}
}

func TestConnectionWrapperRemoveSubscription(t *testing.T) {
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)

	wrapper.AddSubscription("key1")
	wrapper.RemoveSubscription("key1")

	if wrapper.SubscriptionCount() != 0 {
		t.Errorf("SubscriptionCount() = %d, want 0", wrapper.SubscriptionCount())
	}

	if wrapper.HasSubscription("key1") {
		t.Error("HasSubscription(key1) = true, want false")
	}
}

func TestNewPoolManager(t *testing.T) {
	pool := NewPoolManager("wss://example.com/ws", 5, 100)

	if pool == nil {
		t.Fatal("NewPoolManager() returned nil")
	}

	if pool.MaxConnections() != 5 {
		t.Errorf("MaxConnections() = %d, want 5", pool.MaxConnections())
	}

	if pool.MaxSubscriptions() != 100 {
		t.Errorf("MaxSubscriptions() = %d, want 100", pool.MaxSubscriptions())
	}

	if pool.ConnectionCount() != 0 {
		t.Errorf("ConnectionCount() = %d, want 0", pool.ConnectionCount())
	}
}

func TestPoolManagerAcquireConnection(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// 保持连接直到测试结束
		<-time.After(5 * time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	pool := NewPoolManager(wsURL, 2, 2)
	ctx := context.Background()

	// 启动连接池创建初始连接
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer pool.Close()

	// 验证已创建连接
	if pool.ConnectionCount() != 1 {
		t.Errorf("After Start(), ConnectionCount() = %d, want 1", pool.ConnectionCount())
	}

	// 测试订阅会复用现有连接
	sub := Subscription{
		Channel: ChannelWebData2,
		User:    "0x123",
	}

	handle1, err := pool.Subscribe(sub, func(msg wsMessage) error { return nil })
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	if handle1 == nil {
		t.Fatal("Subscribe() returned nil handle")
	}

	// 验证订阅成功
	if pool.SubscriptionCount() != 1 {
		t.Errorf("After Subscribe(), SubscriptionCount() = %d, want 1", pool.SubscriptionCount())
	}

	// 第二个订阅应该复用同一连接（未满）
	sub2 := Subscription{
		Channel: ChannelWebData2,
		User:    "0x456",
	}

	handle2, err := pool.Subscribe(sub2, func(msg wsMessage) error { return nil })
	if err != nil {
		t.Fatalf("Second Subscribe() failed: %v", err)
	}

	if handle2 == nil {
		t.Fatal("Second Subscribe() returned nil handle")
	}

	// 验证连接数仍为 1（复用）
	if pool.ConnectionCount() != 1 {
		t.Errorf("After second Subscribe(), ConnectionCount() = %d, want 1 (reused)", pool.ConnectionCount())
	}
}

func TestPoolManagerSubscribe(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// 保持连接直到测试结束
		<-time.After(5 * time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	pool := NewPoolManager(wsURL, 2, 2)
	ctx := context.Background()

	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer pool.Close()

	sub := Subscription{
		Channel: ChannelWebData2,
		User:    "0x123",
	}

	callback := func(msg wsMessage) error {
		return nil
	}

	handle, err := pool.Subscribe(sub, callback)
	if err != nil {
		t.Fatalf("Subscribe() failed: %v", err)
	}

	if handle == nil {
		t.Fatal("Subscribe() returned nil handle")
	}

	if pool.SubscriptionCount() != 1 {
		t.Errorf("SubscriptionCount() = %d, want 1", pool.SubscriptionCount())
	}
}

func TestPoolManagerSubscribeDedup(t *testing.T) {
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// 保持连接直到测试结束
		<-time.After(5 * time.Second)
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[len("http"):]

	pool := NewPoolManager(wsURL, 2, 10)
	ctx := context.Background()

	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer pool.Close()

	sub1 := Subscription{
		Channel: ChannelWebData2,
		User:    "0x162cc7c861ebd0c06b3d72319201150482518185",
	}

	sub2 := Subscription{
		Channel: ChannelWebData2,
		User:    "0x399965e15d4e61ec3529cc98b7f7ebb93b733336",
	}

	// 订阅两个不同的用户
	_, err1 := pool.Subscribe(sub1, func(msg wsMessage) error { return nil })
	_, err2 := pool.Subscribe(sub2, func(msg wsMessage) error { return nil })

	if err1 != nil || err2 != nil {
		t.Fatalf("Subscribe() failed: %v, %v", err1, err2)
	}

	// 应该有两个订阅
	if pool.SubscriptionCount() != 2 {
		t.Errorf("SubscriptionCount() = %d, want 2", pool.SubscriptionCount())
	}

	// 订阅相同用户应该去重（添加第二个回调）
	handle3, err3 := pool.Subscribe(sub1, func(msg wsMessage) error { return nil })
	if err3 != nil {
		t.Fatalf("Third Subscribe() failed: %v", err3)
	}

	if handle3 == nil {
		t.Fatal("Third Subscribe() returned nil handle")
	}

	// 订阅数仍应为 2（去重）
	if pool.SubscriptionCount() != 2 {
		t.Errorf("After duplicate Subscribe(), SubscriptionCount() = %d, want 2 (dedup)", pool.SubscriptionCount())
	}
}

// TestPoolManagerReconnectNoDeadlock 测试重连不会死锁
func TestPoolManagerReconnectNoDeadlock(t *testing.T) {
	pool := NewPoolManager("wss://example.com/ws", 2, 10)

	// 添加模拟连接
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)
	pool.connections = append(pool.connections, wrapper)

	// 添加模拟订阅
	sub := Subscription{
		Channel: ChannelWebData2,
		User:    "0xtest",
	}
	pool.subscriptions["test-key"] = &subscriptionInfo{
		subscription: sub,
		callbacks:    map[int64]Callback{1: func(msg wsMessage) error { return nil }},
		connection:   wrapper,
	}

	// 启动一个 goroutine 来检测死锁
	done := make(chan bool, 1)
	go func() {
		// 尝试调用重连（如果会死锁，这个 goroutine 会卡住）
		pool.repairConnections()
		done <- true
	}()

	// 等待 5 秒，如果超时说明有死锁
	select {
	case <-done:
		// 成功，没有死锁
	case <-time.After(5 * time.Second):
		t.Fatal("reconnectAll() appears to be deadlocked")
	}
}

// TestPoolManagerIsReconnecting 测试重连状态标志
func TestPoolManagerIsReconnecting(t *testing.T) {
	pool := NewPoolManager("wss://example.com/ws", 2, 10)

	// 当前实现：IsReconnecting() 返回 false（重连是内部管理的）
	if pool.IsReconnecting() {
		t.Error("IsReconnecting() = true, want false (reconnect is internal)")
	}

	// 注意：由于重连现在是内部管理的，不再有外部可设置的状态标志
	// 这个测试仅验证方法不会 panic
}

// TestPoolManagerConcurrentAccess 测试并发访问不会panic
func TestPoolManagerConcurrentAccess(t *testing.T) {
	pool := NewPoolManager("wss://example.com/ws", 2, 10)

	// 添加模拟连接
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)
	pool.connections = append(pool.connections, wrapper)

	// 并发测试
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()
			// 模拟各种操作
			pool.IsConnected()
			pool.IsReconnecting()
			pool.ConnectionCount()
			pool.SubscriptionCount()
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestPoolManagerReconnectPartialDisconnection 测试部分连接断开场景
func TestPoolManagerReconnectPartialDisconnection(t *testing.T) {
	pool := NewPoolManager("wss://example.com/ws", 3, 10)

	// 添加 3 个连接
	for i := 0; i < 3; i++ {
		client := NewClient("wss://example.com/ws")
		wrapper := NewConnectionWrapper(client)
		pool.connections = append(pool.connections, wrapper)
	}

	// 添加一些订阅
	for i := 0; i < 3; i++ {
		wrapper := pool.connections[i]
		wrapper.AddSubscription(fmt.Sprintf("sub-%d-1", i))
		wrapper.AddSubscription(fmt.Sprintf("sub-%d-2", i))
	}

	// 记录初始状态
	initialCount := pool.ConnectionCount()
	if initialCount != 3 {
		t.Fatalf("Initial connection count = %d, want 3", initialCount)
	}

	// 模拟：第 1 个连接断开
	pool.connections[0].client.Close()

	// 在模拟环境中，所有连接都会被视为"已断开"（因为没有真实 WebSocket 连接）
	// 所以 reconnectAll 会尝试重建所有连接，但由于无法连接到真实服务器，重建失败

	// 调用 repairConnections
	if err := pool.repairConnections(); err != nil {
		t.Fatalf("repairConnections() failed: %v", err)
	}

	// 在模拟环境中，由于重连失败，连接池保留旧连接（虽然已断开）
	// 实际实现中不会移除重连失败的连接，下次可以继续尝试
	finalCount := pool.ConnectionCount()
	if finalCount != 3 {
		t.Errorf("After reconnect (all failed), connection count = %d, want 3 (connections preserved)", finalCount)
	}

	// 验证连接确实处于断开状态
	for i, cw := range pool.connections {
		if cw.Client().IsConnected() {
			t.Errorf("Connection %d should be disconnected after failed reconnect", i)
		}
	}
}

// TestConnectionWrapperGetSubscriptions 测试获取订阅列表
func TestConnectionWrapperGetSubscriptions(t *testing.T) {
	client := NewClient("wss://example.com/ws")
	wrapper := NewConnectionWrapper(client)

	// 添加订阅
	wrapper.AddSubscription("key1")
	wrapper.AddSubscription("key2")
	wrapper.AddSubscription("key3")

	// 获取订阅列表
	subs := wrapper.GetSubscriptionKeys()

	if len(subs) != 3 {
		t.Errorf("GetSubscriptionKeys() returned %d items, want 3", len(subs))
	}

	// 验证 key 存在（不保证顺序）
	found := make(map[string]bool)
	for _, key := range subs {
		found[key] = true
	}

	if !found["key1"] || !found["key2"] || !found["key3"] {
		t.Error("GetSubscriptionKeys() did not return all expected keys")
	}
}
