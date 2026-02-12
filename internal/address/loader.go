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

// AddressLoader 地址加载器 - 从 hl_active_addresses 表加载监控地址
type AddressLoader struct {
	subscribers   []AddressSubscriber
	interval      time.Duration
	removeGrace   time.Duration
	lastAddrs     map[string]bool
	pendingRemove map[string]time.Time // 待移除地址 → 发现消失的时间
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewAddressLoader 创建地址加载器
func NewAddressLoader(subscribers []AddressSubscriber, interval, removeGrace time.Duration) *AddressLoader {
	ctx, cancel := context.WithCancel(context.Background())
	return &AddressLoader{
		subscribers:   subscribers,
		interval:      interval,
		removeGrace:   removeGrace,
		lastAddrs:     make(map[string]bool),
		pendingRemove: make(map[string]time.Time),
		ctx:           ctx,
		cancel:        cancel,
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

	now := time.Now()

	l.mu.Lock()

	var toAdd, toUnsubscribe []string
	var pendingCount, recoveredCount int

	// 新增地址：DB 中存在但 lastAddrs 中不存在
	for addr := range addrs {
		if !l.lastAddrs[addr] {
			toAdd = append(toAdd, addr)
		}
		// 地址恢复：从 pending 中移除
		if _, pending := l.pendingRemove[addr]; pending {
			delete(l.pendingRemove, addr)
			recoveredCount++
		}
	}

	// 消失地址：lastAddrs 中存在但 DB 中不存在
	for addr := range l.lastAddrs {
		if !addrs[addr] {
			if _, pending := l.pendingRemove[addr]; !pending {
				l.pendingRemove[addr] = now
			}
		}
	}

	// 检查宽限期到期的地址
	for addr, since := range l.pendingRemove {
		if now.Sub(since) >= l.removeGrace {
			toUnsubscribe = append(toUnsubscribe, addr)
			delete(l.pendingRemove, addr)
		}
	}
	pendingCount = len(l.pendingRemove)

	// 更新 lastAddrs: DB 地址 + 仍在宽限期内的 pending 地址
	l.lastAddrs = addrs
	for addr := range l.pendingRemove {
		l.lastAddrs[addr] = true
	}

	l.mu.Unlock()

	// 执行订阅
	for _, addr := range toAdd {
		for _, sub := range l.subscribers {
			if err = sub.SubscribeAddress(addr); err != nil {
				logger.Error().Err(err).Str("address", addr).Msg("subscribe address failed")
			} else {
				logger.Info().Str("address", addr).Msg("subscribed new address")
			}
		}
	}

	// 执行取消订阅（宽限期到期）
	for _, addr := range toUnsubscribe {
		for _, sub := range l.subscribers {
			if err = sub.UnsubscribeAddress(addr); err != nil {
				logger.Error().Err(err).Str("address", addr).Msg("unsubscribe address failed")
			} else {
				logger.Info().Str("address", addr).Msg("unsubscribed address (grace expired)")
			}
		}
	}

	if recoveredCount > 0 {
		logger.Info().Int("recovered", recoveredCount).Msg("addresses recovered from pending removal")
	}

	logger.Info().
		Int("total", len(addrs)).
		Int("added", len(toAdd)).
		Int("unsubscribed", len(toUnsubscribe)).
		Int("pending_remove", pendingCount).
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
