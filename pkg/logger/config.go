package logger

import "github.com/rs/zerolog"

var (
	DEBUG = "debug"
	INFO  = "info"
	WARN  = "warn"
	ERROR = "error"
	FATAL = "fatal"
)

func setLogLevel(level string) {
	switch level {
	case DEBUG:
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case INFO:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case WARN:
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case ERROR:
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case FATAL:
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	default:
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

// LevelFileEntry 定义单个日志级别文件配置
type LevelFileEntry struct {
	Level string // 日志级别: debug, info, warn, error, fatal
	Path  string // 日志文件路径
}

// LevelFiles 日志级别文件配置集合
type LevelFiles []LevelFileEntry

// IsEmpty 判断是否为空
func (lf LevelFiles) IsEmpty() bool {
	return len(lf) == 0
}

// GetPath 获取指定级别的文件路径
func (lf LevelFiles) GetPath(level string) (string, bool) {
	for _, entry := range lf {
		if entry.Level == level {
			return entry.Path, true
		}
	}
	return "", false
}

// HasLevel 判断是否包含指定级别
func (lf LevelFiles) HasLevel(level string) bool {
	for _, entry := range lf {
		if entry.Level == level {
			return true
		}
	}
	return false
}

// GetPaths 获取所有文件路径
func (lf LevelFiles) GetPaths() []string {
	paths := make([]string, 0, len(lf))
	for _, entry := range lf {
		paths = append(paths, entry.Path)
	}
	return paths
}

type Config struct {
	LevelFiles LevelFiles // 分等级文件路径（可为空，空时使用默认 info 文件）
	MaxSize    int        // 日志文件最大大小（MB）
	MaxBackups int        // 保存的旧日志文件最大数量
	MaxAge     int        // 保留旧日志文件的最大天数
	Level      string     // 日志级别 (debug, info, warn, error, fatal)
	Compress   bool       // 是否压缩旧日志
	Console    bool       // 是否同时输出到控制台
}

// DefaultConfig 返回默认配置（单 info 文件）
func DefaultConfig() Config {
	return Config{
		LevelFiles: LevelFiles{
			{Level: ERROR, Path: "logs/err.log"},
			{Level: INFO, Path: "logs/info.log"},
		},
		MaxSize:    10,
		MaxBackups: 100,
		MaxAge:     5,
		Level:      INFO,
		Compress:   false,
		Console:    false,
	}
}

type Builder struct {
	config Config
}

func NewBuilder() *Builder {
	return &Builder{
		config: DefaultConfig(),
	}
}

func (b *Builder) SetMaxSize(size int) *Builder {
	b.config.MaxSize = size
	return b
}

func (b *Builder) SetMaxBackups(backups int) *Builder {
	b.config.MaxBackups = backups
	return b
}

func (b *Builder) SetMaxAge(days int) *Builder {
	b.config.MaxAge = days
	return b
}

func (b *Builder) SetLevel(level string) *Builder {
	b.config.Level = level
	return b
}

func (b *Builder) EnableCompression(enable bool) *Builder {
	b.config.Compress = enable
	return b
}

func (b *Builder) EnableConsoleOutput(enable bool) *Builder {
	b.config.Console = enable
	return b
}

// AddLevelFile 添加单个级别文件配置
func (b *Builder) AddLevelFile(level, path string) *Builder {
	b.config.LevelFiles = append(b.config.LevelFiles, LevelFileEntry{
		Level: level,
		Path:  path,
	})
	return b
}

// SetLevelFiles 设置多个级别文件配置
func (b *Builder) SetLevelFiles(files LevelFiles) *Builder {
	b.config.LevelFiles = files
	return b
}

func (b *Builder) Build() error {
	return initLogger(b.config)
}
