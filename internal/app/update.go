package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/sleroq/sb/internal/files"
	"github.com/sleroq/sb/singbox"
	"github.com/sleroq/sb/subscription"
)

type Manager struct{ Settings Settings }

// Update attempts every requested source and installs a validated mixed snapshot
// when at least one fetch succeeds.
func (m Manager) Update(ctx context.Context, ids []string) error {
	lock, err := m.Settings.Lock()
	if err != nil {
		return err
	}
	failed, err := m.updateLocked(ctx, ids)
	_ = lock.Close() // prepare runs during restart, so release before activation.
	if err != nil {
		return err
	}
	restartErr := m.Restart(ctx)
	if len(failed) > 0 {
		if restartErr != nil {
			return fmt.Errorf("%s; %w", failed, restartErr)
		}
		return fmt.Errorf("configuration installed and restarted; %s", failed)
	}
	return restartErr
}

func (m Manager) updateLocked(ctx context.Context, ids []string) (string, error) {
	s := m.Settings
	catalog, err := subscription.Load(s.Stores, s.OverridesFile)
	if err != nil {
		return "", err
	}
	sources := catalog.Sources()
	if err := validateRequestedSources(sources, ids); err != nil {
		return "", err
	}
	cache, err := s.LoadCache()
	if err != nil {
		return "", err
	}
	health, err := s.LoadHealth()
	if err != nil {
		return "", err
	}
	updated, stale, unavailable := s.fetchSources(ctx, sources, ids, cache, health)
	if err := s.WriteHealth(health); err != nil {
		return "", err
	}
	summary := fmt.Sprintf("updated: %v; stale: %v; unavailable: %v", updated, stale, unavailable)
	if len(updated) == 0 && len(stale)+len(unavailable) > 0 {
		return "", fmt.Errorf("no subscription fetch succeeded; %s", summary)
	}
	if err := m.buildAndInstall(ctx, sources, cache); err != nil {
		return "", fmt.Errorf("candidate not installed; %s: %w", summary, err)
	}
	if len(stale)+len(unavailable) > 0 {
		return summary, nil
	}
	return "", nil
}

func validateRequestedSources(sources []subscription.Source, ids []string) error {
	for _, id := range ids {
		i := slices.IndexFunc(sources, func(source subscription.Source) bool { return source.ID == id })
		if i < 0 {
			return fmt.Errorf("unknown subscription %s", id)
		}
		if sources[i].Disabled {
			return fmt.Errorf("subscription %s is disabled; enable it first", id)
		}
	}
	return nil
}

func (s Settings) fetchSources(ctx context.Context, sources []subscription.Source, ids []string, cache Cache, health Health) (updated, stale, unavailable []string) {
	converter := subscription.Converter{Binary: s.Converter}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, source := range sources {
		if source.Disabled || (len(ids) > 0 && !slices.Contains(ids, source.ID)) {
			continue
		}
		if source.ExcludeProtocols == "" {
			source.ExcludeProtocols = s.ExcludeProtocols
		}
		if source.ExcludeNodeNames == "" {
			source.ExcludeNodeNames = s.ExcludeNodeNames
		}
		nodes, err := converter.Fetch(ctx, source)
		if err != nil {
			health[source.ID] = SourceHealth{AttemptedAt: now, Error: err.Error()}
			if _, ok := cache[source.ID]; !ok {
				cache[source.ID] = CachedSource{Unavailable: true}
			}
			if cache[source.ID].Unavailable {
				unavailable = append(unavailable, source.ID)
			} else {
				stale = append(stale, source.ID)
			}
			continue
		}
		health[source.ID] = SourceHealth{AttemptedAt: now}
		cache[source.ID] = CachedSource{UpdatedAt: now, Nodes: nodes}
		updated = append(updated, source.ID)
	}
	return updated, stale, unavailable
}

// Prepare renders current declared policy against cached nodes, with no network
// access or service restart. It is suitable for ExecStart preparation.
func (m Manager) Prepare(ctx context.Context) error {
	lock, err := m.Settings.Lock()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	s := m.Settings
	if s.LegacyOutboundsFile != "" {
		template, err := os.ReadFile(s.TemplateFile)
		if err != nil {
			return err
		}
		var nodes []singbox.Outbound
		if err := files.Read(s.LegacyOutboundsFile, &nodes); err != nil {
			return err
		}
		config, err := singbox.Legacy(template, nodes, s.RoutingMark)
		if err != nil {
			return err
		}
		if err := m.Validate(ctx, config); err != nil {
			return err
		}
		return files.Write(s.ConfigPath(), config, 0600)
	}
	catalog, err := subscription.Load(s.Stores, s.OverridesFile)
	if err != nil {
		return err
	}
	cache, err := s.LoadCache()
	if err != nil {
		return err
	}
	return m.buildAndInstall(ctx, catalog.Sources(), cache)
}

func (m Manager) buildAndInstall(ctx context.Context, sources []subscription.Source, cache Cache) error {
	pin, err := m.Settings.loadPin()
	if err != nil {
		return err
	}
	config, retained, manifest, err := m.buildCandidate(ctx, sources, cache, pin)
	if err != nil {
		return err
	}
	if err := m.Settings.pruneHealth(sources); err != nil {
		return err
	}
	return m.Settings.Install(config, retained, manifest)
}

