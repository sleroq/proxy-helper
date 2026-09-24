// Package clash speaks the local Clash-compatible selection API used by
// sing-box and mihomo. It does not assume their config formats are alike.
package clash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client controls live selection through an unauthenticated loopback API.
type Client struct {
	URL    string
	Client *http.Client
}

type Selector struct {
	Now string   `json:"now"`
	All []string `json:"all"`
}

func (c Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, input)
	if err != nil {
		return nil, fmt.Errorf("invalid Clash API URL")
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c Client) request(ctx context.Context, method, path string, body any, result any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}

	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("clash API unavailable; is the client running?")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("clash API returned HTTP %d", response.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			return fmt.Errorf("invalid Clash API response")
		}
	}
	return nil
}

func (c Client) Selector(ctx context.Context) (Selector, error) {
	var result Selector
	err := c.request(ctx, http.MethodGet, "/proxies/proxy", nil, &result)
	return result, err
}

func (c Client) Use(ctx context.Context, tag string) error {
	return c.request(ctx, http.MethodPut, "/proxies/proxy", map[string]string{"name": tag}, nil)
}

func (c Client) Test(ctx context.Context, group, testURL string) (map[string]int, error) {
	query := url.Values{"url": {testURL}, "timeout": {"10000"}}
	var result map[string]int
	err := c.request(ctx, http.MethodGet, "/group/"+url.PathEscape(group)+"/delay?"+query.Encode(), nil, &result)
	return result, err
}
