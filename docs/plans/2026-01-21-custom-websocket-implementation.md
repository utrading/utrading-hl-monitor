# 自主 WebSocket 实现计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 实现完全自主的 WebSocket 客户端和连接池管理，替代 go-hyperliquid 的 WebsocketClient，解决 WebData2 多地址订阅数据丢失问题。

**Architecture:**
- **Client**: 底层 WebSocket 客户端，封装 gorilla/websocket，负责连接和消息传输
- **PoolManager**: 连接池管理器，管理多个连接，实现订阅去重和负载均衡
- **Dispatcher**: 消息分发器，根据 channel 和 user 字段分发消息到对应回调
- **Subscription**: 订阅管理，支持多回调共享同一订阅（去重）

**Tech Stack:**
- `github.com/gorilla/websocket`: WebSocket 协议实现
- 标准库 `encoding/json`, `context`, `sync`
- 项目现有 `pkg/logger` 日志包

---

## 前置工作

### Task 0: 创建目录结构

**Files:**
- Create: `internal/ws/`

**Step 1: 创建目录**

```bash
mkdir -p internal/ws
```

**Step 2: 创建 .gitkeep**

```bash
touch internal/ws/.gitkeep
```

**Step 3: Commit**

```bash
git add internal/ws/.gitkeep
git commit -m "chore: create ws directory for custom websocket implementation"
```

---

## 第一阶段：类型定义

### Task 1: 定义核心类型

**Files:**
- Create: `internal/ws/types.go`
- Test: `internal/ws/types_test.go`

**Step 1: 编写类型定义测试**

```go
// internal/ws/types_test.go
package ws

import (
    "testing"
)

func TestSubscriptionKey(t *testing.T) {
    tests := []struct {
        name     string
        sub      Subscription
        expected string
    }{
        {
            name: "user subscription",
            sub: Subscription{
                Channel: ChannelWebData2,
                User:    "0x123",
            },
            expected: "webData2:0x123",
        },
        {
            name: "coin subscription",
            sub: Subscription{
                Channel: ChannelL2Book,
                Coin:    "BTC",
            },
            expected: "l2Book:BTC",
        },
        {
            name: "general subscription",
            sub: Subscription{
                Channel: ChannelAllMids,
            },
            expected: "allMids",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := tt.sub.Key(); got != tt.expected {
                t.Errorf("Key() = %v, want %v", got, tt.expected)
            }
        })
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws && go test -v -run TestSubscriptionKey
```

Expected: `undefined: Subscription`

**Step 3: 实现类型定义**

```go
// internal/ws/types.go
package ws

import "encoding/json"

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
    Channel Channel `json:"channel"`
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

// wsMessage WebSocket 消息
type wsMessage struct {
    Channel Channel          `json:"channel"`
    Data    json.RawMessage  `json:"data"`
}

// Callback 消息回调函数
type Callback func(msg wsMessage) error
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws && go test -v -run TestSubscriptionKey
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws/types.go internal/ws/types_test.go
git commit -m "feat(ws): define core types and Subscription.Key()"
```

---

## 第二阶段：底层 WebSocket 客户端

### Task 2: 实现 Client 基础结构

**Files:**
- Create: `internal/ws/client.go`
- Test: `internal/ws/client_test.go`

**Step 1: 编写 Client 创建测试**

```go
// internal/ws/client_test.go
package ws

import (
    "testing"
)

func TestNewClient(t *testing.T) {
    client := NewClient("wss://example.com/ws")

    if client == nil {
        t.Fatal("NewClient() returned nil")
    }

    if client.url != "wss://example.com/ws" {
        t.Errorf("url = %v, want %v", client.url, "wss://example.com/ws")
    }

    if client.handlers == nil {
        t.Error("handlers map not initialized")
    }

    if client.done == nil {
        t.Error("done channel not initialized")
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws && go test -v -run TestNewClient
```

Expected: `undefined: NewClient`

**Step 3: 实现 Client 结构体**

```go
// internal/ws/client.go
package ws

import (
    "sync"

    "github.com/gorilla/websocket"
)

// Client 底层 WebSocket 客户端
type Client struct {
    url       string
    conn      *websocket.Conn
    mu        sync.RWMutex
    writeMu   sync.Mutex
    handlers  map[string][]Callback
    done      chan struct{}
    closeOnce sync.Once
}

// NewClient 创建 WebSocket 客户端
func NewClient(url string) *Client {
    return &Client{
        url:      url,
        handlers: make(map[string][]Callback),
        done:     make(chan struct{}),
    }
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws && go test -v -run TestNewClient
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws/client.go internal/ws/client_test.go
git commit -m "feat(ws): implement Client basic structure"
```

---

### Task 3: 实现 Connect 方法

**Files:**
- Modify: `internal/ws/client.go`
- Modify: `internal/ws/client_test.go`

**Step 1: 添加 Connect 测试（需要 mock 服务器）**

```go
// internal/ws/client_test.go
package ws

import (
    "context"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/gorilla/websocket"
)

func TestClientConnect(t *testing.T) {
    // 创建测试 WebSocket 服务器
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

    // 将 HTTP 转换为 WebSocket URL
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
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws && go test -v -run TestClientConnect
```

Expected: `undefined: client.Connect` or `undefined: client.IsConnected`

**Step 3: 实现 Connect 和 IsConnected 方法**

