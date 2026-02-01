package dal

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	proxymysql "github.com/go-sql-driver/mysql"
	"github.com/rs/zerolog/log"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
	"golang.org/x/net/proxy"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"

	"github.com/utrading/utrading-hl-monitor/config"
	"github.com/utrading/utrading-hl-monitor/internal/models"
)

type GormLogger struct{}

func (l GormLogger) Printf(f string, args ...any) {
	log.Printf(f, args...)
}

func (l GormLogger) Print(args ...any) {
	log.Print(args...)
}

var (
	mysqlDB     *gorm.DB
	mysqlDBOnce sync.Once
)

func InitMysqlDB(cfg config.MySQL) {
	mysqlDBOnce.Do(func() {
		mysqlDB = connectMySQL(cfg)
	})
}

// registerProxyDialer 注册 SOCKS5 代理拨号器
func registerProxyDialer(proxyAddr string) error {
	dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, &net.Dialer{})
	if err != nil {
		return fmt.Errorf("create proxy dialer failed: %w", err)
	}

	proxymysql.RegisterDialContext("dial", func(ctx context.Context, addr string) (net.Conn, error) {
		return dialer.Dial("tcp", addr)
	})

	return nil
}

func connectMySQL(cfg config.MySQL) *gorm.DB {
	// 注册代理（如果启用）
	if cfg.ProxyEnabled {
		if err := registerProxyDialer(cfg.ProxyAddr); err != nil {
			panic(fmt.Sprintf("register proxy failed: %v", err))
		}
		logger.Infof("mysql proxy enabled: %s", cfg.ProxyAddr)
	}

	newLogger := gormlogger.New(
		GormLogger{}, gormlogger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  gormlogger.Warn,
			Colorful:                  true,
			IgnoreRecordNotFoundError: true,
		},
	)

	// 主库连接
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{
		Logger:      newLogger,
		PrepareStmt: true,
	})
	if err != nil {
		panic(fmt.Sprintf("connect mysql master failed: %v", err))
	}

	// 配置读写分离
	dbCfg := dbresolver.Config{}

	// 添加从库
	if len(cfg.SlaveAddr) > 0 {
		var replicas []gorm.Dialector
		for _, addr := range cfg.SlaveAddr {
			replicas = append(replicas, mysql.Open(addr))
		}
		dbCfg.Replicas = replicas
		dbCfg.TraceResolverMode = true
		logger.Infof("mysql %d slave(s) configured", len(cfg.SlaveAddr))
	}

	// 配置连接池
	maxIdleTime := time.Hour
	if cfg.SetConnMaxIdleTime > 0 {
		maxIdleTime = time.Duration(cfg.SetConnMaxIdleTime) * time.Second
	}

	maxLifetime := 2 * time.Hour
	if cfg.SetConnMaxLifetime > 0 {
		maxLifetime = time.Duration(cfg.SetConnMaxLifetime) * time.Second
	}

	// 应用 dbresolver 插件
	if len(cfg.SlaveAddr) > 0 {
		plugin := dbresolver.Register(dbCfg).
			SetConnMaxIdleTime(maxIdleTime).
			SetConnMaxLifetime(maxLifetime).
			SetMaxIdleConns(cfg.MaxIdleConnections).
			SetMaxOpenConns(cfg.MaxOpenConnections)
		if err = db.Use(plugin); err != nil {
			panic(fmt.Sprintf("register dbresolver failed: %v", err))
		}
	}

	// 配置主库连接池
	sqlDB, err := db.DB()
	if err != nil {
		panic(fmt.Sprintf("get sql.DB failed: %v", err))
	}

	sqlDB.SetMaxIdleConns(cfg.MaxIdleConnections)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConnections)
	sqlDB.SetConnMaxIdleTime(maxIdleTime)
	sqlDB.SetConnMaxLifetime(maxLifetime)

	logger.Info().Msgf("mysql connected: max_idle=%d, max_open=%d, max_idle_time=%v, max_lifetime=%v",
		cfg.MaxIdleConnections, cfg.MaxOpenConnections, maxIdleTime, maxLifetime)

	return db
}

func MySQL() *gorm.DB {
	return mysqlDB
}

func CloseMySQL() {
	if mysqlDB == nil {
		return
	}
	sqlDB, err := mysqlDB.DB()
	if err != nil {
		logger.Error().Err(err)
		return
	}
	if err = sqlDB.Close(); err != nil {
		logger.Error().Err(err)
	}

	logger.Infof("mysqlDB closed.")
}

// AutoMigrate 自动迁移数据库表结构
// 失败时记录警告日志，不中断服务启动
func AutoMigrate() {
	db := MySQL()
	if db == nil {
		log.Error().Msg("database not initialized, skip auto migration")
		return
	}

	modelList := []interface{}{
		&models.HlWatchAddress{},
		&models.HlPositionCache{},
		&models.OrderAggregation{},
		&models.HlAddressSignal{},
	}

	for _, model := range modelList {
		if err := db.AutoMigrate(model); err != nil {
			log.Warn().Err(err).
				Str("table", getTableName(model)).
				Msg("auto migrate failed, continuing anyway")
		} else {
			log.Info().Str("table", getTableName(model)).Msg("auto migrate success")
		}
	}
}

// getTableName 获取模型的表名
func getTableName(model interface{}) string {
	if t, ok := model.(interface{ TableName() string }); ok {
		return t.TableName()
	}
	return "unknown"
}
