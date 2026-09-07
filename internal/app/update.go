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

// Update fetches all requested sources before touching installed state. Other
// enabled sources retain their cache; it never silently drops a failed source.
func (m Manager) Update(ctx context.Context, ids []string) error {
	lock, err := m.Settings.Lock()
	if err != nil {
		return err
	}
	err = m.updateLocked(ctx, ids)
	_ = lock.Close() // prepare runs during restart, so release before activation.
	if err != nil {
		return err
	}
	return m.Restart(ctx)
}

func (m Manager) updateLocked(ctx context.Context, ids []string) error {
	s := m.Settings
	catalog, err := subscription.Load(s.Stores, s.OverridesFile)
	if err != nil {
		return err
	}
	sources := catalog.Sources()
	for _, id := range ids {
		i := slices.IndexFunc(sources, func(source subscription.Source) bool { return source.ID == id })
		if i < 0 {
			return fmt.Errorf("unknown subscription %s", id)
		}
		if sources[i].Disabled {
			return fmt.Errorf("subscription %s is disabled; enable it first", id)
		}
	}
	cache, err := s.LoadCache()
	if err != nil {
		return err
	}
	converter := subscription.Converter{Binary: s.Converter}
	now := time.Now().UTC().Format(time.RFC3339)
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
			return err
		}
		cache[source.ID] = CachedSource{UpdatedAt: now, Nodes: nodes}
	}
	return m.buildAndInstall(ctx, sources, cache)
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
	s := m.Settings
	template, err := os.ReadFile(s.TemplateFile)
	if err != nil {
		return err
	}
	var extra []singbox.Outbound
	for _, path := range []string{s.ExtraOutboundsFile, s.StaticOutboundsFile} {
		if path == "" {
			continue
		}
		var nodes []singbox.Outbound
		if err := files.Read(path, &nodes); err != nil {
			return err
		}
		if nodes == nil {
			return fmt.Errorf("outbounds file must contain a JSON array")
		}
		extra = append(extra, nodes...)
	}
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
			return fmt.Errorf("subscription %s has no cached nodes; run sb update %s", source.ID, source.ID)
		}
		group := singbox.Group{Name: source.ID, Nodes: entry.Nodes}
		if entry.UpdatedAt > manifest.UpdatedAt {
			manifest.UpdatedAt = entry.UpdatedAt
		}
		for _, node := range entry.Nodes {
			tag, server := node.String("tag"), node.String("server")
			automatic := source.Allows(tag, server)
			if automatic {
				group.Automatic = append(group.Automatic, tag)
			}
			manifest.Nodes = append(manifest.Nodes, NodeInfo{Tag: tag, Type: node.String("type"), Server: server, Source: source.ID, Automatic: automatic})
		}
		groups = append(groups, group)
	}
	config, err := singbox.Compose(template, groups, extra, singbox.Options{URL: s.TestURL, Interval: s.TestInterval, Tolerance: s.Tolerance, RoutingMark: s.RoutingMark})
	if err != nil {
		return err
	}
	if err := m.Validate(ctx, config); err != nil {
		return err
	}
	return s.Install(config, retained, manifest)
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
		return fmt.Errorf("sing-box rejected candidate configuration (diagnostics suppressed because they may contain credentials)")
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
