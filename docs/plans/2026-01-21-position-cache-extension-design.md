# PositionBalanceCache 扩展设计 - 支持持仓数量查询

**日期**: 2026-01-21
**作者**: Claude Code
**状态**: 设计完成

## 概述

扩展 `PositionBalanceCache` 以支持直接查询具体币种的持仓数量，从而实现准确的 CloseRate（平仓比例）计算。

## 当前问题

**现有 PositionBalanceCache**：
- 只缓存总价值（SpotTotalUSD、AccountValue）
- 不包含具体持仓信息
- 计算 CloseRate 时无法获取当前持仓数量

**calculateCloseRate 当前实现**：
```go
func (p *OrderProcessor) calculateCloseRate(direction string, size float64) float64 {
    if direction != "close" {
        return 0
    }
    return size  // 简化处理，直接返回 size
}
```

## 设计目标

扩展 PositionBalanceCache：
1. 缓存反序列化后的持仓数据结构体
2. 提供 GetSpotBalance 和 GetFuturesPosition 方法
3. 支持按 coin/symbol 精确查询持仓数量

## 架构设计

### PositionBalanceCache 结构扩展

**修改前**：
```go
type PositionBalanceCache struct {
    spotTotals    concurrent.Map[string, float64] // address → SpotTotalUSD
    accountValues concurrent.Map[string, float64] // address → AccountValue
}
```

**修改后**：
```go
type PositionBalanceCache struct {
    spotTotals      concurrent.Map[string, float64]                  // address → SpotTotalUSD
    accountValues    concurrent.Map[string, float64]                  // address → AccountValue
    spotBalances     concurrent.Map[string, *models.SpotBalancesData] // address → 现货持仓数据
    futuresPositions concurrent.Map[string, *models.FuturesPositionsData] // address → 合约持仓数据
}
```

### 新增方法

```go
// Set 更新总价值和持仓数据
func (c *PositionBalanceCache) Set(address string, spotTotal float64, accountValue float64, spotBalances *models.SpotBalancesData, futuresPositions *models.FuturesPositionsData)

// GetSpotBalance 获取指定现货币种的持仓数量
func (c *PositionBalanceCache) GetSpotBalance(address string, coin string) (float64, bool)

// GetFuturesPosition 获取指定合约币种的持仓数量
func (c *PositionBalanceCache) GetFuturesPosition(address string, coin string) (float64, bool)
```

### 实现细节

**GetSpotBalance 实现**：
```go
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
```

**GetFuturesPosition 实现**：
```go
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
```

## PositionManager 修改

**修改位置**：`processPositionCache` 方法

**修改前**：
```go
// 同时更新内存缓存
accountValue := cast.ToFloat64(marginSummary.AccountValue)
m.positionBalanceCache.Set(addr, spotTotalUSD, accountValue)
```

**修改后**：
```go
// 同时更新内存缓存（包括持仓数据）
accountValue := cast.ToFloat64(marginSummary.AccountValue)
m.positionBalanceCache.Set(addr, spotTotalUSD, accountValue, &spotBalances, &futuresPositions)
```

**Set 方法签名更新**：
```go
func (c *PositionBalanceCache) Set(address string, spotTotal float64, accountValue float64, spotBalances *models.SpotBalancesData, futuresPositions *models.FuturesPositionsData)
```

## OrderProcessor 集成

### calculateCloseRate 完整实现

```go
// calculateCloseRate 计算平仓比例
func (p *OrderProcessor) calculateCloseRate(direction string, assetType string, address string, symbol string, size float64) float64 {
    // 只有平仓订单才计算平仓比例
    if direction != "close" {
        return 0
    }

    if p.positionBalanceCache == nil {
        return 0
    }

    var currentPosition float64
    var ok bool

    // 根据 assetType 判断现货还是合约
    if assetType == "spot" {
        // 去掉 symbol 尾部的 USDC，获取原始 coin 名称
        coin := strings.TrimSuffix(symbol, "USDC")
        currentPosition, ok = p.positionBalanceCache.GetSpotBalance(address, coin)
    } else { // futures
        currentPosition, ok = p.positionBalanceCache.GetFuturesPosition(address, symbol)
    }

    if !ok || currentPosition <= 0 {
        // 无法获取当前持仓，返回 0
        return 0
    }

    // 计算平仓比例: 平仓数量 / 当前持仓数量
    if size > currentPosition {
        // 防止超过 100%
        return 1.0
    }

    return size / currentPosition
}
```

### buildSignal 调用修改

```go
// 计算 CloseRate（平仓比例）
closeRate := p.calculateCloseRate(direction, assetType, agg.Address, agg.Symbol, agg.TotalSize)
```

## 数据流

```
WebSocket WebData2 消息
    ↓
PositionManager.processPositionCache
    ↓
┌─────────────────────────────────────────────┐
│ 解析持仓数据                                  │
│   - spotBalances (models.SpotBalancesData)   │
│   - futuresPositions (models.FuturesPositionsData)│
└─────────────────────────────────────────────┘
    ↓
PositionBalanceCache.Set()
    ↓ (存储结构体数据)
┌─────────────────────────────────────────────┐
│ OrderProcessor.buildSignal                   │
│   ↓                                          │
│ calculateCloseRate()                         │
│   - GetSpotBalance(spot) 或                   │
│   - GetFuturesPosition(futures)              │
│   - 计算 size / currentPosition              │
└─────────────────────────────────────────────┘
```

## 边界情况

| 情况 | 处理方式 |
|------|----------|
| 无法获取当前持仓 | 返回 0（表示无法计算） |
| size > currentPosition | 返回 1.0（防止超过 100%） |
| 开仓订单 | 返回 0（只有平仓订单有 CloseRate） |
| assetType = "spot" | 去掉 symbol 的 "USDC" 后缀查询 |
| assetType = "futures" | 直接使用 symbol 查询 |

## 优势分析

1. **准确计算**：可以精确计算平仓比例，而不是简化处理
2. **内存缓存**：避免重复反序列化 JSON
3. **类型安全**：直接访问结构体字段，避免字符串解析
4. **向后兼容**：保留原有的总价值缓存

## 权衡考虑

1. **内存占用**：每个地址额外缓存持仓数据结构体，但数据量小（通常 < 10 个持仓项）
2. **同步更新**：PositionManager 需要在每次 WebData2 到达时更新缓存，增加少量开销
3. **symbol 转换**：现货查询时需要去掉 "USDC" 后缀，增加字符串操作
