package processor

import (
	"sync"

	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// MessageHandler 消息处理器接口
type MessageHandler interface {
	HandleMessage(msg Message) error
}

// MessageQueue 异步消息队列
type MessageQueue struct {
	queue   chan Message
	wg      sync.WaitGroup
	handler MessageHandler
	done    chan struct{}
}

// NewMessageQueue 创建消息队列
func NewMessageQueue(size int, handler MessageHandler) *MessageQueue {
	if size <= 0 {
		size = 10000
	}
	return &MessageQueue{
		queue:   make(chan Message, size),
		handler: handler,
		done:    make(chan struct{}),
	}
}

// Start 启动工作协程
func (q *MessageQueue) Start() {
	q.wg.Add(1)
	go q.worker()
}

func (q *MessageQueue) worker() {
	defer q.wg.Done()
	for {
		select {
		case msg := <-q.queue:
			if err := q.handler.HandleMessage(msg); err != nil {
				logger.Error().Err(err).Str("type", msg.Type()).Msg("handle message failed")
			}
		case <-q.done:
			return
		}
	}
}

// Enqueue 发送消息（带背压策略）
func (q *MessageQueue) Enqueue(msg Message) error {
	select {
	case q.queue <- msg:
		return nil
	default:
		// 队列满，启用同步降级策略
		logger.Warn().
			Str("type", msg.Type()).
			Int("queue_size", len(q.queue)).
			Msg("message queue full, falling back to sync processing")

		// 同步处理消息（阻塞调用）
		return q.handler.HandleMessage(msg)
	}
}

// Stop 停止队列
func (q *MessageQueue) Stop() {
	close(q.done)
	q.wg.Wait()
}

// SetHandler 设置消息处理器
func (q *MessageQueue) SetHandler(handler MessageHandler) {
	q.handler = handler
}

// Size 返回当前队列大小
func (q *MessageQueue) Size() int {
	return len(q.queue)
}
