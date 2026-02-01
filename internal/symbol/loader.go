package symbol

import (
	"context"
	"strings"
	"time"

	"github.com/sonirico/go-hyperliquid"
	"github.com/utrading/utrading-hl-monitor/internal/cache"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// Loader Symbol 元数据加载器
type Loader struct {
	cache          *cache.SymbolCache
	client         *hyperliquid.Info
	httpURL        string
	reloadInterval time.Duration
	done           chan struct{}
}

// NewLoader 创建 Loader，首次加载失败会返回错误
func NewLoader(symbolCache *cache.SymbolCache, httpURL string) (*Loader, error) {
	sl := &Loader{
		cache:          symbolCache,
		client:         hyperliquid.NewInfo(context.TODO(), httpURL, false, nil, nil),
		httpURL:        httpURL,
		reloadInterval: 2 * time.Hour,
		done:           make(chan struct{}),
	}

	if err := sl.loadMeta(); err != nil {
		return nil, err
	}

	logger.Info().
		Int("spot_count", sl.getSpotCount()).
		Int("perp_count", sl.getPerpCount()).
		Msg("Loader initialized")

	return sl, nil
}

// Start 启动后台重载
func (sl *Loader) Start() {
	ticker := time.NewTicker(sl.reloadInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := sl.loadMeta(); err != nil {
					logger.Error().Err(err).Msg("reload symbol meta failed")
				}
			case <-sl.done:
				return
			}
		}
	}()
}

// Close 停止重载
func (sl *Loader) Close() {
	close(sl.done)
}

// loadMeta 从 Hyperliquid API 加载元数据
func (sl *Loader) loadMeta() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	spotMeta, err := sl.client.SpotMeta(ctx)
	if err != nil {
		return err
	}

	sl.buildSpotCache(spotMeta)

	perpMeta, err := sl.client.PerpMeta(ctx)
	if err != nil {
		return err
	}

	sl.buildPerpCache(perpMeta)

	logger.Info().
		Int("spot_count", sl.getSpotCount()).
		Int("perp_count", sl.getPerpCount()).
		Msg("symbol meta reloaded")

	return nil
}

// buildSpotCache 构建现货缓存
func (sl *Loader) buildSpotCache(spotMeta *hyperliquid.SpotMeta) {
	spotTokenLen := len(spotMeta.Tokens)

	for _, spotInfo := range spotMeta.Universe {
		if len(spotInfo.Tokens) < 2 ||
			spotTokenLen <= spotInfo.Tokens[1] ||
			spotTokenLen <= spotInfo.Tokens[0] {
			continue
		}

		baseToken := spotMeta.Tokens[spotInfo.Tokens[0]]
		quoteCoin := spotMeta.Tokens[spotInfo.Tokens[1]].Name
		baseCoin := hyperliquid.MainnetToAlias(baseToken.Name)
		symbol := baseCoin + quoteCoin
		sl.cache.SetSpotSymbol(spotInfo.Name, symbol)
	}
}

// buildPerpCache 构建合约缓存
func (sl *Loader) buildPerpCache(perpMeta []*hyperliquid.Meta) {
	for _, meta := range perpMeta {
		for _, assetInfo := range meta.Universe {
			cleanName := assetInfo.Name
			if strings.Contains(assetInfo.Name, ":") {
				parts := strings.Split(assetInfo.Name, ":")
				if len(parts) == 2 && parts[0] == "xyz" {
					cleanName = parts[1]
				}
			}

			cleanName = hyperliquid.MainnetToAlias(cleanName)
			symbol := cleanName + "USDC"
			sl.cache.SetPerpSymbol(cleanName, symbol)

			if assetInfo.Name != cleanName {
				sl.cache.SetPerpSymbol(assetInfo.Name, symbol)
			}
		}
	}
}

// getSpotCount 获取现货缓存数量
func (sl *Loader) getSpotCount() int {
	stats := sl.cache.Stats()
	if spotCount, ok := stats["spot_name_to_symbol_count"].(int64); ok {
		return int(spotCount)
	}
	return 0
}

// getPerpCount 获取合约缓存数量
func (sl *Loader) getPerpCount() int {
	stats := sl.cache.Stats()
	if perpCount, ok := stats["perp_name_to_symbol_count"].(int64); ok {
		return int(perpCount)
	}
	return 0
}
