package logger

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logMu             sync.RWMutex
	multiLevelWriter  zerolog.LevelWriter
	lumberjackWriters map[string]*lumberjack.Logger
	currentDate       atomic.Value
	closed            chan struct{}
	DateFormat        = "2006-01-02"
	TimeFormat        = "2006-01-02 15:04:05"
)

// initLogger 初始化日志系统
func initLogger(config Config) error {
	// 初始化当前日期
	currentDate.Store(time.Now().Format(DateFormat))
	// 初始化关闭信号通道
	closed = make(chan struct{})
	// 设置日志格式
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack

	// 设置日志级别
	setLogLevel(config.Level)

	// 如果 LevelFiles 为空，使用默认 info 文件
	if config.LevelFiles.IsEmpty() {
		config.LevelFiles = LevelFiles{
			{Level: INFO, Path: "logs/info.log"},
		}
	}

	// 创建所有日志文件目录
	for _, filePath := range config.LevelFiles.GetPaths() {
		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return err
		}
	}

	setWriter(config)

	go checkDateChange(config)

	return nil
}

func setWriter(config Config) {
	// 构建已配置的等级位掩码
	var configuredLevels uint8
	for _, entry := range config.LevelFiles {
		configuredLevels |= 1 << parseLevel(entry.Level)
	}

	newWriters := make([]io.Writer, 0, len(config.LevelFiles)+1)
	newLumberjackWriters := make(map[string]*lumberjack.Logger, len(config.LevelFiles))

	for _, entry := range config.LevelFiles {
		lj, err := createLumberjackWriter(entry.Path, config)
		if err != nil {
			panic("logger: failed to create writer for " + entry.Level + ": " + err.Error())
		}
		newLumberjackWriters[entry.Level] = lj

		level := parseLevel(entry.Level)
		filtered := &levelFilterWriter{
			level:            level,
			configuredLevels: configuredLevels,
			Writer:           wrapConsoleWriter(lj),
		}
		newWriters = append(newWriters, filtered)
	}

	if config.Console {
		consoleWriter := &zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: TimeFormat,
		}
		newWriters = append(newWriters, consoleWriter)
	}

	// 加锁替换全局变量
	logMu.Lock()
	defer logMu.Unlock()

	cleanupOldWriters()
	lumberjackWriters = newLumberjackWriters
	multiLevelWriter = zerolog.MultiLevelWriter(newWriters...)
	log.Logger = zerolog.New(multiLevelWriter).With().Timestamp().Caller().Logger()
}

// cleanupOldWriters 清理旧的 writers
func cleanupOldWriters() {
	if lumberjackWriters != nil {
		closeAllWriters()
	}
}

// levelFilterWriter 写入指定等级的日志，支持级别降级
type levelFilterWriter struct {
	level            zerolog.Level
	configuredLevels uint8 // 已配置的文件等级位掩码
	io.Writer
}

func (w *levelFilterWriter) WriteLevel(level zerolog.Level, p []byte) (n int, err error) {
	// 完全匹配
	if level == w.level {
		return w.Writer.Write(p)
	}

	// 其他规则
	switch w.level {
	case zerolog.InfoLevel:
		// 没有配置就写入 INFO
		if w.configuredLevels&(1<<level) == 0 {
			return w.Writer.Write(p)
		}
	case zerolog.ErrorLevel:
		// FATAL 没配置 同时写入 ERROR
		if level == zerolog.FatalLevel && w.configuredLevels&(1<<level) == 0 {
			return w.Writer.Write(p)
		}
	}
	return len(p), nil
}

// createLumberjackWriter 创建 lumberjack writer
func createLumberjackWriter(filePath string, config Config) (*lumberjack.Logger, error) {
	return &lumberjack.Logger{
		Filename:   filePath,
		MaxSize:    config.MaxSize,
		MaxBackups: config.MaxBackups,
		MaxAge:     config.MaxAge,
		Compress:   config.Compress,
	}, nil
}

// wrapConsoleWriter 包装 ConsoleWriter
func wrapConsoleWriter(w io.Writer) io.Writer {
	return &zerolog.ConsoleWriter{
		Out:        w,
		TimeFormat: TimeFormat,
		NoColor:    true,
	}
}

// parseLevel 解析等级名称到 zerolog.Level
func parseLevel(levelName string) zerolog.Level {
	switch levelName {
	case "debug", "DEBUG":
		return zerolog.DebugLevel
	case "info", "INFO":
		return zerolog.InfoLevel
	case "warn", "WARN":
		return zerolog.WarnLevel
	case "error", "ERROR":
		return zerolog.ErrorLevel
	case "fatal", "FATAL":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}

// closeAllWriters 关闭所有 writer
func closeAllWriters() {
	for levelName, lj := range lumberjackWriters {
		if err := lj.Close(); err != nil {
			log.Logger.Err(err).Str("level", levelName).Msg("failed to close lumberjack writer")
		}
	}
	lumberjackWriters = nil
}

func checkDateChange(config Config) {
	now := time.Now()
	next := getNextDay(now)
	ticker := time.NewTicker(next.Sub(now))
	defer ticker.Stop()
	for {
		select {
		case <-closed:
			return
		case t := <-ticker.C:
			newDate := t.Format(DateFormat)
			oldDate := currentDate.Load().(string)
			if newDate != oldDate {
				currentDate.Store(newDate)
				rotateAllFiles(config)
			}
			next = getNextDay(t)
			ticker.Reset(next.Sub(t))
		}
	}
}

// rotateAllFiles 轮转所有日志文件
func rotateAllFiles(config Config) {
	for i := 0; i < 3; i++ {
		var lastErr error
		for levelName, lj := range lumberjackWriters {
			if err := lj.Rotate(); err != nil {
				lastErr = err
				log.Logger.Err(err).Str("level", levelName).Msg("Failed to rotate log file")
			}
		}
		if lastErr != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		setWriter(config)
		log.Logger.Info().Msg("Log files rotated by date")
		break
	}
}

func getNextDay(ti time.Time) time.Time {
	return time.Date(ti.Year(), ti.Month(), ti.Day(), 0, 0, 0, 0, ti.Location()).AddDate(0, 0, 1)
}

// L 返回全局 logger
func L() zerolog.Logger {
	return log.Logger
}

func Info() *zerolog.Event {
	return log.Logger.Info()
}

func Debug() *zerolog.Event {
	return log.Logger.Debug()
}

func Error() *zerolog.Event {
	return log.Logger.Error()
}

func Warn() *zerolog.Event {
	return log.Logger.Warn()
}

func Fatal() *zerolog.Event {
	return log.Logger.Fatal()
}

// Err 直接记录错误
func Err(err error) *zerolog.Event {
	return log.Logger.Err(err)
}

// Close 关闭日志
func Close() {
	select {
	case closed <- struct{}{}:
	default:
	}

	if len(lumberjackWriters) > 0 {
		closeAllWriters()
	}
}
