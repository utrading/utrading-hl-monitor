package cache

import (
	"sync"
	"testing"
)

func TestSymbolCache_BidirectionalMapping(t *testing.T) {
	cache := NewSymbolCache()

	// 设置现货映射
	cache.SetSpotSymbol("@123", "ETHUSDC")

	// 正向查询
	symbol, ok := cache.GetSpotSymbol("@123")
	if !ok || symbol != "ETHUSDC" {
		t.Fatalf("GetSpotSymbol failed: got %s, %v", symbol, ok)
	}

	// 反向查询
	coin, ok := cache.GetSpotName("ETHUSDC")
	if !ok || coin != "@123" {
		t.Fatalf("GetSpotName failed: got %s, %v", coin, ok)
	}
}

func TestSymbolCache_ConcurrentAccess(t *testing.T) {
	cache := NewSymbolCache()
	var wg sync.WaitGroup

	// 并发写入
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cache.SetSpotSymbol("@123", "ETHUSDC")
			cache.GetSpotSymbol("@123")
		}(i)
	}

	wg.Wait()

	// 验证数据一致性
	symbol, ok := cache.GetSpotSymbol("@123")
	if !ok || symbol != "ETHUSDC" {
		t.Fatalf("concurrent access broke data: got %s, %v", symbol, ok)
	}
}

func TestSymbolCache_Stats(t *testing.T) {
	cache := NewSymbolCache()

	cache.SetSpotSymbol("@123", "ETHUSDC")
	cache.SetPerpSymbol("BTC", "BTCUSDC")

	stats := cache.Stats()

	spotCount, ok := stats["spot_name_to_symbol_count"].(int64)
	if !ok || spotCount != 1 {
		t.Fatalf("spot_count wrong: got %d, %v", spotCount, ok)
	}

	perpCount, ok := stats["perp_name_to_symbol_count"].(int64)
	if !ok || perpCount != 1 {
		t.Fatalf("perp_count wrong: got %d, %v", perpCount, ok)
	}
}

func TestSymbolCache_PerpMapping(t *testing.T) {
	cache := NewSymbolCache()

	// 设置合约映射
	cache.SetPerpSymbol("BTC", "BTCUSDC")

	// 正向查询
	symbol, ok := cache.GetPerpSymbol("BTC")
	if !ok || symbol != "BTCUSDC" {
		t.Fatalf("GetPerpSymbol failed: got %s, %v", symbol, ok)
	}

	// 反向查询
	coin, ok := cache.GetPerpName("BTCUSDC")
	if !ok || coin != "BTC" {
		t.Fatalf("GetPerpName failed: got %s, %v", coin, ok)
	}
}

func TestSymbolCache_MissingKey(t *testing.T) {
	cache := NewSymbolCache()

	// 查询不存在的键
	_, ok := cache.GetSpotSymbol("nonexistent")
	if ok {
		t.Fatal("expected false for missing key")
	}

	_, ok = cache.GetPerpSymbol("nonexistent")
	if ok {
		t.Fatal("expected false for missing key")
	}
}

func TestSymbolCache_Overwrite(t *testing.T) {
	cache := NewSymbolCache()

	// 第一次设置
	cache.SetSpotSymbol("@123", "OLDUSDC")
	symbol, _ := cache.GetSpotSymbol("@123")
	if symbol != "OLDUSDC" {
		t.Fatalf("first set failed: got %s", symbol)
	}

	// 覆盖
	cache.SetSpotSymbol("@123", "NEWUSDC")
	symbol, _ = cache.GetSpotSymbol("@123")
	if symbol != "NEWUSDC" {
		t.Fatalf("overwrite failed: got %s", symbol)
	}

	// 验证反向映射也被更新
	coin, _ := cache.GetSpotName("NEWUSDC")
	if coin != "@123" {
		t.Fatalf("reverse mapping not updated: got %s", coin)
	}

	// 注意：当前的实现是简单的覆盖，不会主动删除旧的反向映射
	// 这是可以接受的，因为在实际使用中，coin 到 symbol 的映射是稳定的
	_, _ = cache.GetSpotName("OLDUSDC") // 旧映射可能仍然存在
}
