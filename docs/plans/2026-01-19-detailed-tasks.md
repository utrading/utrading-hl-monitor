# 优化实施详细任务清单

**生成日期**: 2026-01-19  
**总工期**: 12-13 天  
**目标**: 分阶段实施缓存层、消息处理层、WebSocket 层优化

---

## 任务清单说明

每个任务包含：
- **编号**: 唯一标识
- **标题**: 简短描述
- **优先级**: P0(关键) / P1(重要) / P2(可选)
- **预计时间**: 小时数
- **依赖**: 前置任务
- **文件**: 涉及的文件路径
- **验收标准**: 完成的标志
- **备注**: 注意事项

---

## 阶段 0: 基础准备 (0.5 天)

### T001: 安装新依赖包
- **优先级**: P0
- **预计时间**: 0.5h
- **依赖**: 无
- **文件**: 
  - `go.mod`
- **步骤**:
  ```bash
  go get github.com/dgraph-io/ristretto@latest
  go get github.com/patrickmn/go-cache@latest
  go mod tidy
  ```
- **验收标准**: 
  - `go.mod` 包含两个新依赖
  - `go build` 成功无报错
- **备注**: 确认 ristretto 版本 >= v1.0.0

---

### T002: 创建 cache 包目录结构
- **优先级**: P0
- **预计时间**: 0.5h
- **依赖**: T001
- **文件**:
  - `internal/cache/` (新建目录)
  - `internal/cache/.gitkeep`
- **验收标准**: 目录创建成功
- **备注**: 准备好缓存模块的基础结构

---

## 阶段 1.1: 订单去重器优化 (1 天)

### T003: 实现 DedupCache
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T002
- **文件**:
  - `internal/cache/dedup_cache.go` (新建)
- **步骤**:
  1. 定义 `DedupCache` 结构体
  2. 实现 `NewDedupCache(ttl)` 构造函数
  3. 实现 `IsSeen(address, oid, direction)` 方法
  4. 实现 `Mark(address, oid, direction)` 方法
  5. 实现 `dedupKey()` 私有方法
  6. 实现 `LoadFromDB(dao)` 方法
  7. 实现 `Stats()` 方法
- **验收标准**:
  - 代码编译通过
  - TTL 自动过期正常工作
- **备注**: 使用 go-cache，清理间隔设为 2×TTL

---

### T004: DedupCache 单元测试
- **优先级**: P0
- **预计时间**: 1.5h
- **依赖**: T003
- **文件**:
  - `internal/cache/dedup_cache_test.go` (新建)
- **步骤**:
  1. 测试 `IsSeen` 首次查询返回 false
  2. 测试 `Mark` 后 `IsSeen` 返回 true
  3. 测试不同 direction 互不影响
  4. 测试 TTL 过期机制（100ms 过期）
  5. 测试 `LoadFromDB` 模拟加载
  6. 测试并发安全性（10 个协程同时读写）
- **验收标准**:
  - 所有测试用例通过
  - 测试覆盖率 > 80%
- **备注**: 使用 `testify/assert` 简化断言

---

### T005: 修改 OrderDeduper 使用 DedupCache
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T003
- **文件**:
  - `internal/ws/deduper.go` (修改)
- **步骤**:
  1. 导入 `internal/cache` 包
  2. 将 `sync.Map` 替换为 `*cache.DedupCache`
  3. 删除 `cleanupLoop` 协程
  4. 删除 `cleanup()` 定时清理方法
  5. 删除 `done` 和 `cleanup.Ticker`
  6. 更新 `IsSeen` 调用新缓存接口
  7. 更新 `Mark` 调用新缓存接口
- **验收标准**:
  - 代码编译通过
  - 旧测试用例仍然通过
- **备注**: 保持对外接口不变，最小化影响范围

---

### T006: 集成测试 OrderDeduper
- **优先级**: P1
- **预计时间**: 1.5h
- **依赖**: T005
- **文件**:
  - `internal/ws/deduper_integration_test.go` (新建)
- **步骤**:
  1. 测试启动时从数据库加载
  2. 测试运行时去重逻辑
  3. 测试 30 分钟 TTL 过期
  4. 测试并发场景（100 个地址同时写入）
- **验收标准**:
  - 集成测试通过
  - 内存占用对比旧实现降低 30%