```go
// internal/ws/client.go
package ws

import (
    "context"
    "fmt"
    "time"

    "github.com/gorilla/websocket"
)

// Connect 连接 WebSocket
func (c *Client) Connect(ctx context.Context) error {
    dialer := websocket.Dialer{
        HandshakeTimeout: 10 * time.Second,
    }

    conn, _, err := dialer.DialContext(ctx, c.url, nil)
    if err != nil {
        return fmt.Errorf("dial error: %w", err)
    }

    c.mu.Lock()
    c.conn = conn
    c.mu.Unlock()

    // 启动读取循环
    go c.readPump(ctx)
    // 启动心跳
    go c.pingPump(ctx)

    return nil
}

// IsConnected 检查连接状态
func (c *Client) IsConnected() bool {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.conn != nil
}

// readPump 读取循环
func (c *Client) readPump(ctx context.Context) {
    defer func() {
        c.mu.Lock()
        if c.conn != nil {
            c.conn.Close()
            c.conn = nil
        }
        c.mu.Unlock()
    }()

    for {
        select {
        case <-ctx.Done():
            return
        case <-c.done:
            return
        default:
            c.mu.RLock()
            conn := c.conn
            c.mu.RUnlock()

            if conn == nil {
                return
            }

            _, msg, err := conn.ReadMessage()
            if err != nil {
                return
            }

            // TODO: 解析并分发消息
            _ = msg
        }
    }
}

// pingPump 心跳循环
func (c *Client) pingPump(ctx context.Context) {
    ticker := time.NewTicker(50 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-c.done:
            return
        case <-ticker.C:
            if err := c.Ping(); err != nil {
                return
            }
        }
    }
}
```

**Step 4: 添加 Ping 和 Close 方法**

```go
// internal/ws/client.go (添加到文件末尾)

// Ping 发送心跳
func (c *Client) Ping() error {
    return c.writeJSON(map[string]string{"method": "ping"})
}

// writeJSON 写入 JSON 消息
func (c *Client) writeJSON(v any) error {
    c.writeMu.Lock()
    defer c.writeMu.Unlock()

    c.mu.RLock()
    conn := c.conn
    c.mu.RUnlock()

    if conn == nil {
        return fmt.Errorf("connection closed")
    }

    return conn.WriteJSON(v)
}

// Close 关闭连接
func (c *Client) Close() error {
    c.closeOnce.Do(func() {
        close(c.done)

        c.mu.Lock()
        if c.conn != nil {
            c.conn.Close()
            c.conn = nil
        }
        c.mu.Unlock()
    })
    return nil
}
```

**Step 5: 运行测试验证通过**

```bash
cd internal/ws && go test -v -run TestClientConnect -timeout 10s
```

Expected: `PASS`

**Step 6: Commit**

```bash
git add internal/ws/client.go internal/ws/client_test.go
git commit -m "feat(ws): implement Connect, IsConnected, Ping, Close methods"
```

---

### Task 4: 实现 Subscribe 和 Unsubscribe

**Files:**
- Modify: `internal/ws/client.go`
- Modify: `internal/ws/client_test.go`

**Step 1: 添加 Subscribe 测试**

```go
// internal/ws/client_test.go
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

    if subData["channel"] != "webData2" {
        t.Errorf("channel = %v, want webData2", subData["channel"])
    }

    if subData["user"] != "0x123" {
        t.Errorf("user = %v, want 0x123", subData["user"])
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws && go test -v -run TestClientSubscribe
```

Expected: `undefined: client.Subscribe`

**Step 3: 实现 Subscribe 和 Unsubscribe 方法**

```go
// internal/ws/client.go
// 在 Client 结构体中添加 Subscribe 和 Unsubscribe 方法

// Subscribe 发送订阅请求
func (c *Client) Subscribe(sub Subscription) error {
    return c.writeJSON(map[string]any{
        "method":       "subscribe",
        "subscription": sub,
    })
}

// Unsubscribe 发送取消订阅请求
func (c *Client) Unsubscribe(sub Subscription) error {
    return c.writeJSON(map[string]any{
        "method":       "unsubscribe",
        "subscription": sub,
    })
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws && go test -v -run TestClientSubscribe
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws/client.go internal/ws/client_test.go
git commit -m "feat(ws): implement Subscribe and Unsubscribe methods"
```

---

### Task 5: 实现消息分发机制

**Files:**
- Modify: `internal/ws/client.go`
- Modify: `internal/ws/client_test.go`

**Step 1: 添加消息分发测试**

```go
// internal/ws/client_test.go
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
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws && go test -v -run TestClientMessageDispatch
```

Expected: `undefined: client.SetMessageHandler`

**Step 3: 实现消息分发机制**

```go
// internal/ws/client.go
package ws

import (
    "context"
    "encoding/json"
    "fmt"
    "sync"
    "time"

    "github.com/gorilla/websocket"
)

// Client 底层 WebSocket 客户端
type Client struct {
    url         string
    conn        *websocket.Conn
    mu          sync.RWMutex
    writeMu     sync.Mutex
    handlers    map[string][]Callback
    done        chan struct{}
    closeOnce   sync.Once
    onMessage   func(wsMessage)  // 消息处理函数
}

// SetMessageHandler 设置消息处理函数
func (c *Client) SetMessageHandler(handler func(wsMessage)) {
    c.onMessage = handler
}

// readPump 读取循环
func (c *Client) readPump(ctx context.Context) {
    defer func() {
        c.mu.Lock()
        if c.conn != nil {
            c.conn.Close()
            c.conn = nil
        }
        c.mu.Unlock()
    }()

    for {
        select {
        case <-ctx.Done():
            return
        case <-c.done:
            return
        default:
            c.mu.RLock()
            conn := c.conn
            c.mu.RUnlock()

            if conn == nil {
                return
            }

            _, msg, err := conn.ReadMessage()
            if err != nil {
                return
            }

            var wsMsg wsMessage
            if err := json.Unmarshal(msg, &wsMsg); err != nil {
                continue
            }

            // 调用外部处理函数
            if c.onMessage != nil {
                c.onMessage(wsMsg)
            }
        }
    }
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws && go test -v -run TestClientMessageDispatch -timeout 10s
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws/client.go internal/ws/client_test.go
git commit -m "feat(ws2): implement message dispatch mechanism"
```

