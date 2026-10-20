package subscription

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const responseLimit = 8 << 20

var errResponseTooLarge = errors.New("subscription response too large")

// fetchResponse returns a checked response whose body the caller must close.
func fetchResponse(ctx context.Context, source Source, client *http.Client) (*http.Response, error) {
	address, err := sourceAddress(source)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("source %s: invalid request", source.ID)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("source %s: fetch failed", source.ID)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return nil, fmt.Errorf("source %s: HTTP %d", source.ID, response.StatusCode)
	}
	if response.ContentLength > responseLimit {
		_ = response.Body.Close()
		return nil, fmt.Errorf("source %s: %w", source.ID, errResponseTooLarge)
	}
	return response, nil
}

func sourceAddress(source Source) (string, error) {
	address := source.URL
	if source.URLFile != "" {
		data, err := os.ReadFile(source.URLFile)
		if err != nil {
			return "", fmt.Errorf("source %s: cannot read URL file", source.ID)
		}
		address = strings.TrimRight(string(data), "\r\n")
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || strings.ContainsAny(address, "\r\n") {
		return "", fmt.Errorf("source %s: invalid HTTP subscription URL", source.ID)
	}
	return address, nil
}
