package subscription

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

const responseLimit = 8 << 20

var errResponseTooLarge = errors.New("subscription response too large")

// fetchResponse returns a checked response whose body the caller must close.
func fetchResponse(ctx context.Context, source Source, client *http.Client, hwid string) (*http.Response, error) {
	address, err := sourceAddress(source)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("source %s: invalid request", source.ID)
	}
	if hwid != "" {
		if !regexp.MustCompile(`^[a-zA-Z0-9=-]{10,64}$`).MatchString(hwid) {
			return nil, fmt.Errorf("source %s: invalid hwid; expected 10-64 letters, digits, = or -", source.ID)
		}
		request.Header.Set("X-Hwid", hwid)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("source %s: fetch failed", source.ID)
	}
	if err := checkResponse(response, source); err != nil {
		_ = response.Body.Close()
		return nil, err
	}
	return response, nil
}

func checkResponse(response *http.Response, source Source) error {
	if response.Header.Get("X-Hwid-Not-Supported") == "true" {
		return fmt.Errorf("source %s: provider requires a device ID; configure hwid", source.ID)
	}
	if response.Header.Get("X-Hwid-Max-Devices-Reached") == "true" || response.Header.Get("X-Hwid-Limit") == "true" {
		return fmt.Errorf("source %s: provider device limit reached; "+
			"reuse an existing device ID or free a device slot", source.ID)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("source %s: HTTP %d", source.ID, response.StatusCode)
	}
	if response.ContentLength > responseLimit {
		return fmt.Errorf("source %s: %w", source.ID, errResponseTooLarge)
	}
	return nil
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
