# PendingOrderCache 重构实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**目标:** 将 `OrderProcessor.pendingOrders` 从 `map + sync.RWMutex` 重构为基于 `concurrent.Map` 的 `PendingOrderCache`，并优化 tid 去重逻辑。

**架构:** 使用项目已有的 `pkg/concurrent.Map`（基于 `sync.Map` 的泛型封装）实现线程安全的缓存层。`PendingOrderCache` 管理所有待处理订单，每个 `PendingOrder` 内部使用 `concurrent.Map` 实现 O(1) tid 去重。

**技术栈:**
- `github.com/utrading/utrading-hl-monitor/pkg/concurrent` - 并发安全 Map
- Go 1.23+ (泛型支持)
- 现有测试框架

---

## Task 1: 创建 PendingOrderCache 结构

**文件:**
- 创建: `internal/cache/pending_order_cache.go`

**步骤 1: 创建缓存结构**

```go
package cache

import (
	"github.com/utrading/utrading-hl-monitor/internal/processor"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

// PendingOrderCache 待处理订单缓存
// 使用 concurrent.Map 实现线程安全的短期暂存
type PendingOrderCache struct {
	orders concurrent.Map[string, *processor.PendingOrder]
}

// NewPendingOrderCache 创建缓存实例
func NewPendingOrderCache() *PendingOrderCache {
	return &PendingOrderCache{}
}

// Get 获取订单
func (c *PendingOrderCache) Get(key string) (*processor.PendingOrder, bool) {
	return c.orders.Load(key)
}

// Set 存储订单
func (c *PendingOrderCache) Set(key string, order *processor.PendingOrder) {
	c.orders.Store(key, order)
}

// Delete 删除订单
func (c *PendingOrderCache) Delete(key string) {
	c.orders.Delete(key)
}

// Len 返回订单数量
func (c *PendingOrderCache) Len() int64 {
	return c.orders.Len()
}

// Range 遍历所有订单
func (c *PendingOrderCache) Range(f func(key string, order *processor.PendingOrder) bool) {
	c.orders.Range(f)
}

// Clear 清空所有订单
func (c *PendingOrderCache) Clear() {
	c.orders.Clear()
}
```

**步骤 2: 验证编译**

运行: `go build ./internal/cache/`
预期: 编译成功，无错误

**步骤 3: 提交**

```bash
git add internal/cache/pending_order_cache.go
git commit -m "feat: 添加 PendingOrderCache 基于并发 Map 的订单缓存"
```

---

## Task 2: 修改 PendingOrder 结构

**文件:**
- 修改: `internal/processor/order_processor.go:34-40`

**步骤 1: 添加 tid 去重字段**

将 `PendingOrder` 结构修改为：

```go
// PendingOrder 待处理订单
type PendingOrder struct {
	seenTids concurrent.Map[int64, struct{}] // tid 去重
	Aggregation    *models.OrderAggregation
	FirstFillTime  time.Time
	SymbolConverter SymbolConverter
	BalanceCalc    BalanceCalculator
}
```

**步骤 2: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 3: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: PendingOrder 添加 concurrent.Map 用于 tid 去重"
```

---

## Task 3: 修改 OrderProcessor 结构

**文件:**
- 修改: `internal/processor/order_processor.go:48-61`

**步骤 1: 替换 pendingOrders 字段类型**

将 `OrderProcessor` 结构中的 `pendingOrders` 和 `mu` 字段替换为：

```go
// OrderProcessor 订单处理器
type OrderProcessor struct {
	pendingOrders *cache.PendingOrderCache // 替代 map + RWMutex
	publisher     Publisher
	batchWriter   *BatchWriter
	deduper       cache.DedupCacheInterface
	symbolConv    SymbolConverter
	balanceCalc   BalanceCalculator
	timeout       time.Duration
	flushChan     chan flushKey
	done          chan struct{}
	wg            sync.WaitGroup
}
```

**步骤 2: 更 NewOrderProcessor 初始化**

```go
func NewOrderProcessor(
	publisher Publisher,
	batchWriter *BatchWriter,
	deduper cache.DedupCacheInterface,
	symbolConv SymbolConverter,
	balanceCalc BalanceCalculator,
) *OrderProcessor {
	if batchWriter == nil {
		logger.Warn().Msg("order processor created without batch writer, writes will be skipped")
	}

	op := &OrderProcessor{
		pendingOrders: cache.NewPendingOrderCache(), // 使用新缓存
		publisher:     publisher,
		batchWriter:   batchWriter,
		deduper:       deduper,
		symbolConv:    symbolConv,
		balanceCalc:   balanceCalc,
		timeout:       5 * time.Minute,
		flushChan:     make(chan flushKey, 1000),
		done:          make(chan struct{}),
	}

	// 启动后台协程
	op.wg.Add(2)
	go op.flushProcessor()
	go op.timeoutScanner()

	return op
}
```

**步骤 3: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 4: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: OrderProcessor 使用 PendingOrderCache 替代 map+RWMutex"
```