- **备注**: 可使用 `runtime.MemStats` 对比内存

---

### T007: 订单去重器优化文档
- **优先级**: P2
- **预计时间**: 0.5h
- **依赖**: T006
- **文件**:
  - `internal/cache/README.md` (新建)
- **步骤**:
  1. 说明去重缓存的设计原理
  2. 记录 TTL 配置建议
  3. 记录性能对比数据
- **验收标准**: 文档清晰完整

---

## 阶段 1.2: Symbol 转换缓存优化 (1 天)

### T008: 实现 SymbolCache
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T002
- **文件**:
  - `internal/cache/symbol_cache.go` (新建)
- **步骤**:
  1. 定义 `SymbolCache` 结构体
  2. 实现 `NewSymbolCache()` 构造函数
  3. 实现 `GetSpotSymbol(assetID)` 方法
  4. 实现 `SetSpotSymbol(assetID, symbol)` 方法
  5. 实现 `GetPerpSymbol(coin)` 方法
  6. 实现 `SetPerpSymbol(coin, symbol)` 方法
  7. 实现 `Warmup(allAssets)` 预热方法
  8. 实现 `Stats()` 统计方法
- **验收标准**:
  - 代码编译通过
  - Ristretlo 缓存正确配置
- **备注**: 
  - spot: MaxCost=128KB, NumCounters=1e4
  - perp: MaxCost=64KB, NumCounters=5e3

---

### T009: SymbolCache 单元测试
- **优先级**: P0
- **预计时间**: 1.5h
- **依赖**: T008
- **文件**:
  - `internal/cache/symbol_cache_test.go` (新建)
- **步骤**:
  1. 测试 Spot symbol 的 Get/Set
  2. 测试 Perp symbol 的 Get/Set
  3. 测试缓存未命中返回空
  4. 测试 `Warmup` 方法
  5. 测试 `Stats` 返回正确指标
  6. 测试并发安全性
- **验收标准**:
  - 所有测试用例通过
  - 测试覆盖率 > 80%
- **备注**: 模拟 `hyperliquid.Asset` 数据

---

### T010: 修改 SymbolConverter 使用 SymbolCache
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T008
- **文件**:
  - `internal/ws/symbol_converter.go` (修改或查找)
- **步骤**:
  1. 定位当前的 SymbolConverter 实现
  2. 将 map 缓存替换为 `*cache.SymbolCache`
  3. 更新构造函数初始化缓存
  4. 更新 `ConvertSpotSymbol` 使用新接口
  5. 更新 `ConvertPerpSymbol` 使用新接口
- **验收标准**:
  - 代码编译通过
  - 旧功能正常工作
- **备注**: 如果 SymbolConverter 在 SubscriptionManager 中，需要拆分出来

---

### T011: 实现 Symbol 缓存预热
- **优先级**: P1
- **预计时间**: 1.5h
- **依赖**: T010
- **文件**:
  - `internal/ws/subscription.go` (修改)
- **步骤**:
  1. 在 `Start()` 方法中添加预热逻辑
  2. 调用 `poolManager.GetAllAssets()` 获取所有资产
  3. 调用 `symbolConverter.Warmup(allAssets)`
  4. 添加错误处理和日志
- **验收标准**:
  - 启动日志显示预热条目数
  - 缓存命中率 > 90%
- **备注**: 如果 GetAllAssets 不存在，需要先实现

---

### T012: Symbol 缓存优化文档
- **优先级**: P2
- **预计时间**: 0.5h
- **依赖**: T011
- **文件**:
  - `internal/cache/README.md` (追加)
- **步骤**:
  1. 说明 Symbol 缓存的内存配置
  2. 记录预热机制
  3. 记录性能对比
- **验收标准**: 文档完整

---

## 阶段 1.3: 价格缓存优化 (0.5 天)

### T013: 实现 PriceCache
- **优先级**: P0
- **预计时间**: 1.5h
- **依赖**: T002
- **文件**:
  - `internal/cache/price_cache.go` (新建)
- **步骤**:
  1. 定义 `PriceCache` 结构体
  2. 实现 `NewPriceCache()` 构造函数
  3. 实现 `GetSpotPrice(symbol)` 方法
  4. 实现 `SetSpotPrice(symbol, price)` 方法
  5. 实现 `GetPerpPrice(symbol)` 方法
  6. 实现 `SetPerpPrice(symbol, price)` 方法
  7. 实现 `Stats()` 统计方法
