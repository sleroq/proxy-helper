package subscription

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sleroq/ph/internal/files"
	"github.com/sleroq/ph/proxy"
)

// Converter adapts sing-box-sub's CLI. Child output and HTTP errors are not
// forwarded: either may contain subscription credentials.
type Converter struct {
	Binary string
	Client *http.Client
}

func (c Converter) Fetch(ctx context.Context, source Source) ([]proxy.Node, error) {
	response, err := fetchResponse(ctx, source, c.Client)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	return c.Parse(ctx, response.Body, source)
}

// Parse converts a subscription body from any reader into native outbounds.
// It does not fetch, read stores, or apply automatic-selection policy. The
// source supplies only diagnostic identity and converter filter options.
func (c Converter) Parse(ctx context.Context, body io.Reader, source Source) ([]proxy.Node, error) {
	dir, err := os.MkdirTemp("", "ph-convert-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	input := filepath.Join(dir, "subscription")
	output := filepath.Join(dir, "nodes.json")
	if err := saveResponse(input, body, source.ID); err != nil {
		return nil, err
	}
	protocols := source.ExcludeProtocols
	if protocols == "" {
		protocols = "ssr"
	}
	command := exec.CommandContext(ctx, c.Binary, input,
		"--only-nodes", "--prefix", source.Prefix,
		"--exclude-protocol", protocols,
		"--exclude-node-name", source.ExcludeNodeNames,
		"--out", output)
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("source %s: converter failed", source.ID)
	}
	var nodes []proxy.Node
	if err := files.Read(output, &nodes); err != nil {
		return nil, fmt.Errorf("source %s: invalid converter output", source.ID)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("source %s: no supported outbounds", source.ID)
	}
	if err := validateServers(nodes, source.ID); err != nil {
		return nil, err
	}
	return nodes, nil
}

func validateServers(nodes []proxy.Node, id string) error {
	for _, node := range nodes {
		if _, ok := node["server"]; ok {
			server := node.String("server")
			if server == "" || server == "0.0.0.0" || server == "::" {
				return fmt.Errorf("source %s: placeholder server; check subscription", id)
			}
		}
	}
	return nil
}

func saveResponse(input string, body io.Reader, id string) error {
	file, err := os.OpenFile(input, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	size, copyErr := io.Copy(file, io.LimitReader(body, responseLimit+1))
	closeErr := file.Close()
	if size > responseLimit {
		return fmt.Errorf("source %s: subscription response too large", id)
	}
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("source %s: cannot save response", id)
	}
	return nil
}
