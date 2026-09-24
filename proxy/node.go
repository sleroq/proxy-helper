// Package proxy defines the backend-neutral cached subscription representation.
package proxy

import "encoding/json"

// Node preserves unknown fields and exact JSON numbers. Its JSON fields are
// currently sing-box-shaped; backends translate them when composing configs.
type Node map[string]json.RawMessage

func (n Node) String(key string) string {
	var s string
	_ = json.Unmarshal(n[key], &s)
	return s
}

// Group carries source nodes and their automatic-selection candidates.
type Group struct {
	Name      string
	Nodes     []Node
	Automatic []string
}