func (m Manager) buildCandidate(ctx context.Context, sources []subscription.Source, cache Cache, pin string) (json.RawMessage, Cache, Manifest, error) {
	s := m.Settings
	template, err := os.ReadFile(s.TemplateFile)
	if err != nil {
		return nil, nil, Manifest{}, err
	}
	extra, err := s.loadExtraOutbounds()
	if err != nil {
		return nil, nil, Manifest{}, err
	}
	groups, retained, manifest, err := assembleManifest(sources, cache)
	if err != nil {
		return nil, nil, Manifest{}, err
	}

	manifest.PinnedTag = pin
	for _, group := range groups {
		for _, node := range group.Nodes {
			manifest.PinActive = manifest.PinActive || pin != "" && node.String("tag") == pin
		}
	}
	for _, node := range extra {
		tag := node.String("tag")
		manifest.PinActive = manifest.PinActive || pin != "" && tag == pin
		// The API already exposes tags; keep extra outbound endpoints private.
		manifest.ExtraNodes = append(manifest.ExtraNodes, NodeInfo{Tag: tag, Source: "local"})
	}

	options := singbox.Options{
		URL:         s.TestURL,
		Interval:    s.TestInterval,
		Tolerance:   s.Tolerance,
		RoutingMark: s.RoutingMark,
		PinnedLeaf:  pin,
	}
	config, err := singbox.Compose(template, groups, extra, options)
	if err != nil {
		return nil, nil, Manifest{}, err
	}
	if err := m.Validate(ctx, config); err != nil {
		return nil, nil, Manifest{}, err
	}
	return config, retained, manifest, nil
}

func (s Settings) loadExtraOutbounds() ([]singbox.Outbound, error) {
	var extra []singbox.Outbound
	for _, path := range []string{s.ExtraOutboundsFile, s.StaticOutboundsFile} {
		if path == "" {
			continue
		}
		var nodes []singbox.Outbound
		if err := files.Read(path, &nodes); err != nil {
			return nil, err
		}
		if nodes == nil {
			return nil, fmt.Errorf("outbounds file must contain a JSON array")
		}
		extra = append(extra, nodes...)
	}
	return extra, nil
}

func (s Settings) pruneHealth(sources []subscription.Source) error {
	health, err := s.LoadHealth()
	if err != nil {
		return err
	}
	for id := range health {
		if !slices.ContainsFunc(sources, func(source subscription.Source) bool { return source.ID == id }) {
			delete(health, id)
		}
	}
	return s.WriteHealth(health)
}

func assembleManifest(sources []subscription.Source, cache Cache) ([]singbox.Group, Cache, Manifest, error) {
	var groups []singbox.Group
	manifest := Manifest{Nodes: []NodeInfo{}}
	retained := Cache{}
	for _, source := range sources {
		entry, ok := cache[source.ID]
		if ok {
			retained[source.ID] = entry
		}
		if source.Disabled {
			continue
		}
		if !ok {
			return nil, nil, Manifest{}, fmt.Errorf("subscription %s has no cached nodes; run sb update %s", source.ID, source.ID)
		}

		info := SourceInfo{
			ID: source.ID, UpdatedAt: entry.UpdatedAt,
			NodeCount: len(entry.Nodes), Unavailable: entry.Unavailable,
		}
		manifest.Sources = append(manifest.Sources, info)

		group := singbox.Group{Name: source.ID, Nodes: entry.Nodes}
		last, _ := time.Parse(time.RFC3339Nano, manifest.UpdatedAt)
		updated, _ := time.Parse(time.RFC3339Nano, entry.UpdatedAt)
		if updated.After(last) {
			manifest.UpdatedAt = entry.UpdatedAt
		}

		for _, node := range entry.Nodes {
			tag, server := node.String("tag"), node.String("server")
			automatic := source.Allows(tag, server)
			if automatic {
				group.Automatic = append(group.Automatic, tag)
			}
			info := NodeInfo{
				Tag: tag, Type: node.String("type"), Server: server,
				Source: source.ID, Automatic: automatic,
			}
			manifest.Nodes = append(manifest.Nodes, info)
		}
		groups = append(groups, group)
	}
	return groups, retained, manifest, nil
}

func (m Manager) Validate(ctx context.Context, config json.RawMessage) error {
	f, err := os.CreateTemp(m.Settings.StateDir, "check-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(config); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, m.Settings.SingBox, "check", "-c", f.Name()).Run(); err != nil {
		return fmt.Errorf("sing-box rejected candidate configuration " +
			"(diagnostics suppressed because they may contain credentials)")
	}
	return nil
}

func (m Manager) Restart(ctx context.Context) error {
	args := m.Settings.RestartCommand
	if len(args) == 0 {
		return nil
	}
	if err := exec.CommandContext(ctx, args[0], args[1:]...).Run(); err != nil {
		return fmt.Errorf("configuration installed, but service restart failed: %w", err)
	}
	return nil
}

func (m Manager) Manifest() (Manifest, error) {
	var manifest Manifest
	err := files.Read(filepath.Join(m.Settings.StateDir, "subscription.json"), &manifest)
	return manifest, err
}

// Edit serializes store and policy changes with updates. Changes are staged:
// callers explicitly apply with prepare (offline) or update (fetch + restart).
func (m Manager) Edit(change func(*subscription.Catalog) error) error {
	lock, err := m.Settings.Lock()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	c, err := subscription.Load(m.Settings.Stores, m.Settings.OverridesFile)
	if err != nil {
		return err
	}
	return change(c)
}
