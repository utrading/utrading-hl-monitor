package processor

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// mockHandler 模拟消息处理器
type mockHandler struct {
	mu     sync.Mutex
	calls  []Message
	delays map[string]int // 消息类型 -> 延迟毫秒
}

func newMockHandler() *mockHandler {
	return &mockHandler{
		calls:  make([]Message, 0),
		delays: make(map[string]int),
	}
}

// errorMessage 错误消息（用于测试）
type errorMessage struct{}

func (e errorMessage) Type() string { return "error" }

func (h *mockHandler) HandleMessage(msg Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, msg)

	// 模拟处理延迟
	if delay, ok := h.delays[msg.Type()]; ok {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}

	if msg.Type() == "error" {
		return errors.New("mock error")
	}
	return nil
}

func (h *mockHandler) CallCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.calls)
}

func TestMessageQueue_Enqueue(t *testing.T) {
	handler := newMockHandler()
	q := NewMessageQueue(10, handler)
	q.Start()
	defer q.Stop()

	msg := PositionUpdateMessage{Address: "test"}
	err := q.Enqueue(msg)
	assert.NoError(t, err)

	// 等待处理
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, handler.CallCount())
}

func TestMessageQueue_Backpressure(t *testing.T) {
	handler := newMockHandler()
	handler.delays["order_fill"] = 100 // 慢处理
	q := NewMessageQueue(2, handler)   // 小队列
	q.Start()
	defer q.Stop()

	// 填满队列
	for i := 0; i < 10; i++ {
		msg := OrderFillMessage{Address: "test"}
		_ = q.Enqueue(msg)
	}

	// 队列应该已处理（因为队列满时降级为同步处理）
	assert.GreaterOrEqual(t, handler.CallCount(), 1)
}

func TestMessageQueue_ErrorHandling(t *testing.T) {
	handler := newMockHandler()
	q := NewMessageQueue(100, handler)
	q.Start()
	defer q.Stop()

	errMsg := errorMessage{}
	_ = q.Enqueue(errMsg)

	time.Sleep(50 * time.Millisecond)
	// 错误不应崩溃
	assert.GreaterOrEqual(t, handler.CallCount(), 1)
}

// T045: 消息队列性能基准测试

func BenchmarkMessageQueue_Enqueue(b *testing.B) {
	handler := newMockHandler()
	q := NewMessageQueue(10000, handler)
	q.Start()
	defer q.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := PositionUpdateMessage{Address: "bench"}
		q.Enqueue(msg)
	}
}

func BenchmarkMessageQueue_EnqueueBackpressure(b *testing.B) {
	handler := &mockHandler{delays: map[string]int{"position_update": 10}}
	q := NewMessageQueue(100, handler) // 小队列触发背压
	q.Start()
	defer q.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		msg := PositionUpdateMessage{Address: "bench"}
		q.Enqueue(msg)
	}
}