---

## 第三阶段：连接池管理器

### Task 6: 实现 ConnectionWrapper

**Files:**
- Create: `internal/ws2/pool.go`
- Test: `internal/ws2/pool_test.go`

**Step 1: 编写 ConnectionWrapper 测试**

```go
// internal/ws2/pool_test.go
package ws2

import (
    "testing"
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
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws2 && go test -v -run TestConnectionWrapper
```

Expected: `undefined: NewConnectionWrapper`

**Step 3: 实现 ConnectionWrapper**

```go
// internal/ws2/pool.go
package ws2

import (
    "sync"
)

// ConnectionWrapper 连接包装器
type ConnectionWrapper struct {
    client        *Client
    subscriptions map[string]Subscription
    mu            sync.RWMutex
}

// NewConnectionWrapper 创建连接包装器
func NewConnectionWrapper(client *Client) *ConnectionWrapper {
    return &ConnectionWrapper{
        client:        client,
        subscriptions: make(map[string]Subscription),
    }
}

// Client 获取底层客户端
func (cw *ConnectionWrapper) Client() *Client {
    return cw.client
}

// SubscriptionCount 获取订阅数量
func (cw *ConnectionWrapper) SubscriptionCount() int {
    cw.mu.RLock()
    defer cw.mu.RUnlock()
    return len(cw.subscriptions)
}

// HasSubscription 检查是否有指定订阅
func (cw *ConnectionWrapper) HasSubscription(key string) bool {
    cw.mu.RLock()
    defer cw.mu.RUnlock()
    _, exists := cw.subscriptions[key]
    return exists
}

// AddSubscription 添加订阅
func (cw *ConnectionWrapper) AddSubscription(key string) {
    cw.mu.Lock()
    defer cw.mu.Unlock()
    cw.subscriptions[key] = Subscription{}
}

// RemoveSubscription 移除订阅
func (cw *ConnectionWrapper) RemoveSubscription(key string) {
    cw.mu.Lock()
    defer cw.mu.Unlock()
    delete(cw.subscriptions, key)
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws2 && go test -v -run TestConnectionWrapper
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws2/pool.go internal/ws2/pool_test.go
git commit -m "feat(ws2): implement ConnectionWrapper"
```

---

### Task 7: 实现 PoolManager 基础结构

**Files:**
- Modify: `internal/ws2/pool.go`
- Modify: `internal/ws2/pool_test.go`

**Step 1: 添加 PoolManager 测试**

```go
// internal/ws2/pool_test.go
package ws2

import (
    "testing"
)

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
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws2 && go test -v -run TestNewPoolManager
```

Expected: `undefined: NewPoolManager`

**Step 3: 实现 PoolManager 基础结构**

```go
// internal/ws2/pool.go
package ws2

import (
    "context"
    "fmt"
    "sync"

    "github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// subscriptionInfo 订阅信息
type subscriptionInfo struct {
    subscription Subscription
    callbacks    []Callback
    connection   *ConnectionWrapper
}

// PoolManager 连接池管理器
type PoolManager struct {
    url               string
    connections       []*ConnectionWrapper
    mu                sync.RWMutex
    maxConnections    int
    maxSubscriptions  int
    subscriptions     map[string]*subscriptionInfo
    subscriptionsMu   sync.RWMutex
    dispatcher        *Dispatcher
    started           bool
}

// NewPoolManager 创建连接池
func NewPoolManager(url string, maxConns, maxSubs int) *PoolManager {
    pm := &PoolManager{
        url:              url,
        connections:      make([]*ConnectionWrapper, 0, maxConns),
        maxConnections:   maxConns,
        maxSubscriptions: maxSubs,
        subscriptions:    make(map[string]*subscriptionInfo),
    }
    pm.dispatcher = NewDispatcher(pm)
    return pm
}

// MaxConnections 获取最大连接数
func (pm *PoolManager) MaxConnections() int {
    return pm.maxConnections
}

// MaxSubscriptions 获取每连接最大订阅数
func (pm *PoolManager) MaxSubscriptions() int {
    return pm.maxSubscriptions
}

// ConnectionCount 获取当前连接数
func (pm *PoolManager) ConnectionCount() int {
    pm.mu.RLock()
    defer pm.mu.RUnlock()
    return len(pm.connections)
}

// Start 启动连接池
func (pm *PoolManager) Start(ctx context.Context) error {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    if pm.started {
        return nil
    }

    // 创建初始连接
    client := NewClient(pm.url)
    client.SetMessageHandler(pm.dispatcher.Dispatch)

    if err := client.Connect(ctx); err != nil {
        return fmt.Errorf("initial connection failed: %w", err)
    }

    wrapper := NewConnectionWrapper(client)
    pm.connections = append(pm.connections, wrapper)
    pm.started = true

    logger.Info().
        Int("max_connections", pm.maxConnections).
        Int("max_subscriptions", pm.maxSubscriptions).
        Msg("PoolManager started")

    return nil
}

// Close 关闭连接池
func (pm *PoolManager) Close() error {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    for _, cw := range pm.connections {
        cw.client.Close()
    }

    pm.connections = make([]*ConnectionWrapper, 0)
    pm.started = false

    return nil
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws2 && go test -v -run TestNewPoolManager
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws2/pool.go internal/ws2/pool_test.go
git commit -m "feat(ws2): implement PoolManager basic structure"
```

