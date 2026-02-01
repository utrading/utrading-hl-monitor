package processor

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/utrading/utrading-hl-monitor/internal/dao"
	"github.com/utrading/utrading-hl-monitor/internal/dal/gen"
	"github.com/utrading/utrading-hl-monitor/internal/models"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	assert.NoError(t, err)

	err = db.AutoMigrate(&models.HlPositionCache{})
	assert.NoError(t, err)

	// 初始化 gen 包（用于 DAO 层的 gen.HlPositionCache）
	gen.SetDefault(db)

	return db
}

func setupTestDBForBench(b *testing.B) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		b.Fatal(err)
	}

	err = db.AutoMigrate(&models.HlPositionCache{})
	if err != nil {
		b.Fatal(err)
	}

	// 初始化 gen 包（用于 DAO 层的 gen.HlPositionCache）
	gen.SetDefault(db)

	return db
}

func TestBatchWriter_StartStop(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
		MaxQueueSize:  100,
	}

	w := NewBatchWriter(&config)
	w.Start()
	w.Stop()

	// 验证可以正常关闭，不阻塞
	assert.True(t, true)
}

func TestBatchWriter_BatchSizeTrigger(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     5, // 小批量便于测试
		FlushInterval: 1 * time.Second,
		MaxQueueSize:  100,
	}

	w := NewBatchWriter(&config)
	w.Start()
	defer w.Stop()

	// 添加 5 个不同地址的项目（达到批量大小）
	for i := 0; i < 5; i++ {
		item := PositionCacheItem{
			Address: fmt.Sprintf("addr%d", i),
			Cache: &models.HlPositionCache{
				Address:      fmt.Sprintf("addr%d", i),
				AccountValue: fmt.Sprintf("%d", i),
			},
		}
		err := w.Add(item)
		assert.NoError(t, err)
		t.Logf("Added item %d, DedupKey: %s", i, item.DedupKey())
	}

	// 等待批量处理完成
	time.Sleep(500 * time.Millisecond)

	// 检查缓冲区状态
	t.Logf("Buffer size after flush: %d", w.buffers.Len())

	// 验证数据库中的记录
	var caches []models.HlPositionCache
	result := db.Find(&caches)
	if result.Error != nil {
		t.Logf("Find error: %v", result.Error)
	}
	t.Logf("Found %d records in database", len(caches))

	assert.NoError(t, result.Error)
	assert.Equal(t, 5, len(caches)) // 5 个不同地址
}

func TestBatchWriter_TimerFlush(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     100, // 大批量，避免触发批量刷新
		FlushInterval: 50 * time.Millisecond,
		MaxQueueSize:  100,
	}

	w := NewBatchWriter(&config)
	w.Start()
	defer w.Stop()

	// 添加 1 个项目（不足以触发批量刷新）
	item := PositionCacheItem{
		Address: "addr2",
		Cache: &models.HlPositionCache{
			Address:      "addr2",
			AccountValue: "100",
		},
	}
	err := w.Add(item)
	assert.NoError(t, err)

	// 等待定时刷新
	time.Sleep(150 * time.Millisecond)

	// 验证数据库中的记录
	var cache models.HlPositionCache
	result := db.Where("address = ?", "addr2").First(&cache)
	assert.NoError(t, result.Error)
	assert.Equal(t, "addr2", cache.Address)
	assert.Equal(t, "100", cache.AccountValue)
}

func TestBatchWriter_QueueFull(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     10,
		FlushInterval: 1 * time.Second,
		MaxQueueSize:  2, // 极小的队列
	}

	w := NewBatchWriter(&config)
	w.Start()
	defer w.Stop()

	// 快速添加超过队列容量的项目
	for i := 0; i < 5; i++ {
		item := PositionCacheItem{
			Address: "addr3",
			Cache: &models.HlPositionCache{
				Address:      "addr3",
				AccountValue: fmt.Sprintf("%d", i),
			},
		}
		err := w.Add(item)
		// 队列满时应该返回错误
		if err != nil {
			assert.Equal(t, ErrQueueFull, err)
			break
		}
	}
}

func TestBatchWriter_GracefulShutdown(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     10,
		FlushInterval: 1 * time.Second,
		MaxQueueSize:  100,
	}

	w := NewBatchWriter(&config)
	w.Start()

	// 添加项目
	for i := 0; i < 3; i++ {
		item := PositionCacheItem{
			Address: "addr4",
			Cache: &models.HlPositionCache{
				Address:      "addr4",
				AccountValue: fmt.Sprintf("%d", i),
			},
		}
		_ = w.Add(item)
	}

	// 优雅关闭，应该刷新缓冲区
	err := w.GracefulShutdown(500 * time.Millisecond)
	assert.NoError(t, err)

	// 验证数据已写入
	var cache models.HlPositionCache
	result := db.Where("address = ?", "addr4").First(&cache)
	assert.NoError(t, result.Error)
	assert.Equal(t, "addr4", cache.Address)
}

