package manager

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/utrading/utrading-hl-monitor/internal/models"
)

// mockOrderAggregationDAO 模拟 DAO
type mockOrderAggregationDAO struct {
	orders []*models.OrderAggregation
}

func (m *mockOrderAggregationDAO) GetSentOrdersSince(since time.Time) ([]*models.OrderAggregation, error) {
	return m.orders, nil
}

// TestOrderDeduper_LoadFromDB 集成测试：从数据库加载
func TestOrderDeduper_LoadFromDB(t *testing.T) {
	// 准备测试数据
	testOrders := []*models.OrderAggregation{
		{Oid: 1, Address: "0xabc1", Direction: "Open Long"},
		{Oid: 2, Address: "0xabc2", Direction: "Open Short"},
		{Oid: 3, Address: "0xabc3", Direction: "Close Long"},
	}

	mockDAO := &mockOrderAggregationDAO{orders: testOrders}

	// 创建去重器并加载数据
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	err := deduper.LoadFromDB(mockDAO)
	assert.NoError(t, err)

	// 验证所有订单都已加载
	assert.True(t, deduper.IsSeen("0xabc1", 1, "Open Long"))
	assert.True(t, deduper.IsSeen("0xabc2", 2, "Open Short"))
	assert.True(t, deduper.IsSeen("0xabc3", 3, "Close Long"))

	stats := deduper.GetStats()
	assert.Equal(t, 3, stats["entries"])
}

// TestOrderDeduper_ConcurrentStress 并发压力测试
func TestOrderDeduper_ConcurrentStress(t *testing.T) {
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	// 模拟 100 个地址同时写入
	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func(id int) {
			addr := "0xconcurrent"
			oid := int64(id)
			dir := "Open Long"
			deduper.Mark(addr, oid, dir)
			deduper.IsSeen(addr, oid, dir)
			done <- true
		}(i)
	}

	// 等待所有协程完成
	for i := 0; i < 100; i++ {
		<-done
	}

	// 验证数据一致性
	stats := deduper.GetStats()
	assert.Equal(t, 100, stats["entries"])

	// 随机验证几个
	assert.True(t, deduper.IsSeen("0xconcurrent", 0, "Open Long"))
	assert.True(t, deduper.IsSeen("0xconcurrent", 50, "Open Long"))
	assert.True(t, deduper.IsSeen("0xconcurrent", 99, "Open Long"))
}
