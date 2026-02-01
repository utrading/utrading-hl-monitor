package symbol

import (
	"testing"
	"time"

	"github.com/utrading/utrading-hl-monitor/internal/cache"
)

func TestLoader_NewLoader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	symbolCache := cache.NewSymbolCache()

	loader, err := NewLoader(symbolCache, "https://api.hyperliquid.xyz/info")
	if err != nil {
		t.Skipf("Skip test due to API error: %v", err)
	}
	defer loader.Close()

	stats := symbolCache.Stats()
	spotCount := stats["spot_name_to_symbol_count"].(int64)
	perpCount := stats["perp_name_to_symbol_count"].(int64)

	if spotCount == 0 {
		t.Error("spot cache should not be empty")
	}
	if perpCount == 0 {
		t.Error("perp cache should not be empty")
	}

	t.Logf("Loaded %d spot symbols, %d perp symbols", spotCount, perpCount)
}

func TestLoader_ReloadLoop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	symbolCache := cache.NewSymbolCache()

	loader, err := NewLoader(symbolCache, "https://api.hyperliquid.xyz/info")
	if err != nil {
		t.Skipf("Skip test due to API error: %v", err)
	}
	defer loader.Close()

	loader.Start()
	time.Sleep(100 * time.Millisecond)

	t.Log("reload loop started successfully")
}

func TestLoader_SpotMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	symbolCache := cache.NewSymbolCache()

	loader, err := NewLoader(symbolCache, "https://api.hyperliquid.xyz/info")
	if err != nil {
		t.Skipf("Skip test due to API error: %v", err)
	}
	defer loader.Close()

	// 测试常见的现货交易对
	testCases := []struct {
		coin string
	}{
		{"ETH"},
		{"BTC"},
		{"SOL"},
	}

	for _, tc := range testCases {
		symbol, ok := symbolCache.GetSpotSymbol(tc.coin)
		if !ok {
			t.Errorf("coin %s not found in cache", tc.coin)
		} else {
			t.Logf("coin %s -> symbol %s", tc.coin, symbol)
		}
	}
}

func TestLoader_PerpMapping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	symbolCache := cache.NewSymbolCache()

	loader, err := NewLoader(symbolCache, "https://api.hyperliquid.xyz/info")
	if err != nil {
		t.Skipf("Skip test due to API error: %v", err)
	}
	defer loader.Close()

	// 测试常见的合约交易对
	testCases := []struct {
		coin string
	}{
		{"BTC"},
		{"ETH"},
		{"SOL"},
	}

	for _, tc := range testCases {
		symbol, ok := symbolCache.GetPerpSymbol(tc.coin)
		if !ok {
			t.Errorf("coin %s not found in cache", tc.coin)
		} else {
			t.Logf("coin %s -> symbol %s", tc.coin, symbol)
		}
	}
}
