// Package mihomo composes native Mihomo JSON configurations from sing-box nodes.
package mihomo

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/sleroq/sb/proxy"
)

type Options struct {
	URL        string
	Interval   string
	Tolerance  uint
	PinnedLeaf string
}

func Compose(template json.RawMessage, groups []proxy.Group, options Options) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(template, &root) != nil || root == nil {
		return nil, fmt.Errorf("template must be a JSON object")
	}
	if err := prepareTemplate(root); err != nil {
		return nil, err
	}

	url, interval, err := testSettings(options)
	if err != nil {
		return nil, err
	}
	set, err := composeGroups(groups, url, interval, options.Tolerance)
	if err != nil {
		return nil, err
	}

	groupConfigs, err := selectorGroups(set, url, interval, options)
	if err != nil {
		return nil, err
	}
	for key, value := range map[string]any{"proxies": set.proxies, "proxy-groups": groupConfigs, "rules": []string{"MATCH,proxy"}, "mode": "rule"} {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		root[key] = data
	}
	return json.MarshalIndent(root, "", "  ")
}

func prepareTemplate(root map[string]json.RawMessage) error {
	if err := checkTemplateKeys(root); err != nil {
		return err
	}

	profile := map[string]json.RawMessage{}
	if raw, ok := root["profile"]; ok {
		if json.Unmarshal(raw, &profile) != nil || profile == nil {
			return fmt.Errorf("profile must be a JSON object")
		}
	}
	// Mihomo defaults to persisting selector changes; sb use is intentionally
	// live-only, while sb pin owns the durable selection.
	if raw, ok := profile["store-selected"]; ok && string(raw) != "false" {
		return fmt.Errorf("profile.store-selected conflicts with sb pin/use semantics")
	}
	profile["store-selected"] = json.RawMessage(`false`)
	encodedProfile, _ := json.Marshal(profile)
	root["profile"] = encodedProfile
	if _, ok := root["mixed-port"]; !ok {
		root["mixed-port"] = json.RawMessage(`2080`)
	}
	if _, ok := root["external-controller"]; !ok {
		root["external-controller"] = json.RawMessage(`"127.0.0.1:9090"`)
	}
	return nil
}

type composedGroups struct {
	proxies    []map[string]json.RawMessage
	configs    []map[string]any
	leaves     []string
	automatic  []string
	autoGroups []string
}

func composeGroups(groups []proxy.Group, url string, interval int, tolerance uint) (composedGroups, error) {
	names, err := reserveGroupNames(groups)
	if err != nil {
		return composedGroups{}, err
	}

	var set composedGroups
	for _, group := range groups {
		members := map[string]bool{}
		for _, node := range group.Nodes {
			tag := node.String("tag")
			if tag == "" || names[tag] {
				return composedGroups{}, fmt.Errorf("empty, duplicate, or reserved node tag %q", tag)
			}
			converted, err := convert(node)
			if err != nil {
				return composedGroups{}, err
			}
			names[tag] = true
			members[tag] = true
			set.leaves = append(set.leaves, tag)
			set.proxies = append(set.proxies, converted)
		}
		if len(group.Automatic) > 0 {
			for _, tag := range group.Automatic {
				if !members[tag] {
					return composedGroups{}, fmt.Errorf("automatic candidate is not a source node")
				}
			}
			name := "auto-" + group.Name
			config := map[string]any{
				"name": name, "type": "url-test", "proxies": group.Automatic,
				"url": url, "interval": interval, "tolerance": tolerance,
			}
			set.configs = append(set.configs, config)
			set.autoGroups = append(set.autoGroups, name)
			set.automatic = append(set.automatic, group.Automatic...)
		}
	}
	return set, nil
}

func testSettings(options Options) (string, int, error) {
	interval := 300
	if options.Interval != "" {
		d, err := time.ParseDuration(options.Interval)
		if err != nil || d < time.Second {
			return "", 0, fmt.Errorf("invalid URL-test interval")
		}
		interval = int(d / time.Second)
	}
	url := options.URL
	if url == "" {
		url = "https://www.gstatic.com/generate_204"
	}
	return url, interval, nil
}

func selectorGroups(set composedGroups, url string, interval int, options Options) ([]map[string]any, error) {
	if len(set.leaves) == 0 {
		return nil, fmt.Errorf("no nodes")
	}
	choices := slices.Concat(set.autoGroups, set.leaves)
	if len(set.automatic) > 0 {
		config := map[string]any{
			"name": "auto", "type": "url-test", "proxies": set.automatic,
			"url": url, "interval": interval, "tolerance": options.Tolerance,
		}
		set.configs = append(set.configs, config)
		choices = append([]string{"auto"}, choices...)
	}
	if options.PinnedLeaf != "" && slices.Contains(set.leaves, options.PinnedLeaf) {
		choices = append([]string{options.PinnedLeaf}, choices...)
	}
	set.configs = append(set.configs, map[string]any{"name": "proxy", "type": "select", "proxies": choices})
	return set.configs, nil
}

func checkTemplateKeys(root map[string]json.RawMessage) error {
	for _, key := range []string{"proxies", "proxy-groups", "proxy-providers", "rules", "mode", "tun"} {
		if _, exists := root[key]; exists {
			return fmt.Errorf("template must not specify %s", key)
		}
	}
	for _, key := range []string{"outbounds", "inbounds", "route", "experimental"} {
		if _, exists := root[key]; exists {
			return fmt.Errorf("sing-box template key %s is unsupported", key)
		}
	}
	return nil
}

func reserveGroupNames(groups []proxy.Group) (map[string]bool, error) {
	names := map[string]bool{"DIRECT": true, "REJECT": true, "auto": true, "proxy": true}
	for _, group := range groups {
		if len(group.Automatic) > 0 {
			name := "auto-" + group.Name
			if names[name] {
				return nil, fmt.Errorf("duplicate or reserved group name %q", name)
			}
			names[name] = true
		}
	}
	return names, nil
}
