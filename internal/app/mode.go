package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sleroq/sb/internal/files"
	"github.com/sleroq/sb/subscription"
)

type Mode struct {
	Bypass    bool `json:"bypass"`
	TunnelOff bool `json:"tunnel_off"`
}

func (s Settings) LoadMode() (Mode, error) {
	var mode Mode
	err := files.Read(filepath.Join(s.StateDir, "mode.json"), &mode)
	if os.IsNotExist(err) {
		return Mode{}, nil
	}
	return mode, err
}

func (m Manager) SetMode(ctx context.Context, command string, enabled bool) error {
	s := m.Settings
	if s.LegacyOutboundsFile != "" {
		return fmt.Errorf("%s unavailable with legacy outbounds", command)
	}
	if len(s.RestartCommand) == 0 {
		return fmt.Errorf("%s requires restart_command", command)
	}
	lock, err := s.Lock()
	if err != nil {
		return err
	}
	err = m.setModeLocked(ctx, command, enabled)
	_ = lock.Close()
	if err != nil {
		return err
	}
	return m.Restart(ctx)
}

func (m Manager) setModeLocked(ctx context.Context, command string, enabled bool) error {
	s := m.Settings
	mode, err := s.LoadMode()
	if err != nil {
		return err
	}
	if command == "bypass" {
		mode.Bypass = enabled
	} else {
		mode.TunnelOff = !enabled
	}
	catalog, err := subscription.Load(s.Stores, s.OverridesFile)
	if err != nil {
		return err
	}
	cache, err := s.LoadCache()
	if err != nil {
		return err
	}
	pin, err := s.loadPin()
	if err != nil {
		return err
	}
	sources := catalog.Sources()
	tunnelCommand := command == "tunnel"
	config, retained, manifest, err := m.buildCandidateWithMode(ctx, sources, cache, pin, mode, tunnelCommand)
	if err == nil {
		err = s.Install(config, retained, manifest)
	}
	if err == nil {
		err = files.Write(filepath.Join(s.StateDir, "mode.json"), mode, 0600)
	}
	return err
}
