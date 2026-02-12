package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/cleaner"
	"github.com/utrading/utrading-hl-monitor/internal/processor"
	"github.com/utrading/utrading-hl-monitor/internal/symbol"

	"github.com/utrading/utrading-hl-monitor/config"
	"github.com/utrading/utrading-hl-monitor/internal/address"
	"github.com/utrading/utrading-hl-monitor/internal/dal"
	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/internal/manager"
	"github.com/utrading/utrading-hl-monitor/internal/monitor"
	"github.com/utrading/utrading-hl-monitor/internal/nats"
	"github.com/utrading/utrading-hl-monitor/internal/ws"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
	"github.com/utrading/utrading-hl-monitor/pkg/sigproc"
)

func main() {
	var configFile string
	var testMode bool
	flag.StringVar(&configFile, "config", "cfg.toml", "config file path")
	flag.BoolVar(&testMode, "test", false, "run in test mode with mock data")
	flag.Parse()

	// 加载配置
	if err := config.Init(configFile); err != nil {
		panic(err)
	}
	cfg := config.Get()

	// 初始化日志
	if err := initLogger(cfg); err != nil {
		panic("init logger failed: " + err.Error())
	}
	defer logger.Close()

	logger.Info().Msg("hl_monitor service starting...")

	// 初始化指标
	monitor.InitMetrics()

	// 初始化数据库
	dal.InitMysqlDB(cfg.MySQL)

	// 自动迁移表结构
	dal.AutoMigrate()

	// 初始化 DAO
	dao.InitDAO(dal.MySQL())

	// 创建数据清理器
	dataCleaner := cleaner.NewCleaner(dal.MySQL())
	dataCleaner.Start()

	// 初始化 NATS
	publisher, err := nats.NewPublisher(cfg.NATS.Endpoint)
	if err != nil {
		logger.Fatal().Err(err).Msg("init nats publisher failed")
	}
	defer publisher.Close()

	// 初始化 WebSocket
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建 ws PoolManager（用于 PositionManager 和 SubscriptionManager）
	wsPoolManager := ws.NewPoolManager(
		cfg.HLMonitor.HyperliquidWSURL,
		cfg.HLMonitor.MaxConnections,
		cfg.HLMonitor.MaxSubscriptionsPerConnection,
	)
	if err = wsPoolManager.Start(ctx); err != nil {
		logger.Fatal().Err(err).Msg("start ws pool manager failed")
	}

	// 创建 Symbol 管理器（内部会加载 Symbol 数据）
	symbolManager, err := symbol.NewManager()
	if err != nil {
		logger.Fatal().Err(err).Msg("init symbol manager failed")
	}
	defer symbolManager.Close()

	// 创建批量写入器
	batchWriter := processor.NewBatchWriter(nil)
	batchWriter.Start()

	// 初始化仓位管理器（监听仓位变化，使用 ws.PoolManager）
	posManager := manager.NewPositionManager(wsPoolManager, symbolManager.PriceCache(), symbolManager.SymbolCache(), batchWriter)

	// 获取仓位余额缓存（从 PositionManager 传递给 SubscriptionManager）
	positionBalanceCache := posManager.PositionBalanceCache()

	// 初始化订阅管理器（监听订单成交，也使用 ws.PoolManager）
	subManager := manager.NewSubscriptionManager(wsPoolManager, publisher, symbolManager.SymbolCache(), positionBalanceCache, batchWriter)

	// 加载已发送的订单到去重缓存（防止服务重启后重复处理）
	deduper := subManager.GetDeduper()
	if err = deduper.LoadFromDB(dao.OrderAggregation()); err != nil {
		logger.Warn().Err(err).Msg("failed to load sent orders to dedup cache")
	}

	// 初始化地址加载器（从 hl_watch_addresses 表加载）
	addrLoader := address.NewAddressLoader(
		[]address.AddressSubscriber{subManager, posManager},
		cfg.HLMonitor.AddressReloadInterval,
		cfg.HLMonitor.AddressRemoveGrace,
	)

	// 启动地址加载器
	if err = addrLoader.Start(); err != nil {
		logger.Fatal().Err(err).Msg("start address loader failed")
	}

	// 初始化健康检查服务器
	healthServer := monitor.NewHealthServer(
		cfg.HLMonitor.HealthServerAddr,
		subManager,
		wsPoolManager,
		publisher,
	)
	if err = healthServer.Start(ctx); err != nil {
		logger.Fatal().Err(err).Msg("start health server failed")
	}
	defer healthServer.Stop(context.Background())

	logger.Info().
		Str("ws_url", cfg.HLMonitor.HyperliquidWSURL).
		Str("health_addr", cfg.HLMonitor.HealthServerAddr).
		Msg("hl_monitor service started successfully")

	// 优雅关闭
	sigproc.GracefulShutdown(func(sig os.Signal) {
		logger.Info().Str("signal", sig.String()).Msg("shutting down...")

		// 停止数据清理器
		dataCleaner.Stop()

		// 停止接收新信号
		cancel()

		// 停止地址加载器
		addrLoader.Stop()

		// 关闭订阅管理器
		subManager.Close()

		// 关闭仓位管理器
		posManager.Close()

		// 关闭 ws PoolManager
		wsPoolManager.Close()

		// 关闭健康检查服务器
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		healthServer.Stop(shutdownCtx)

		// 关闭配置重载
		config.Stop()

		// 关闭批量写入器
		batchWriter.Stop()

		// 关闭数据库
		dal.CloseMySQL()

		logger.Info().Msg("hl_monitor service stopped")
	})

	<-ctx.Done()
}

func initLogger(cfg *config.Config) error {
	return logger.NewBuilder().
		SetMaxSize(cfg.Logger.MaxSize).
		SetMaxBackups(cfg.Logger.MaxBackups).
		SetMaxAge(cfg.Logger.MaxAge).
		SetLevel(cfg.Logger.Level).
		EnableCompression(cfg.Logger.Compress).
		EnableConsoleOutput(cfg.Logger.Console).
		Build()
}