---

### Task 8: 实现 acquireConnection

**Files:**
- Modify: `internal/ws2/pool.go`
- Modify: `internal/ws2/pool_test.go`

**Step 1: 添加 acquireConnection 测试**

```go
// internal/ws2/pool_test.go
package ws2

import (
    "context"
    "testing"
)

func TestPoolManagerAcquireConnection(t *testing.T) {
    pool := NewPoolManager("wss://example.com/ws", 2, 2)
    ctx := context.Background()

    if err := pool.Start(ctx); err != nil {
        t.Fatalf("Start() failed: %v", err)
    }
    defer pool.Close()

    // 第一次获取：返回现有连接
    conn1, err := pool.acquireConnection()
    if err != nil {
        t.Fatalf("acquireConnection() failed: %v", err)
    }

    if conn1 == nil {
        t.Fatal("acquireConnection() returned nil")
    }

    // 第二次获取：返回现有连接（有容量）
    conn2, err := pool.acquireConnection()
    if err != nil {
        t.Fatalf("acquireConnection() failed: %v", err)
    }

    if conn2 != conn1 {
        t.Error("should return same connection when capacity available")
    }

    // 模拟连接满载
    conn1.AddSubscription("key1")
    conn1.AddSubscription("key2")

    // 第三次获取：创建新连接
    conn3, err := pool.acquireConnection()
    if err != nil {
        t.Fatalf("acquireConnection() failed: %v", err)
    }

    if conn3 == conn1 {
        t.Error("should create new connection when first is full")
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws2 && go test -v -run TestPoolManagerAcquireConnection
```

Expected: `undefined: pool.acquireConnection`

**Step 3: 实现 acquireConnection 方法**

```go
// internal/ws2/pool.go

// acquireConnection 获取或创建连接（内部方法）
func (pm *PoolManager) acquireConnection() (*ConnectionWrapper, error) {
    pm.mu.Lock()
    defer pm.mu.Unlock()

    // 1. 尝试找到有容量的现有连接
    for _, cw := range pm.connections {
        if cw.SubscriptionCount() < pm.maxSubscriptions {
            return cw, nil
        }
    }

    // 2. 创建新连接
    if len(pm.connections) < pm.maxConnections {
        return pm.createConnection()
    }

    // 3. 返回负载最少的连接（降级）
    return pm.leastLoadedConnection(), nil
}

// createConnection 创建新连接
func (pm *PoolManager) createConnection() (*ConnectionWrapper, error) {
    client := NewClient(pm.url)
    client.SetMessageHandler(pm.dispatcher.Dispatch)

    ctx := context.Background()
    if err := client.Connect(ctx); err != nil {
        return nil, fmt.Errorf("connection failed: %w", err)
    }

    wrapper := NewConnectionWrapper(client)
    pm.connections = append(pm.connections, wrapper)

    logger.Info().
        Int("connection_index", len(pm.connections)-1).
        Int("total_connections", len(pm.connections)).
        Msg("Created new connection")

    return wrapper, nil
}

// leastLoadedConnection 获取负载最少的连接
func (pm *PoolManager) leastLoadedConnection() *ConnectionWrapper {
    var selected *ConnectionWrapper
    minCount := int(^uint(0) >> 1) // 最大 int 值

    for _, cw := range pm.connections {
        count := cw.SubscriptionCount()
        if count < minCount {
            minCount = count
            selected = cw
        }
    }

    logger.Warn().Msg("All connections at capacity, returning least loaded")
    return selected
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws2 && go test -v -run TestPoolManagerAcquireConnection
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws2/pool.go internal/ws2/pool_test.go
git commit -m "feat(ws2): implement acquireConnection with load balancing"
```

---

### Task 9: 实现 Subscribe 方法

**Files:**
- Modify: `internal/ws2/pool.go`
- Modify: `internal/ws2/pool_test.go`

**Step 1: 添加 Subscribe 测试**

```go
// internal/ws2/pool_test.go
package ws2

import (
    "context"
    "testing"
    "time"

    "github.com/gorilla/websocket"
)

func TestPoolManagerSubscribe(t *testing.T) {
    upgrader := websocket.Upgrader{}

    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        conn, err := upgrader.Upgrade(w, r, nil)
        if err != nil {
            return
        }
        defer conn.Close()

        // 读取订阅请求
        var msg map[string]interface{}
        conn.ReadJSON(&msg)
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

    callbackCalled := false
    callback := func(msg wsMessage) error {
        callbackCalled = true
        return nil
    }

    handle, err := pool.Subscribe(sub, callback)
    if err != nil {
        t.Fatalf("Subscribe() failed: %v", err)
    }

    if handle == nil {
        t.Fatal("Subscribe() returned nil handle")
    }

    // 等待订阅完成
    time.Sleep(100 * time.Millisecond)

    if pool.SubscriptionCount() != 1 {
        t.Errorf("SubscriptionCount() = %d, want 1", pool.SubscriptionCount())
    }
}

func TestPoolManagerSubscribeDedup(t *testing.T) {
    upgrader := websocket.Upgrader{}
    subscribeCount := 0

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
                subscribeCount++
            }
        }
    }))
    defer server.Close()

    wsURL := "ws" + server.URL[len("http"):]

    pool := NewPoolManager(wsURL, 2, 10)
    ctx := context.Background()

    if err := pool.Start(ctx); err != nil {
        t.Fatalf("Start() failed: %v", err)
    }
    defer pool.Close()

    sub := Subscription{
        Channel: ChannelWebData2,
        User:    "0x123",
    }

    // 订阅两次相同的内容
    _, err1 := pool.Subscribe(sub, func(msg wsMessage) error { return nil })
    _, err2 := pool.Subscribe(sub, func(msg wsMessage) error { return nil })

    if err1 != nil || err2 != nil {
        t.Fatalf("Subscribe() failed: %v, %v", err1, err2)
    }

    // 等待订阅完成
    time.Sleep(100 * time.Millisecond)

    // 应该只发送一次订阅请求（去重）
    if subscribeCount != 1 {
        t.Errorf("subscribe requests = %d, want 1 (dedup)", subscribeCount)
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws2 && go test -v -run TestPoolManagerSubscribe
```

