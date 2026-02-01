// Package hyperliquid provides a Go client library for the Hyperliquid exchange API.
// It includes support for both REST API and WebSocket connections, allowing users to
// access market data, manage orders, and handle user account operations.
package hyperliquid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rs/zerolog"
)

const (
	MainnetAPIURL = "https://api.hyperliquid.xyz"
	TestnetAPIURL = "https://api.hyperliquid-testnet.xyz"
	LocalAPIURL   = "http://localhost:3001"

	MainnetWsURL = "wss://api.hyperliquid.xyz/ws"
	TestnetWsURL = "wss://api.hyperliquid-testnet.xyz/ws"

	// httpErrorStatusCode is the minimum status code considered an error
	httpErrorStatusCode = 400
)

type Client struct {
	logger     *zerolog.Logger
	debug      bool
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string, opts ...ClientOpt) *Client {
	if baseURL == "" {
		baseURL = MainnetAPIURL
	}

	cli := &Client{
		baseURL:    baseURL,
		httpClient: new(http.Client),
	}

	for _, opt := range opts {
		opt.Apply(cli)
	}

	return cli
}

func (c *Client) post(ctx context.Context, path string, payload any) ([]byte, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		url,
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-ex", "hyperliquid")

	if c.debug {
		c.logger.Debug().Msgf("HTTP request: method:POST, url:%s, body:%s", url, string(jsonData))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body := make([]byte, 0)
	if resp.Body != nil {
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}
	}

	if c.debug {
		c.logger.Debug().Msgf("HTTP response: method:POST, status:%s, body:%s", resp.Status, string(body))
	}

	if resp.StatusCode >= httpErrorStatusCode {
		if len(string(body)) == 0 {
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
		}

		if !json.Valid(body) {
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
		}

		var apiErr APIError
		if err := json.Unmarshal(body, &apiErr); err != nil {
			return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
		}

		return nil, apiErr
	}

	return body, nil
}