---

## Task 4: 重写 handleOrderFill 方法

**文件:**
- 修改: `internal/processor/order_processor.go:116-194`

**步骤 1: 重写 handleOrderFill 使用新 API**

```go
// handleOrderFill 处理订单成交
func (p *OrderProcessor) handleOrderFill(msg OrderFillMessage) error {
	fill, ok := msg.Fill.(hyperliquid.WsOrderFill)
	if !ok {
		return fmt.Errorf("invalid fill type")
	}

	key := p.orderKey(msg.Address, fill.Oid, msg.Direction)

	// 转换 symbol
	symbol, err := p.symbolConv.Convert(fill.Coin, fill.Dir)
	if err != nil {
		logger.Warn().
			Str("coin", fill.Coin).
			Str("dir", fill.Dir).
			Err(err).
			Msg("symbol convert failed, using raw coin")
		symbol = fill.Coin
	}

	// 使用 LoadOrStore 原子操作获取或创建订单
	pending, loaded := p.pendingOrders.LoadOrStore(key, &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
		Aggregation: &models.OrderAggregation{
			Oid:          fill.Oid,
			Address:      msg.Address,
			Symbol:       symbol,
			Direction:    msg.Direction,
			OrderStatus:  "open",
			LastFillTime: time.Now().Unix(),
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
			Fills:        []hyperliquid.WsOrderFill{fill},
			TotalSize:    cast.ToFloat64(fill.Sz),
			WeightedAvgPx: cast.ToFloat64(fill.Px),
		},
		FirstFillTime:   time.Now(),
		SymbolConverter: p.symbolConv,
		BalanceCalc:     p.balanceCalc,
	})

	if !loaded {
		// 新订单，更新监控指标
		monitor.SetOrderAggregationActive(int(p.pendingOrders.Len()))
		logger.Debug().
			Int64("oid", fill.Oid).
			Str("direction", msg.Direction).
			Msg("new order aggregation created")
	} else {
		// 已存在订单，使用 LoadOrStore 进行 tid 去重
		_, tidLoaded := pending.seenTids.LoadOrStore(fill.Tid, struct{}{})
		if tidLoaded {
			// 重复的 fill
			logger.Debug().
				Int64("oid", fill.Oid).
				Int64("tid", fill.Tid).
				Str("direction", msg.Direction).
				Msg("duplicate fill skipped")
			return nil
		}

		// 追加 fill
		pending.Aggregation.Fills = append(pending.Aggregation.Fills, fill)
		pending.Aggregation.TotalSize, pending.Aggregation.WeightedAvgPx = p.calculateWeightedAvg(pending.Aggregation.Fills)
		pending.Aggregation.LastFillTime = time.Now().Unix()
		pending.Aggregation.UpdatedAt = time.Now()

		// 记录 fill 数量
		monitor.ObserveFillsPerOrder(len(pending.Aggregation.Fills))
	}

	// 持久化到数据库
	p.persistOrder(pending.Aggregation)

	return nil
}
```