Expected: `undefined: pool.Subscribe`

**Step 3: 实现 Subscribe 方法**

```go
// internal/ws2/pool.go

// SubscriptionHandle 订阅句柄
type SubscriptionHandle struct {
    key string
    pm  *PoolManager
}

// Unsubscribe 取消订阅
func (sh *SubscriptionHandle) Unsubscribe() error {
    return sh.pm.unsubscribe(sh.key)
}

// Subscribe 订阅（自动去重）
func (pm *PoolManager) Subscribe(sub Subscription, callback Callback) (*SubscriptionHandle, error) {
    key := sub.Key()

    pm.subscriptionsMu.Lock()
    defer pm.subscriptionsMu.Unlock()

    // 检查是否已订阅
    if info, exists := pm.subscriptions[key]; exists {
        // 添加回调（去重：同一个订阅多个回调）
        info.callbacks = append(info.callbacks, callback)
        return &SubscriptionHandle{key: key, pm: pm}, nil
    }

    // 获取或创建连接
    conn, err := pm.acquireConnection()
    if err != nil {
        return nil, err
    }

    // 发送订阅请求
    if err := conn.Client().Subscribe(sub); err != nil {
        return nil, fmt.Errorf("subscribe request failed: %w", err)
    }

    // 记录订阅
    pm.subscriptions[key] = &subscriptionInfo{
        subscription: sub,
        callbacks:    []Callback{callback},
        connection:   conn,
    }

    conn.AddSubscription(key)

    logger.Info().
        Str("key", key).
        Int("connection_subs", conn.SubscriptionCount()).
        Msg("Subscribed")

    return &SubscriptionHandle{key: key, pm: pm}, nil
}

// unsubscribe 内部取消订阅方法
func (pm *PoolManager) unsubscribe(key string) error {
    pm.subscriptionsMu.Lock()
    defer pm.subscriptionsMu.Unlock()

    info, exists := pm.subscriptions[key]
    if !exists {
        return nil
    }

    // 移除回调
    if len(info.callbacks) > 1 {
        // 还有其他回调，只移除当前订阅
        return nil
    }

    // 最后一个回调，取消服务器订阅
    if err := info.connection.Client().Unsubscribe(info.subscription); err != nil {
        return err
    }

    info.connection.RemoveSubscription(key)
    delete(pm.subscriptions, key)

    logger.Info().Str("key", key).Msg("Unsubscribed")

    return nil
}

// SubscriptionCount 获取订阅总数
func (pm *PoolManager) SubscriptionCount() int {
    pm.subscriptionsMu.RLock()
    defer pm.subscriptionsMu.RUnlock()
    return len(pm.subscriptions)
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws2 && go test -v -run TestPoolManagerSubscribe
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws2/pool.go internal/ws2/pool_test.go
git commit -m "feat(ws2): implement Subscribe with deduplication"
```

---

## 第四阶段：消息分发器

### Task 10: 实现 Dispatcher

**Files:**
- Create: `internal/ws2/dispatcher.go`
- Test: `internal/ws2/dispatcher_test.go`

**Step 1: 编写 Dispatcher 测试**

```go
// internal/ws2/dispatcher_test.go
package ws2

import (
    "encoding/json"
    "testing"
)

func TestDispatcherDispatchWebData2(t *testing.T) {
    pm := NewPoolManager("wss://example.com/ws", 1, 10)

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

    if !callbackCalled {
        t.Error("callback was not called")
    }

    if receivedMsg.Channel != ChannelWebData2 {
        t.Errorf("channel = %v, want webData2", receivedMsg.Channel)
    }
}

func TestDispatcherDispatchAllMids(t *testing.T) {
    pm := NewPoolManager("wss://example.com/ws", 1, 10)

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

    if !callbackCalled {
        t.Error("callback was not called")
    }
}
```

**Step 2: 运行测试验证失败**

```bash
cd internal/ws2 && go test -v -run TestDispatcher
```

Expected: `undefined: NewDispatcher` or `dispatcher.Dispatch`

**Step 3: 实现 Dispatcher**

