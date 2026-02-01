package examples

import (
	"context"
	"testing"
	"time"

	"github.com/sonirico/go-hyperliquid"
)

func TestCandleWebSocket(t *testing.T) {
	ws := hyperliquid.NewWebsocketClient("")

	if err := ws.Connect(context.Background()); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	done := make(chan bool)

	sub, err := ws.Candles(
		hyperliquid.CandlesSubscriptionParams{
			Coin:     "BTC",
			Interval: "1m",
		},
		func(candles []hyperliquid.Candle, err error) {
			if err != nil {
				t.Errorf("Error in candle callback: %v", err)
				return
			}

			for _, candle := range candles {
				t.Logf("Received candle: %+v", candle)
			}

			done <- true
		},
	)

	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	defer sub.Close()

	select {
	case <-done:
		// Test passed
	case <-time.After(10 * time.Second):
		t.Error("Timeout waiting for candle update")
	}
}
