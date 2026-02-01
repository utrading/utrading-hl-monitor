# BatchWriter 数据去重优化实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**目标:** 完善 BatchWriter，添加基于 concurrent.Map 的缓冲区内数据去重机制，支持 OrderAggregation 和 HlPositionCache 的批量写入，降低数据库刷新频率从 100ms 到 2 秒。

**架构:** BatchItem 接口添加 DedupKey() 方法，缓冲区从 map+锁改为 concurrent.Map[string]BatchItem 实现 O(1) 查找和覆盖，DAO 层实现批量 upsert 方法支持 ON CONFLICT 处理。

**技术栈:** Go 1.23+, concurrent.Map, GORM, gorm-gen, Prometheus

---

## Task 1: 扩展 BatchItem 接口添加 DedupKey 方法

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 修改 BatchItem 接口定义**

在 `internal/processor/batch_writer.go` 中修改接口：

```go
// BatchItem 批量写入项接口
type BatchItem interface {
    TableName() string
    DedupKey() string  // 新增：返回去重键
}
```

**Step 2: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译失败（PositionCacheItem 未实现 DedupKey 方法）

**Step 3: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "refactor(batch): 添加 DedupKey 方法到 BatchItem 接口"
```

---

## Task 2: PositionCacheItem 实现 DedupKey 方法

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 找到 PositionCacheItem 定义**

在 `internal/processor/batch_writer.go` 找到 PositionCacheItem 结构体定义（约第 20-28 行）。

**Step 2: 添加 DedupKey 方法实现**

在 PositionCacheItem 的 TableName() 方法后添加：

```go
// DedupKey 返回去重键（基于 address）
func (i PositionCacheItem) DedupKey() string {
    return "pc:" + i.Address  // pc = position cache
}
```

**Step 3: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译通过

**Step 4: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "feat(batch): PositionCacheItem 实现 DedupKey 方法"
```

---

## Task 3: 创建 OrderAggregationItem 类型

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 在 PositionCacheItem 定义后添加 OrderAggregationItem**

在 `internal/processor/batch_writer.go` 中 PositionCacheItem 定义后添加：

```go
// OrderAggregationItem 订单聚合项
type OrderAggregationItem struct {
    Aggregation *models.OrderAggregation
}

func (i OrderAggregationItem) TableName() string {
    return "hl_order_aggregation"
}

func (i OrderAggregationItem) DedupKey() string {
    return fmt.Sprintf("oa:%d:%s:%s",
        i.Aggregation.Oid,
        i.Aggregation.Address,
        i.Aggregation.Direction)
}
```

**Step 2: 添加 fmt 包导入**

在文件开头的 import 区块添加：

```go
import (
    "errors"
    "fmt"
    "sync"
    "time"
    // ...
)
```

**Step 3: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译通过

