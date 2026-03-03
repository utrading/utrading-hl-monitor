package cache

import (
	"strings"
	"sync"
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

type PairCategoryCache struct {
	data *concurrent.Map[string, uint8]
	mu   sync.RWMutex
}

func NewPairCategoryCache() *PairCategoryCache {
	return &PairCategoryCache{
		data: &concurrent.Map[string, uint8]{},
	}
}

func (c *PairCategoryCache) GetCoinType(symbol string) string {
	coin := c.extractCoin(symbol)
	category, _ := c.data.Load(coin)

	switch category {
	case 1:
		return "A"
	case 2:
		return "B"
	case 3, 4:
		return "C"
	case 5:
		return "D"
	default:
		return "D"
	}
}

func (c *PairCategoryCache) Load() error {
	configs, err := dao.PairConfig().ListAll()
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	for _, cfg := range configs {
		if cfg.Symbol != "" {
			c.data.Store(cfg.Symbol, cfg.Category)
			count++
		}
	}

	logger.Info().Int("count", count).Msg("pair category cache loaded")
	return nil
}

func (c *PairCategoryCache) Start() {
	if err := c.Load(); err != nil {
		logger.Error().Err(err).Msg("reload pair category cache failed")
	}

	ticker := time.NewTicker(30 * time.Minute)
	go func() {
		for range ticker.C {
			if err := c.Load(); err != nil {
				logger.Error().Err(err).Msg("reload pair category cache failed")
			}
		}
	}()
}

func (c *PairCategoryCache) extractCoin(symbol string) string {
	symbol = strings.ToUpper(symbol)
	suffixes := []string{"USDT", "USDC", "USD", "USDH"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(symbol, suffix) {
			return strings.TrimSuffix(symbol, suffix)
		}
	}
	return symbol
}
