package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/sleroq/sb/clash"
	"github.com/sleroq/sb/internal/app"
	"github.com/sleroq/sb/proxy"
	"github.com/sleroq/sb/singbox"
	"github.com/sleroq/sb/subscription"
)

type singBox struct{ binary, converter string }

func (b singBox) Name() string { return "sing-box" }

func (b singBox) Template() json.RawMessage {
	return json.RawMessage(`{
		"log":{"level":"warn"},
		"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":2080}],
		"route":{"final":"proxy"},
		"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}
	}`)
}

func (b singBox) Fetch(ctx context.Context, source subscription.Source) ([]proxy.Node, error) {
	if b.converter != "" {
		return (subscription.Converter{Binary: b.converter}).Fetch(ctx, source)
	}
	return (subscription.Native{}).Fetch(ctx, source)
}

func (b singBox) Compose(template json.RawMessage, groups []proxy.Group, extra []proxy.Node, opts app.Options) (json.RawMessage, error) {
	options := singbox.Options{
		URL: opts.URL, Interval: opts.Interval, Tolerance: opts.Tolerance,
		RoutingMark: opts.RoutingMark, PinnedLeaf: opts.PinnedLeaf,
		Bypass: opts.Bypass, TunnelOff: opts.TunnelOff, TunnelMode: opts.TunnelMode,
	}
	return singbox.Compose(template, groups, extra, options)
}

func (b singBox) Legacy(template json.RawMessage, nodes []proxy.Node, mark uint) (json.RawMessage, error) {
	return singbox.Legacy(template, nodes, mark)
}

func (b singBox) SelectorDefault(config json.RawMessage) (string, error) {
	var root struct {
		Outbounds []proxy.Node `json:"outbounds"`
	}
	if err := json.Unmarshal(config, &root); err != nil {
		return "", err
	}
	i := slices.IndexFunc(root.Outbounds, func(o proxy.Node) bool { return o.String("tag") == "proxy" })
	if i < 0 {
		return "", fmt.Errorf("generated selector missing")
	}
	return root.Outbounds[i].String("default"), nil
}

func (b singBox) CheckCommand(path, _ string) (string, []string) {
	return b.binary, []string{"check", "-c", path}
}

func (b singBox) Control(url string) Controller { return clash.Client{URL: url} }
func (b singBox) Mode(string) error             { return nil }
