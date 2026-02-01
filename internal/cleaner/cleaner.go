package cleaner

import (
	"time"

	"gorm.io/gorm"

	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// Cleaner 数据清理器，定时清理历史数据
type Cleaner struct {
	db       *gorm.DB
	interval time.Duration // 清理间隔
	done     chan struct{} // 停止信号
}

// NewCleaner 创建清理器
func NewCleaner(db *gorm.DB) *Cleaner {
	return &Cleaner{
		db:       db,
		interval: 1 * time.Hour, // 固定 1 小时
		done:     make(chan struct{}),
	}
}

// Start 启动清理任务
func (c *Cleaner) Start() {
	go func() {
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		logger.Info().Msg("cleaner started")

		// 启动时立即执行一次
		c.clean()

		for {
			select {
			case <-ticker.C:
				c.clean()
			case <-c.done:
				logger.Info().Msg("cleaner stopped")
				return
			}
		}
	}()
}

// Stop 停止清理器
func (c *Cleaner) Stop() {
	close(c.done)
}

// clean 执行清理任务
func (c *Cleaner) clean() {
	logger.Debug().Msg("running cleanup task")

	// 清理 OrderAggregation（保留 2 小时）
	if err := c.cleanOrderAggregation(); err != nil {
		logger.Error().Err(err).Msg("clean order aggregation failed")
	}

	// 清理 HlAddressSignal（保留 7 天）
	if err := c.cleanAddressSignals(); err != nil {
		logger.Error().Err(err).Msg("clean address signals failed")
	}
}

// cleanOrderAggregation 清理 2 小时前的订单聚合数据
func (c *Cleaner) cleanOrderAggregation() error {
	cutoff := time.Now().Add(-2 * time.Hour).Unix()
	deleted, err := dao.OrderAggregation().DeleteOld(cutoff)
	if err != nil {
		return err
	}

	if deleted > 0 {
		logger.Info().
			Int64("deleted", deleted).
			Time("cutoff", time.Unix(cutoff, 0)).
			Msg("cleaned old order aggregations")
	}

	return nil
}

// cleanAddressSignals 清理 7 天前的地址信号数据
func (c *Cleaner) cleanAddressSignals() error {
	cutoff := time.Now().AddDate(0, 0, -7) // 7 天前
	deleted, err := dao.Signal().DeleteOld(cutoff)
	if err != nil {
		return err
	}

	if deleted > 0 {
		logger.Info().
			Int64("deleted", deleted).
			Time("cutoff", cutoff).
			Msg("cleaned old address signals")
	}

	return nil
}
