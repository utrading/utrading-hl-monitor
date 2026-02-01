package config

import (
	"os"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

type HLMonitor struct {
	HyperliquidWSURL              string        `toml:"hyperliquid_ws_url"`
	HealthServerAddr              string        `toml:"health_server_addr"`
	AddressReloadInterval         time.Duration `toml:"address_reload_interval"`
	MaxConnections                int           `toml:"max_connections"`
	MaxSubscriptionsPerConnection int           `toml:"max_subscriptions_per_connection"`
}

type MySQL struct {
	DSN                  string   `toml:"dsn"`
	SlaveAddr            []string `toml:"slave_addr"`
	MaxIdleConnections   int      `toml:"max_idle_connections"`
	MaxOpenConnections   int      `toml:"max_open_connections"`
	SetConnMaxLifetime   int      `toml:"set_conn_max_lifetime"`
	SetConnMaxIdleTime   int      `toml:"set_conn_max_idle_time"`
	ProxyEnabled         bool     `toml:"proxy_enabled"`
	ProxyAddr            string   `toml:"proxy_addr"`
}

type NATS struct {
	Endpoint string `toml:"endpoint"`
}

type Logger struct {
	Level      string `toml:"level"`
	MaxSize    int    `toml:"max_size"`
	MaxBackups int    `toml:"max_backups"`
	MaxAge     int    `toml:"max_age"`
	Compress   bool   `toml:"compress"`
	Console    bool   `toml:"console"`
}

type OrderAggregation struct {
	Timeout      time.Duration `toml:"timeout"`
	ScanInterval time.Duration `toml:"scan_interval"`
	MaxRetry     int           `toml:"max_retry"`
	RetryDelay   time.Duration `toml:"retry_delay"`
}

type Config struct {
	HLMonitor        HLMonitor        `toml:"hl_monitor"`
	MySQL            MySQL            `toml:"mysql"`
	NATS             NATS             `toml:"nats"`
	Logger           Logger           `toml:"log"`
	OrderAggregation OrderAggregation `toml:"order_aggregation"`
}

var (
	cfg         *Config
	cfgPath     string
	cfgLock     sync.RWMutex
	lastModTime time.Time
	stopChan    chan struct{}
)

func Default() *Config {
	return &Config{
		HLMonitor: HLMonitor{
			HyperliquidWSURL:              "wss://api.hyperliquid.xyz/ws",
			HealthServerAddr:              "0.0.0.0:16800",
			AddressReloadInterval:         time.Minute,
			MaxConnections:                20,  // 默认最多 20 个连接
			MaxSubscriptionsPerConnection: 100, // 每个连接最多订阅 100 个地址
		},
		MySQL: MySQL{
			DSN:                "root:password@tcp(localhost:3306)/utrading?charset=utf8mb4&parseTime=True&loc=Local",
			SlaveAddr:          []string{},
			MaxIdleConnections: 16,
			MaxOpenConnections: 64,
			SetConnMaxLifetime: 7200,
			SetConnMaxIdleTime: 3600,
			ProxyEnabled:       false,
			ProxyAddr:          "127.0.0.1:7890",
		},
		NATS: NATS{
			Endpoint: "nats://localhost:4222",
		},
		Logger: Logger{
			Level:      "info",
			MaxSize:    10,
			MaxBackups: 60,
			MaxAge:     7,
			Compress:   false,
			Console:    false,
		},
		OrderAggregation: OrderAggregation{
			Timeout:      5 * time.Minute,
			ScanInterval: 30 * time.Second,
			MaxRetry:     3,
			RetryDelay:   1 * time.Second,
		},
	}
}

func Load(path string) error {
	c := Default()
	if _, err := toml.DecodeFile(path, c); err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	cfgLock.Lock()
	defer cfgLock.Unlock()
	cfg = c
	cfgPath = path
	lastModTime = info.ModTime()

	return nil
}

func Get() *Config {
	cfgLock.RLock()
	defer cfgLock.RUnlock()
	return cfg
}

// Init 初始化配置并启动定期重载（默认10秒）
func Init(path string) error {
	return InitWithInterval(path, 10*time.Second)
}

// InitWithInterval 初始化配置并指定重载间隔
func InitWithInterval(path string, interval time.Duration) error {
	if err := Load(path); err != nil {
		return err
	}

	stopChan = make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				reloadIfNeeded()
			case <-stopChan:
				return
			}
		}
	}()

	return nil
}

// Stop 停止配置重载
func Stop() {
	if stopChan != nil {
		close(stopChan)
	}
}

// reloadIfNeeded 仅在文件修改时重载
func reloadIfNeeded() {
	cfgLock.RLock()
	path := cfgPath
	lastMod := lastModTime
	cfgLock.RUnlock()

	if path == "" {
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		logger.Error().Err(err).Msg("config stat failed")
		return
	}

	if info.ModTime().After(lastMod) {
		if err = Load(path); err != nil {
			logger.Error().Err(err).Msg("config reload failed")
		} else {
			logger.Info().Msg("config reloaded")
		}
	}
}
