package logger

/*
本文件展示如何在不同场景下初始化日志系统

// ========== 场景 1: 在 main.go 中初始化 ==========

package main

import (
    "trading/lib/logger"
    "github.com/spf13/viper"
)

func main() {
    // 从配置文件读取日志配置
    logLevel := viper.GetString("log.level")
    if logLevel == "" {
        logLevel = logger.INFO
    }

    // 初始化日志系统
    err := logger.NewBuilder().
        SetFilePath("logs/trading.log").
        SetMaxSize(100).
        SetMaxBackups(30).
        SetMaxAge(7).
        SetLevel(logLevel).
        EnableCompression(true).
        EnableConsoleOutput(viper.GetBool("log.console")).
        Build()

    if err != nil {
        panic("初始化日志系统失败: " + err.Error())
    }

    // 确保程序退出时关闭日志
    defer logger.Close()

    logger.Info().
        Str("version", "1.0.0").
        Str("log_level", logLevel).
        Msg("应用启动")

    // 其他启动代码...
}

// ========== 场景 2: 在 cfg.toml 中配置日志 ==========

// cfg.toml 添加以下配置：
//
// [log]
// level = "info"           # debug, info, warn, error, fatal
// filepath = "logs/trading.log"
// max_size = 100           # MB
// max_backups = 30         # 文件数
// max_age = 7              # 天数
// compress = true          # 是否压缩
// console = false          # 是否输出到控制台（开发时设为true）

// 然后在代码中使用：
func initLoggerFromConfig() error {
    return logger.NewBuilder().
        SetFilePath(viper.GetString("log.filepath")).
        SetMaxSize(viper.GetInt("log.max_size")).
        SetMaxBackups(viper.GetInt("log.max_backups")).
        SetMaxAge(viper.GetInt("log.max_age")).
        SetLevel(viper.GetString("log.level")).
        EnableCompression(viper.GetBool("log.compress")).
        EnableConsoleOutput(viper.GetBool("log.console")).
        Build()
}

// ========== 场景 3: 不同环境使用不同配置 ==========

import "os"

func initLoggerByEnv() error {
    env := os.Getenv("ENV")

    builder := logger.NewBuilder()

    switch env {
    case "production":
        // 生产环境：只记录到文件，info级别
        builder.
            SetFilePath("logs/trading.log").
            SetMaxSize(100).
            SetMaxBackups(60).
            SetMaxAge(15).
            SetLevel(logger.INFO).
            EnableCompression(true).
            EnableConsoleOutput(false)

    case "staging":
        // 预发布环境：记录到文件，debug级别
        builder.
            SetFilePath("logs/staging.log").
            SetMaxSize(50).
            SetMaxBackups(30).
            SetMaxAge(7).
            SetLevel(logger.DEBUG).
            EnableCompression(true).
            EnableConsoleOutput(false)

    default:
        // 开发环境：同时输出到控制台和文件，debug级别
        builder.
            SetFilePath("logs/dev.log").
            SetMaxSize(10).
            SetMaxBackups(5).
            SetMaxAge(3).
            SetLevel(logger.DEBUG).
            EnableCompression(false).
            EnableConsoleOutput(true)
    }

    return builder.Build()
}

// ========== 场景 4: 微服务/多模块使用不同日志文件 ==========

// 主服务
func initMainLogger() error {
    return logger.NewBuilder().
        SetFilePath("logs/trading.log").
        SetLevel(logger.INFO).
        Build()
}

// Webhook 服务
func initWebhookLogger() error {
    return logger.NewBuilder().
        SetFilePath("logs/webhook.log").
        SetLevel(logger.DEBUG).
        Build()
}

// 统计服务
func initStatisticsLogger() error {
    return logger.NewBuilder().
        SetFilePath("logs/statistics.log").
        SetLevel(logger.INFO).
        Build()
}

// ========== 场景 5: 在测试中初始化 ==========

import "testing"

func TestSomething(t *testing.T) {
    // 测试时使用临时日志文件
    err := logger.NewBuilder().
        SetFilePath("logs/test.log").
        SetLevel(logger.DEBUG).
        EnableConsoleOutput(true).
        Build()

    if err != nil {
        t.Fatal(err)
    }
    defer logger.Close()

    // 测试代码...
    logger.Debug().Msg("测试日志")
}

// ========== 场景 6: 优雅关闭 ==========

import (
    "os"
    "os/signal"
    "syscall"
)

func gracefulShutdown() {
    // 捕获退出信号
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        sig := <-sigChan
        logger.Info().
            Str("signal", sig.String()).
            Msg("收到退出信号，准备关闭")

        // 执行清理工作...

        // 关闭日志系统
        logger.Close()

        os.Exit(0)
    }()
}

// ========== 场景 7: 使用默认配置快速启动 ==========

func quickStart() error {
    // 使用默认配置
    config := logger.DefaultConfig()
    return logger.initLogger(config)
}

// ========== 场景 8: 动态调整日志级别 ==========

import "github.com/rs/zerolog"

func changeLogLevel(level string) {
    switch level {
    case "debug":
        zerolog.SetGlobalLevel(zerolog.DebugLevel)
    case "info":
        zerolog.SetGlobalLevel(zerolog.InfoLevel)
    case "warn":
        zerolog.SetGlobalLevel(zerolog.WarnLevel)
    case "error":
        zerolog.SetGlobalLevel(zerolog.ErrorLevel)
    }

    logger.Info().
        Str("new_level", level).
        Msg("日志级别已调整")
}

// 可以通过HTTP接口动态调整
func setupLogLevelAPI(app *iris.Application) {
    app.Post("/admin/log/level", func(ctx iris.Context) {
        var req struct {
            Level string `json:"level"`
        }
        if err := ctx.ReadJSON(&req); err != nil {
            ctx.StatusCode(400)
            return
        }

        changeLogLevel(req.Level)
        ctx.JSON(iris.Map{"status": "ok", "level": req.Level})
    })
}

// ========== 实际项目中的完整初始化示例 ==========

package main

import (
    "flag"
    "fmt"
    "os"
    "path/filepath"

    "trading/lib/logger"
    "github.com/spf13/viper"
)

func initLogger() error {
    // 确保日志目录存在
    logDir := viper.GetString("log.dir")
    if logDir == "" {
        logDir = "logs"
    }

    if err := os.MkdirAll(logDir, 0755); err != nil {
        return fmt.Errorf("创建日志目录失败: %w", err)
    }

    // 生成日志文件路径
    logFile := filepath.Join(logDir, "trading.log")

    // 获取日志级别
    logLevel := viper.GetString("log.level")
    if logLevel == "" {
        logLevel = logger.INFO
    }

    // 判断是否为开发环境
    isDev := viper.GetBool("debug") || os.Getenv("ENV") == "development"

    // 构建日志配置
    builder := logger.NewBuilder().
        SetFilePath(logFile).
        SetMaxSize(viper.GetInt("log.max_size")).
        SetMaxBackups(viper.GetInt("log.max_backups")).
        SetMaxAge(viper.GetInt("log.max_age")).
        SetLevel(logLevel).
        EnableCompression(!isDev). // 开发环境不压缩
        EnableConsoleOutput(isDev)  // 开发环境输出到控制台

    if err := builder.Build(); err != nil {
        return fmt.Errorf("初始化日志系统失败: %w", err)
    }

    // 记录启动信息
    logger.Info().
        Str("log_file", logFile).
        Str("log_level", logLevel).
        Bool("dev_mode", isDev).
        Msg("日志系统初始化完成")

    return nil
}

func main() {
    // 解析命令行参数
    var configFile string
    flag.StringVar(&configFile, "conf", "cfg.toml", "配置文件路径")
    flag.Parse()

    // 加载配置
    viper.SetConfigFile(configFile)
    if err := viper.ReadInConfig(); err != nil {
        fmt.Printf("读取配置文件失败: %v\n", err)
        os.Exit(1)
    }

    // 初始化日志
    if err := initLogger(); err != nil {
        fmt.Printf("初始化日志失败: %v\n", err)
        os.Exit(1)
    }
    defer logger.Close()

    // 记录启动信息
    logger.Info().
        Str("version", "1.0.0").
        Str("config", configFile).
        Msg("应用启动")

    // 其他启动逻辑...

    logger.Info().Msg("应用正常退出")
}

*/
