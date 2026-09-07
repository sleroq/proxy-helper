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
	UpdatedAt string             `json:"updated_at"`
	Nodes     []singbox.Outbound `json:"nodes"`
}
type Cache map[string]CachedSource
type NodeInfo struct {
	Tag       string `json:"tag"`
	Type      string `json:"type"`
	Server    string `json:"server"`
	Source    string `json:"source"`
	Automatic bool   `json:"automatic"`
}
type Manifest struct {
	UpdatedAt string     `json:"updatedAt"`
	Nodes     []NodeInfo `json:"nodes"`
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
		return nil, err
	}
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
