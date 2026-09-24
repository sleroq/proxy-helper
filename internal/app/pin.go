package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sleroq/sb/internal/files"
	"github.com/sleroq/sb/subscription"
)

type pinState struct {
	Tag string `json:"tag"`
}

func (s Settings) loadPin() (string, error) {
	var pin pinState
	err := files.Read(filepath.Join(s.StateDir, "pin.json"), &pin)
	if os.IsNotExist(err) {
		return "", nil
	}
	return pin.Tag, err
}

// Pin validates the entire candidate before changing persistent intent or installed state.
// An empty tag restores the generated selector default.
func (m Manager) Pin(ctx context.Context, tag string) (string, error) {
	s := m.Settings
	if s.LegacyOutboundsFile != "" {
		return "", fmt.Errorf("pin/unpin is unavailable in legacy-outbounds mode")
	}
	lock, err := s.Lock()
	if err != nil {
		return "", fmt.Errorf("cannot modify pin: state directory requires owner authorization: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := m.CheckActive(); err != nil {
		return "", err
	}
	catalog, err := subscription.Load(s.Stores, s.OverridesFile)
	if err != nil {
		return "", err
	}
	cache, err := s.LoadCache()
	if err != nil {
		return "", err
	}
	config, retained, manifest, err := m.buildCandidate(ctx, catalog.Sources(), cache, tag)
	if err != nil {
		return "", err
	}
	return m.installPin(tag, config, retained, manifest)
}

func (m Manager) installPin(tag string, config json.RawMessage, retained Cache, manifest Manifest) (string, error) {
	s := m.Settings
	if tag != "" && !manifest.PinActive {
		return "", fmt.Errorf("tag %q is not an available leaf outbound", tag)
	}
	choice, err := m.Backend.SelectorDefault(config)
	if err != nil {
		return "", err
	}
	if err := files.Write(filepath.Join(s.StateDir, "pin.json"), pinState{Tag: tag}, 0600); err != nil {
		return "", fmt.Errorf("cannot save pin: state directory requires owner authorization: %w", err)
	}
	if err := s.Install(config, retained, manifest); err != nil {
		return "", err
	}
	return choice, nil
}