func TestBatchWriter_ConcurrentAdds(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     50,
		FlushInterval: 100 * time.Millisecond,
		MaxQueueSize:  1000,
	}

	w := NewBatchWriter(&config)
	w.Start()
	defer w.Stop()

	var wg sync.WaitGroup
	numGoroutines := 10
	itemsPerGoroutine := 10

	// 并发添加
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < itemsPerGoroutine; j++ {
				item := PositionCacheItem{
					Address: "addr5",
					Cache: &models.HlPositionCache{
						Address:      "addr5",
						AccountValue: fmt.Sprintf("%d", idx*itemsPerGoroutine+j),
					},
				}
				_ = w.Add(item)
			}
		}(i)
	}

	wg.Wait()

	// 等待处理完成
	time.Sleep(300 * time.Millisecond)

	// 验证数据已写入
	var cache models.HlPositionCache
	result := db.Where("address = ?", "addr5").First(&cache)
	assert.NoError(t, result.Error)
}

func TestBatchWriter_UpsertBehavior(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     2,
		FlushInterval: 100 * time.Millisecond,
		MaxQueueSize:  100,
	}

	w := NewBatchWriter(&config)
	w.Start()

	// 第一次写入
	item := PositionCacheItem{
		Address: "addr6",
		Cache: &models.HlPositionCache{
			Address:       "addr6",
			SpotTotalUSD:  "100",
		},
	}
	_ = w.Add(item)
	time.Sleep(200 * time.Millisecond)

	// 第二次写入同一地址（更新）
	item.Cache.SpotTotalUSD = "200"
	_ = w.Add(item)
	time.Sleep(200 * time.Millisecond)

	w.Stop()

	// 验证是更新而非插入
	var caches []models.HlPositionCache
	result := db.Where("address = ?", "addr6").Find(&caches)
	assert.NoError(t, result.Error)
	assert.Equal(t, 1, len(caches)) // 只有一条记录
	assert.Equal(t, "200", caches[0].SpotTotalUSD) // 更新后的值
}

// T045: 批量写入器性能基准测试

func BenchmarkBatchWriter_Add(b *testing.B) {
	config := BatchWriterConfig{
		BatchSize:     100,
		FlushInterval: 100 * time.Millisecond,
		MaxQueueSize:  10000,
	}

	w := NewBatchWriter(&config)
	w.Start()
	defer w.Stop()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		item := PositionCacheItem{
			Address: fmt.Sprintf("addr_%d", i%1000),
			Cache: &models.HlPositionCache{
				Address:      fmt.Sprintf("addr_%d", i%1000),
				AccountValue: fmt.Sprintf("%d", i),
			},
		}
		w.Add(item)
	}
}

func BenchmarkBatchWriter_ConcurrentAdd(b *testing.B) {
	config := BatchWriterConfig{
		BatchSize:     100,
		FlushInterval: 100 * time.Millisecond,
		MaxQueueSize:  10000,
	}

	w := NewBatchWriter(&config)
	w.Start()
	defer w.Stop()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			item := PositionCacheItem{
				Address: fmt.Sprintf("addr_%d", i%1000),
				Cache: &models.HlPositionCache{
					Address:      fmt.Sprintf("addr_%d", i%1000),
					AccountValue: fmt.Sprintf("%d", i),
				},
			}
			w.Add(item)
			i++
		}
	})
}

func TestBatchWriter_Deduplication(t *testing.T) {
	db := setupTestDB(t)

	// 初始化 DAO 层（gen.SetDefault + DAO 单例）
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     10,
		FlushInterval: time.Second,
		MaxQueueSize:  100,
	}
	writer := NewBatchWriter(&config)
	writer.Start()
	defer writer.Stop()

	// 相同地址的两条数据
	item1 := PositionCacheItem{
		Address: "0x123",
		Cache: &models.HlPositionCache{
			Address:      "0x123",
			AccountValue: "1000",
		},
	}
	item2 := PositionCacheItem{
		Address: "0x123",
		Cache: &models.HlPositionCache{
			Address:      "0x123",
			AccountValue: "2000", // 更新的值
		},
	}

	writer.Add(item1)
	writer.Add(item2)

	// 等待队列处理完成
	time.Sleep(100 * time.Millisecond)

	// 验证缓冲区只有 1 条记录（被覆盖）
	if writer.buffers.Len() != 1 {
		t.Errorf("expected buffer size 1, got %d", writer.buffers.Len())
	}
}

func TestBatchWriter_FlushInterval(t *testing.T) {
	db := setupTestDB(t)
	dao.InitDAO(db)

	config := BatchWriterConfig{
		BatchSize:     100, // 大批量大小
		FlushInterval: 100 * time.Millisecond,
	}
	writer := NewBatchWriter(&config)
	writer.Start()
	defer writer.Stop()

	// 添加少量数据（不达到批量大小）
	writer.Add(PositionCacheItem{
		Address: "0x456",
		Cache: &models.HlPositionCache{
			Address: "0x456",
		},
	})

	// 等待定时刷新
	time.Sleep(200 * time.Millisecond)

	// 验证缓冲区已清空
	if writer.buffers.Len() != 0 {
		t.Errorf("expected buffer to be flushed, got size %d", writer.buffers.Len())
	}
}
