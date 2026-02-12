package address

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/pkg/goplus"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
	"gorm.io/gorm"
)

// AddressSubscriber 地址订阅管理器接口
type AddressSubscriber interface {
	SubscribeAddress(addr string) error
	UnsubscribeAddress(addr string) error
}

// AddressLoader 地址加载器 - 从 hl_watch_addresses 表加载监控地址
type AddressLoader struct {
	subscribers []AddressSubscriber
	interval    time.Duration
	lastAddrs   map[string]bool
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
}

// NewAddressLoader 创建地址加载器
func NewAddressLoader(subscribers []AddressSubscriber, interval time.Duration) *AddressLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &AddressLoader{
		subscribers: subscribers,
		interval:    interval,
		lastAddrs:   make(map[string]bool),
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start 启动加载器
func (l *AddressLoader) Start() error {
	if err := l.loadAndSync(); err != nil {
		return err
	}

	goplus.Go(func() {
		l.periodicReload()
	})
	return nil
}

func (l *AddressLoader) periodicReload() {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			if err := l.loadAndSync(); err != nil {
				logger.Error().Err(err).Msg("address reload failed")
			}
		}
	}
}

func (l *AddressLoader) loadAndSync() error {
	addrs, err := l.loadActiveAddresses()
	if err != nil {
		return err
	}

	l.mu.Lock()
	toAdd := make([]string, 0)
	toRemove := make([]string, 0)

	for addr := range addrs {
		if !l.lastAddrs[addr] {
			toAdd = append(toAdd, addr)
		}
	}

	for addr := range l.lastAddrs {
		if !addrs[addr] {
			toRemove = append(toRemove, addr)
		}
	}

	l.lastAddrs = addrs
	l.mu.Unlock()

	for _, addr := range toAdd {
		for _, sub := range l.subscribers {
			if err = sub.SubscribeAddress(addr); err != nil {
				logger.Error().Err(err).Str("address", addr).Msg("subscribe address failed")
			} else {
				logger.Info().Str("address", addr).Msg("subscribed new address")
			}
		}
	}

	for _, addr := range toRemove {
		for _, sub := range l.subscribers {
			if err = sub.UnsubscribeAddress(addr); err != nil {
				logger.Error().Err(err).Str("address", addr).Msg("unsubscribe address failed")
			} else {
				logger.Info().Str("address", addr).Msg("unsubscribed address")
			}
		}
	}

	logger.Info().
		Int("total", len(addrs)).
		Int("added", len(toAdd)).
		Int("removed", len(toRemove)).
		Msg("address sync completed")
	return nil
}

// loadActiveAddresses 从 hl_active_addresses 表加载地址
func (l *AddressLoader) loadActiveAddresses() (map[string]bool, error) {
	addresses, err := dao.ActiveAddress().ListDistinctAddresses()
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return make(map[string]bool), nil
		}
		return nil, err
	}

	result := make(map[string]bool, len(addresses))
	for _, addr := range addresses {
		result[addr] = true
	}

	logger.Debug().
		Int("count", len(result)).
		Msg("loaded addresses from hl_active_addresses")

	return result, nil
}

// Stop 停止加载器
func (l *AddressLoader) Stop() {
	l.cancel()
}
