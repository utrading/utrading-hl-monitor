package logger

import "github.com/rs/zerolog"

// ========== 迁移助手方法 ==========
// 提供更灵活的日志记录方式，便于从旧日志系统迁移

// LogInfo 结构化 Info 日志
func LogInfo(msg string, fields map[string]interface{}) {
	event := Info()
	for k, v := range fields {
		event = addField(event, k, v)
	}
	event.Msg(msg)
}

// LogError 结构化 Error 日志
func LogError(err error, msg string, fields map[string]interface{}) {
	event := Error().Err(err)
	for k, v := range fields {
		event = addField(event, k, v)
	}
	event.Msg(msg)
}

// LogDebug 结构化 Debug 日志
func LogDebug(msg string, fields map[string]interface{}) {
	event := Debug()
	for k, v := range fields {
		event = addField(event, k, v)
	}
	event.Msg(msg)
}

// LogWarn 结构化 Warn 日志
func LogWarn(msg string, fields map[string]interface{}) {
	event := Warn()
	for k, v := range fields {
		event = addField(event, k, v)
	}
	event.Msg(msg)
}

// addField 动态添加字段
func addField(event *zerolog.Event, key string, value interface{}) *zerolog.Event {
	switch v := value.(type) {
	case string:
		return event.Str(key, v)
	case int:
		return event.Int(key, v)
	case int64:
		return event.Int64(key, v)
	case uint:
		return event.Uint(key, v)
	case uint64:
		return event.Uint64(key, v)
	case float64:
		return event.Float64(key, v)
	case float32:
		return event.Float32(key, v)
	case bool:
		return event.Bool(key, v)
	case error:
		return event.AnErr(key, v)
	default:
		return event.Interface(key, v)
	}
}