- **验收标准**:
  - 代码编译通过
  - spot 和 perp 缓存独立配置
- **备注**: 
  - spot: MaxCost=256KB, NumCounters=1e4
  - perp: MaxCost=256KB, NumCounters=1e4

---

### T014: PriceCache 单元测试
- **优先级**: P0
- **预计时间**: 1h
- **依赖**: T013
- **文件**:
  - `internal/cache/price_cache_test.go` (新建)
- **步骤**:
  1. 测试 Spot price 的 Get/Set
  2. 测试 Perp price 的 Get/Set
  3. 测试缓存未命中返回 0
  4. 测试 `Stats` 返回正确指标
  5. 测试并发安全性
- **验收标准**:
  - 所有测试通过
  - 覆盖率 > 80%
- **备注**: 测试价格精度（float64）

---

### T015: 替换现有价格缓存实现
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T013
- **文件**:
  - `internal/ws/price_cache.go` 或相关文件 (查找)
- **步骤**:
  1. 使用 Grep 搜索 sync.Map 价格缓存
  2. 替换为 `*cache.PriceCache`
  3. 更新所有读写调用
- **验收标准**:
  - 代码编译通过
  - 价格查询功能正常
- **备注**: 需要仔细查找所有价格缓存使用位置

---

## 阶段 1.4: 缓存接口抽象 (0.5 天)

### T016: 定义缓存接口
- **优先级**: P0
- **预计时间**: 1h
- **依赖**: T003, T008, T013
- **文件**:
  - `internal/cache/interface.go` (新建)
- **步骤**:
  1. 定义 `SymbolCacheInterface`
  2. 定义 `DedupCacheInterface`
  3. 定义 `PriceCacheInterface`
- **验收标准**:
  - 接口定义完整
  - 所有缓存实现满足接口
- **备注**: 接口便于测试和 Mock

---

### T017: 更新 CLAUDE.md 缓存层说明
- **优先级**: P1
- **预计时间**: 0.5h
- **依赖**: T016
- **文件**:
  - `CLAUDE.md` (修改)
- **步骤**:
  1. 添加缓存层架构图
  2. 说明各缓存的使用场景
  3. 添加缓存配置示例
- **验收标准**: 文档准确清晰

---

## 阶段 2.1: 异步消息队列 (1.5 天)

### T018: 创建 processor 包
- **优先级**: P0
- **预计时间**: 0.5h
- **依赖**: 无
- **文件**:
  - `internal/processor/` (新建)
  - `internal/processor/.gitkeep`
- **验收标准**: 目录创建成功

---

### T019: 实现消息类型定义
- **优先级**: P0
- **预计时间**: 1h
- **依赖**: T018
- **文件**:
  - `internal/processor/message.go` (新建)
- **步骤**:
  1. 定义 `Message` 接口
  2. 定义 `OrderFillMessage` 结构体
  3. 定义 `PositionUpdateMessage` 结构体
  4. 实现 `Type()` 方法
- **验收标准**: 
  - 代码编译通过
  - 消息类型完整

---

### T020: 实现 MessageHandler 接口
- **优先级**: P0
- **预计时间**: 0.5h
- **依赖**: T019
- **文件**:
  - `internal/processor/message.go`
- **步骤**:
  1. 定义 `MessageHandler` 接口
  2. 定义 `HandleMessage(msg)` 方法
- **验收标准**: 接口定义清晰

---

### T021: 实现 MessageQueue
- **优先级**: P0
- **预计时间**: 2.5h
- **依赖**: T020
- **文件**:
  - `internal/processor/message_queue.go` (新建)
- **步骤**:
  1. 定义 `MessageQueue` 结构体
  2. 实现 `NewMessageQueue(size, workers, handler)` 构造函数
  3. 实现 `Start()` 启动工作协程
  4. 实现 `worker()` 工作协程逻辑
  5. 实现 `Enqueue(msg)` 发送消息（带背压策略）
  6. 实现 `Stop()` 停止队列
- **验收标准**:
  - 代码编译通过
  - 背压策略正确实现
- **备注**: 队列满时同步降级处理

---

