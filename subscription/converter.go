package subscription

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sleroq/sb/internal/files"
	"github.com/sleroq/sb/singbox"
)

// Converter adapts sing-box-sub's CLI. Child output and HTTP errors are not
// forwarded: either may contain subscription credentials.
type Converter struct {
	Binary string
	Client *http.Client
}

func (c Converter) Fetch(ctx context.Context, source Source) ([]singbox.Outbound, error) {
	address := source.URL
	if source.URLFile != "" {
		data, err := os.ReadFile(source.URLFile)
		if err != nil {
			return nil, fmt.Errorf("source %s: cannot read URL file", source.ID)
		}
		address = strings.TrimRight(string(data), "\r\n")
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || strings.ContainsAny(address, "\r\n") {
		return nil, fmt.Errorf("source %s: invalid HTTP subscription URL", source.ID)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("source %s: invalid request", source.ID)
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("source %s: fetch failed", source.ID)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source %s: HTTP %d", source.ID, response.StatusCode)
	}
	if response.ContentLength > 8<<20 {
		return nil, fmt.Errorf("source %s: subscription response too large", source.ID)
	}
	return c.Parse(ctx, response.Body, source)
}

// Parse converts a subscription body from any reader into native outbounds.
// It does not fetch, read stores, or apply automatic-selection policy. The
// source supplies only diagnostic identity and converter filter options.
func (c Converter) Parse(ctx context.Context, body io.Reader, source Source) ([]singbox.Outbound, error) {
	dir, err := os.MkdirTemp("", "sb-convert-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	input := filepath.Join(dir, "subscription")
	output := filepath.Join(dir, "nodes.json")
	file, err := os.OpenFile(input, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	size, copyErr := io.Copy(file, io.LimitReader(body, (8<<20)+1))
	closeErr := file.Close()
	if size > 8<<20 {
		return nil, fmt.Errorf("source %s: subscription response too large", source.ID)
	}
	if copyErr != nil || closeErr != nil {
		return nil, fmt.Errorf("source %s: cannot save response", source.ID)
	}
	protocols := source.ExcludeProtocols
	if protocols == "" {
		protocols = "ssr"
	}
	command := exec.CommandContext(ctx, c.Binary, input, "--only-nodes", "--prefix", source.Prefix, "--exclude-protocol", protocols, "--exclude-node-name", source.ExcludeNodeNames, "--out", output)
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("source %s: converter failed", source.ID)
	}
	var nodes []singbox.Outbound
	if err := files.Read(output, &nodes); err != nil {
		return nil, fmt.Errorf("source %s: invalid converter output", source.ID)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("source %s: no supported outbounds", source.ID)
	}
	for _, node := range nodes {
		if _, ok := node["server"]; ok {
			server := node.String("server")
			if server == "" || server == "0.0.0.0" || server == "::" {
				return nil, fmt.Errorf("source %s: placeholder server; check subscription", source.ID)
			}
		}
	}
	return nodes, nil
}