```go
// internal/ws2/dispatcher.go
package ws2

import (
    "encoding/json"
)

// Dispatcher 消息分发器
type Dispatcher struct {
    pm *PoolManager
}

// NewDispatcher 创建分发器
func NewDispatcher(pm *PoolManager) *Dispatcher {
    return &Dispatcher{pm: pm}
}

// Dispatch 处理收到的消息
func (d *Dispatcher) Dispatch(msg wsMessage) {
    channel := string(msg.Channel)

    // 根据不同频道类型处理
    switch channel {
    case string(ChannelWebData2):
        d.dispatchWebData2(msg)
    case string(ChannelUserFills):
        d.dispatchUserFills(msg)
    case string(ChannelOrderUpdates):
        d.dispatchOrderUpdates(msg)
    default:
        // 通用频道分发
        d.dispatchGeneric(msg)
    }
}

// dispatchWebData2 WebData2 消息分发（需要解析 User 字段）
func (d *Dispatcher) dispatchWebData2(msg wsMessage) {
    var data struct {
        User string `json:"user"`
    }

    if err := json.Unmarshal(msg.Data, &data); err != nil {
        // 无法解析 User，广播到所有 WebData2 订阅
        d.broadcastToChannel(ChannelWebData2, msg)
        return
    }

    key := string(ChannelWebData2) + ":" + data.User
    d.dispatchToKey(key, msg)
}

// dispatchUserFills UserFills 消息分发
func (d *Dispatcher) dispatchUserFills(msg wsMessage) {
    var data struct {
        User string `json:"user"`
    }

    if err := json.Unmarshal(msg.Data, &data); err != nil {
        d.broadcastToChannel(ChannelUserFills, msg)
        return
    }

    key := string(ChannelUserFills) + ":" + data.User
    d.dispatchToKey(key, msg)
}

// dispatchOrderUpdates OrderUpdates 消息分发
func (d *Dispatcher) dispatchOrderUpdates(msg wsMessage) {
    // OrderUpdates 是数组，需要特殊处理
    var data []json.RawMessage

    if err := json.Unmarshal(msg.Data, &data); err != nil {
        return
    }

    // 广播到所有 OrderUpdates 订阅
    d.broadcastToChannel(ChannelOrderUpdates, msg)
}

// dispatchGeneric 通用频道分发
func (d *Dispatcher) dispatchGeneric(msg wsMessage) {
    key := string(msg.Channel)
    d.dispatchToKey(key, msg)
}

// dispatchToKey 分发到指定键的订阅
func (d *Dispatcher) dispatchToKey(key string, msg wsMessage) {
    d.pm.subscriptionsMu.RLock()
    info, exists := d.pm.subscriptions[key]
    d.pm.subscriptionsMu.RUnlock()

    if exists {
        for _, cb := range info.callbacks {
            if err := cb(msg); err != nil {
                // 记录错误但不中断
                continue
            }
        }
    }
}

// broadcastToChannel 广播到指定频道的所有订阅
func (d *Dispatcher) broadcastToChannel(channel Channel, msg wsMessage) {
    prefix := string(channel) + ":"

    d.pm.subscriptionsMu.RLock()
    defer d.pm.subscriptionsMu.RUnlock()

    for key, info := range d.pm.subscriptions {
        // 检查是否是该频道的订阅
        if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
            for _, cb := range info.callbacks {
                if err := cb(msg); err != nil {
                    continue
                }
            }
        }
    }
}
```

**Step 4: 运行测试验证通过**

```bash
cd internal/ws2 && go test -v -run TestDispatcher
```

Expected: `PASS`

**Step 5: Commit**

```bash
git add internal/ws2/dispatcher.go internal/ws2/dispatcher_test.go
git commit -m "feat(ws2): implement Dispatcher with channel-based routing"
```

---

## 第五阶段：集成测试

### Task 11: 端到端测试

**Files:**
- Create: `internal/ws2/e2e_test.go`

**Step 1: 编写端到端测试**

```go
// internal/ws2/e2e_test.go
package ws2

import (
    "context"
    "encoding/json"
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
                subscriptions = append(subscriptions, sub["channel"].(string)+":"+sub["user"].(string))
                subsMu.Unlock()

                // 发送确认消息
                response := map[string]interface{}{
                    "channel": sub["channel"],
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
```

**Step 2: 运行测试**

```bash
cd internal/ws2 && go test -v -run TestE2E -timeout 30s
```

Expected: `PASS`

**Step 3: Commit**

```bash
git add internal/ws2/e2e_test.go
git commit -m "test(ws2): add end-to-end integration tests"
```

---

## 第六阶段：迁移 PositionManager

### Task 12: 重构 PositionManager 使用新的 ws2

**Files:**
- Modify: `internal/ws/position_manager.go`
- Modify: `internal/ws/subscription_manager.go`

**Step 1: 修改 PositionManager 导入 ws2**

