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
	TemplateFile        string                     `json:"template_file"`
	StaticOutboundsFile string                     `json:"static_outbounds_file,omitempty"`
	ExtraOutboundsFile  string                     `json:"extra_outbounds_file,omitempty"`
	LegacyOutboundsFile string                     `json:"legacy_outbounds_file,omitempty"`
	StateDir            string                     `json:"state_dir"`
	APIURL              string                     `json:"api_url"`
	TestURL             string                     `json:"test_url"`
	TestInterval        string                     `json:"test_interval"`
	Tolerance           uint                       `json:"tolerance"`
	RoutingMark         uint                       `json:"routing_mark,omitempty"`
	SingBox             string                     `json:"sing_box"`
	Backend             string                     `json:"backend,omitempty"`
	Backends            map[string]BackendSettings `json:"backends,omitempty"`
	Mihomo              string                     `json:"mihomo,omitempty"`
	Converter           string                     `json:"converter"`
	RestartCommand      []string                   `json:"restart_command,omitempty"`
	Stores              []subscription.Store       `json:"stores"`
	OverridesFile       string                     `json:"overrides_file"`
	ExcludeProtocols    string                     `json:"exclude_protocols"`
	ExcludeNodeNames    string                     `json:"exclude_node_names"`
}

type BackendSettings struct {
	TemplateFile string `json:"template_file"`
	Binary       string `json:"binary"`
}

func (s Settings) SelectedBackend() (string, error) {
	name := s.Backend
	if name == "" {
		name = "sing-box"
	}
	var state activeRecord
	err := files.Read(filepath.Join(s.StateDir, "active-backend.json"), &state)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err == nil {
		return state.Backend, nil
	}
	return name, nil
}

func (s Settings) ActiveBackend() (string, error) {
	name, err := s.SelectedBackend()
	if err != nil {
		return "", err
	}
	if len(s.Backends) != 0 {
		if _, ok := s.Backends[name]; !ok {
			return "", fmt.Errorf("backend %q is not configured", name)
		}
	} else {
		defaultName := s.Backend
		if defaultName == "" {
			defaultName = "sing-box"
		}
		if name != defaultName {
			return "", fmt.Errorf("backend %q is not configured", name)
		}
	}
	return name, nil
}

func (s Settings) BackendPaths(name string) (string, string, error) {
	if len(s.Backends) != 0 {
		entry, ok := s.Backends[name]
		if !ok {
			return "", "", fmt.Errorf("backend %q is not configured", name)
		}
		return entry.TemplateFile, entry.Binary, nil
	}
	defaultName := s.Backend
	if defaultName == "" {
		defaultName = "sing-box"
	}
	if name != defaultName {
		return "", "", fmt.Errorf("backend %q is not configured", name)
	}
	binary := s.SingBox
	if name == "mihomo" {
		binary = s.Mihomo
	}
	return s.TemplateFile, binary, nil
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
		TestInterval: "5m", Tolerance: 50, SingBox: "sing-box", Mihomo: "mihomo",
		ExcludeProtocols: "ssr",
	}
	if err := files.Read(path, &s); err != nil {
		return s, err
	}

	if err := s.resolvePaths(path); err != nil {
		return s, err
	}
	if s.StateDir == "" || (s.TemplateFile == "" && len(s.Backends) == 0) {
		return s, fmt.Errorf("state_dir and template_file are required")
	}
	return s, nil
}

func (s *Settings) resolvePaths(path string) error {
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return err
	}
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	s.TemplateFile = resolve(s.TemplateFile)
	for name, entry := range s.Backends {
		if name != "sing-box" && name != "mihomo" {
			return fmt.Errorf("unsupported backend %q", name)
		}
		entry.TemplateFile = resolve(entry.TemplateFile)
		entry.Binary = resolve(entry.Binary)
		if entry.TemplateFile == "" || entry.Binary == "" {
			return fmt.Errorf("backend %q requires template_file and binary", name)
		}
		s.Backends[name] = entry
	}
	s.StaticOutboundsFile = resolve(s.StaticOutboundsFile)
	s.ExtraOutboundsFile = resolve(s.ExtraOutboundsFile)
	s.LegacyOutboundsFile = resolve(s.LegacyOutboundsFile)
	s.StateDir = resolve(s.StateDir)
	s.OverridesFile = resolve(s.OverridesFile)
	for i := range s.Stores {
		s.Stores[i].Path = resolve(s.Stores[i].Path)
	}

	return nil
}

// Init creates a non-TUN, unprivileged standalone setup. Explicitly opt
// into system routing in the native template and service manager, not here.
func Init(path, name string, template json.RawMessage) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("configuration already exists")
	} else if !os.IsNotExist(err) {
		return err
	}

	base := filepath.Dir(path)
	s := Settings{
		TemplateFile: "template.json", StateDir: "state", APIURL: "http://127.0.0.1:9090",
		TestURL: "https://www.gstatic.com/generate_204", TestInterval: "5m", Tolerance: 50,
		SingBox: "sing-box", Mihomo: "mihomo", Backend: name, ExcludeProtocols: "ssr",
		Stores:        []subscription.Store{{Name: "local", Path: "subscriptions.json", Writable: true}},
		OverridesFile: "overrides.json",
	}

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