**Step 4: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "feat(batch): 创建 OrderAggregationItem 类型"
```

---

## Task 4: 修改 BatchWriter 结构使用 concurrent.Map

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 修改 BatchWriter 结构体定义**

将 BatchWriter 结构体修改为：

```go
import (
    "github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

// BatchWriter 批量写入器
type BatchWriter struct {
    config    BatchWriterConfig
    queue     chan BatchItem
    buffers   concurrent.Map[string]BatchItem  // 改为 concurrent.Map
    db        *gorm.DB
    flushTick *time.Ticker
    done      chan struct{}
    wg        sync.WaitGroup
}
```

删除 `mu sync.RWMutex` 字段（不再需要）。

**Step 2: 修改 NewBatchWriter 初始化**

将 `buffers: make(map[string][]BatchItem)` 改为：

```go
buffers: concurrent.Map[string,BatchItem]{},
```

**Step 3: 修改 FlushInterval 默认值**

将 `config.FlushInterval = 100 * time.Millisecond` 改为：

```go
config.FlushInterval = 2 * time.Second  // 100ms → 2s
```

**Step 4: 添加 concurrent 包导入**

在 import 区块添加：

```go
"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
```

**Step 5: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译失败（receiveLoop 和 flush 方法需要修改）

**Step 6: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "refactor(batch): 将缓冲区改为 concurrent.Map，FlushInterval 改为 2 秒"
```

---

## Task 5: 重写 receiveLoop 实现去重逻辑

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 完全重写 receiveLoop 方法**

替换现有的 receiveLoop 函数：

```go
func (w *BatchWriter) receiveLoop() {
    defer w.wg.Done()
    for {
        select {
        case item := <-w.queue:
            key := item.DedupKey()
            w.buffers.Store(key, item)  // 直接覆盖，Len() 自动维护

            // 检查是否达到批量大小
            if w.buffers.Len() >= int64(w.config.BatchSize) {
                w.flushAll()
            }
        case <-w.done:
            // 处理队列中剩余的数据
            for len(w.queue) > 0 {
                item := <-w.queue
                key := item.DedupKey()
                w.buffers.Store(key, item)
            }
            return
        }
    }
}
```

**Step 2: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译失败（flush 方法需要修改）

**Step 3: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "refactor(batch): 重写 receiveLoop 实现去重逻辑"
```

---

## Task 6: 重写 flush 和 flushAll 方法

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 重写 flush 方法**

替换现有的 flush 函数：

```go
// flush 刷新指定表
func (w *BatchWriter) flush(tables ...string) {
    if len(tables) == 0 {
        return
    }

    // 按 table 分组收集数据
    grouped := make(map[string][]BatchItem)
    var keysToDelete []string

    w.buffers.Range(func(key, value any) bool {
        item := value.(BatchItem)
        table := item.TableName()

        // 检查是否需要刷新此表
        for _, t := range tables {
            if t == table {
                grouped[table] = append(grouped[table], item)
                keysToDelete = append(keysToDelete, key.(string))
                break
            }
        }
        return true
    })

    // 执行批量 upsert
    for table, items := range grouped {
        if err := w.batchUpsert(table, items); err != nil {
            logger.Error().Err(err).Str("table", table).Int("count", len(items)).Msg("batch upsert failed")
        } else {
            logger.Debug().Str("table", table).Int("count", len(items)).Msg("batch upsert success")
        }
    }

    // 删除已刷新的数据
    for _, key := range keysToDelete {
        w.buffers.Delete(key)
    }
}
```

**Step 2: 重写 flushAll 方法**

替换现有的 flushAll 函数：

```go
// flushAll 刷新所有表
func (w *BatchWriter) flushAll() {
    tables := make(map[string]bool)
    w.buffers.Range(func(key, value any) bool {
        item := value.(BatchItem)
        tables[item.TableName()] = true
        return true
    })

    tableList := make([]string, 0, len(tables))
    for table := range tables {
        tableList = append(tableList, table)
    }

    w.flush(tableList...)
}
```

**Step 3: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译通过

**Step 4: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "refactor(batch): 重写 flush 和 flushAll 方法"
```

---

## Task 7: 添加 OrderAggregation 批量 upsert 支持

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 修改 batchUpsert 方法**

在 switch 语句中添加 case：

```go
func (w *BatchWriter) batchUpsert(table string, items []BatchItem) error {
    switch table {
    case "hl_position_cache":
        return w.batchUpsertPositions(items)
    case "hl_order_aggregation":  // 新增
        return w.batchUpsertOrderAggregations(items)
    default:
        logger.Warn().Str("table", table).Msg("unsupported table for batch upsert")
        return nil
    }
}
```

**Step 2: 添加 batchUpsertOrderAggregations 方法**

在 batchUpsertPositions 方法后添加：

```go
// batchUpsertOrderAggregations 批量 upsert 订单聚合
func (w *BatchWriter) batchUpsertOrderAggregations(items []BatchItem) error {
    aggs := make([]*models.OrderAggregation, 0, len(items))
    for _, item := range items {
        if agg, ok := item.(OrderAggregationItem); ok {
            aggs = append(aggs, agg.Aggregation)
        }
    }

    if len(aggs) == 0 {
        return nil
    }

    // 调用 DAO 层批量 upsert
    return dao.OrderAggregation().BatchUpsert(aggs)
}
```

**Step 3: 添加 dao 包导入**

在 import 区块添加（如果没有）：

```go
"github.com/utrading/utrading-hl-monitor/internal/dao"
```

**Step 4: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译失败（DAO 方法不存在）

**Step 5: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "feat(batch): 添加 OrderAggregation 批量 upsert 支持"
```

---

## Task 8: DAO 层添加 OrderAggregation 批量 upsert

**Files:**
- Modify: `internal/dao/order_aggregation.go`

**Step 1: 添加 clause 包导入**

在 import 区块添加：

```go
import (
    "sync"
    "time"

    "gorm.io/gorm"
    "gorm.io/gorm/clause"  // 新增

    "github.com/utrading/utrading-hl-monitor/internal/dal/gen"
    "github.com/utrading/utrading-hl-monitor/internal/models"
)
```

**Step 2: 添加 BatchUpsert 方法**

在文件末尾添加：

```go
// BatchUpsert 批量 upsert 订单聚合
// 按 Oid+Address+Direction 复合键冲突处理
func (d *OrderAggregationDAO) BatchUpsert(aggs []*models.OrderAggregation) error {
    db := gen.OrderAggregation.UnderlyingDB()
    return db.Clauses(clause.OnConflict{
        Columns: []clause.Column{
            {Name: "oid"},
            {Name: "address"},
            {Name: "direction"},
        },
        DoUpdates: clause.AssignmentColumns([]string{
            "symbol", "fills", "total_size", "weighted_avg_px",
            "order_status", "last_fill_time", "updated_at",
        }),
    }).Create(&aggs).Error
}
```

**Step 3: 验证编译**

Run: `go build ./internal/dao/...`
Expected: 编译通过

**Step 4: 运行 DAO 测试**

Run: `go test ./internal/dao/... -short -v`
Expected: 测试通过

**Step 5: 提交**

```bash
git add internal/dao/order_aggregation.go
git commit -m "feat(dao): 添加 OrderAggregation 批量 upsert 方法"
```

---

## Task 9: DAO 层添加 PositionCache 批量 upsert

**Files:**
- Modify: `internal/dao/position.go`

**Step 1: 添加 BatchUpsertPositionCache 方法**

在 `UpsertPositionCache` 方法后添加：

```go
// BatchUpsertPositionCache 批量 upsert 仓位缓存
func (d *PositionDAO) BatchUpsertPositionCache(caches []*models.HlPositionCache) error {
    db := gen.HlPositionCache.UnderlyingDB()
    return db.Clauses(clause.OnConflict{
        Columns:   []clause.Column{{Name: "address"}},
        DoUpdates: clause.AssignmentColumns([]string{
            "spot_balances", "spot_total_usd", "futures_positions",
            "account_value", "total_margin_used", "total_ntl_pos",
            "withdrawable", "updated_at",
        }),
    }).Create(&caches).Error
}
```

**Step 2: 验证编译**

Run: `go build ./internal/dao/...`
Expected: 编译通过

**Step 3: 运行 DAO 测试**

Run: `go test ./internal/dao/... -short -v`
Expected: 测试通过

**Step 4: 提交**

```bash
git add internal/dao/position.go
git commit -m "feat(dao): 添加 PositionCache 批量 upsert 方法"
```

---

## Task 10: 更新 batchUpsertPositions 调用 DAO

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 修改 batchUpsertPositions 方法**

替换现有的 batchUpsertPositions 函数：

```go
// batchUpsertPositions 批量 upsert 仓位缓存
func (w *BatchWriter) batchUpsertPositions(items []BatchItem) error {
    caches := make([]*models.HlPositionCache, 0, len(items))
    for _, item := range items {
        if pos, ok := item.(PositionCacheItem); ok {
            caches = append(caches, pos.Cache)
        }
    }

    if len(caches) == 0 {
        return nil
    }

    // 调用 DAO 层批量 upsert
    return dao.Position().BatchUpsertPositionCache(caches)
}
```

**Step 2: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译通过

**Step 3: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "refactor(batch): PositionCache 批量 upsert 改为调用 DAO"
```

---

## Task 11: 删除不再需要的 GORM 导入

**Files:**
- Modify: `internal/processor/batch_writer.go`

**Step 1: 检查 GORM 使用情况**

Run: `grep -n "gorm\." internal/processor/batch_writer.go`
Expected: 只剩导入声明，没有实际使用

**Step 2: 删除 GORM 相关导入**

删除以下导入：

```go
"gorm.io/gorm"
"gorm.io/gorm/clause"
```

**Step 3: 验证编译**

Run: `go build ./internal/processor/...`
Expected: 编译通过

**Step 4: 提交**

```bash
git add internal/processor/batch_writer.go
git commit -m "refactor(batch): 删除不再需要的 GORM 导入"
```

---

## Task 12: 添加 Prometheus 监控指标

**Files:**
- Modify: `internal/monitor/metrics.go`

**Step 1: 添加批量写入指标**

在 `internal/monitor/metrics.go` 中添加：

```go
var (
    batchDedupCacheHit = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "hl_monitor_batch_dedup_cache_hit_total",
            Help: "Total number of deduplication cache hits",
        },
        []string{"table"},
    )
)
```

**Step 2: 验证编译**

Run: `go build ./internal/monitor/...`
Expected: 编译通过

**Step 3: 提交**

```bash
git add internal/monitor/metrics.go
git commit -m "feat(monitor): 添加批量写入去重指标"
```

---

## Task 13: 编写去重功能单元测试

**Files:**
- Modify: `internal/processor/batch_writer_test.go`

**Step 1: 添加去重测试**

```go
func TestBatchWriter_Deduplication(t *testing.T) {
    db := setupTestDB(t)
    writer := NewBatchWriter(db, BatchWriterConfig{
        BatchSize:     10,
        FlushInterval: time.Second,
    })

    // 相同地址的两条数据
    item1 := PositionCacheItem{
        Address: "0x123",
        Cache: &models.HlPositionCache{
            Address:     "0x123",
            AccountValue: "1000",
        },
    }
    item2 := PositionCacheItem{
        Address: "0x123",
        Cache: &models.HlPositionCache{
            Address:     "0x123",
            AccountValue: "2000",  // 更新的值
        },
    }

    writer.Add(item1)
    writer.Add(item2)

    // 验证缓冲区只有 1 条记录（被覆盖）
    if writer.buffers.Len() != 1 {
        t.Errorf("expected buffer size 1, got %d", writer.buffers.Len())
    }

    writer.Stop()
}
```

**Step 2: 运行测试**

Run: `go test ./internal/processor/... -run TestBatchWriter_Deduplication -v`
Expected: PASS

**Step 3: 提交**

```bash
git add internal/processor/batch_writer_test.go
git commit -m "test(batch): 添加去重功能单元测试"
```

---

## Task 14: 编写刷新间隔测试

**Files:**
- Modify: `internal/processor/batch_writer_test.go`

**Step 1: 添加刷新间隔测试**

```go
func TestBatchWriter_FlushInterval(t *testing.T) {
    db := setupTestDB(t)
    writer := NewBatchWriter(db, BatchWriterConfig{
        BatchSize:     100,  // 大批量大小
        FlushInterval: 100 * time.Millisecond,
    })
    writer.Start()
    defer writer.Stop()

    // 添加少量数据（不达到批量大小）
    writer.Add(PositionCacheItem{
        Address: "0x456",
        Cache:   &models.HlPositionCache{Address: "0x456"},
    })

    // 等待定时刷新
    time.Sleep(200 * time.Millisecond)

    // 验证缓冲区已清空
    if writer.buffers.Len() != 0 {
        t.Errorf("expected buffer to be flushed, got size %d", writer.buffers.Len())
    }
}
```

**Step 2: 运行测试**

Run: `go test ./internal/processor/... -run TestBatchWriter_FlushInterval -v`
Expected: PASS

**Step 3: 提交**

```bash
git add internal/processor/batch_writer_test.go
git commit -m "test(batch): 添加刷新间隔测试"
```

---

## Task 15: 验证完整项目编译和测试

**Step 1: 完整编译验证**

Run: `go build ./...`
Expected: 无错误

**Step 2: 运行所有测试**

Run: `go test ./... -short -v 2>&1 | grep -E "(PASS|FAIL|ok)"`
Expected: 所有测试 PASS

**Step 3: 检查 git status**

Run: `git status`
Expected: 无未提交的修改（除了 docs/plans 目录）

**Step 4: 查看提交历史**

Run: `git log --oneline -15`
Expected: 看到所有相关提交

---

## Task 16: 更新配置文件示例

**Files:**
- Modify: `cfg.toml` 或创建 `cfg.example.toml`

**Step 1: 更新 optimization 配置**

确保配置文件中有正确的默认值：

```toml
[optimization]
enabled = true
batch_size = 100         # 批量大小
flush_interval_ms = 2000 # 刷新间隔（毫秒）改为 2000
```

**Step 2: 验证配置语法**

Run: `go run cmd/hl_monitor/main.go -config cfg.toml -h`
Expected: 帮助信息正常显示

**Step 3: 提交**

```bash
git add cfg.toml
git commit -m "config: 更新 flush_interval_ms 默认值为 2000"
```

---

## Task 17: 更新 CLAUDE.md 文档

**Files:**
- Modify: `CLAUDE.md`

**Step 1: 更新消息处理层章节**

找到 "### 消息处理层" 章节，更新 BatchWriter 描述：

```markdown
### 消息处理层

`internal/processor/` 实现异步消息处理和批量写入：

1. **MessageQueue**：异步消息队列
   - 4 个 worker 并发处理
   - 队列满时自动降级为同步处理（背压保护）
   - 优雅关闭超时 5 秒

2. **BatchWriter**：批量数据库写入
   - 批量大小：100 条（可配置）
   - 刷新间隔：2 秒（可配置）
   - **缓冲区内去重**：基于 `concurrent.Map` 和 `DedupKey()` 方法
   - 支持 `hl_position_cache` 和 `hl_order_aggregation` 表
   - DAO 层实现批量 upsert（ON CONFLICT）
   - 优雅关闭强制刷新缓冲区
```

**Step 2: 添加去重机制说明**

在同一章节添加：

```markdown
**去重机制：**

`BatchWriter` 使用 `concurrent.Map` 实现缓冲区内去重：

```go
// 每个 BatchItem 实现去重键
func (i PositionCacheItem) DedupKey() string {
    return "pc:" + i.Address  // 按 address 去重
}

func (i OrderAggregationItem) DedupKey() string {
    return fmt.Sprintf("oa:%d:%s:%s", i.Oid, i.Address, i.Direction)
}

// 相同键的数据会直接覆盖，保留最新值
writer.Add(item)  // 内部调用 item.DedupKey() 并覆盖
```
```

**Step 3: 验证编译**

Run: `go build ./...`
Expected: 无错误

**Step 4: 提交**

```bash
git add CLAUDE.md
git commit -m "docs: 更新 BatchWriter 去重机制说明"
```

---

## 验收标准

1. ✅ `BatchItem` 接口包含 `DedupKey()` 方法
2. ✅ `PositionCacheItem` 和 `OrderAggregationItem` 实现了 `DedupKey()`
3. ✅ `BatchWriter.buffers` 使用 `concurrent.Map`
4. ✅ `FlushInterval` 默认值改为 2 秒
5. ✅ DAO 层实现 `BatchUpsert` 方法
6. ✅ 支持 `hl_order_aggregation` 和 `hl_position_cache` 表
7. ✅ 去重功能单元测试通过
8. ✅ 刷新间隔测试通过
9. ✅ 所有代码编译通过
10. ✅ 文档已更新
