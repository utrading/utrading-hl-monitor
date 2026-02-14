---
title: Address Signal 数量限制清理
type: feat
date: 2026-02-14
---

# Address Signal 数量限制清理

## Overview

为 Address Signal 清理逻辑增加数量限制条件，防止表膨胀。

## Problem Statement

现有的 `cleanAddressSignals` 方法仅基于时间条件（保留 7 天）清理信号。随着业务增长，7 天内的信号数量可能超过数据库存储限制或影响查询性能。

## Proposed Solution

**清理策略**：时间优先，数量兜底

```
1. 先删除 7 天前的信号（现有逻辑）
      ↓
2. 检查剩余信号总数
      ↓
3. 如果总数 > 50 万 → 删除最旧的 (总数 - 50 万) 条
```

## Technical Approach

### 修改文件

#### 1. internal/dao/signal.go

新增方法：

```go
// Count 统计信号总数
func (d *SignalDAO) Count() (int64, error)

// DeleteOldest 删除最旧的 N 条记录
func (d *SignalDAO) DeleteOldest(limit int64) (int64, error)
```

#### 2. internal/cleaner/cleaner.go

修改 `cleanAddressSignals()` 方法，增加数量检查：

```go
func (c *Cleaner) cleanAddressSignals() error {
    // 1. 时间清理（现有逻辑）
    cutoff := time.Now().AddDate(0, 0, -7)
    deleted, err := dao.Signal().DeleteOld(cutoff)
    // ...

    // 2. 数量检查
    const maxSignals = 500000
    count, err := dao.Signal().Count()
    if count > maxSignals {
        excess := count - maxSignals
        deleted, err := dao.Signal().DeleteOldest(excess)
        // ...
    }
}
```

## Acceptance Criteria

- [ ] 新增 `SignalDAO.Count()` 方法
- [ ] 新增 `SignalDAO.DeleteOldest(limit)` 方法
- [ ] 修改 `cleanAddressSignals()` 增加数量检查
- [ ] 数量限制为 50 万条
- [ ] 执行顺序：先时间清理，后数量清理

## References

- Brainstorm: `docs/brainstorms/2026-02-13-signal-count-cleanup-brainstorm.md`
