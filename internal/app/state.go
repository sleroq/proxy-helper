package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/sleroq/sb/internal/files"
	"github.com/sleroq/sb/singbox"
)

type CachedSource struct {
	UpdatedAt   string             `json:"updated_at"`
	Nodes       []singbox.Outbound `json:"nodes"`
	Unavailable bool               `json:"unavailable,omitempty"`
}
type Cache map[string]CachedSource
type NodeInfo struct {
	Tag       string `json:"tag"`
	Type      string `json:"type"`
	Server    string `json:"server"`
	Source    string `json:"source"`
	Automatic bool   `json:"automatic"`
}
type SourceInfo struct {
	ID          string `json:"id"`
	UpdatedAt   string `json:"updatedAt"`
	NodeCount   int    `json:"nodeCount"`
	Unavailable bool   `json:"unavailable"`
}
type Manifest struct {
	Mode       *Mode        `json:"mode,omitempty"`
	UpdatedAt  string       `json:"updatedAt"`
	Nodes      []NodeInfo   `json:"nodes"`
	ExtraNodes []NodeInfo   `json:"extraNodes,omitempty"`
	Sources    []SourceInfo `json:"sources"`
	PinnedTag  string       `json:"pinnedTag,omitempty"`
	PinActive  bool         `json:"pinActive,omitempty"`
}
type SourceHealth struct {
	AttemptedAt string `json:"attemptedAt"`
	Error       string `json:"error,omitempty"`
}
type Health map[string]SourceHealth

func (s Settings) LoadHealth() (Health, error) {
	health := Health{}
	err := files.Read(filepath.Join(s.StateDir, "health.json"), &health)
	if os.IsNotExist(err) {
		return health, nil
	}
	if err != nil {
		return nil, err
	}
	if health == nil {
		return nil, fmt.Errorf("health must be an object")
	}
	return health, nil
}

func (s Settings) WriteHealth(health Health) error {
	return files.Write(filepath.Join(s.StateDir, "health.json"), health, 0644)
}

// Lock is released by the OS on process exit, including SIGKILL.
func (s Settings) Lock() (*os.File, error) {
	if err := os.MkdirAll(s.StateDir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(s.StateDir, "sb.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another sb mutation is running")
	}
	return f, nil
}

func (s Settings) ConfigPath() string { return filepath.Join(s.StateDir, "config.json") }

func (s Settings) LoadCache() (Cache, error) {
	cache := Cache{}
	err := files.Read(filepath.Join(s.StateDir, "cache.json"), &cache)
	if err == nil {
		if cache == nil {
			return nil, fmt.Errorf("cache must be an object")
		}
		return cache, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	return s.loadLegacyCache(cache)
}

func (s Settings) loadLegacyCache(cache Cache) (Cache, error) {
	// Migrate the original shell CLI cache without fetching or copying secrets
	// into a public file. Legacy files remain intact until a successful commit.
	var nodes []singbox.Outbound
	if err := files.Read(filepath.Join(s.StateDir, "subscription-outbounds.json"), &nodes); err != nil {
		if os.IsNotExist(err) {
			return cache, nil
		}
		return nil, err
	}
	var mapping map[string][]string
	if err := files.Read(filepath.Join(s.StateDir, "subscription-sources.json"), &mapping); err != nil {
		// Older caches may predate source tracking. Without this mapping the
		// nodes cannot be attributed safely; let update fetch a fresh cache.
		if os.IsNotExist(err) {
			return cache, nil
		}
		return nil, err
	}
	return attributeLegacyNodes(cache, nodes, mapping)
}

func attributeLegacyNodes(cache Cache, nodes []singbox.Outbound, mapping map[string][]string) (Cache, error) {
	byTag := map[string]singbox.Outbound{}
	for _, node := range nodes {
		tag := node.String("tag")
		if tag == "" || byTag[tag] != nil {
			return nil, fmt.Errorf("invalid legacy cache tags")
		}
		byTag[tag] = node
	}
	for id, tags := range mapping {
		entry := CachedSource{}
		for _, tag := range tags {
			node, ok := byTag[tag]
			if !ok {
				return nil, fmt.Errorf("legacy source mapping does not match nodes")
			}
			entry.Nodes = append(entry.Nodes, node)
			delete(byTag, tag)
		}
		cache[id] = entry
	}
	if len(byTag) != 0 {
		return nil, fmt.Errorf("legacy source mapping does not match nodes")
	}
	return cache, nil
}

func (s Settings) Install(config json.RawMessage, cache Cache, manifest Manifest) error {
	if err := files.Write(filepath.Join(s.StateDir, "cache.json"), cache, 0600); err != nil {
		return err
	}
	if err := files.Write(filepath.Join(s.StateDir, "subscription.json"), manifest, 0644); err != nil {
		return err
	}
	return files.Write(s.ConfigPath(), config, 0600)
}
