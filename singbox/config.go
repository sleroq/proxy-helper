// Package singbox composes native sing-box configurations without depending on
// a particular core version or owning subscription management policy.
package singbox

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Outbound preserves unknown protocol fields and exact JSON numbers.
type Outbound map[string]json.RawMessage

func (o Outbound) String(key string) string {
	var s string
	_ = json.Unmarshal(o[key], &s)
	return s
}

// Generated fields use only JSON primitives, so encoding cannot fail.
func field[T string | uint | []string](value T) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

type Group struct {
	Name      string
	Nodes     []Outbound
	Automatic []string
}

type Options struct {
	URL         string
	Interval    string
	Tolerance   uint
	RoutingMark uint
	PinnedLeaf  string
	Bypass      bool
	TunnelOff   bool
	TunnelMode  bool
}

type outboundSet struct {
	nodes     []Outbound
	tags      map[string]bool
	leaves    []string
	automatic []string
	groupTags []string
}

func (set *outboundSet) add(node Outbound) error {
	tag := node.String("tag")
	if tag == "" || set.tags[tag] {
		return fmt.Errorf("empty, duplicate, or reserved outbound tag %q", tag)
	}
	set.tags[tag] = true
	set.leaves = append(set.leaves, tag)
	set.nodes = append(set.nodes, node)
	return nil
}

func (set *outboundSet) addLeaves(groups []Group, extra []Outbound) error {
	for _, group := range groups {
		for _, node := range group.Nodes {
			if err := set.add(node); err != nil {
				return err
			}
		}
	}
	for _, node := range extra {
		if err := set.add(node); err != nil {
			return err
		}
		set.automatic = append(set.automatic, node.String("tag"))
	}
	return nil
}

func (set *outboundSet) addGroup(group Group, options Options) error {
	if len(group.Automatic) == 0 {
		return nil
	}
	tag := "auto-" + group.Name
	if set.tags[tag] {
		return fmt.Errorf("source group tag collision %q", tag)
	}
	set.tags[tag] = true
	membership := map[string]bool{}
	for _, node := range group.Nodes {
		membership[node.String("tag")] = true
	}
	for _, candidate := range group.Automatic {
		if !membership[candidate] {
			return fmt.Errorf("automatic candidate is not a source node")
		}
	}
	set.nodes = append(set.nodes, urltest(tag, group.Automatic, options))
	set.groupTags = append(set.groupTags, tag)
	set.automatic = append(set.automatic, group.Automatic...)
	return nil
}

func urltest(tag string, candidates []string, options Options) Outbound {
	return Outbound{
		"type": field("urltest"), "tag": field(tag), "outbounds": field(candidates),
		"url": field(options.URL), "interval": field(options.Interval), "tolerance": field(options.Tolerance),
	}
}

// Compose replaces only outbounds. Empty automatic groups are omitted; if all
// candidates are excluded, the selector remains usable manually.
func Compose(template json.RawMessage, groups []Group, extra []Outbound, options Options) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(template, &root); err != nil || root == nil {
		return nil, fmt.Errorf("template must be a JSON object")
	}

	set, choices, err := composeOutbounds(groups, extra, options)
	if err != nil {
		return nil, err
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("no selectable outbounds; keep at least one enabled subscription or extra outbound")
	}

	selected := choices[0]
	if !options.Bypass && slices.Contains(set.leaves, options.PinnedLeaf) {
		selected = options.PinnedLeaf
	}
	selector := Outbound{
		"type": field("selector"), "tag": field("proxy"),
		"outbounds": field(choices), "default": field(selected),
	}
	direct := Outbound{"type": field("direct"), "tag": field("direct")}
	set.nodes = append(set.nodes, selector, direct)
	if err := applyMode(root, options); err != nil {
		return nil, err
	}
	return install(root, set.nodes, options.RoutingMark)
}

func composeOutbounds(groups []Group, extra []Outbound, options Options) (outboundSet, []string, error) {
	set := outboundSet{tags: map[string]bool{"direct": true, "auto": true, "proxy": true}}
	if !options.Bypass {
		if err := set.addLeaves(groups, extra); err != nil {
			return set, nil, err
		}
		for _, group := range groups {
			if err := set.addGroup(group, options); err != nil {
				return set, nil, err
			}
		}
	}
	choices := slices.Concat(set.groupTags, set.leaves)
	if options.Bypass {
		choices = []string{"direct"}
	}
	if len(set.automatic) > 0 && !options.Bypass {
		set.nodes = append(set.nodes, urltest("auto", set.automatic, options))
		choices = append([]string{"auto"}, choices...)
	}
	return set, choices, nil
}

// Legacy installs an explicitly managed complete outbound array.
func Legacy(template json.RawMessage, nodes []Outbound, mark uint) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(template, &root); err != nil || root == nil {
		return nil, fmt.Errorf("template must be a JSON object")
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("legacy outbounds must not be empty")
	}
	return install(root, nodes, mark)
}

func install(root map[string]json.RawMessage, nodes []Outbound, mark uint) (json.RawMessage, error) {
	// Copy each outbound before modifying it; callers retain ownership of inputs.
	copied := make([]Outbound, len(nodes))
	for i, node := range nodes {
		copied[i] = maps.Clone(node)
		if mark != 0 && node.String("type") == "direct" {
			copied[i]["routing_mark"] = field(mark)
		}
	}
	data, err := json.Marshal(copied)
	if err != nil {
		return nil, err
	}
	root["outbounds"] = data
	return json.MarshalIndent(root, "", "  ")
}
