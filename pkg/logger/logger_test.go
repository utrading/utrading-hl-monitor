package logger

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInitLogger(t *testing.T) {
	// 创建临时日志目录
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// 初始化日志系统
	err := NewBuilder().
		AddLevelFile(INFO, logFile).
		SetMaxSize(10).
		SetMaxBackups(3).
		SetMaxAge(1).
		SetLevel(DEBUG).
		EnableCompression(false).
		EnableConsoleOutput(false).
		Build()

	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	// 写入一条日志以确保文件被创建
	Info().Msg("init test")

	// 验证日志文件是否创建
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("日志文件未创建")
	}
}

func TestBasicLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	err := NewBuilder().
		AddLevelFile(INFO, logFile).
		SetLevel(DEBUG).
		EnableConsoleOutput(false).
		Build()

	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	// 测试不同级别的日志
	Debug().Msg("debug message")
	Info().Msg("info message")
	Warn().Msg("warn message")
	Error().Msg("error message")

	// 验证日志文件有内容
	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("无法读取日志文件: %v", err)
	}

	if info.Size() == 0 {
		t.Error("日志文件为空")
	}
}

func TestStructuredLogging(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	err := NewBuilder().
		AddLevelFile(INFO, logFile).
		SetLevel(DEBUG).
		Build()

	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	// 测试结构化日志
	Info().
		Str("string", "value").
		Int("int", 123).
		Uint("uint", 456).
		Float64("float", 3.14).
		Bool("bool", true).
		Time("time", time.Now()).
		Dur("duration", time.Second).
		Msg("结构化日志测试")

	// 验证日志文件有内容
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}

	if len(content) == 0 {
		t.Error("日志文件为空")
	}
}

func TestErrorLogging(t *testing.T) {
	tmpDir := t.TempDir()
	infoFile := filepath.Join(tmpDir, "info.log")
	errorFile := filepath.Join(tmpDir, "error.log")

	err := NewBuilder().
		AddLevelFile(INFO, infoFile).
		AddLevelFile(ERROR, errorFile).
		SetLevel(DEBUG).
		Build()

	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	// 测试错误日志
	testErr := errors.New("test error")
	Error().Err(testErr).Msg("错误日志测试")
	Err(testErr).Msg("使用 Err 方法")

	// 验证错误日志文件有内容
	content, err := os.ReadFile(errorFile)
	if err != nil {
		t.Fatalf("读取错误日志文件失败: %v", err)
	}

	if len(content) == 0 {
		t.Error("错误日志文件为空")
	}
}

func TestCompatMethods(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	err := NewBuilder().
		AddLevelFile(INFO, logFile).
		SetLevel(DEBUG).
		Build()

	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	// 测试兼容方法
	Printf("printf message: %s", "test")
	Infof("infof message: %d", 123)
	Debugf("debugf message: %f", 3.14)
	Warnf("warnf message: %t", true)
	Errorf("errorf message: %v", errors.New("test"))

	// 验证日志文件有内容
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}

	if len(content) == 0 {
		t.Error("日志文件为空")
	}
}