### T022: MessageQueue 单元测试
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T021
- **文件**:
  - `internal/processor/message_queue_test.go` (新建)
- **步骤**:
  1. Mock MessageHandler
  2. 测试正常消息流转
  3. 测试队列满时的背压策略
  4. 测试多 worker 并发处理
  5. 测试 Stop 优雅关闭
- **验收标准**:
  - 所有测试通过
  - 覆盖率 > 80%
- **备注**: 使用 `gomock` 或手动 Mock

---

## 阶段 2.2: 批量写入器 (2 天)

### T023: 实现批量写入器数据结构
- **优先级**: P0
- **预计时间**: 1.5h
- **依赖**: T018
- **文件**:
  - `internal/processor/batch_writer.go` (新建)
- **步骤**:
  1. 定义 `BatchItem` 接口
  2. 定义 `PositionCacheItem` 结构体
  3. 定义 `BatchWriterConfig` 结构体
  4. 定义 `BatchWriter` 结构体
  5. 定义错误类型
- **验收标准**: 数据结构定义清晰

---

### T024: 实现 BatchWriter 核心逻辑
- **优先级**: P0
- **预计时间**: 3h
- **依赖**: T023
- **文件**:
  - `internal/processor/batch_writer.go`
- **步骤**:
  1. 实现 `NewBatchWriter(db, config)` 构造函数
  2. 实现 `Start()` 启动接收和刷新协程
  3. 实现 `receiveLoop()` 接收协程
  4. 实现 `flushLoop()` 定时刷新协程
  5. 实现 `flush(tables...)` 刷新指定表
  6. 实现 `flushAll()` 刷新所有表
  7. 实现 `Add(item)` 添加写入项
  8. 实现 `Stop()` 停止写入器
  9. 实现 `GracefulShutdown(timeout)` 优雅关闭
- **验收标准**:
  - 代码编译通过
  - 批量逻辑正确
- **备注**: 
  - 默认批量大小 100
  - 默认刷新间隔 100ms

---

### T025: 实现 gorm-gen 批量 upsert
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T024
- **文件**:
  - `internal/processor/batch_writer.go`
- **步骤**:
  1. 实现 `batchUpsert(table, items)` 路由方法
  2. 实现 `batchUpsertPositions(items)` 具体实现
  3. 使用 `gen.Q.HlPositionCache.UnderlyingDB()`
  4. 配置 `clause.OnConflict` 实现 UPSERT
- **验收标准**:
  - UPSERT 语义正确
  - 冲突时更新所有字段
- **备注**: 参考现有 DAO 的 UpsertPositionCache

---

### T026: BatchWriter 单元测试
- **优先级**: P0
- **预计时间**: 2.5h
- **依赖**: T025
- **文件**:
  - `internal/processor/batch_writer_test.go` (新建)
- **步骤**:
  1. Mock gorm.DB
  2. 测试单条 Add 触发批量
  3. 测试定时刷新触发
  4. 测试优雅关闭流程
  5. 测试超时强制刷新
  6. 测试并发安全性
- **验收标准**:
  - 所有测试通过
  - 覆盖率 > 80%
- **备注**: 使用 `sqlmock` Mock 数据库

---

### T027: BatchWriter 集成测试
- **优先级**: P1
- **预计时间**: 2h
- **依赖**: T026
- **文件**:
  - `internal/processor/batch_writer_integration_test.go` (新建)
- **步骤**:
  1. 使用测试数据库
  2. 测试真实批量 upsert
  3. 验证数据正确性
  4. 测试并发写入场景
- **验收标准**:
  - 集成测试通过
  - 数据库写入频率降低 90%
- **备注**: 可使用 Docker MySQL

---

## 阶段 2.3: 消息处理器实现 (0.5 天)

### T028: 实现 PositionProcessor
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T027
- **文件**:
  - `internal/processor/position_processor.go` (新建)
- **步骤**:
  1. 定义 `PositionProcessor` 结构体
  2. 实现 `NewPositionProcessor(bw, pm)` 构造函数
  3. 实现 `HandleMessage(msg)` 方法
  4. 实现 `handlePositionUpdate(msg)` 私有方法
- **验收标准**:
  - 代码编译通过
  - 正确调用 BatchWriter
- **备注**: 集成现有的 position.Manager

---