**步骤 2: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 3: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: handleOrderFill 使用 concurrent.Map 实现 O(1) tid 去重"
```

---

## Task 5: 重写 UpdateStatus 方法

**文件:**
- 修改: `internal/processor/order_processor.go:196-214`

**步骤 1: 使用新 API**

```go
// UpdateStatus 更新订单状态
func (p *OrderProcessor) UpdateStatus(address string, oid int64, status string, direction string) {
	if direction == "" {
		// 查找所有方向的订单（通过遍历）
		p.pendingOrders.Range(func(key string, pending *PendingOrder) bool {
			if pending.Aggregation.Address == address && pending.Aggregation.Oid == oid {
				p.triggerFlush(key, "status")
			}
			return true
		})
	} else {
		key := p.orderKey(address, oid, direction)
		if _, exists := p.pendingOrders.Get(key); exists {
			p.triggerFlush(key, "status")
		}
	}
}
```

**步骤 2: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 3: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: UpdateStatus 使用 PendingOrderCache.Range"
```

---

## Task 6: 重写 flushOrder 方法

**文件:**
- 修改: `internal/processor/order_processor.go:278-325`

**步骤 1: 使用新 API**

```go
// flushOrder 发送订单信号
func (p *OrderProcessor) flushOrder(key string, trigger string) {
	pending, exists := p.pendingOrders.Get(key)
	if !exists {
		return
	}

	if pending.Aggregation.SignalSent {
		return
	}

	// 构建信号
	signal := p.buildSignal(pending.Aggregation)
	if signal == nil {
		return
	}

	// 1. 发布到 NATS
	if err := p.publisher.PublishAddressSignal(signal); err != nil {
		logger.Error().Err(err).Int64("oid", pending.Aggregation.Oid).Msg("publish signal failed")
		return
	}

	// 2. 标记已发送
	pending.Aggregation.SignalSent = true

	// 3. 标记到去重器
	if p.deduper != nil {
		p.deduper.Mark(pending.Aggregation.Address, pending.Aggregation.Oid, pending.Aggregation.Direction)
	}

	// 4. 从待处理列表移除
	p.pendingOrders.Delete(key)
	monitor.SetOrderAggregationActive(int(p.pendingOrders.Len()))

	// 5. 记录发送指标
	monitor.IncOrderFlush(trigger)

	logger.Info().
		Int64("oid", pending.Aggregation.Oid).
		Str("symbol", signal.Symbol).
		Float64("size", signal.Size).
		Str("trigger", trigger).
		Msg("order signal sent")
}
```

**步骤 2: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 3: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: flushOrder 使用 PendingOrderCache.Delete"
```

---

## Task 7: 重写 scanTimeoutOrders 方法

**文件:**
- 修改: `internal/processor/order_processor.go:417-431`

**步骤 1: 使用新 API**

```go
// scanTimeoutOrders 扫描超时订单
func (p *OrderProcessor) scanTimeoutOrders() {
	now := time.Now()
	timeoutThreshold := now.Add(-p.timeout)

	p.pendingOrders.Range(func(key string, pending *PendingOrder) bool {
		// 未发送且超时
		if !pending.Aggregation.SignalSent && pending.FirstFillTime.Before(timeoutThreshold) {
			p.triggerFlush(key, "timeout")
		}
		return true
	})
}
```

**步骤 2: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 3: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: scanTimeoutOrders 使用 PendingOrderCache.Range"
```

---

## Task 8: 重写 ActiveCount 方法

**文件:**
- 修改: `internal/processor/order_processor.go:439-444`

**步骤 1: 简化实现**

```go
// ActiveCount 返回活跃订单数
func (p *OrderProcessor) ActiveCount() int {
	return int(p.pendingOrders.Len())
}
```

