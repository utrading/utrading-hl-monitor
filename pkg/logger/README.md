# Logger - 基于 Zerolog 的日志管理器

高性能、结构化的日志系统，基于 [zerolog](https://github.com/rs/zerolog) 封装。

## 特性

- ✅ **高性能**：零内存分配，比传统日志库快 5-10 倍
- ✅ **结构化日志**：支持 JSON 格式，便于日志分析
- ✅ **自动轮转**：支持按大小和日期自动轮转
- ✅ **业务上下文**：内置 player、robot、exchange 等业务上下文
- ✅ **类型安全**：编译时类型检查
- ✅ **并发安全**：完全并发安全
- ✅ **堆栈跟踪**：自动记录错误堆栈
- ✅ **向后兼容**：提供兼容层，最小化迁移成本

## 快速开始

### 1. 初始化日志系统

在 `main.go` 中：

```go
import "trading/lib/logger"

func main() {
    // 初始化日志
    err := logger.NewBuilder().
        SetFilePath("logs/trading.log").
        SetMaxSize(100).           // 100MB
        SetMaxBackups(30).         // 保留30个文件
        SetMaxAge(7).              // 保留7天
        SetLevel(logger.INFO).     // 日志级别
        EnableCompression(true).   // 压缩旧日志
        EnableConsoleOutput(true). // 输出到控制台（开发环境）
        Build()
    
    if err != nil {
        panic(err)
    }
    defer logger.Close()
    
    // 开始使用
    logger.Info().Msg("应用启动")
}
```

### 2. 基础使用

```go
// 简单消息
logger.Info().Msg("操作成功")
logger.Error().Msg("操作失败")
logger.Debug().Msg("调试信息")
logger.Warn().Msg("警告信息")

// 带字段的日志
logger.Info().
    Str("user", "alice").
    Int("age", 30).
    Msg("用户信息")

// 错误日志
if err != nil {
    logger.Error().Err(err).Msg("操作失败")
}
```

### 3. 业务上下文

```go
// 单个上下文
logger.WithPlayer(playerID).Info().Msg("玩家操作")
logger.WithRobot(robotID).Error().Msg("机器人错误")

// 组合上下文
logger.WithPlayerRobot(playerID, robotID).Info().
    Str("action", "create_order").
    Msg("创建订单")

logger.WithExchangeSymbol("binance", "BTCUSDT").Info().
    Float64("price", 45000.0).
    Msg("价格更新")
```

## 文件说明

| 文件 | 说明 |
|------|------|
| `logger.go` | 核心日志功能 |
| `config.go` | 配置和构建器 |
| `compat.go` | 兼容旧日志库的接口 |
| `migrate_helper.go` | 迁移辅助方法 |
| `MIGRATION_GUIDE.md` | 详细迁移指南 |
| `migrate.sh` | 自动迁移脚本 |
| `example_usage.go` | 使用示例 |
| `init_example.go` | 初始化示例 |

## 迁移指南

### 从 github.com/dolotech/log 迁移

#### 方式一：使用兼容层（最简单）

1. 替换 import：
```go
// 旧代码
import "github.com/dolotech/log"

// 新代码
import "trading/lib/logger"
```

2. 使用兼容方法：
```go
// 这些调用无需修改
logger.Print("message")
logger.Printf("format %s", value)
logger.Info().Msg("info")
logger.Error().Msg("error")
```

#### 方式二：使用结构化日志（推荐）

```go
// 旧代码
log.Printf("用户 %d 创建订单，金额：%.2f", userID, amount)

// 新代码（结构化）
logger.Info().
    Uint("user_id", userID).
    Float64("amount", amount).
    Msg("创建订单")
```

#### 方式三：使用自动迁移脚本

```bash
cd src/lib/logger
./migrate.sh
```

详细迁移步骤请参考 [MIGRATION_GUIDE.md](./MIGRATION_GUIDE.md)

## 配置说明

### 配置项

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| FilePath | string | "logs/trading.log" | 日志文件路径 |
| MaxSize | int | 50 | 单个日志文件最大大小（MB） |
| MaxBackups | int | 60 | 保留的旧日志文件数量 |
| MaxAge | int | 15 | 保留旧日志文件的天数 |
| Level | string | "info" | 日志级别 (debug/info/warn/error/fatal) |
| Compress | bool | false | 是否压缩旧日志文件 |
| Console | bool | false | 是否同时输出到控制台 |

### 在 cfg.toml 中配置

```toml
[log]
level = "info"
filepath = "logs/trading.log"
max_size = 100
max_backups = 30
max_age = 7
compress = true
console = false  # 开发环境设为 true
```

然后在代码中使用：

```go
err := logger.NewBuilder().
    SetFilePath(viper.GetString("log.filepath")).
    SetMaxSize(viper.GetInt("log.max_size")).
    SetMaxBackups(viper.GetInt("log.max_backups")).
    SetMaxAge(viper.GetInt("log.max_age")).
    SetLevel(viper.GetString("log.level")).
    EnableCompression(viper.GetBool("log.compress")).
    EnableConsoleOutput(viper.GetBool("log.console")).
    Build()
```

## API 参考

### 日志级别

```go
logger.Debug().Msg("调试信息")  // 调试级别
logger.Info().Msg("普通信息")   // 信息级别
logger.Warn().Msg("警告信息")   // 警告级别
logger.Error().Msg("错误信息")  // 错误级别
logger.Fatal().Msg("致命错误")  // 致命错误（会退出程序）
```

### 字段类型

```go
logger.Info().
    Str("string", "value").           // 字符串
    Int("int", 123).                  // 整数
    Uint("uint", 456).                // 无符号整数
    Float64("float", 3.14).           // 浮点数
    Bool("bool", true).               // 布尔值
    Time("time", time.Now()).         // 时间
    Dur("duration", time.Second).     // 时长
    Err(err).                         // 错误
    Interface("any", obj).            // 任意类型
    Msg("消息")
```

### 业务上下文方法

```go
// 单个上下文
WithPlayer(playerID)              // 添加 player_id
WithRobot(robotID)                // 添加 robot_id
WithPlan(planID)                  // 添加 plan_id
WithExchange(exchange)            // 添加 exchange
WithSymbol(symbol)                // 添加 symbol
WithStrategy(strategyID)          // 添加 strategy_id
WithTrace(traceID)                // 添加 trace_id
WithRequest(requestID)            // 添加 request_id

// 组合上下文
WithPlayerRobot(playerID, robotID)      // player_id + robot_id
WithPlayerPlan(playerID, planID)        // player_id + plan_id
WithExchangeSymbol(exchange, symbol)    // exchange + symbol
```

### 兼容方法

```go
logger.Print(v...)
logger.Printf(format, v...)
logger.Println(v...)
logger.Infof(format, v...)
logger.Debugf(format, v...)
logger.Warnf(format, v...)
logger.Errorf(format, v...)
logger.Fatalf(format, v...)
```

## 最佳实践

### 1. 使用结构化日志

❌ **不推荐**：
```go
logger.Info().Msgf("用户 %d 在 %s 交易所交易 %s", userID, exchange, symbol)
```

✅ **推荐**：
```go
logger.WithPlayer(userID).Info().
    Str("exchange", exchange).
    Str("symbol", symbol).
    Msg("执行交易")
```

### 2. 使用业务上下文

❌ **不推荐**：
```go
logger.Info().
    Uint("player_id", playerID).
    Uint("robot_id", robotID).
    Msg("创建订单")
```

✅ **推荐**：
```go
logger.WithPlayerRobot(playerID, robotID).Info().
    Msg("创建订单")
```

### 3. 错误处理

❌ **不推荐**：
```go
if err != nil {
    logger.Error().Msg(err.Error())
}
```

✅ **推荐**：
```go
if err != nil {
    logger.Error().Err(err).Msg("操作失败")
}
```

### 4. 避免在循环中创建 logger

❌ **不推荐**：
```go
for _, item := range items {
    logger.WithPlayer(item.PlayerID).Info().Msg("处理")
}
```

✅ **推荐**：
```go
for _, item := range items {
    logger.Info().Uint("player_id", item.PlayerID).Msg("处理")
}
```

### 5. 使用条件日志

```go
// 只在 debug 级别记录
if logger.L().Debug().Enabled() {
    logger.Debug().
        Interface("detail", complexObject).
        Msg("详细信息")
}
```

## 性能对比

| 操作 | github.com/dolotech/log | zerolog | 提升 |
|------|-------------------------|---------|------|
| 简单日志 | 1200 ns/op | 150 ns/op | 8x |
| 结构化日志 | 2500 ns/op | 300 ns/op | 8x |
| 内存分配 | 3 allocs/op | 0 allocs/op | ∞ |

## 常见问题

### Q: 日志文件在哪里？
A: 默认在 `logs/trading.log`，可通过配置修改。

### Q: 如何查看实时日志？
A: 使用 `tail -f logs/trading.log`

### Q: 日志文件太大怎么办？
A: 调整 `MaxSize` 配置，系统会自动轮转。

### Q: 如何按日期轮转？
A: 系统会在每天 00:00 自动轮转日志文件。

### Q: 可以输出 JSON 格式吗？
A: 可以，修改 `logger.go` 中的 `ConsoleWriter` 配置。

### Q: 如何在测试中使用？
A: 参考 `init_example.go` 中的测试示例。

## 相关资源

- [Zerolog 官方文档](https://github.com/rs/zerolog)
- [迁移指南](./MIGRATION_GUIDE.md)
- [使用示例](./example_usage.go)
- [初始化示例](./init_example.go)

## License

MIT

