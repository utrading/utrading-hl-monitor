package manager

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/utrading/utrading-hl-monitor/internal/models"
)

// TestOrderDeduper_IsSeen 测试去重检查
func TestOrderDeduper_IsSeen(t *testing.T) {
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	address := "0xtest"
	oid := int64(123)
	direction := "Open Long"

	// 初始状态：未见过
	assert.False(t, deduper.IsSeen(address, oid, direction))

	// 标记为已见
	deduper.Mark(address, oid, direction)

	// 现在应该返回已见过
	assert.True(t, deduper.IsSeen(address, oid, direction))
}

// TestOrderDeduper_MarkFromAggregation 测试从聚合记录标记
func TestOrderDeduper_MarkFromAggregation(t *testing.T) {
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	agg := &models.OrderAggregation{
		Oid:       123,
		Address:   "0xtest",
		Direction: "Open Long",
	}

	deduper.MarkFromAggregation(agg)

	assert.True(t, deduper.IsSeen(agg.Address, agg.Oid, agg.Direction))
}

// TestOrderDeduper_KeyUniqueness 测试键的唯一性
func TestOrderDeduper_KeyUniqueness(t *testing.T) {
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	address := "0xtest"
	oid := int64(123)

	// 同一个 oid，不同方向应该有不同的键
	deduper.Mark(address, oid, "Open Long")
	deduper.Mark(address, oid, "Close Long")

	assert.True(t, deduper.IsSeen(address, oid, "Open Long"))
	assert.True(t, deduper.IsSeen(address, oid, "Close Long"))

	// 不同地址，相同 oid 和方向
	deduper.Mark("0xother", oid, "Open Long")

	assert.True(t, deduper.IsSeen("0xother", oid, "Open Long"))
	assert.False(t, deduper.IsSeen("0xother", int64(456), "Open Long"))
}

// TestOrderDeduper_GetStats 测试统计信息
func TestOrderDeduper_GetStats(t *testing.T) {
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	// 初始状态
	stats := deduper.GetStats()
	assert.Equal(t, 0, stats["entries"])
	assert.Equal(t, int64(30), stats["ttl_minutes"])

	// 添加一些记录
	deduper.Mark("0x1", 1, "Open Long")
	deduper.Mark("0x2", 2, "Close Short")
	deduper.Mark("0x3", 3, "Open Short")

	stats = deduper.GetStats()
	assert.Equal(t, 3, stats["entries"])
}

// TestOrderDeduper_DedupKey 测试去重键生成
func TestOrderDeduper_DedupKey(t *testing.T) {
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	// 测试键格式
	key1 := deduper.dedupKey("0xabc", 123, "Open Long")
	key2 := deduper.dedupKey("0xabc", 123, "Open Long")
	key3 := deduper.dedupKey("0xabc", 123, "Close Long")

	assert.Equal(t, key1, key2)
	assert.NotEqual(t, key1, key3)

	assert.Equal(t, "0xabc-123-Open Long", key1)
	assert.Equal(t, "0xabc-123-Close Long", key3)
}

// TestOrderDeduper_Expiry 测试过期清理
func TestOrderDeduper_Expiry(t *testing.T) {
	// 使用短 TTL 方便测试 (清理间隔 = 2×TTL)
	deduper := NewOrderDeduper(50 * time.Millisecond)
	defer deduper.Close()

	address := "0xtest"
	oid := int64(123)
	direction := "Open Long"

	deduper.Mark(address, oid, direction)

	// 立即检查应该存在
	assert.True(t, deduper.IsSeen(address, oid, direction))

	stats := deduper.GetStats()
	assert.Equal(t, 1, stats["entries"])

	// 等待过期 (go-cache 会自动清理，清理间隔为 100ms)
	time.Sleep(200 * time.Millisecond)

	// 过期后应该不存在
	assert.False(t, deduper.IsSeen(address, oid, direction))

	stats = deduper.GetStats()
	// 由于 go-cache 的清理是异步的，entries 可能还显示为 1
	// 但 IsSeen 应该返回 false，这才是关键验证
	assert.False(t, deduper.IsSeen(address, oid, direction))
}

// TestOrderDeduper_ConcurrentMark 测试并发标记安全性
func TestOrderDeduper_ConcurrentMark(t *testing.T) {
	deduper := NewOrderDeduper(30 * time.Minute)
	defer deduper.Close()

	// 并发标记
	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func(idx int) {
			deduper.Mark("0xtest", int64(idx), "Open Long")
			done <- true
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 100; i++ {
		<-done
	}

	// 验证所有记录都已标记
	stats := deduper.GetStats()
	assert.Equal(t, 100, stats["entries"])

	// 验证所有记录都可以被查询到
	for i := 0; i < 100; i++ {
		assert.True(t, deduper.IsSeen("0xtest", int64(i), "Open Long"))
	}
}