**步骤 2: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 3: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: ActiveCount 直接返回 PendingOrderCache.Len"
```

---

## Task 9: 修改 SetTimeout 方法

**文件:**
- 修改: `internal/processor/order_processor.go:95-100`

**步骤 1: 移除锁保护**

```go
// SetTimeout 设置超时时间
func (p *OrderProcessor) SetTimeout(timeout time.Duration) {
	p.timeout = timeout
}
```

由于不再使用 `mu`，可以直接设置字段。

**步骤 2: 验证编译**

运行: `go build ./internal/processor/`
预期: 编译成功

**步骤 3: 提交**

```bash
git add internal/processor/order_processor.go
git commit -m "refactor: SetTimeout 移除不必要的锁保护"
```

---

## Task 10: 添加 PendingOrderCache 单元测试

**文件:**
- 创建: `internal/cache/pending_order_cache_test.go`

**步骤 1: 编写测试**

```go
package cache

import (
	"testing"
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

func TestPendingOrderCache_Basic(t *testing.T) {
	cache := NewPendingOrderCache()

	// 测试 Set 和 Get
	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
		Aggregation: &models.OrderAggregation{
			Oid: 123,
		},
	}

	cache.Set("test-key", order)

	got, ok := cache.Get("test-key")
	if !ok {
		t.Fatal("expected to find order")
	}
	if got.Aggregation.Oid != 123 {
		t.Errorf("expected oid 123, got %d", got.Aggregation.Oid)
	}
}

func TestPendingOrderCache_Delete(t *testing.T) {
	cache := NewPendingOrderCache()

	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
	}
	cache.Set("key1", order)

	// 删除
	cache.Delete("key1")

	_, ok := cache.Get("key1")
	if ok {
		t.Error("expected order to be deleted")
	}
}

func TestPendingOrderCache_Len(t *testing.T) {
	cache := NewPendingOrderCache()

	if cache.Len() != 0 {
		t.Errorf("expected len 0, got %d", cache.Len())
	}

	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
	}
	cache.Set("key1", order)
	cache.Set("key2", order)

	if cache.Len() != 2 {
		t.Errorf("expected len 2, got %d", cache.Len())
	}
}

func TestPendingOrderCache_Range(t *testing.T) {
	cache := NewPendingOrderCache()

	// 添加 3 个订单
	for i := 1; i <= 3; i++ {
		order := &PendingOrder{
			seenTids: concurrent.NewMap[int64, struct{}](),
			Aggregation: &models.OrderAggregation{
				Oid: int64(i),
			},
		}
		cache.Set(string(rune('0'+i)), order)
	}

	count := 0
	cache.Range(func(key string, order *PendingOrder) bool {
		count++
		return true
	})

	if count != 3 {
		t.Errorf("expected 3 orders, got %d", count)
	}
}

func TestPendingOrderCache_Clear(t *testing.T) {
	cache := NewPendingOrderCache()

	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
	}
	cache.Set("key1", order)
	cache.Set("key2", order)

	cache.Clear()

	if cache.Len() != 0 {
		t.Errorf("expected len 0 after clear, got %d", cache.Len())
	}
}
```

**步骤 2: 运行测试**

运行: `go test ./internal/cache/ -v -run TestPendingOrderCache`
预期: 全部 PASS

**步骤 3: 提交**

```bash
git add internal/cache/pending_order_cache_test.go
git commit -m "test: 添加 PendingOrderCache 单元测试"
```

---

## Task 11: 添加并发安全测试

**文件:**
- 创建: `internal/processor/order_processor_concurrent_test.go`

**步骤 1: 编写并发测试**

```go
package processor

import (
	"sync"
	"testing"
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/cache"
	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

func TestPendingOrder_ConcurrentFills(t *testing.T) {
	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
		Aggregation: &models.OrderAggregation{
			Oid: 123,
		},
		FirstFillTime: time.Now(),
	}

	// 模拟 100 个并发 fill
	const numFills = 100
	var wg sync.WaitGroup

	for i := int64(0); i < numFills; i++ {
		wg.Add(1)
		go func(tid int64) {
			defer wg.Done()
			// 使用 LoadOrStore 检查重复
			_, loaded := order.seenTids.LoadOrStore(tid, struct{}{})
			if !loaded {
				// 第一次见到这个 tid
				order.Aggregation.TotalSize += 1.0
			}
		}(i)
	}

	wg.Wait()

	// 验证：100 个唯一的 tid
	if order.Aggregation.TotalSize != float64(numFills) {
		t.Errorf("expected total size %d, got %f", numFills, order.Aggregation.TotalSize)
	}
}