func TestHelperMethods(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	err := NewBuilder().
		AddLevelFile(INFO, logFile).
		SetLevel(DEBUG).
		Build()

	if err != nil {
		t.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	// 测试辅助方法
	LogInfo("info message", map[string]interface{}{
		"key1": "value1",
		"key2": 123,
	})

	LogDebug("debug message", map[string]interface{}{
		"key1": "value1",
	})

	LogWarn("warn message", map[string]interface{}{
		"key1": "value1",
	})

	testErr := errors.New("test error")
	LogError(testErr, "error message", map[string]interface{}{
		"key1": "value1",
	})

	// 验证日志文件有内容
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("读取日志文件失败: %v", err)
	}

	if len(content) == 0 {
		t.Error("日志文件为空")
	}
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		name  string
		level string
	}{
		{"Debug Level", DEBUG},
		{"Info Level", INFO},
		{"Warn Level", WARN},
		{"Error Level", ERROR},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			infoFile := filepath.Join(tmpDir, "info.log")
			warnFile := filepath.Join(tmpDir, "warn.log")
			errorFile := filepath.Join(tmpDir, "error.log")

			err := NewBuilder().
				AddLevelFile(INFO, infoFile).
				AddLevelFile(WARN, warnFile).
				AddLevelFile(ERROR, errorFile).
				SetLevel(tt.level).
				Build()

			if err != nil {
				t.Fatalf("初始化日志失败: %v", err)
			}
			defer Close()

			// 记录不同级别的日志
			Debug().Msg("debug")
			Info().Msg("info")
			Warn().Msg("warn")
			Error().Msg("error")

			// 验证日志文件存在 (INFO 总是被写入，至少在 Debug/Info 级别时)
			if tt.level == DEBUG || tt.level == INFO {
				if _, err := os.Stat(infoFile); os.IsNotExist(err) {
					t.Error("info 日志文件未创建")
				}
			}
			// ERROR 总是被写入
			if _, err := os.Stat(errorFile); os.IsNotExist(err) {
				t.Error("error 日志文件未创建")
			}
		})
	}
}

func TestBuilderPattern(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	// 测试构建器模式
	builder := NewBuilder()
	builder.AddLevelFile(INFO, logFile)
	builder.SetMaxSize(10)
	builder.SetMaxBackups(5)
	builder.SetMaxAge(3)
	builder.SetLevel(INFO)
	builder.EnableCompression(true)
	builder.EnableConsoleOutput(false)

	err := builder.Build()
	if err != nil {
		t.Fatalf("构建日志系统失败: %v", err)
	}
	defer Close()

	Info().Msg("测试构建器模式")

	// 验证日志文件创建
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("日志文件未创建")
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.LevelFiles.IsEmpty() {
		t.Error("默认 LevelFiles 不应为空")
	}

	if config.MaxSize != 10 {
		t.Errorf("默认 MaxSize 错误: %d", config.MaxSize)
	}

	if config.MaxBackups != 100 {
		t.Errorf("默认 MaxBackups 错误: %d", config.MaxBackups)
	}

	if config.MaxAge != 5 {
		t.Errorf("默认 MaxAge 错误: %d", config.MaxAge)
	}

	if config.Level != INFO {
		t.Errorf("默认 Level 错误: %s", config.Level)
	}

	if config.Compress != false {
		t.Error("默认 Compress 应为 false")
	}

	if config.Console != false {
		t.Error("默认 Console 应为 false")
	}

	// 验证默认 LevelFiles 包含 ERROR 和 INFO
	if !config.LevelFiles.HasLevel(ERROR) {
		t.Error("默认 LevelFiles 应包含 ERROR")
	}
	if !config.LevelFiles.HasLevel(INFO) {
		t.Error("默认 LevelFiles 应包含 INFO")
	}

	errorPath, ok := config.LevelFiles.GetPath(ERROR)
	if !ok || errorPath != "logs/err.log" {
		t.Errorf("默认 ERROR 路径错误: %s", errorPath)
	}

	infoPath, ok := config.LevelFiles.GetPath(INFO)
	if !ok || infoPath != "logs/info.log" {
		t.Errorf("默认 INFO 路径错误: %s", infoPath)
	}
}

func BenchmarkSimpleLogging(b *testing.B) {
	tmpDir := b.TempDir()
	logFile := filepath.Join(tmpDir, "bench.log")

	err := NewBuilder().
		AddLevelFile(INFO, logFile).
		SetLevel(INFO).
		Build()

	if err != nil {
		b.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Info().Msg("benchmark message")
	}
}

func BenchmarkStructuredLogging(b *testing.B) {
	tmpDir := b.TempDir()
	logFile := filepath.Join(tmpDir, "bench.log")

	err := NewBuilder().
		AddLevelFile(INFO, logFile).
		SetLevel(INFO).
		Build()

	if err != nil {
		b.Fatalf("初始化日志失败: %v", err)
	}
	defer Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Info().
			Str("key1", "value1").
			Int("key2", 123).
			Float64("key3", 3.14).
			Msg("benchmark message")
	}
}
