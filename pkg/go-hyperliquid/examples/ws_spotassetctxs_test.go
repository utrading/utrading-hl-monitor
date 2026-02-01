package examples

import (
	"context"
	"testing"
	"time"

	"github.com/sonirico/go-hyperliquid"
)

func TestSpotAssetCtxsWebSocket(t *testing.T) {
	ws := hyperliquid.NewWebsocketClient("")

	if err := ws.Connect(context.Background()); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	done := make(chan bool)

	sub, err := ws.SpotAssetCtxs(
		func(data hyperliquid.SpotAssetCtxs, err error) {
			if err != nil {
				t.Errorf("Error in spotAssetCtxs callback: %v", err)
				return
			}

			t.Logf("Received spotAssetCtxs: %+v", data)

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
		t.Error("Timeout waiting for spotAssetCtxs update")
	}
}
