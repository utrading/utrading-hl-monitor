package nats

import (
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/utrading/utrading-hl-monitor/internal/monitor"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// Publisher NATS 发布器
type Publisher struct {
	*nats.Conn
	mu     sync.RWMutex
	closed bool
}

// NewPublisher 创建 NATS 发布器
func NewPublisher(url string) (*Publisher, error) {
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, err
	}

	p := &Publisher{
		Conn: conn,
	}

	// 更新指标
	monitor.GetMetrics().SetNATSConnected(true)

	return p, nil
}

// PublishAddressSignal 发布地址信号
func (p *Publisher) PublishAddressSignal(signal *HlAddressSignal) error {
	data, err := signal.Marshal()
	if err != nil {
		logger.Error().Err(err).Msg("marshal signal failed")
		return err
	}

	return p.Publish(TopicHLAddressSignal, data)
}

// IsConnected 检查发布器是否已连接
func (p *Publisher) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return !p.closed && p.Conn != nil && !p.Conn.IsClosed()
}

// Close 关闭连接
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true

	// 更新指标
	monitor.GetMetrics().SetNATSConnected(false)

	if p.Conn != nil {
		p.Conn.Close()
	}
	return nil
}
