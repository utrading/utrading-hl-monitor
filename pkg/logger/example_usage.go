package logger

// 本文件提供日志使用示例，仅用于参考，不会被编译到最终程序中

/*
// ========== 示例 1: 初始化日志系统 ==========

func main() {
    // 开发环境配置
    err := NewBuilder().
        SetFilePath("logs/dev.log").
        SetLevel(DEBUG).
        EnableConsoleOutput(true).
        Build()

    if err != nil {
        panic(err)
    }
    defer Close()

    // 或使用生产环境配置
    err := NewBuilder().
        SetFilePath("logs/trading.log").
        SetMaxSize(100).           // 100MB
        SetMaxBackups(30).         // 保留30个备份
        SetMaxAge(7).              // 保留7天
        SetLevel(INFO).
        EnableCompression(true).
        Build()
}

// ========== 示例 2: 基础日志使用 ==========

func BasicLogging() {
    // 简单消息
    Info().Msg("应用启动")
    Debug().Msg("调试信息")
    Warn().Msg("警告信息")
    Error().Msg("错误信息")

    // 带字段的日志
    Info().
        Str("version", "1.0.0").
        Int("port", 8080).
        Msg("服务器启动")

    // 错误日志
    err := someFunction()
    if err != nil {
        Error().Err(err).Msg("操作失败")
    }
}

// ========== 示例 3: 结构化日志 ==========

func StructuredLogging() {
    // 记录用户操作
    Info().
        Uint("user_id", 12345).
        Str("action", "login").
        Str("ip", "192.168.1.1").
        Time("timestamp", time.Now()).
        Msg("用户登录")

    // 记录订单信息
    Info().
        Uint("order_id", 98765).
        Float64("amount", 1234.56).
        Str("status", "completed").
        Dur("duration", time.Second*2).
        Msg("订单完成")

    // 记录交易信息
    Info().
        Str("symbol", "BTCUSDT").
        Str("side", "BUY").
        Float64("price", 45000.00).
        Float64("quantity", 0.1).
        Msg("交易执行")
}

// ========== 示例 4: 使用上下文方法 ==========

func ContextLogging(playerID, robotID uint, exchange, symbol string) {
    // 单个上下文
    WithPlayer(playerID).Info().Msg("玩家操作")
    WithRobot(robotID).Error().Msg("机器人错误")
    WithExchange(exchange).Debug().Msg("交易所信息")
    WithSymbol(symbol).Warn().Msg("交易对警告")

    // 组合上下文
    WithPlayerRobot(playerID, robotID).Info().
        Str("action", "create_order").
        Msg("创建订单")

    WithExchangeSymbol(exchange, symbol).Info().
        Float64("price", 45000.0).
        Msg("价格更新")

    // 链式添加更多字段
    WithPlayerRobot(playerID, robotID).Info().
        Str("exchange", exchange).
        Str("symbol", symbol).
        Float64("quantity", 0.1).
        Str("side", "BUY").
        Msg("交易执行")
}

// ========== 示例 5: 迁移前后对比 ==========

// 【迁移前】使用 github.com/dolotech/log
func OldWay(playerID uint, robotID uint, err error) {
    logger.Infof("开始处理")
    log.Printf("处理用户 %d 的机器人 %d", playerID, robotID)
    if err != nil {
        logger.Errorf("处理失败:", err)
    }
}

// 【迁移后】使用兼容层（最小改动）
func CompatibleWay(playerID uint, robotID uint, err error) {
    Info().Msg("开始处理")
    Infof("处理用户 %d 的机器人 %d", playerID, robotID)
    if err != nil {
        Error().Err(err).Msg("处理失败")
    }
}

// 【迁移后】使用结构化日志（推荐）
func BestPractice(playerID uint, robotID uint, err error) {
    Info().Msg("开始处理")

    Info().
        Uint("player_id", playerID).
        Uint("robot_id", robotID).
        Msg("处理请求")

    if err != nil {
        WithPlayerRobot(playerID, robotID).Error().
            Err(err).
            Msg("处理失败")
    }
}

// ========== 示例 6: 不同场景的日志记录 ==========

// 场景1: API请求日志
func LogAPIRequest(method, path, ip string, duration time.Duration, statusCode int) {
    Info().
        Str("method", method).
        Str("path", path).
        Str("ip", ip).
        Dur("duration", duration).
        Int("status", statusCode).
        Msg("API请求")
}

// 场景2: 交易日志
func LogTrade(exchange, symbol, side string, price, quantity float64) {
    WithExchangeSymbol(exchange, symbol).Info().
        Str("side", side).
        Float64("price", price).
        Float64("quantity", quantity).
        Float64("total", price*quantity).
        Msg("交易执行")
}

// 场景3: 错误日志with堆栈
func LogErrorWithStack(err error, context map[string]interface{}) {
    event := Error().Stack().Err(err)
    for k, v := range context {
        switch val := v.(type) {
        case string:
            event = event.Str(k, val)
        case int:
            event = event.Int(k, val)
        case uint:
            event = event.Uint(k, val)
        case float64:
            event = event.Float64(k, val)
        default:
            event = event.Interface(k, val)
        }
    }
    event.Msg("发生错误")
}

// 场景4: 性能监控日志
func LogPerformance(operation string, duration time.Duration, details map[string]interface{}) {
    event := Info().
        Str("operation", operation).
        Dur("duration", duration).
        Float64("duration_ms", float64(duration.Milliseconds()))

    for k, v := range details {
        event = event.Interface(k, v)
    }

    event.Msg("性能监控")
}

// 场景5: 业务事件日志
func LogBusinessEvent(eventType string, playerID uint, metadata map[string]interface{}) {
    WithPlayer(playerID).Info().
        Str("event_type", eventType).
        Interface("metadata", metadata).
        Msg("业务事件")
}

// ========== 示例 7: 条件日志 ==========

func ConditionalLogging(verbose bool) {
    // 只在调试模式记录
    if L().Debug().Enabled() {
        Debug().
            Str("detail", "详细的调试信息").
            Msg("调试日志")
    }

    // 条件日志
    if verbose {
        Info().Msg("详细模式已启用")
    }
}

// ========== 示例 8: 使用辅助方法记录日志 ==========

func UseHelperMethods() {
    // 使用迁移助手方法
    LogInfo("操作成功", map[string]interface{}{
        "user_id":  123,
        "action":   "create",
        "resource": "order",
    })

    err := errors.New("数据库连接失败")
    LogError(err, "数据库错误", map[string]interface{}{
        "host": "localhost",
        "port": 3306,
    })
}

// ========== 示例 9: 全局Logger vs 局部Logger ==========

func GlobalVsLocal() {
    // 使用全局 logger
    Info().Msg("全局日志")

    // 创建局部 logger（带固定上下文）
    robotLogger := WithRobot(12345)
    robotLogger.Info().Msg("机器人启动")
    robotLogger.Debug().Msg("机器人调试信息")
    robotLogger.Error().Msg("机器人错误")

    // 在局部 logger 基础上继续添加字段
    robotLogger.Info().
        Str("exchange", "binance").
        Msg("连接交易所")
}

// ========== 示例 10: 异步/批量日志 ==========

func AsyncLogging() {
    // zerolog 本身已经很快，通常不需要异步
    // 但如果需要批量记录，可以这样：

    events := make([]map[string]interface{}, 0, 100)

    // 收集事件
    for i := 0; i < 100; i++ {
        events = append(events, map[string]interface{}{
            "index": i,
            "value": i * 10,
        })
    }

    // 批量记录
    for _, event := range events {
        Info().
            Int("index", event["index"].(int)).
            Int("value", event["value"].(int)).
            Msg("批量事件")
    }
}

*/
