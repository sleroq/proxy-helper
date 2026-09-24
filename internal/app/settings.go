// Package app coordinates subscription management, configuration and activation.
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sleroq/sb/internal/files"
	"github.com/sleroq/sb/subscription"
)

type Settings struct {
	TemplateFile        string               `json:"template_file"`
	StaticOutboundsFile string               `json:"static_outbounds_file,omitempty"`
	ExtraOutboundsFile  string               `json:"extra_outbounds_file,omitempty"`
	LegacyOutboundsFile string               `json:"legacy_outbounds_file,omitempty"`
	StateDir            string               `json:"state_dir"`
	APIURL              string               `json:"api_url"`
	TestURL             string               `json:"test_url"`
	TestInterval        string               `json:"test_interval"`
	Tolerance           uint                 `json:"tolerance"`
	RoutingMark         uint                 `json:"routing_mark,omitempty"`
	SingBox             string               `json:"sing_box"`
	Converter           string               `json:"converter"`
	RestartCommand      []string             `json:"restart_command,omitempty"`
	Stores              []subscription.Store `json:"stores"`
	OverridesFile       string               `json:"overrides_file"`
	ExcludeProtocols    string               `json:"exclude_protocols"`
	ExcludeNodeNames    string               `json:"exclude_node_names"`
}

func DefaultConfig() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "sb", "config.json"), nil
}

func LoadSettings(path string) (Settings, error) {
	s := Settings{
		APIURL: "http://127.0.0.1:9090", TestURL: "https://www.gstatic.com/generate_204",
		TestInterval: "5m", Tolerance: 50, SingBox: "sing-box",
		ExcludeProtocols: "ssr",
	}
	if err := files.Read(path, &s); err != nil {
		return s, err
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return s, err
	}
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	s.TemplateFile = resolve(s.TemplateFile)
	s.StaticOutboundsFile = resolve(s.StaticOutboundsFile)
	s.ExtraOutboundsFile = resolve(s.ExtraOutboundsFile)
	s.LegacyOutboundsFile = resolve(s.LegacyOutboundsFile)
	s.StateDir = resolve(s.StateDir)
	s.OverridesFile = resolve(s.OverridesFile)
	for i := range s.Stores {
		s.Stores[i].Path = resolve(s.Stores[i].Path)
	}
	if s.StateDir == "" || s.TemplateFile == "" {
		return s, fmt.Errorf("state_dir and template_file are required")
	}
	return s, nil
}

// Init creates a non-TUN, unprivileged standalone setup. Explicitly opt into
// system routing in the native template and service manager, not in this CLI.
func Init(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("configuration already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	base := filepath.Dir(path)
	s := Settings{
		TemplateFile: "template.json", StateDir: "state", APIURL: "http://127.0.0.1:9090",
		TestURL: "https://www.gstatic.com/generate_204", TestInterval: "5m", Tolerance: 50,
		SingBox: "sing-box", ExcludeProtocols: "ssr",
		Stores:        []subscription.Store{{Name: "local", Path: "subscriptions.json", Writable: true}},
		OverridesFile: "overrides.json",
	}
	template := json.RawMessage(`{
		"log":{"level":"warn"},
		"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":2080}],
		"route":{"final":"proxy"},
		"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}
	}`)
	if _, err := os.Stat(filepath.Join(base, "template.json")); err == nil {
		return fmt.Errorf("template.json already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := files.Write(filepath.Join(base, "template.json"), template, 0644); err != nil {
		return err
	}
	return files.Write(path, s, 0644)
}
