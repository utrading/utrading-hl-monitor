package cache

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPositionBalanceCache_SetAndGet(t *testing.T) {
	cache := NewPositionBalanceCache()

	// Set
	cache.Set("0x123", 1000.50, 5000.25, nil, nil)

	// GetSpotTotal
	spotTotal, ok := cache.GetSpotTotal("0x123")
	assert.True(t, ok)
	assert.Equal(t, 1000.50, spotTotal)

	// GetAccountValue
	accountValue, ok := cache.GetAccountValue("0x123")
	assert.True(t, ok)
	assert.Equal(t, 5000.25, accountValue)
}

func TestPositionBalanceCache_NotFound(t *testing.T) {
	cache := NewPositionBalanceCache()

	_, ok := cache.GetSpotTotal("0x999")
	assert.False(t, ok)

	_, ok = cache.GetAccountValue("0x999")
	assert.False(t, ok)
}

func TestPositionBalanceCache_Delete(t *testing.T) {
	cache := NewPositionBalanceCache()
	cache.Set("0x123", 1000.0, 5000.0, nil, nil)

	cache.Delete("0x123")

	_, ok := cache.GetSpotTotal("0x123")
	assert.False(t, ok)

	_, ok = cache.GetAccountValue("0x123")
	assert.False(t, ok)
}

func TestPositionBalanceCache_Concurrent(t *testing.T) {
	cache := NewPositionBalanceCache()
	var wg sync.WaitGroup

	// 并发写入
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			address := fmt.Sprintf("0x%d", idx)
			cache.Set(address, float64(idx), float64(idx*10), nil, nil)
		}(i)
	}

	wg.Wait()

	// 验证
	for i := 0; i < 100; i++ {
		address := fmt.Sprintf("0x%d", i)
		spotTotal, ok := cache.GetSpotTotal(address)
		assert.True(t, ok)
		assert.Equal(t, float64(i), spotTotal)

		accountValue, ok := cache.GetAccountValue(address)
		assert.True(t, ok)
		assert.Equal(t, float64(i*10), accountValue)
	}
}

func TestPositionBalanceCache_Overwrite(t *testing.T) {
	cache := NewPositionBalanceCache()

	// 第一次写入
	cache.Set("0x123", 1000.0, 5000.0, nil, nil)

	spotTotal, _ := cache.GetSpotTotal("0x123")
	assert.Equal(t, 1000.0, spotTotal)

	// 覆盖写入
	cache.Set("0x123", 2000.0, 8000.0, nil, nil)

	spotTotal, _ = cache.GetSpotTotal("0x123")
	assert.Equal(t, 2000.0, spotTotal)

	accountValue, _ := cache.GetAccountValue("0x123")
	assert.Equal(t, 8000.0, accountValue)
}

func TestPositionBalanceCache_ConcurrentReadWrite(t *testing.T) {
	cache := NewPositionBalanceCache()
	cache.Set("0x123", 1000.0, 5000.0, nil, nil)

	var wg sync.WaitGroup
	iterations := 1000

	// 并发读取
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.GetSpotTotal("0x123")
			cache.GetAccountValue("0x123")
		}()
	}

	// 并发写入
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cache.Set("0x123", float64(1000+idx), float64(5000+idx), nil, nil)
		}(i)
	}

	wg.Wait()

	// 最终值应该是某次写入的值
	spotTotal, ok := cache.GetSpotTotal("0x123")
	assert.True(t, ok)
	assert.GreaterOrEqual(t, spotTotal, 1000.0)
	assert.Less(t, spotTotal, 1100.0)
}