```go
// internal/ws/position_manager.go
package ws

import (
    "encoding/json"
    "fmt"
    "sync"
    "time"

    "github.com/spf13/cast"
    "github.com/utrading/utrading-hl-monitor/internal/cache"
    "github.com/utrading/utrading-hl-monitor/internal/models"
    "github.com/utrading/utrading-hl-monitor/internal/processor"
    "github.com/utrading/utrading-hl-monitor/internal/ws2"
    "github.com/utrading/utrading-hl-monitor/pkg/logger"
)

var _ address.AddressSubscriber = (*PositionManager)(nil)

// PositionManager 仓位管理器 - 监听地址的仓位变化
type PositionManager struct {
    pool                 *ws2.PoolManager
    addresses            map[string]bool
    handles              map[string]*ws2.SubscriptionHandle
    priceCache           *cache.PriceCache
    symbolCache          *cache.SymbolCache
    positionBalanceCache *cache.PositionBalanceCache
    messageQueue         *processor.MessageQueue
    messagesReceived     map[string]int64
    messagesFiltered     int64
    mu                   sync.RWMutex
}

// NewPositionManager 创建仓位管理器
func NewPositionManager(
    pool *ws2.PoolManager,
    priceCache *cache.PriceCache,
    symbolCache *cache.SymbolCache,
    batchWriter *processor.BatchWriter,
) *PositionManager {
    messageQueue := processor.NewMessageQueue(1000, 1, nil)
    positionProcessor := processor.NewPositionProcessor(batchWriter)
    messageQueue.SetHandler(positionProcessor)
    messageQueue.Start()

    return &PositionManager{
        pool:                 pool,
        addresses:            make(map[string]bool),
        handles:              make(map[string]*ws2.SubscriptionHandle),
        priceCache:           priceCache,
        symbolCache:          symbolCache,
        positionBalanceCache: cache.NewPositionBalanceCache(),
        messageQueue:         messageQueue,
        messagesReceived:     make(map[string]int64),
    }
}

func (m *PositionManager) subscribeAddress(addr string) error {
    sub := ws2.Subscription{
        Channel: ws2.ChannelWebData2,
        User:    addr,
    }

    handle, err := m.pool.Subscribe(sub, func(msg ws2.WsMessage) error {
        // 验证消息中的 User 是否匹配订阅地址
        var data struct {
            User string `json:"user"`
        }
        if err := json.Unmarshal(msg.Data, &data); err != nil {
            return err
        }

        if data.User != addr {
            m.mu.Lock()
            m.messagesFiltered++
            m.mu.Unlock()
            return nil
        }

        // 记录有效消息
        m.mu.Lock()
        m.messagesReceived[addr]++
        m.mu.Unlock()

        m.handleWebData2(msg)
        return nil
    })

    if err != nil {
        logger.Error().Err(err).Str("address", addr).Msg("failed to subscribe webdata2")
        return err
    }

    m.mu.Lock()
    m.handles[addr] = handle
    m.mu.Unlock()

    logger.Info().Str("address", addr).Msg("subscribed position data")
    return nil
}
```

**Step 2: 运行测试**

```bash
go test ./internal/ws/... -v -run TestPositionManager
```

**Step 3: 更新 main.go**

```go
// cmd/hl_monitor/main.go

import (
    "github.com/utrading/utrading-hl-monitor/internal/ws2"
)

func main() {
    // ... 现有代码 ...

    // 创建连接池
    poolManager := ws2.NewPoolManager(
        cfg.Hyperliquid.WsURL,
        cfg.Hyperliquid.MaxConnections,
        cfg.Hyperliquid.MaxSubscriptionsPerConnection,
    )

    if err := poolManager.Start(context.Background()); err != nil {
        logger.Fatal().Err(err).Msg("failed to start connection pool")
    }
    defer poolManager.Close()

    // 创建 PositionManager（使用新的连接池）
    positionManager := ws.NewPositionManager(
        poolManager,
        symbolManager.PriceCache(),
        symbolManager.SymbolCache(),
        batchWriter,
    )

    // 创建 SubscriptionManager（使用新的连接池）
    subscriptionManager := ws.NewSubscriptionManager(
        poolManager,
        natsPublisher,
        symbolManager.SymbolCache(),
        positionManager.PositionBalanceCache(),
        batchWriter,
    )
}
```

**Step 4: Commit**

```bash
git add internal/ws/position_manager.go cmd/hl_monitor/main.go
git commit -m "refactor(ws): migrate PositionManager to use ws2.PoolManager"
```

---

### Task 13: 迁移 SubscriptionManager

**Files:**
- Modify: `internal/ws/subscription_manager.go`

**Step 1: 修改 SubscriptionManager**

