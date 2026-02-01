package hyperliquid

import "fmt"

func (w *WebsocketClient) SpotAssetCtxs(
	callback func(SpotAssetCtxs, error),
) (*Subscription, error) {
	payload := remoteSpotAssetCtxsSubscriptionPayload{
		Type: ChannelSpotAssetCtxs,
	}

	return w.subscribe(payload, func(msg any) {

		data, ok := msg.(SpotAssetCtxs)
		if !ok {
			callback(SpotAssetCtxs{}, fmt.Errorf("invalid message type"))
			return
		}

		callback(data, nil)
	})
}