### T029: 集成 MessageQueue 和 Processor
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T022, T028
- **文件**:
  - `internal/ws/subscription.go` (修改)
- **步骤**:
  1. 在 SubscriptionManager 中添加 MessageQueue
  2. 在 Start() 中启动 MessageQueue
  3. 修改 handleWebData 使用队列
  4. 在 Shutdown() 中停止队列
- **验收标准**:
  - 消息通过队列处理
  - 性能提升明显
- **备注**: 需要灰度控制新旧逻辑切换

---

## 阶段 3.1: 灰度发布策略 (1 天)

### T030: 创建 optimization 包
- **优先级**: P0
- **预计时间**: 0.5h
- **依赖**: 无
- **文件**:
  - `internal/optimization/` (新建)
  - `internal/optimization/.gitkeep`
- **验收标准**: 目录创建成功

---

### T031: 实现 GrayscaleController
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T030
- **文件**:
  - `internal/optimization/grayscale.go` (新建)
- **步骤**:
  1. 定义 `GrayscaleController` 结构体
  2. 实现 `NewGrayscaleController(ratio)` 构造函数
  3. 实现 `IsEnabled(address)` 哈希分桶方法
  4. 实现 `SetRatio(ratio)` 动态调整方法
  5. 实现 `GetRatio()` 查询方法
- **验收标准**:
  - 代码编译通过
  - 哈希分布均匀
- **备注**: 使用 FNV 哈希算法

---

### T032: GrayscaleController 单元测试
- **优先级**: P0
- **预计时间**: 1.5h
- **依赖**: T031
- **文件**:
  - `internal/optimization/grayscale_test.go` (新建)
- **步骤**:
  1. 测试 ratio=0 时全部禁用
  2. 测试 ratio=100 时全部启用
  3. 测试 ratio=50 时分布均匀
  4. 测试同一地址结果稳定
  5. 测试 SetRatio 动态调整
- **验收标准**:
  - 所有测试通过
  - 分布误差 < 5%
- **备注**: 采样 1000 个地址验证分布

---

### T033: 集成灰度控制到 SubscriptionManager
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T032
- **文件**:
  - `internal/ws/subscription.go` (修改)
- **步骤**:
  1. 添加 `grayscale *optimization.GrayscaleController` 字段
  2. 在构造函数中初始化灰度控制器
  3. 在 handleOrderFills 中检查 `grayscale.IsEnabled()`
  4. 实现新旧逻辑分支
- **验收标准**:
  - 代码编译通过
  - 灰度控制生效
- **备注**: 保留旧逻辑用于回滚

---

### T034: 配置文件支持灰度比例
- **优先级**: P1
- **预计时间**: 1h
- **依赖**: T033
- **文件**:
  - `cfg.toml` (修改)
  - `internal/config/config.go` (修改)
- **步骤**:
  1. 添加 `[optimization]` 配置段
  2. 添加 `graylist_ratio` 配置项
  3. 在配置结构体中添加字段
  4. 在 main.go 中读取配置
- **验收标准**:
  - 配置文件正确解析
  - 灰度比例可动态调整
- **备注**: 重启服务后生效

---

## 阶段 3.2: WebSocket 层优化 (1 天)

### T035: 实现 ConnectionWrapper
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: 无
- **文件**:
  - `internal/ws/pool.go` (修改)
- **步骤**:
  1. 定义 `ConnectionWrapper` 结构体
  2. 添加 `addresses map[string]bool` 字段
  3. 添加 `subs map[string]*Subscription` 字段
  4. 添加 `mu sync.RWMutex` 锁
- **验收标准**: 
  - 代码编译通过
  - 线程安全
- **备注**: 包装现有 WebSocket 连接

---

### T036: 实现 PoolManager 多连接管理
- **优先级**: P0
- **预计时间**: 3h
- **依赖**: T035
- **文件**:
  - `internal/ws/pool.go` (修改)
- **步骤**:
  1. 修改 Pool → PoolManager
  2. 添加 `connections []*ConnectionWrapper` 字段
  3. 添加 `maxPerConn int` 配置
  4. 实现 `selectLeastLoadedConnection()` 方法
  5. 实现 `createNewConnection()` 方法
  6. 修改订阅方法使用多连接
- **验收标准**:
  - 代码编译通过
  - 负载均衡正确
