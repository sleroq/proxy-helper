package singbox

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

// Clash is a minimal client for sing-box's optional Clash-compatible API.
type Clash struct {
	URL    string
	Client *http.Client
}
type Selector struct {
	Now string   `json:"now"`
	All []string `json:"all"`
}

func (c Clash) request(ctx context.Context, method, path string, body any, result any) error {
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		input = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, input)
	if err != nil {
		return fmt.Errorf("invalid Clash API URL")
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Clash API unavailable; is sing-box running?")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Clash API returned HTTP %d", response.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			return fmt.Errorf("invalid Clash API response")
		}
	}
	return nil
}

func (c Clash) Selector(ctx context.Context) (Selector, error) {
	var result Selector
	err := c.request(ctx, http.MethodGet, "/proxies/proxy", nil, &result)
	return result, err
}

func (c Clash) Use(ctx context.Context, tag string) error {
	return c.request(ctx, http.MethodPut, "/proxies/proxy", map[string]string{"name": tag}, nil)
}

func (c Clash) Test(ctx context.Context, group, testURL string) (map[string]int, error) {
	query := url.Values{"url": {testURL}, "timeout": {"10000"}}
	var result map[string]int
	err := c.request(ctx, http.MethodGet, "/group/"+url.PathEscape(group)+"/delay?"+query.Encode(), nil, &result)
	return result, err
}
