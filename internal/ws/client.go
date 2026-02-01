package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

const (
	writeWait      = 10 * time.Second // 写入超时
	pongWait       = 60 * time.Second // 读取超时（应大于心跳间隔）
	pingPeriod     = 50 * time.Second // 心跳间隔
	maxMessageSize = 1024 * 1024 * 2  // 最大消息限制 2MB
)

type Client struct {
	url     string
	conn    *websocket.Conn
	mu      sync.RWMutex
	writeMu sync.Mutex

	// 状态控制
	done      chan struct{}
	closeOnce sync.Once

	// 回调
	onMessage    func(wsMessage) error
	onDisconnect func()
}

func NewClient(url string) *Client {
	if url == "" {
		panic("ws: URL cannot be empty")
	}
	return &Client{
		url:  url,
		done: make(chan struct{}),
	}
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		return nil // 已经连接
	}
	c.mu.Unlock()

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, c.url, nil)
	if err != nil {
		return fmt.Errorf("dial error: %w", err)
	}

	// 配置连接参数
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))

	// 处理标准 Pong 帧（如果服务器发送标准控制帧）
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// 核心优化：监控 Context 和 done 信号，主动关闭连接
	go func() {
		select {
		case <-ctx.Done():
		case <-c.done:
		}
		c.internalClose() // 强制关闭底层连接，解除 ReadMessage 阻塞
	}()

	go c.readPump()
	go c.pingPump()

	return nil
}

// internalClose 内部关闭方法，不触发通知逻辑
func (c *Client) internalClose() {
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.internalClose()
	})
	return nil
}

func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.conn != nil
}

func (c *Client) readPump() {
	defer func() {
		c.internalClose()
		c.notifyDisconnect()
	}()

	for {
		// 检查是否已经主动关闭
		select {
		case <-c.done:
			return
		default:
		}

		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			return
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Error().Err(err).Msg("ws read error")
			}
			return
		}

		// 每次读取成功，刷新 ReadDeadline
		conn.SetReadDeadline(time.Now().Add(pongWait))

		// 从对象池获取 wsMessage
		wsMsg := msgPool.Get().(*WsMessage)

		if err = json.Unmarshal(msg, wsMsg); err != nil {
			logger.Warn().Err(err).Msg("unmarshal ws message error")
			// 放回池中（重置）
			wsMsg.Data = nil
			wsMsg.Channel = ""
			msgPool.Put(wsMsg)
			continue
		}

		if c.onMessage != nil {
			if err = c.onMessage(*wsMsg); err != nil {
				logger.Error().Err(err).Msg("onMessage callback error")
			}
		}

		// 放回池中（重置字段避免内存泄漏）
		// 注意：Data 字段引用的是 msg 的字节数组，会被下次读取覆盖
		// 所以这里只需清空指针即可
		wsMsg.Data = nil
		wsMsg.Channel = ""
		msgPool.Put(wsMsg)
	}
}

func (c *Client) pingPump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if err := c.Ping(); err != nil {
				return
			}
		}
	}
}

func (c *Client) Ping() error {
	// 同时发送应用层 Ping 和标准的控制帧 Ping
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("connection closed")
	}

	// 设置写入超时
	conn.SetWriteDeadline(time.Now().Add(writeWait))

	// 1. 发送标准 Ping 帧
	if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
		return err
	}

	// 2. 发送业务层 Ping (JSON)
	return conn.WriteJSON(map[string]string{"method": "ping"})
}

func (c *Client) Subscribe(sub Subscription) error {
	return c.writeJSONWithDeadline(map[string]any{
		"method":       "subscribe",
		"subscription": sub,
	})
}

func (c *Client) Unsubscribe(sub Subscription) error {
	return c.writeJSONWithDeadline(map[string]any{
		"method":       "unsubscribe",
		"subscription": sub,
	})
}

// writeJSONWithDeadline 替代原 writeJSON，增加超时控制
func (c *Client) writeJSONWithDeadline(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("connection closed")
	}

	conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteJSON(v)
}

func (c *Client) notifyDisconnect() {
	c.mu.RLock()
	callback := c.onDisconnect
	c.mu.RUnlock()

	if callback != nil {
		callback()
	}
}

func (c *Client) SetMessageHandler(handler func(wsMessage) error) {
	c.onMessage = handler
}

func (c *Client) SetDisconnectCallback(callback func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onDisconnect = callback
}
