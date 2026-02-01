package processor

import (
	"github.com/utrading/utrading-hl-monitor/internal/models"
	"github.com/utrading/utrading-hl-monitor/pkg/logger"
)

// PositionProcessor 仓位消息处理器
// 将仓位更新消息转换为批量写入项
type PositionProcessor struct {
	batchWriter *BatchWriter
}

// NewPositionProcessor 创建仓位处理器
func NewPositionProcessor(bw *BatchWriter) *PositionProcessor {
	return &PositionProcessor{
		batchWriter: bw,
	}
}

// HandleMessage 实现 MessageHandler 接口
func (p *PositionProcessor) HandleMessage(msg Message) error {
	switch m := msg.(type) {
	case PositionUpdateMessage:
		return p.handlePositionUpdate(m)
	default:
		logger.Warn().Str("type", msg.Type()).Msg("unknown message type")
		return nil
	}
}

// handlePositionUpdate 处理仓位更新消息
func (p *PositionProcessor) handlePositionUpdate(msg PositionUpdateMessage) error {
	if p.batchWriter == nil {
		return nil
	}

	// 将消息转换为批量写入项
	data, ok := msg.Data.(*PositionCacheData)
	if !ok {
		logger.Warn().
			Str("address", msg.Address).
			Msg("invalid position cache data type")
		return nil
	}

	item := PositionCacheItem{
		Address: msg.Address,
		Cache:   data.Cache,
	}

	// 添加到批量写入队列
	if err := p.batchWriter.Add(item); err != nil {
		logger.Error().
			Err(err).
			Str("address", msg.Address).
			Msg("failed to add position to batch writer")
		return err
	}

	return nil
}

// PositionCacheData 仓位缓存数据
type PositionCacheData struct {
	Cache *models.HlPositionCache
}

// NewPositionCacheMessage 创建仓位缓存消息
func NewPositionCacheMessage(addr string, cache *models.HlPositionCache) PositionUpdateMessage {
	return PositionUpdateMessage{
		Address: addr,
		Data: &PositionCacheData{
			Cache: cache,
		},
	}
}