- **备注**: 
  - 默认每连接最多 100 地址
  - 1000 地址需要 10 个连接

---

### T037: 实现取消订阅功能
- **优先级**: P1
- **预计时间**: 2h
- **依赖**: T036
- **文件**:
  - `internal/ws/subscription.go` (修改)
- **步骤**:
  1. 检查 go-hyperliquid 是否支持 Unsubscribe
  2. 实现 `findConnectionByAddress(addr)` 方法
  3. 修改 `UnsubscribeAddress(addr)` 发送取消消息
  4. 更新连接的地址映射
- **验收标准**:
  - 取消订阅消息发送成功
  - 地址映射正确更新
- **备注**: 如果库不支持，记录 issue

---

### T038: WebSocket 层测试
- **优先级**: P1
- **预计时间**: 2h
- **依赖**: T037
- **文件**:
  - `internal/ws/pool_test.go` (修改)
- **步骤**:
  1. 测试多连接创建
  2. 测试负载均衡分配
  3. 测试连接满时自动扩容
  4. 测试取消订阅
- **验收标准**:
  - 所有测试通过
  - 1000 地址分配到 10 个连接
- **备注**: 可 Mock WebSocket 连接

---

## 阶段 4: 依赖注入容器 (0.5 天)

### T039: 实现 Registry 容器
- **优先级**: P0
- **预计时间**: 2h
- **依赖**: T017, T029, T034
- **文件**:
  - `internal/registry/` (新建)
  - `internal/registry/registry.go` (新建)
- **步骤**:
  1. 定义 `Container` 结构体
  2. 添加所有缓存、处理器、管理器字段
  3. 实现 `NewContainer()` 构造函数
  4. 实现 `Initialize(db, cfg)` 初始化方法
  5. 实现 `Shutdown()` 优雅关闭方法
  6. 实现所有 Getters
- **验收标准**:
  - 代码编译通过
  - 组件初始化顺序正确
- **备注**: 
  - 缓存层 → 处理层 → 业务层
  - 使用 sync.RWMutex 保护

---

### T040: 集成 Registry 到 main.go
- **优先级**: P0
- **预计时间**: 1.5h
- **依赖**: T039
- **文件**:
  - `cmd/hl_monitor/main.go` (修改)
- **步骤**:
  1. 使用 registry.NewContainer()
  2. 调用 container.Initialize()
  3. 从容器获取各组件
  4. 在信号处理中调用 Shutdown()
- **验收标准**:
  - 服务正常启动
  - 优雅关闭正确执行
- **备注**: 
  - 批量写入器等待 5 秒
  - 超时后强制刷新

---

## 阶段 5: 监控指标扩展 (0.5 天)

### T041: 实现缓存 Prometheus 指标
- **优先级**: P1
- **预计时间**: 1.5h
- **依赖**: T016
- **文件**:
  - `internal/monitor/metrics.go` (修改)
- **步骤**:
  1. 添加 `hl_cache_hit_total` Counter
  2. 添加 `hl_cache_miss_total` Counter
  3. 在各缓存中集成指标上报
- **验收标准**:
  - 指标正确上报
  - 区分缓存类型
- **备注**: 
  - label: cache_type (symbol/price/dedup)

---

### T042: 实现队列 Prometheus 指标
- **优先级**: P1
- **预计时间**: 1h
- **依赖**: T022
- **文件**:
  - `internal/monitor/metrics.go`
- **步骤**:
  1. 添加 `hl_message_queue_size` Gauge
  2. 添加 `hl_message_queue_full_total` Counter
  3. 在 MessageQueue 中集成上报
- **验收标准**:
  - 队列大小实时监控
  - 队列满事件记录

---

### T043: 实现批量写入 Prometheus 指标
- **优先级**: P1
- **预计时间**: 1h
- **依赖**: T027
- **文件**:
  - `internal/monitor/metrics.go`
- **步骤**:
  1. 添加 `hl_batch_write_size` Histogram
  2. 添加 `hl_batch_write_duration_seconds` Histogram
  3. 在 BatchWriter 中集成上报
- **验收标准**:
  - 批量大小分布可见
  - 写入耗时可观测

---

## 阶段 6: 压力测试 (1 天)

### T044: 缓存层压力测试
- **优先级**: P1
- **预计时间**: 2h
- **依赖**: T017
- **文件**:
  - `internal/cache/stress_test.go` (新建)
