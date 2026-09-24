package app

import (
	"context"
	"encoding/json"

	"github.com/sleroq/sb/proxy"
	"github.com/sleroq/sb/subscription"
)

type Options struct {
	URL, Interval, PinnedLeaf     string
	Tolerance, RoutingMark        uint
	Bypass, TunnelOff, TunnelMode bool
}

type Backend interface {
	Name() string
	Fetch(context.Context, subscription.Source) ([]proxy.Node, error)
	Compose(json.RawMessage, []proxy.Group, []proxy.Node, Options) (json.RawMessage, error)
	Legacy(json.RawMessage, []proxy.Node, uint) (json.RawMessage, error)
	SelectorDefault(json.RawMessage) (string, error)
	CheckCommand(configPath, stateDir string) (string, []string)
	Mode(command string) error
}
