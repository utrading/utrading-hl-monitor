package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDedupCache_IsSeen(t *testing.T) {
	cache := NewDedupCache(30 * time.Second)

	// 测试首次查询
	assert.False(t, cache.IsSeen("addr1", 123, "open"))

	// 测试标记
	cache.Mark("addr1", 123, "open")
	assert.True(t, cache.IsSeen("addr1", 123, "open"))

	// 测试不同方向
	assert.False(t, cache.IsSeen("addr1", 123, "close"))

	// 测试不同地址
	assert.False(t, cache.IsSeen("addr2", 123, "open"))

	// 测试不同 oid
	assert.False(t, cache.IsSeen("addr1", 456, "open"))
}

func TestDedupCache_TTL(t *testing.T) {
	cache := NewDedupCache(100 * time.Millisecond)

	cache.Mark("addr1", 123, "open")
	assert.True(t, cache.IsSeen("addr1", 123, "open"))

	// 等待过期
	time.Sleep(150 * time.Millisecond)
	assert.False(t, cache.IsSeen("addr1", 123, "open"))
}

func TestDedupCache_Concurrent(t *testing.T) {
	cache := NewDedupCache(30 * time.Second)
	done := make(chan bool)

	// 10 个协程同时读写
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				addr := "addr_concurrent"
				oid := int64(id*1000 + j)
				dir := "open"
				cache.Mark(addr, oid, dir)
				cache.IsSeen(addr, oid, dir)
			}
			done <- true
		}(i)
	}

	// 等待所有协程完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 验证数据正确
	assert.True(t, cache.IsSeen("addr_concurrent", 5000, "open"))
}

func TestDedupCache_dedupKey(t *testing.T) {
	cache := NewDedupCache(30 * time.Second)

	// 测试 key 格式
	cache.Mark("0xABC", 123, "open")
	assert.True(t, cache.IsSeen("0xABC", 123, "open"))

	// 不同参数生成不同 key
	cache.Mark("0xABC", 123, "close")
	assert.True(t, cache.IsSeen("0xABC", 123, "close"))

	cache.Mark("0xABC", 456, "open")
	assert.True(t, cache.IsSeen("0xABC", 456, "open"))
}

func TestDedupCache_Stats(t *testing.T) {
	cache := NewDedupCache(5 * time.Minute)

	cache.Mark("addr1", 1, "open")
	cache.Mark("addr2", 2, "close")
	cache.Mark("addr3", 3, "open")

	stats := cache.Stats()
	assert.Equal(t, 3, stats["item_count"])
	assert.Equal(t, 5.0, stats["ttl_minutes"])
}

// T044: 性能基准测试

func BenchmarkDedupCache_Mark(b *testing.B) {
	cache := NewDedupCache(30 * time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Mark("addr_bench", int64(i), "open")
	}
}

func BenchmarkDedupCache_IsSeen(b *testing.B) {
	cache := NewDedupCache(30 * time.Minute)
	// 预填充
	for i := 0; i < 10000; i++ {
		cache.Mark("addr_bench", int64(i), "open")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.IsSeen("addr_bench", int64(i%10000), "open")
	}
}

func BenchmarkDedupCache_Concurrent(b *testing.B) {
	cache := NewDedupCache(30 * time.Minute)
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			cache.Mark("addr_parallel", int64(i), "open")
			cache.IsSeen("addr_parallel", int64(i), "open")
			i++
		}
	})
}
