// Package subscription manages source identity, editable stores, and automatic selection.
package subscription

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/sleroq/sb/internal/files"
)

// Policy filters automatic selection only; excluded nodes remain selectable.
// Patterns use Go regular expressions; exclusions win over inclusions.
type Policy struct {
	Auto           *bool    `json:"auto,omitempty"`
	IncludeTags    []string `json:"include_tags,omitempty"`
	ExcludeTags    []string `json:"exclude_tags,omitempty"`
	IncludeServers []string `json:"include_servers,omitempty"`
	ExcludeServers []string `json:"exclude_servers,omitempty"`
}

func (p Policy) Validate() error {
	for _, patterns := range [][]string{p.IncludeTags, p.ExcludeTags, p.IncludeServers, p.ExcludeServers} {
		for _, pattern := range patterns {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("invalid automatic selection pattern")
			}
		}
	}
	return nil
}

func matches(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if ok, _ := regexp.MatchString(pattern, value); ok {
			return true
		}
	}
	return false
}

func (p Policy) Allows(tag, server string) bool {
	return (p.Auto == nil || *p.Auto) &&
		(len(p.IncludeTags) == 0 || matches(p.IncludeTags, tag)) &&
		(len(p.IncludeServers) == 0 || matches(p.IncludeServers, server)) &&
		!matches(p.ExcludeTags, tag) && !matches(p.ExcludeServers, server)
}

type Source struct {
	ID               string `json:"id"`
	URLFile          string `json:"url_file,omitempty"`
	URL              string `json:"url,omitempty"`
	Prefix           string `json:"prefix,omitempty"`
	Disabled         bool   `json:"disabled,omitempty"`
	ExcludeProtocols string `json:"exclude_protocols,omitempty"`
	ExcludeNodeNames string `json:"exclude_node_names,omitempty"`
	Policy
}

func (s Source) Validate() error {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(s.ID) {
		return fmt.Errorf("subscription ID must match [A-Za-z0-9_-]+")
	}
	if (s.URL == "") == (s.URLFile == "") {
		return fmt.Errorf("subscription %s must set exactly one of url or url_file", s.ID)
	}
	return s.Policy.Validate()
}

type Store struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Writable bool   `json:"writable"`
}

type Document struct {
	Subscriptions []Source `json:"subscriptions"`
}
type Override struct {
	Disabled *bool   `json:"disabled,omitempty"`
	Policy   *Policy `json:"policy,omitempty"`
}
type Catalog struct {
	Stores        []Store
	Documents     []Document
	Overrides     map[string]Override
	OverridesFile string
}

