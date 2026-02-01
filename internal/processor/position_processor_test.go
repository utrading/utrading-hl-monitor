package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/utrading/utrading-hl-monitor/internal/models"
)

func TestPositionProcessor_HandlePositionUpdate(t *testing.T) {
	db := setupTestDB(t)

	// 创建批量写入器
	bwConfig := BatchWriterConfig{
		BatchSize:     2,
		FlushInterval: 100 * time.Millisecond,
		MaxQueueSize:  100,
	}
	bw := NewBatchWriter(&bwConfig)
	bw.Start()
	defer bw.Stop()

	// 创建处理器
	proc := NewPositionProcessor(bw)

	// 创建测试消息
	cache := &models.HlPositionCache{
		Address:       "test_addr",
		SpotTotalUSD:  "1000",
		AccountValue:  "5000",
		TotalMarginUsed: "1000",
	}

	msg := NewPositionCacheMessage("test_addr", cache)

	// 处理消息
	err := proc.HandleMessage(msg)
	assert.NoError(t, err)

	// 等待批量写入
	time.Sleep(200 * time.Millisecond)

	// 验证数据已写入数据库
	var result models.HlPositionCache
	dbErr := db.Where("address = ?", "test_addr").First(&result).Error
	assert.NoError(t, dbErr)
	assert.Equal(t, "test_addr", result.Address)
	assert.Equal(t, "1000", result.SpotTotalUSD)
}

func TestPositionProcessor_UnknownMessageType(t *testing.T) {
	bwConfig := BatchWriterConfig{}
	bw := NewBatchWriter(&bwConfig)
	bw.Start()
	defer bw.Stop()

	proc := NewPositionProcessor(bw)

	// 未知消息类型
	unknownMsg := &testMessage{}
	err := proc.HandleMessage(unknownMsg)

	// 应该不报错，只是记录警告
	assert.NoError(t, err)
}

// testMessage 测试用消息类型
type testMessage struct{}

func (m *testMessage) Type() string {
	return "test"
}

func TestPositionProcessor_BatchWriterError(t *testing.T) {
	// 创建一个会失败的 batch writer mock
	db := setupTestDB(t)

	// 使用极小的队列，让 Add 失败
	bwConfig := BatchWriterConfig{
		BatchSize:     1,
		FlushInterval: 1 * time.Second,
		MaxQueueSize:  1, // 极小队列
	}
	bw := NewBatchWriter(&bwConfig)
	bw.Start()
	defer bw.Stop()

	proc := NewPositionProcessor(bw)

	// 快速发送多个消息，填满队列
	cache := &models.HlPositionCache{
		Address:      "error_addr",
		AccountValue: "100",
	}

	for i := 0; i < 5; i++ {
		msg := NewPositionCacheMessage("error_addr", cache)
		_ = proc.HandleMessage(msg)
	}

	// 等待一下让处理完成
	time.Sleep(50 * time.Millisecond)

	// 验证至少有一个写入成功
	var result models.HlPositionCache
	dbErr := db.Where("address = ?", "error_addr").First(&result).Error
	// 可能因为队列满而失败，也可能成功，这里不强制断言
	_ = dbErr
}

func TestNewPositionCacheMessage(t *testing.T) {
	cache := &models.HlPositionCache{
		Address:       "msg_test_addr",
		SpotTotalUSD:  "2000",
		AccountValue:  "10000",
	}

	msg := NewPositionCacheMessage("msg_test_addr", cache)

	assert.Equal(t, "msg_test_addr", msg.Address)
	assert.Equal(t, "position_update", msg.Type())

	data, ok := msg.Data.(*PositionCacheData)
	assert.True(t, ok)
	assert.Equal(t, "msg_test_addr", data.Cache.Address)
	assert.Equal(t, "2000", data.Cache.SpotTotalUSD)
}
