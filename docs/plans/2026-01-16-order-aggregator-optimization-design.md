# OrderAggregator 性能优化设计

## 背景

当前 `OrderAggregator` 的 `UpdateStatus` 方法在 `direction` 为空时需要遍历所有订单（O(n)），在高并发场景下性能瓶颈明显。同时，WebSocket 可能重复发送相同的 fill（相同 tid），导致内存累积。

## 性能测试结果

基于 1000 地址 × 10 订单 × 2 方向（反手）的测试场景：

| 测试场景 | 操作时间 | 内存分配 | 分配次数 |
|---------|---------|---------|---------|
| 当前实现（遍历） | 665,367 ns/op | 4719 B/op | 115 allocs/op |
| **优化版本**（oid 索引） | **264.6 ns/op** | 120 B/op | 8 allocs/op |
| 重复 fill（无去重） | 294,437 ns/op | 161,323 B/op | 10,006 allocs/op |

**优化收益**：
- 性能提升 **2,513 倍** ⚡
- 内存开销降低 **39 倍**

## 优化方案

### 1. oid 索引优化

#### 目标
将 `UpdateStatus(address, oid, status, "")` 的复杂度从 O(n) 降到 O(k)，其中 k 是该 oid 的方向数（通常 1-2 个）。

#### 数据结构变更

```go
type OrderAggregator struct {
    orders    sync.Map // "address-oid-direction" → *models.OrderAggregation
    oidIndex  sync.Map // "address-oid" → []string (directions)  // 新增

    timeout   time.Duration
    flushChan chan flushRequest
    publisher Publisher
    mu        sync.Mutex
}
```

#### 实现要点

1. **AddFill 维护索引**：
   ```go
   func (a *OrderAggregator) AddFill(address string, fill hyperliquid.WsOrderFill, direction string) {
       key := orderKey(address, fill.Oid, direction)
       oidKey := oidKey(address, fill.Oid)

       a.mu.Lock()
       defer a.mu.Unlock()

       if actual, loaded := a.orders.LoadOrStore(key, newAgg); loaded {
           // 已存在，更新聚合数据
       } else {
           // 新创建，更新 oidIndex
           directions, _ := a.oidIndex.LoadOrStore(oidKey, []string{})
           directions = append(directions.([]string), direction)
           a.oidIndex.Store(oidKey, directions)
       }
   }
   ```

2. **UpdateStatus 使用索引**：
   ```go
   func (a *OrderAggregator) UpdateStatus(address string, oid int64, status string, direction string) {
       if direction == "" {
           oidKey := oidKey(address, oid)
           if directions, ok := a.oidIndex.Load(oidKey); ok {
               for _, dir := range directions.([]string) {
                   key := orderKey(address, oid, dir)
                   // 更新特定方向的记录
               }
           }
       } else {
           // 直接更新特定方向
       }
   }
   ```

3. **flushOrder 清理索引**：
   ```go
   // 信号发送后，可以选择从 oidIndex 中移除该方向
   // 或者保留索引，因为订单记录仍在 orders 中（用于查询）
   ```

### 2. tid 去重优化

#### 目标
防止 WebSocket 重复发送相同 tid 的 fill 导致内存累积。

#### 实现要点

在 `AddFill` 中添加简单检查：
```go
func (a *OrderAggregator) AddFill(address string, fill hyperliquid.WsOrderFill, direction string) {
    // ... 加载或创建聚合记录

    if loaded {
        agg := actual.(*models.OrderAggregation)

        // 检查 tid 是否已存在
        for _, existingFill := range agg.Fills {
            if existingFill.Tid == fill.Tid {
                logger.Debug().
                    Int64("oid", fill.Oid).
                    Int64("tid", fill.Tid).
                    Msg("duplicate fill skipped")
                return // 已存在，跳过
            }
        }

        // 不存在，追加 fill
        agg.Fills = append(agg.Fills, fill)
        // ...
    }
}
```

## 边界情况处理

### 1. 并发安全

- `orders` 和 `oidIndex` 都使用 `sync.Map`
- 修改聚合记录的 value 时使用 `mu` 保护

### 2. 索引一致性

- 新增订单：同时更新 `orders` 和 `oidIndex`
- 删除订单：不需要删除索引（订单记录保留）
- 错误恢复：如果索引不一致，可以重建索引

### 3. tid 唯一性

- tid 是 Hyperliquid 的唯一交易 ID
- 同一订单的不同 fill 有不同的 tid
- 反手订单拆分后，平仓和开仓的 fill 是不同的 tid

## 实施步骤

### 阶段 1：oid 索引优化

1. 修改 `OrderAggregator` 结构体，添加 `oidIndex`
2. 添加 `oidKey()` 辅助函数
3. 修改 `AddFill()` 维护索引
4. 修改 `UpdateStatus()` 使用索引
5. 更新单元测试
6. 运行性能测试验证

### 阶段 2：tid 去重优化

1. 修改 `AddFill()` 添加 tid 检查
2. 添加日志记录去重情况
3. 更新单元测试覆盖去重场景
4. 运行性能测试验证内存优化

## 影响范围

### 修改文件

- `internal/ws/aggregator.go` - 核心逻辑优化

### 测试文件

- `internal/ws/aggregator_test.go` - 单元测试
- `internal/ws/aggregator_bench_test.go` - 性能测试

### 无需修改

- 数据模型（无需 schema 变更）
- DAO 层
- SubscriptionManager（调用方式不变）

## 验证清单

- [ ] 编译通过
- [ ] 单元测试通过
- [ ] 性能测试验证（提升 > 1000 倍）
- [ ] tid 去重功能正确
- [ ] 并发安全测试通过
- [ ] 内存泄漏测试
- [ ] 日志输出正确

## 回滚方案

如果优化后出现问题，可以：
1. 回滚到之前的 commit
2. 或添加配置开关控制是否启用优化
