// Package singbox composes native sing-box configurations without depending on
// a particular core version or owning subscription management policy.
package singbox

import (
	"encoding/json"
	"fmt"
	"maps"
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
}

// Compose replaces only outbounds. Empty automatic groups are omitted; if all
// candidates are excluded, the selector remains usable manually.
func Compose(template json.RawMessage, groups []Group, extra []Outbound, options Options) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(template, &root); err != nil || root == nil {
		return nil, fmt.Errorf("template must be a JSON object")
	}
	var nodes []Outbound
	tags := map[string]bool{"direct": true, "auto": true, "proxy": true}
	var leaves, automatic, sourceGroups []string
	add := func(node Outbound) error {
		tag := node.String("tag")
		if tag == "" || tags[tag] {
			return fmt.Errorf("empty, duplicate, or reserved outbound tag %q", tag)
		}
		tags[tag] = true
		leaves = append(leaves, tag)
		nodes = append(nodes, node)
		return nil
	}
	for _, group := range groups {
		for _, node := range group.Nodes {
			if err := add(node); err != nil {
				return nil, err
			}
		}
	}
	for _, node := range extra {
		if err := add(node); err != nil {
			return nil, err
		}
		automatic = append(automatic, node.String("tag"))
	}
	urltest := func(tag string, candidates []string) Outbound {
		return Outbound{
			"type": field("urltest"), "tag": field(tag), "outbounds": field(candidates),
			"url": field(options.URL), "interval": field(options.Interval), "tolerance": field(options.Tolerance),
		}
	}
	for _, group := range groups {
		if len(group.Automatic) == 0 {
			continue
		}
		tag := "auto-" + group.Name
		if tags[tag] {
			return nil, fmt.Errorf("source group tag collision %q", tag)
		}
		tags[tag] = true
		membership := map[string]bool{}
		for _, node := range group.Nodes {
			membership[node.String("tag")] = true
		}
		for _, candidate := range group.Automatic {
			if !membership[candidate] {
				return nil, fmt.Errorf("automatic candidate is not a source node")
			}
		}
		nodes = append(nodes, urltest(tag, group.Automatic))
		sourceGroups = append(sourceGroups, tag)
		automatic = append(automatic, group.Automatic...)
	}
	choices := append(sourceGroups, leaves...)
	if len(automatic) > 0 {
		nodes = append(nodes, urltest("auto", automatic))
		choices = append([]string{"auto"}, choices...)
	}
	if len(choices) == 0 {
		return nil, fmt.Errorf("no selectable outbounds; keep at least one enabled subscription or extra outbound")
	}
	selector := Outbound{
		"type": field("selector"), "tag": field("proxy"),
		"outbounds": field(choices), "default": field(choices[0]),
	}
	direct := Outbound{"type": field("direct"), "tag": field("direct")}
	nodes = append(nodes, selector, direct)
	return install(root, nodes, options.RoutingMark)
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
