package examples

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sonirico/go-hyperliquid"
)

func TestClearinghouseStateWebSocket(t *testing.T) {
	ws := hyperliquid.NewWebsocketClient("")

	if err := ws.Connect(context.Background()); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	done := make(chan bool)
	messageCount := 0
	maxMessages := 5 // 接收5条消息后退出

	// 监听指定地址的 clearinghouseState
	testAddress := "0x399965e15d4e61ec3529cc98b7f7ebb93b733336"

	sub, err := ws.WebData2(
		hyperliquid.WebData2SubscriptionParams{
			User: testAddress,
		},
		func(webdata2 hyperliquid.WebData2, err error) {
			if err != nil {
				t.Errorf("Error in webData2 callback: %v", err)
				return
			}

			// 检查是否包含 clearinghouseState
			if webdata2.ClearinghouseState != nil {
				state := webdata2.ClearinghouseState

				t.Logf("=== ClearinghouseState Update ===")
				t.Logf("User: %s", webdata2.User)
				t.Logf("Time: %d", state.Time)

				// Margin Summary
				if state.MarginSummary != nil {
					t.Logf("AccountValue: %s", state.MarginSummary.AccountValue)
					t.Logf("TotalMarginUsed: %s", state.MarginSummary.TotalMarginUsed)
					t.Logf("TotalNtlPos: %s", state.MarginSummary.TotalNtlPos)
					t.Logf("TotalRawUsd: %s", state.MarginSummary.TotalRawUsd)
				}

				// Cross Margin Summary
				if state.CrossMarginSummary != nil {
					t.Logf("CrossAccountValue: %s", state.CrossMarginSummary.AccountValue)
					t.Logf("CrossTotalMarginUsed: %s", state.CrossMarginSummary.TotalMarginUsed)
				}

				t.Logf("Withdrawable: %s", state.Withdrawable)
				t.Logf("AssetPositions Count: %d", len(state.AssetPositions))

				// 打印持仓详情
				for _, pos := range state.AssetPositions {
					entryPx := "nil"
					if pos.Position.EntryPx != nil {
						entryPx = *pos.Position.EntryPx
					}
					t.Logf("  Position: %s | EntryPx: %s | Sz: %s | UnrealizedPnl: %s",
						pos.Position.Coin, entryPx, pos.Position.Szi, pos.Position.UnrealizedPnl)
				}

				messageCount++
				if messageCount >= maxMessages {
					done <- true
				}
			}
		},
	)

	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	defer sub.Close()

	t.Logf("Subscribed to clearinghouseState for address: %s", testAddress)
	t.Logf("Waiting for messages...")

	select {
	case <-done:
		t.Logf("Test completed. Received %d messages.", messageCount)
	case <-time.After(60 * time.Second):
		t.Logf("Timeout reached. Received %d messages.", messageCount)
	}
}

// ExampleClearinghouseState 演示如何使用 clearinghouseState 数据
func ExampleClearinghouseState() {
	ws := hyperliquid.NewWebsocketClient("")
	if err := ws.Connect(context.Background()); err != nil {
		fmt.Printf("Failed to connect: %v\n", err)
		return
	}
	defer ws.Close()

	testAddress := "0x399965e15d4e61ec3529cc98b7f7ebb93b733336"

	sub, err := ws.WebData2(
		hyperliquid.WebData2SubscriptionParams{
			User: testAddress,
		},
		func(webdata2 hyperliquid.WebData2, err error) {
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				return
			}

			if webdata2.ClearinghouseState != nil {
				state := webdata2.ClearinghouseState

				// 获取账户净值
				if state.MarginSummary != nil {
					fmt.Printf("Account Value: %s\n", state.MarginSummary.AccountValue)
				}

				// 获取可提取金额
				fmt.Printf("Withdrawable: %s\n", state.Withdrawable)

				// 遍历持仓
				for _, pos := range state.AssetPositions {
					entryPx := "nil"
					if pos.Position.EntryPx != nil {
						entryPx = *pos.Position.EntryPx
					}
					fmt.Printf("%s: %s @ %s\n",
						pos.Position.Coin,
						pos.Position.Szi,
						entryPx)
				}
			}
		},
	)

	if err != nil {
		fmt.Printf("Failed to subscribe: %v\n", err)
		return
	}
	defer sub.Close()

	// 保持连接
	time.Sleep(30 * time.Second)
}
