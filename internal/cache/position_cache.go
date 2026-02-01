package cache

import (
	"github.com/spf13/cast"
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

// PositionBalanceCache 仓位余额缓存
// 缓存现货总价值、账户价值和持仓数据，避免频繁数据库查询
type PositionBalanceCache struct {
	spotTotals      concurrent.Map[string, float64]                  // address → SpotTotalUSD
	accountValues    concurrent.Map[string, float64]                  // address → AccountValue
	spotBalances     concurrent.Map[string, *models.SpotBalancesData] // address → 现货持仓数据
	futuresPositions concurrent.Map[string, *models.FuturesPositionsData] // address → 合约持仓数据
}

// NewPositionBalanceCache 创建缓存实例
func NewPositionBalanceCache() *PositionBalanceCache {
	return &PositionBalanceCache{}
}

// Set 更新总价值和持仓数据
func (c *PositionBalanceCache) Set(address string, spotTotal float64, accountValue float64, spotBalances *models.SpotBalancesData, futuresPositions *models.FuturesPositionsData) {
	c.spotTotals.Store(address, spotTotal)
	c.accountValues.Store(address, accountValue)
	c.spotBalances.Store(address, spotBalances)
	c.futuresPositions.Store(address, futuresPositions)
}

// GetSpotTotal 获取现货总价值
func (c *PositionBalanceCache) GetSpotTotal(address string) (float64, bool) {
	return c.spotTotals.Load(address)
}

// GetAccountValue 获取账户价值
func (c *PositionBalanceCache) GetAccountValue(address string) (float64, bool) {
	return c.accountValues.Load(address)
}

// GetSpotBalance 获取指定现货币种的持仓数量
func (c *PositionBalanceCache) GetSpotBalance(address string, coin string) (float64, bool) {
	data, found := c.spotBalances.Load(address)
	if !found {
		return 0, false
	}

	for _, balance := range *data {
		if balance.Coin == coin {
			return cast.ToFloat64(balance.Total), true
		}
	}
	return 0, false
}

// GetFuturesPosition 获取指定合约币种的持仓数量
func (c *PositionBalanceCache) GetFuturesPosition(address string, coin string) (float64, bool) {
	data, found := c.futuresPositions.Load(address)
	if !found {
		return 0, false
	}

	for _, position := range *data {
		if position.Coin == coin {
			return cast.ToFloat64(position.Szi), true
		}
	}
	return 0, false
}

// Delete 删除缓存（取消订阅时使用）
func (c *PositionBalanceCache) Delete(address string) {
	c.spotTotals.Delete(address)
	c.accountValues.Delete(address)
	c.spotBalances.Delete(address)
	c.futuresPositions.Delete(address)
}
