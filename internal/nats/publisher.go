package nats

import (
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/utrading/utrading-hl-monitor/config"
	"github.com/utrading/utrading-hl-monitor/internal/monitor"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// Publisher NATS 发布器
type Publisher struct {
	*nats.Conn
	mu     sync.RWMutex
	closed bool
}

// NewPublisher 创建 NATS 发布器（带自动重连）
func NewPublisher(cfg config.NATS) (*Publisher, error) {
	reconnectWait := cfg.ReconnectWait
	if reconnectWait <= 0 {
		reconnectWait = 2 * time.Second
	}
	pingInterval := cfg.PingInterval
	if pingInterval <= 0 {
		pingInterval = 20 * time.Second
	}
	connectTimeout := cfg.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	maxReconnects := cfg.MaxReconnects
	if maxReconnects == 0 {
		maxReconnects = -1 // 无限重连
	}

	opts := []nats.Option{
		nats.Name("hl-monitor"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(maxReconnects),
		nats.ReconnectWait(reconnectWait),
		nats.Timeout(connectTimeout),
		nats.PingInterval(pingInterval),
		nats.MaxPingsOutstanding(5),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			logger.Error().Err(err).Msg("nats disconnected, reconnecting...")
			monitor.GetMetrics().SetNATSConnected(false)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			logger.Info().Str("url", nc.ConnectedUrl()).Msg("nats reconnected")
			monitor.GetMetrics().SetNATSConnected(true)
		}),
		nats.ClosedHandler(func(nc *nats.Conn) {
			logger.Error().Str("url", nc.ConnectedUrl()).Msg("nats connection closed permanently")
			monitor.GetMetrics().SetNATSConnected(false)
		}),
	}

	conn, err := nats.Connect(cfg.Endpoint, opts...)
	if err != nil {
		return nil, err
	}

	p := &Publisher{
		Conn: conn,
	}

	// 更新指标
	monitor.GetMetrics().SetNATSConnected(true)

	logger.Info().Str("url", cfg.Endpoint).Msg("nats connected")

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
