// Package backend adapts client-specific configuration and control to sb's
// subscription use cases. Nodes and source policy remain client-independent.
package backend

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sleroq/sb/clash"
	"github.com/sleroq/sb/internal/app"
)

// Selection is the live selector snapshot shown by the CLI.
type Selection = clash.Selector

// Controller exposes the live operations sb needs, not a particular wire API.
type Controller interface {
	Selector(context.Context) (Selection, error)
	Use(context.Context, string) error
	Test(context.Context, string, string) (map[string]int, error)
}

// Client supplies the application's port plus standalone template and live control.
type Client interface {
	app.Backend
	Template() json.RawMessage
	Control(apiURL string) Controller
	RunCommand(configPath, stateDir string) (string, []string)
}

type Config struct {
	Name                string
	SingBox             string
	Mihomo              string
	Converter           string
	LegacyOutboundsFile string
	StaticOutboundsFile string
	ExtraOutboundsFile  string
	RoutingMark         uint
}

func New(config Config) (Client, error) {
	switch config.Name {
	case "", "sing-box":
		if config.SingBox == "" {
			config.SingBox = "sing-box"
		}
		return singBox{binary: config.SingBox, converter: config.Converter}, nil
	case "mihomo":
		if err := config.validateMihomo(); err != nil {
			return nil, err
		}
		if config.Mihomo == "" {
			config.Mihomo = "mihomo"
		}
		return mihomoClient{binary: config.Mihomo}, nil
	default:
		return nil, fmt.Errorf("unsupported backend %q", config.Name)
	}
}

func (config Config) validateMihomo() error {
	if config.LegacyOutboundsFile != "" || config.StaticOutboundsFile != "" ||
		config.ExtraOutboundsFile != "" || config.RoutingMark != 0 {
		return fmt.Errorf("mihomo does not support legacy/static/extra outbounds or routing_mark")
	}
	return nil
}