func TestPendingOrder_DuplicateTidDetection(t *testing.T) {
	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
		Aggregation: &models.OrderAggregation{
			Oid: 123,
		},
		FirstFillTime: time.Now(),
	}

	// 添加相同的 tid 10 次
	const duplicateCount = 10
	const tid = 999

	var wg sync.WaitGroup
	for i := 0; i < duplicateCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, loaded := order.seenTids.LoadOrStore(tid, struct{}{})
			if !loaded {
				order.Aggregation.TotalSize += 1.0
			}
		}()
	}

	wg.Wait()

	// 验证：只应该有一个 fill 被计数
	if order.Aggregation.TotalSize != 1.0 {
		t.Errorf("expected total size 1.0 (duplicate filtered), got %f", order.Aggregation.TotalSize)
	}
}

func TestPendingOrderCache_ConcurrentAccess(t *testing.T) {
	c := cache.NewPendingOrderCache()

	const numGoroutines = 100
	const numOrders = 10

	var wg sync.WaitGroup

	// 并发写入
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOrders; j++ {
				key := string(rune('0' + j))
				order := &PendingOrder{
					seenTids: concurrent.NewMap[int64, struct{}](),
					Aggregation: &models.OrderAggregation{
						Oid: int64(id*100 + j),
					},
				}
				c.Set(key, order)
			}
		}(i)
	}

	wg.Wait()

	// 验证长度
	if c.Len() != int64(numOrders) {
		t.Errorf("expected len %d, got %d", numOrders, c.Len())
	}
}
```

**步骤 2: 运行测试**

运行: `go test ./internal/processor/ -v -run TestPendingOrder_Concurrent -race`
预期: 全部 PASS，无竞态检测报告

**步骤 3: 提交**

```bash
git add internal/processor/order_processor_concurrent_test.go
git commit -m "test: 添加并发安全测试，验证 tid 去重和缓存并发访问"
```

---

## Task 12: 添加性能基准测试

**文件:**
- 创建: `internal/processor/order_processor_bench_test.go`

**步骤 1: 编写基准测试**

```go
package processor

import (
	"sync"
	"testing"

	"github.com/utrading/utrading-hl-monitor/pkg/concurrent"
)

// 基准测试：tid 去重性能
func BenchmarkTidDedup_ConcurrentMap(b *testing.B) {
	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
	}

	b.RunParallel(func(pb *testing.PB) {
		i := int64(0)
		for pb.Next() {
			order.seenTids.LoadOrStore(i, struct{}{})
			i++
		}
	})
}

// 基准测试：缓存访问性能
func BenchmarkPendingOrderCache_GetSet(b *testing.B) {
	c := cache.NewPendingOrderCache()
	order := &PendingOrder{
		seenTids: concurrent.NewMap[int64, struct{}](),
	}

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := string(rune('0' + (i % 10)))
			c.Set(key, order)
			c.Get(key)
			i++
		}
	})
}

// 基准测试：范围遍历性能
func BenchmarkPendingOrderCache_Range(b *testing.B) {
	c := cache.NewPendingOrderCache()

	// 预填充 100 个订单
	for i := 0; i < 100; i++ {
		order := &PendingOrder{
			seenTids: concurrent.NewMap[int64, struct{}](),
		}
		c.Set(string(rune('0'+i%10)), order)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		c.Range(func(key string, order *PendingOrder) bool {
			return true
		})
	}
}