func Load(stores []Store, overridesFile string) (*Catalog, error) {
	c := &Catalog{
		Stores:        stores,
		Documents:     make([]Document, len(stores)),
		Overrides:     map[string]Override{},
		OverridesFile: overridesFile,
	}
	if err := c.loadStores(); err != nil {
		return nil, err
	}
	if err := c.loadOverrides(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) loadStores() error {
	seen := map[string]bool{}
	names := map[string]bool{}
	for i, store := range c.Stores {
		if store.Name == "" || store.Path == "" || names[store.Name] {
			return fmt.Errorf("store names must be nonempty and unique and paths must be set")
		}
		names[store.Name] = true
		if err := files.Read(store.Path, &c.Documents[i]); err != nil && (!store.Writable || !os.IsNotExist(err)) {
			return err
		}
		if err := validateSources(c.Documents[i].Subscriptions, seen); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) loadOverrides() error {
	if c.OverridesFile != "" {
		if err := files.Read(c.OverridesFile, &c.Overrides); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if c.Overrides == nil {
		c.Overrides = map[string]Override{}
	}
	for _, override := range c.Overrides {
		if override.Policy != nil {
			if err := override.Policy.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSources(sources []Source, seen map[string]bool) error {
	for _, source := range sources {
		if err := source.Validate(); err != nil {
			return err
		}
		if seen[source.ID] {
			return fmt.Errorf("duplicate subscription ID %s", source.ID)
		}
		seen[source.ID] = true
	}
	return nil
}

func (c *Catalog) Sources() []Source {
	var sources []Source
	for i, doc := range c.Documents {
		for _, s := range doc.Subscriptions {
			if s.URLFile != "" && !filepath.IsAbs(s.URLFile) {
				s.URLFile = filepath.Join(filepath.Dir(c.Stores[i].Path), s.URLFile)
			}
			if override, ok := c.Overrides[s.ID]; ok {
				if override.Disabled != nil {
					s.Disabled = *override.Disabled
				}
				if override.Policy != nil {
					s.Policy = *override.Policy
				}
			}
			sources = append(sources, s)
		}
	}
	return sources
}

func (c *Catalog) location(id string) (int, int, error) {
	for i, doc := range c.Documents {
		for j, s := range doc.Subscriptions {
			if s.ID == id {
				return i, j, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("unknown subscription %s", id)
}

// Put adds or replaces a complete source in its owning writable store.
func (c *Catalog) Put(source Source, storeName string, replace bool) error {
	if err := source.Validate(); err != nil {
		return err
	}
	if replace {
		return c.replace(source)
	}
	return c.add(source, storeName)
}

func (c *Catalog) replace(source Source) error {
	i, j, err := c.location(source.ID)
	if err != nil {
		return err
	}
	if !c.Stores[i].Writable {
		return fmt.Errorf("subscription %s belongs to a read-only store", source.ID)
	}
	c.Documents[i].Subscriptions[j] = source
	return files.Write(c.Stores[i].Path, c.Documents[i], 0600)
}

func (c *Catalog) add(source Source, storeName string) error {
	if _, _, err := c.location(source.ID); err == nil {
		return fmt.Errorf("duplicate subscription ID %s", source.ID)
	}
	i := -1
	for k, store := range c.Stores {
		if store.Writable && (storeName == "" || store.Name == storeName) {
			if i >= 0 {
				return fmt.Errorf("multiple writable stores; specify --store")
			}
			i = k
		}
	}
	if i < 0 {
		return fmt.Errorf("no matching writable store")
	}
	c.Documents[i].Subscriptions = append(c.Documents[i].Subscriptions, source)
	return files.Write(c.Stores[i].Path, c.Documents[i], 0600)
}

func (c *Catalog) Delete(id string) error {
	i, j, err := c.location(id)
	if err != nil {
		return err
	}
	if !c.Stores[i].Writable {
		return fmt.Errorf("subscription %s belongs to a read-only store; disable it instead", id)
	}
	c.Documents[i].Subscriptions = slices.Delete(c.Documents[i].Subscriptions, j, j+1)
	if err := files.Write(c.Stores[i].Path, c.Documents[i], 0600); err != nil {
		return err
	}
	delete(c.Overrides, id)
	if c.OverridesFile != "" {
		return files.Write(c.OverridesFile, c.Overrides, 0600)
	}
	return nil
}

func (c *Catalog) SetEnabled(id string, enabled bool) error {
	if _, _, err := c.location(id); err != nil {
		return err
	}
	if c.OverridesFile == "" {
		return fmt.Errorf("overrides_file is required")
	}
	disabled := !enabled
	override := c.Overrides[id]
	override.Disabled = &disabled
	c.Overrides[id] = override
	return files.Write(c.OverridesFile, c.Overrides, 0600)
}

// SetPolicy replaces only automatic-selection policy, never source credentials.
func (c *Catalog) SetPolicy(id string, policy Policy) error {
	if _, _, err := c.location(id); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if c.OverridesFile == "" {
		return fmt.Errorf("overrides_file is required")
	}
	override := c.Overrides[id]
	override.Policy = &policy
	c.Overrides[id] = override
	return files.Write(c.OverridesFile, c.Overrides, 0600)
}

func (c *Catalog) Reset(id string) error {
	if _, _, err := c.location(id); err != nil {
		return err
	}
	if c.OverridesFile == "" {
		return fmt.Errorf("overrides_file is required")
	}
	delete(c.Overrides, id)
	return files.Write(c.OverridesFile, c.Overrides, 0600)
}