```go
// internal/ws/subscription_manager.go
package ws

import (
    "context"
    "fmt"
    "math"
    "strings"
    "sync"
    "time"

    "github.com/utrading/utrading-hl-monitor/internal/address"
    "github.com/utrading/utrading-hl-monitor/internal/cache"
    "github.com/utrading/utrading-hl-monitor/internal/nats"
    "github.com/utrading/utrading-hl-monitor/internal/processor"
    "github.com/utrading/utrading-hl-monitor/internal/ws2"
    "github.com/utrading/utrading-hl-monitor/pkg/concurrent"
    "github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// SubscriptionManager 订阅管理器
type SubscriptionManager struct {
    poolManager          *ws2.PoolManager
    publisher            Publisher
    addresses            concurrent.Map[string, struct{}]
    handles              map[string][]*ws2.SubscriptionHandle
    messageQueue         *processor.MessageQueue
    orderProcessor       *processor.OrderProcessor
    deduper              *OrderDeduper
    positionBalanceCache *cache.PositionBalanceCache
    oidToAddress         concurrent.Map[int64, string]
    symbolCache          *cache.SymbolCache
    mu                   sync.RWMutex
}

// NewSubscriptionManager 创建订阅管理器
func NewSubscriptionManager(
    poolManager *ws2.PoolManager,
    publisher Publisher,
    symbolCache *cache.SymbolCache,
    positionBalanceCache *cache.PositionBalanceCache,
    batchWriter *processor.BatchWriter,
) *SubscriptionManager {
    deduper := NewOrderDeduper(30 * time.Minute)
    messageQueue := processor.NewMessageQueue(10000, 4, nil)
    orderProcessor := processor.NewOrderProcessor(publisher, batchWriter, deduper, symbolCache, positionBalanceCache)
    messageQueue.SetHandler(orderProcessor)
    messageQueue.Start()

    return &SubscriptionManager{
        poolManager:          poolManager,
        publisher:            publisher,
        addresses:            concurrent.Map[string, struct{}]{},
        handles:              make(map[string][]*ws2.SubscriptionHandle),
        messageQueue:         messageQueue,
        orderProcessor:       orderProcessor,
        deduper:              deduper,
        positionBalanceCache: positionBalanceCache,
        oidToAddress:         concurrent.Map[int64, string]{},
        symbolCache:          symbolCache,
    }
}

func (m *SubscriptionManager) subscribeAddress(addr string) error {
    // 1. 订阅 OrderFills
    fillsSub := ws2.Subscription{
        Channel: ws2.ChannelUserFills,
        User:    addr,
    }

    fillsHandle, err := m.poolManager.Subscribe(fillsSub, func(msg ws2.WsMessage) error {
        var data struct {
            User string `json:"user"`
            Fills []hyperliquid.WsOrderFill `json:"fills"`
        }
        if err := json.Unmarshal(msg.Data, &data); err != nil {
            return err
        }

        if data.User != addr {
            return nil
        }

        m.handleOrderFills(hyperliquid.WsOrderFills{User: data.User, Fills: data.Fills})
        return nil
    })
    if err != nil {
        return fmt.Errorf("subscribe order fills failed: %w", err)
    }

    // 2. 订阅 OrderUpdates
    updatesSub := ws2.Subscription{
        Channel: ws2.ChannelOrderUpdates,
        User:    addr,
    }

    updatesHandle, err := m.poolManager.Subscribe(updatesSub, func(msg ws2.WsMessage) error {
        var data []hyperliquid.WsOrder
        if err := json.Unmarshal(msg.Data, &data); err != nil {
            return err
        }

        m.handleOrderUpdates(addr, data)
        return nil
    })
    if err != nil {
        fillsHandle.Unsubscribe()
        return fmt.Errorf("subscribe order updates failed: %w", err)
    }

    m.mu.Lock()
    m.handles[addr] = []*ws2.SubscriptionHandle{fillsHandle, updatesHandle}
    m.mu.Unlock()

    logger.Info().
        Str("address", addr).
        Msg("subscribed order fills and updates")

    return nil
}

func (m *SubscriptionManager) UnsubscribeAddress(addr string) error {
    if _, exists := m.addresses.LoadAndDelete(addr); !exists {
        return nil
    }

    m.mu.Lock()
    handles, ok := m.handles[addr]
    if ok {
        for _, handle := range handles {
            handle.Unsubscribe()
        }
        delete(m.handles, addr)
    }
    m.mu.Unlock()

    logger.Info().Str("address", addr).Msg("unsubscribed order fills and updates")
    return nil
}

// Close 关闭订阅管理器
func (m *SubscriptionManager) Close() error {
    m.messageQueue.Stop()
    m.orderProcessor.Stop()
    m.deduper.Close()

    m.mu.Lock()
    defer m.mu.Unlock()

    for _, handles := range m.handles {
        for _, handle := range handles {
            handle.Unsubscribe()
        }
    }
    m.handles = make(map[string][]*ws2.SubscriptionHandle)

    return nil
}
```

**Step 2: 运行测试**

```bash
go test ./internal/ws/... -v
```

**Step 3: Commit**

```bash
git add internal/ws/subscription_manager.go
git commit -m "refactor(ws): migrate SubscriptionManager to use ws2.PoolManager"
```

---

## 第七阶段：清理与优化

### Task 14: 删除旧的 go-hyperliquid WebSocket 依赖

**Files:**
- Modify: `go.mod`
- Delete: `internal/ws/pool_manager.go` (旧的)

**Step 1: 检查是否还有其他地方使用 go-hyperliquid 的 WebSocket**

```bash
grep -r "go-hyperliquid.*WebsocketClient" --include="*.go" .
```

**Step 2: 如果没有其他使用，可以删除旧的 pool_manager.go**

**Step 3: 运行完整测试套件**

```bash
go test ./... -v
```

**Step 4: Commit**

```bash
git add go.mod internal/ws/pool_manager.go
git commit -m "chore: remove old go-hyperliquid WebSocket dependency"
```

---

### Task 15: 添加性能指标和监控

**Files:**
- Create: `internal/ws2/metrics.go`

**Step 1: 实现指标收集**

```go
// internal/ws2/metrics.go
package ws2

import (
    "sync/atomic"

    "github.com/utrading/utrading-hl-monitor/internal/monitor"
)

var (
    // 连接数指标
    connectionCount int64

    // 订阅数指标
    subscriptionCount int64

    // 消息数指标
    messagesReceived int64
    messagesDispatched int64
)

func updateConnectionMetrics(count int) {
    atomic.StoreInt64(&connectionCount, int64(count))
    monitor.SetPoolManagerConnectionCount(count)
}

func updateSubscriptionMetrics(count int) {
    atomic.StoreInt64(&subscriptionCount, int64(count))
}
```

**Step 2: Commit**

```bash
git add internal/ws2/metrics.go
git commit -m "feat(ws2): add performance metrics"
```

---

## 总结

完成以上任务后，你将拥有：

1. **完全自主的 WebSocket 实现** (`internal/ws2/`)
2. **连接池管理**：支持多个连接，自动负载均衡
3. **订阅去重**：相同订阅只发送一次请求
4. **精准消息分发**：根据 channel 和 user 字段分发消息
5. **完整的测试覆盖**：单元测试 + 集成测试

**验证步骤：**

```bash
# 1. 运行所有测试
go test ./internal/ws2/... -v

# 2. 运行服务
make run

# 3. 检查日志，确认 WebData2 订阅正常工作
tail -f logs/output.log | grep webdata2
```