- **步骤**:
  1. 模拟 10000 并发读写
  2. 测试 Ristretlo 内存稳定性
  3. 测试 go-cache TTL 过期
  4. 记录性能指标
- **验收标准**:
  - 无内存泄漏
  - 无死锁
  - QPS > 100000
- **备注**: 使用 `go test -bench`

---

### T045: 消息队列压力测试
- **优先级**: P1
- **预计时间**: 2h
- **依赖**: T029
- **文件**:
  - `internal/processor/stress_test.go` (新建)
- **步骤**:
  1. 模拟 10000 消息/秒
  2. 测试背压策略
  3. 测试队列满时降级
  4. 记录吞吐量和延迟
- **验收标准**:
  - 消息不丢失
  - P99 延迟 < 200ms
- **备注**: 对比同步处理性能

---

### T046: 批量写入压力测试
- **优先级**: P1
- **预计时间**: 2h
- **依赖**: T027
- **文件**:
  - `internal/processor/stress_test.go`
- **步骤**:
  1. 模拟 1000 地址并发写入
  2. 测试数据库连接池
  3. 对比逐条写入性能
  4. 记录数据库 QPS
- **验收标准**:
  - 数据库写入频率降低 90%
  - 无数据丢失
- **备注**: 监控 MySQL slow query log

---

## 阶段 7: 文档和总结 (0.5 天)

### T047: 更新 CLAUDE.md
- **优先级**: P1
- **预计时间**: 1h
- **依赖**: T040
- **文件**:
  - `CLAUDE.md` (修改)
- **步骤**:
  1. 更新架构图
  2. 添加优化模块说明
  3. 更新配置说明
- **验收标准**: 文档准确完整

---

### T048: 创建性能对比报告
- **优先级**: P2
- **预计时间**: 1h
- **依赖**: T046
- **文件**:
  - `docs/performance-report.md` (新建)
- **步骤**:
  1. 记录优化前基准数据
  2. 记录优化后性能数据
  3. 生成对比图表
- **验收标准**: 报告数据真实可信

---

### T049: 更新 README.md
- **优先级**: P2
- **预计时间**: 0.5h
- **依赖**: T047
- **文件**:
  - `README.md` (修改)
- **步骤**:
  1. 更新性能指标
  2. 添加新配置说明
  3. 更新依赖列表
- **验收标准**: README 信息准确

---

## 任务统计

| 阶段 | 任务数 | 预计时间 |
|------|--------|---------|
| 阶段 0: 基础准备 | 2 | 1h |
| 阶段 1.1: 去重器优化 | 5 | 8h |
| 阶段 1.2: Symbol 缓存 | 5 | 7h |
| 阶段 1.3: 价格缓存 | 3 | 4.5h |
| 阶段 1.4: 接口抽象 | 2 | 1.5h |
| 阶段 2.1: 消息队列 | 5 | 7h |
| 阶段 2.2: 批量写入 | 5 | 10.5h |
| 阶段 2.3: 消息处理器 | 2 | 4h |
| 阶段 3.1: 灰度策略 | 5 | 7h |
| 阶段 3.2: WebSocket | 4 | 9h |
| 阶段 4: 依赖注入 | 2 | 3.5h |
| 阶段 5: 监控指标 | 3 | 3.5h |
| 阶段 6: 压力测试 | 3 | 6h |
| 阶段 7: 文档总结 | 3 | 2.5h |
| **总计** | **49** | **75.5h (约 9.5 工作日)** |

---

## 关键路径

```
T001 → T002 → T003 → T005 → T008 → T010 → T019 → T021 → T023 → T024 → T025 → T028 → T031 → T033 → T039 → T040
```

关键路径上的任务需要优先完成，其他任务可并行开发。

---

## 风险提示

1. **T037**: go-hyperliquid 库可能不支持 Unsubscribe，需要提前验证
2. **T025**: gorm-gen 批量操作 API 需要仔细查阅文档
3. **T011**: GetAllAssets 方法可能不存在，需要先实现或找到替代方案
4. **T015**: 价格缓存位置不明确，需要先用 Grep 搜索

---

## 下一步

1. 评审本任务清单
2. 确认优先级和时间估算
3. 开始 T001: 安装依赖包
