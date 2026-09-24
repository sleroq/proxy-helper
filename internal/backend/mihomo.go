package backend

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sleroq/sb/clash"
	"github.com/sleroq/sb/internal/app"
	"github.com/sleroq/sb/mihomo"
	"github.com/sleroq/sb/proxy"
	"github.com/sleroq/sb/subscription"
)

type mihomoClient struct{ binary string }

func (b mihomoClient) Name() string { return "mihomo" }

func (b mihomoClient) Template() json.RawMessage {
	return json.RawMessage(`{
		"mixed-port":2080,
		"bind-address":"127.0.0.1",
		"allow-lan":false,
		"external-controller":"127.0.0.1:9090"
	}`)
}

func (b mihomoClient) Fetch(ctx context.Context, source subscription.Source) ([]proxy.Node, error) {
	return (subscription.Native{}).Fetch(ctx, source)
}

func (b mihomoClient) Compose(template json.RawMessage, groups []proxy.Group, extra []proxy.Node, opts app.Options) (json.RawMessage, error) {
	if len(extra) != 0 || opts.Bypass || opts.TunnelOff || opts.RoutingMark != 0 {
		return nil, fmt.Errorf("mihomo does not support extra outbounds, bypass, tunnel modes or routing_mark")
	}
	options := mihomo.Options{
		URL: opts.URL, Interval: opts.Interval, Tolerance: opts.Tolerance, PinnedLeaf: opts.PinnedLeaf,
	}
	return mihomo.Compose(template, groups, options)
}

func (b mihomoClient) Legacy(json.RawMessage, []proxy.Node, uint) (json.RawMessage, error) {
	return nil, fmt.Errorf("mihomo does not support legacy outbounds")
}

func (b mihomoClient) SelectorDefault(config json.RawMessage) (string, error) {
	var root struct {
		Groups []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
	}
	if err := json.Unmarshal(config, &root); err != nil {
		return "", err
	}
	for _, group := range root.Groups {
		if group.Name == "proxy" && len(group.Proxies) > 0 {
			return group.Proxies[0], nil
		}
	}
	return "", fmt.Errorf("generated selector missing")
}

func (b mihomoClient) CheckCommand(path, stateDir string) (string, []string) {
	return b.binary, []string{"-t", "-f", path, "-d", stateDir}
}

func (b mihomoClient) Control(url string) Controller { return clash.Client{URL: url} }
func (b mihomoClient) Mode(command string) error {
	return fmt.Errorf("%s is not supported by mihomo backend", command)
}
