package app

import (
	"context"
	"encoding/json"

	"github.com/sleroq/ph/proxy"
)

type Options struct {
	URL, Interval, PinnedLeaf     string
	SubscriptionHosts             []string
	Tolerance, RoutingMark        uint
	Bypass, TunnelOff, TunnelMode bool
}

type Backend interface {
	Name() string
	Ready(context.Context, string) error
	Compose(json.RawMessage, []proxy.Group, []proxy.Node, Options) (json.RawMessage, error)
	EnsureSubscriptionBypass(json.RawMessage, []string) (json.RawMessage, bool, error)
	HasSubscriptionBypass(json.RawMessage) (bool, error)
	Legacy(json.RawMessage, []proxy.Node, uint) (json.RawMessage, error)
	SelectorDefault(json.RawMessage) (string, error)
	CheckCommand(configPath, stateDir string) (string, []string)
	Mode(command string) error
}