// 对比基准测试：传统 map + RWMutex
func BenchmarkTidDedup_MapWithMutex(b *testing.B) {
	type OldPendingOrder struct {
		mu      sync.RWMutex
		seenTids map[int64]struct{}
	}

	order := &OldPendingOrder{
		seenTids: make(map[int64]struct{}),
	}

	b.RunParallel(func(pb *testing.PB) {
		i := int64(0)
		for pb.Next() {
			order.mu.Lock()
			order.seenTids[i] = struct{}{}
			order.mu.Unlock()
			i++
		}
	})
}
```

**步骤 2: 运行基准测试**

运行: `go test ./internal/processor/ -bench=BenchmarkTidDedup -benchmem`
预期: concurrent.Map 版本性能不劣于 map+Mutex 版本

**步骤 3: 提交**

```bash
git add internal/processor/order_processor_bench_test.go
git commit -m "test: 添加性能基准测试，对比新旧实现"
```

---

## Task 13: 验证整体编译

**步骤 1: 编译整个项目**

运行: `go build ./...`
预期: 编译成功，无错误

**步骤 2: 运行所有测试**

运行: `go test ./... -v`
预期: 全部 PASS

**步骤 3: 运行竞态检测**

运行: `go test ./... -race`
预期: 无竞态报告

**步骤 4: 提交**

```bash
git add -A
git commit -m "chore: 验证重构完成，所有测试通过"
```

---

## Task 14: 更新文档

**文件:**
- 修改: `CLAUDE.md`

**步骤 1: 更新缓存层架构说明**

在 `CLAUDE.md` 的缓存层架构表中添加 PendingOrderCache：

```markdown
| 缓存类型 | 实现库 | 用途 | TTL |
|---------|-------|------|-----|
| DedupCache | go-cache | 订单去重（address-oid-direction） | 30 分钟 |
| SymbolCache | Ristretto | Symbol 转换缓存（spot/perp） | LRU |
| PriceCache | Ristretto | 现货/合约价格缓存 | LRU |
| PendingOrderCache | concurrent.Map | 待处理订单短期暂存 + tid 去重 | 发送即删 |
```

**步骤 2: 添加使用说明**

```markdown
### PendingOrderCache

待处理订单缓存，用于 OrderProcessor 中存储正在聚合的订单：

```go
cache := cache.NewPendingOrderCache()

// 原子获取或创建订单
pending, loaded := cache.LoadOrStore(key, &PendingOrder{
    seenTids: concurrent.NewMap[int64, struct{}](),
    // ...
})

// tid 去重（O(1)）
_, tidSeen := pending.seenTids.LoadOrStore(fill.Tid, struct{}{})
if tidSeen {
    return // 重复 fill
}
```

**特性**：
- 线程安全，无需手动加锁
- tid 去重从 O(n) 优化到 O(1)
- 订单发送后立即删除，无需 TTL
```

**步骤 3: 提交**

```bash
git add CLAUDE.md
git commit -m "docs: 更新 CLAUDE.md，添加 PendingOrderCache 说明"
```

---

## Task 15: 最终验证

**步骤 1: 运行完整测试套件**

运行: `make test`
预期: 全部通过

**步骤 2: 检查代码规范**

运行: `go vet ./...`
预期: 无警告

**步骤 3: 运行静态分析**

运行: `golangci-lint run`
预期: 无问题（如果项目配置了 golangci-lint）

**步骤 4: 最终提交**

```bash
git add -A
git commit -m "feat: 完成 PendingOrderCache 重构

- 使用 concurrent.Map 替代 map + RWMutex
- tid 去重从 O(n) 优化到 O(1)
- 简化代码，移除手动锁管理
- 添加完整的单元测试、并发测试和基准测试

性能对比：
- tid 去重：O(n slice 遍历) -> O(1) Map 查询
- 并发安全：由 concurrent.Map 保证
- 内存开销：每个 tid 约 24 字节"
```

---

## 验收标准

完成所有任务后，应满足：

1. **功能正确**：所有测试通过，包括并发测试
2. **性能提升**：tid 去重从 O(n) 优化到 O(1)
3. **代码简洁**：移除所有手动 RWMutex 操作
4. **线程安全**：竞态检测通过
5. **文档完整**：CLAUDE.md 更新

## 回滚计划

如果出现问题，可以通过以下方式回滚：

```bash
git revert <commit-hash>  # 回滚特定提交
# 或
git reset --hard <commit-before-refactor>  # 完全回滚
```
