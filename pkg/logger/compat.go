package logger

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ========== 兼容旧 log 库的接口 ==========
// 这些方法提供与 github.com/dolotech/log 兼容的接口
// 便于逐步迁移现有代

// hasFormatVerb 检查格式字符串是否包含格式化动词
func hasFormatVerb(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '%' {
			if i+1 < len(s) && s[i+1] == '%' {
				i++
				continue
			}
			return true
		}
	}
	return false
}

func logf(event *zerolog.Event, format any, args ...any) {
	if event == nil {
		return
	}

	// 跳过2层调用栈，显示实际调用者的位置
	event = event.CallerSkipFrame(2)

	formatStr, ok := format.(string)
	if ok && len(args) == 0 {
		event.Msg(formatStr)
		return
	}

	// 存在格式化占位符
	if ok && hasFormatVerb(formatStr) {
		event.Msgf(formatStr, args...)
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprint(format))
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprint(a))
	}
	event.Msg(b.String())
}

// Printf 兼容旧的 Printf 方法
func Printf(format any, v ...any) {
	logf(log.Logger.Info(), format, v...)
}

// Infof 格式化 Info 日志
func Infof(format any, v ...any) {
	logf(log.Logger.Info(), format, v...)
}

// Debugf 格式化 Debug 日志
func Debugf(format any, v ...any) {
	logf(log.Logger.Debug(), format, v...)
}

// Warnf 格式化 Warn 日志
func Warnf(format any, v ...any) {
	logf(log.Logger.Warn(), format, v...)
}

// Errorf 格式化 Error 日志
func Errorf(format any, v ...any) {
	logf(log.Logger.Error(), format, v...)
}

// Fatalf 格式化 Fatal 日志
func Fatalf(format any, v ...any) {
	logf(log.Logger.Fatal(), format, v...)
}

// Panicf 格式化 Panic 日志
func Panicf(format any, v ...any) {
	logf(log.Logger.Panic(), format, v...)
}
