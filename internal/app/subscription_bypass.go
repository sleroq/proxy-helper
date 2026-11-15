package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/sleroq/ph/internal/files"
	"github.com/sleroq/ph/subscription"
)

func (s Settings) subscriptionHosts(sources []subscription.Source) []string {
	if s.DisableSubscriptionBypass {
		return nil
	}
	var hosts []string
	for _, source := range sources {
		if source.Disabled {
			continue
		}
		host, err := source.Hostname()
		if err == nil && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// prepareSubscriptionBypass changes only installed routing before a fetch.
// The caller holds the state lock and restarts after releasing it.
func (m Manager) prepareSubscriptionBypass(ctx context.Context, ids []string) (bool, error) {
	s := m.Settings
	if _, err := os.Stat(s.subscriptionBypassPending()); err == nil {
		if len(s.RestartCommand) == 0 {
			return false, fmt.Errorf("subscription bypass changes require restart_command before fetching")
		}
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	installed, err := os.ReadFile(s.ConfigPath())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return m.stageSubscriptionBypass(ctx, installed, ids)
}

func (m Manager) stageSubscriptionBypass(ctx context.Context, installed json.RawMessage, ids []string) (bool, error) {
	s := m.Settings
	candidate, changed, err := m.subscriptionBypassCandidate(ctx, installed, ids)
	if err != nil {
		return false, fmt.Errorf("prepare subscription bypass: %w", err)
	}
	if !changed {
		return false, nil
	}
	if len(s.RestartCommand) == 0 {
		return false, fmt.Errorf("subscription bypass changes require restart_command before fetching")
	}
	if err := files.WriteBytes(s.subscriptionBypassPending(), nil, 0600); err != nil {
		return false, err
	}
	if err := files.Write(s.ConfigPath(), candidate, 0600); err != nil {
		return false, err
	}
	return true, nil
}

func (s Settings) subscriptionBypassPending() string {
	return filepath.Join(s.StateDir, "subscription-bypass-pending")
}

func (m Manager) clearSubscriptionBypassPending() error {
	lock, err := m.Settings.Lock()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := m.CheckActive(); err != nil {
		return err
	}
	return os.Remove(m.Settings.subscriptionBypassPending())
}

func (m Manager) subscriptionBypassCandidate(ctx context.Context, installed json.RawMessage, ids []string) (json.RawMessage, bool, error) {
	s := m.Settings
	catalog, err := subscription.Load(s.Stores, s.OverridesFile)
	if err != nil {
		return nil, false, err
	}
	sources := catalog.Sources()
	if err := validateRequestedSources(sources, ids); err != nil {
		return nil, false, err
	}
	mode, err := s.LoadMode()
	if err != nil || mode.Bypass {
		return nil, false, err
	}
	if s.DisableSubscriptionBypass {
		changed, err := m.Backend.HasSubscriptionBypass(installed)
		if err != nil || !changed {
			return nil, false, err
		}
		candidate, err := m.withoutSubscriptionBypass(ctx, sources)
		return candidate, true, err
	}
	candidate, changed, err := m.Backend.EnsureSubscriptionBypass(installed, s.subscriptionHosts(sources))
	if err == nil && changed {
		err = m.Validate(ctx, candidate)
	}
	return candidate, changed, err
}

func (m Manager) withoutSubscriptionBypass(ctx context.Context, sources []subscription.Source) (json.RawMessage, error) {
	if m.Settings.LegacyOutboundsFile != "" {
		return nil, fmt.Errorf("cannot rebuild subscription routing with legacy outbounds")
	}
	cache, err := m.Settings.LoadCache()
	if err != nil {
		return nil, err
	}
	// A new source has no cached nodes yet; retain the installed sources while
	// removing the old routing rules before fetching it.
	for _, source := range sources {
		if _, ok := cache[source.ID]; !ok {
			cache[source.ID] = CachedSource{Unavailable: true}
		}
	}
	pin, err := m.Settings.loadPin()
	if err != nil {
		return nil, err
	}
	candidate, _, _, err := m.buildCandidate(ctx, sources, cache, pin)
	return candidate, err
}
