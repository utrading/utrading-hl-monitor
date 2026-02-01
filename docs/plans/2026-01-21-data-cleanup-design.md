# 数据定时清理模块设计

**日期**: 2026-01-21
**作者**: Claude Code
**状态**: 设计完成

## 概述

创建独立的 `cleaner` 模块，定时清理历史数据，避免数据库无限增长。

## 清理策略

| 表名 | 保留时间 | 清理条件 | 清理频率 |
|------|----------|----------|----------|
| `hl_order_aggregation` | 2 小时 | `updated_at < NOW() - 2h` | 每小时 |
| `hl_address_signals` | 7 天 | `created_at < NOW() - 7d` | 每小时 |

## 架构设计

### 模块结构

```
internal/cleaner/
└── cleaner.go      # Cleaner 主结构
```

### 核心组件

**Cleaner 结构体**：
```go
type Cleaner struct {
    db       *gorm.DB
    interval time.Duration  // 清理间隔（1 小时）
    done     chan struct{}  // 停止信号
}
```

**运行方式**：
- 独立 goroutine，使用 `time.Ticker` 定时触发
- 启动时立即执行一次清理
- 优雅关闭时停止 Ticker

### 数据流

```
Ticker (每 1 小时)
    ↓
clean() 方法
    ↓
┌─────────────────────┬──────────────────────┐
│ cleanOrderAggregation │ cleanAddressSignals  │
│ (2 小时前)            │ (7 天前)              │
└─────────────────────┴──────────────────────┘
    ↓                      ↓
DAO.DeleteOld() → 记录删除数量日志
```

## DAO 层扩展

### SignalDAO 新增方法

```go
// DeleteOld 清理过期数据（早于指定时间的记录）
func (d *SignalDAO) DeleteOld(before time.Time) (int64, error) {
    result := gen.HlAddressSignal.Where(
        gen.HlAddressSignal.CreatedAt.Lt(before),
    ).Delete()

    return result, result.Error
}
```

### OrderAggregationDAO 修改

**修改前**（只删除已发送信号的记录）：
```go
func (d *OrderAggregationDAO) DeleteOld(beforeTimestamp int64) error {
    _, err := gen.OrderAggregation.Where(
        gen.OrderAggregation.SignalSent.Is(true),  // 只删除已发送
        gen.OrderAggregation.UpdatedAt.Lt(time.Unix(beforeTimestamp, 0)),
    ).Delete()
    return err
}
```

**修改后**（删除所有记录）：
```go
// DeleteOld 清理过期数据（删除所有早于指定时间的记录）
func (d *OrderAggregationDAO) DeleteOld(beforeTimestamp int64) (int64, error) {
    result := gen.OrderAggregation.Where(
        // 移除 SignalSent 限制
        gen.OrderAggregation.UpdatedAt.Lt(time.Unix(beforeTimestamp, 0)),
    ).Delete()

    return result, result.Error
}
```

## Cleaner 实现细节

### 核心方法

**Start() 方法**：
```go
func (c *Cleaner) Start() {
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
}
```

**clean() 主方法**：
```go
func (c *Cleaner) clean() {
    logger.Debug().Msg("running cleanup task")

    if err := c.cleanOrderAggregation(); err != nil {
        logger.Error().Err(err).Msg("clean order aggregation failed")
    }

    if err := c.cleanAddressSignals(); err != nil {
        logger.Error().Err(err).Msg("clean address signals failed")
    }
}
```

**cleanOrderAggregation() 方法**：
```go
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
```

**cleanAddressSignals() 方法**：
```go
func (c *Cleaner) cleanAddressSignals() error {
    cutoff := time.Now().AddDate(0, 0, -7)
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
```

## main.go 集成

```go
func main() {
    // ... 初始化数据库、DAO

    // 创建清理器
    cleaner := cleaner.NewCleaner(dal.MySQL())
    go cleaner.Start()
    defer cleaner.Stop()

    // ... 其他组件初始化和启动

    // 优雅关闭
    shutdown := make(chan os.Signal, 1)
    signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
    <-shutdown

    logger.Info().Msg("shutting down...")
    cleaner.Stop()
    // ... 其他清理
}
```

## 日志输出示例

**启动日志**：
```
INFO cleaner started
```

**清理日志**（有数据删除时）：
```
INFO cleaned old order aggregations deleted=150 cutoff=2026-01-21T15:00:00Z
INFO cleaned old address signals deleted=50 cutoff=2026-01-14T17:00:00Z
```

**关闭日志**：
```
INFO cleaner stopped
```

## 配置说明

- **硬编码间隔**: 1 小时（简单直接，无需配置）
- **保留策略**: OrderAggregation 2 小时，HlAddressSignal 7 天
- **日志级别**: 删除记录时输出 info 级别日志

## 错误处理

- 单个清理任务失败不影响另一个任务
- 错误记录到日志，不中断服务
- 下次定时触发时重试

## 优势分析

1. **独立模块**: 职责单一，不影响业务逻辑
2. **复用 DAO**: 利用现有的数据访问层
3. **简单可靠**: 硬编码配置，减少出错点
4. **优雅关闭**: 支持信号驱动的优雅停止
5. **可观测性**: 清理操作有日志记录

## 权衡考虑

1. **硬编码配置**: 不够灵活，但减少配置复杂度
2. **全表扫描**: 大数据量时可能需要索引优化（已有 `idx_created`, `updated_at` 索引）
3. **同步删除**: 删除操作阻塞 goroutine，但频率低（1小时）且事务小
