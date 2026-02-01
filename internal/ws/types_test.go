package ws

import (
	"testing"
)

func TestSubscriptionKey(t *testing.T) {
	tests := []struct {
		name     string
		sub      Subscription
		expected string
	}{
		{
			name: "user subscription",
			sub: Subscription{
				Channel: ChannelWebData2,
				User:    "0x123",
			},
			expected: "webData2:0x123",
		},
		{
			name: "coin subscription",
			sub: Subscription{
				Channel: ChannelL2Book,
				Coin:    "BTC",
			},
			expected: "l2Book:BTC",
		},
		{
			name: "general subscription",
			sub: Subscription{
				Channel: ChannelAllMids,
			},
			expected: "allMids",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sub.Key(); got != tt.expected {
				t.Errorf("Key() = %v, want %v", got, tt.expected)
			}
		})
	}
}
