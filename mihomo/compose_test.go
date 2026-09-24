package mihomo

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/sleroq/sb/proxy"
)

func node(data string) proxy.Node {
	var n proxy.Node
	_ = json.Unmarshal([]byte(data), &n)
	return n
}

func TestCompose(t *testing.T) {
	leaf := node(`{
 "tag":"leaf", "type":"shadowsocks", "server":"host", "server_port":443,
 "method":"aes-128-gcm", "password":"secret"
}`)
	groups := []proxy.Group{{Name: "sub", Nodes: []proxy.Node{leaf}, Automatic: []string{"leaf"}}}
	output, err := Compose(json.RawMessage(`{"dns":{"enable":true}}`), groups, Options{PinnedLeaf: "leaf", Interval: "5m"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Proxies []map[string]any `json:"proxies"`
		Groups  []struct {
			Name    string   `json:"name"`
			Proxies []string `json:"proxies"`
		} `json:"proxy-groups"`
		Rules []string       `json:"rules"`
		Mode  string         `json:"mode"`
		DNS   map[string]any `json:"dns"`
	}
	if err = json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Proxies) != 1 || len(result.Groups) != 3 || len(result.Rules) != 1 {
		t.Fatalf("unexpected composition: %s", output)
	}
	got := []any{
		result.Proxies[0]["type"], result.Proxies[0]["cipher"],
		result.Groups[0].Name, result.Groups[1].Name, result.Groups[2].Proxies,
		result.Rules[0], result.Mode, result.DNS["enable"],
	}
	want := []any{
		"ss", "aes-128-gcm", "auto-sub", "auto",
		[]string{"leaf", "auto", "auto-sub", "leaf"},
		"MATCH,proxy", "rule", true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
}

func TestConvertRejectsUnsupportedNestedOptions(t *testing.T) {
	for _, tc := range []struct {
		name, input string
	}{
		{"tls", `"tls":{"enabled":true,"certificate":"private-value"}`},
		{"reality", `"tls":{"reality":{"private_key":"private-value"}}`},
		{"tcp", `"transport":{"type":"tcp","host":"private-value"}`},
		{"ws", `"transport":{"type":"ws","early_data":"private-value"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := convert(node(`{"tag":"leaf","type":"vless","server":"host","server_port":443,"uuid":"id",` + tc.input + `}`))
			if err == nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestRejectedInputs(t *testing.T) {
	base := node(`{"tag":"leaf","type":"trojan","server":"host","server_port":443,"password":"secret"}`)
	for _, tc := range []struct {
		name     string
		template string
		groups   []proxy.Group
	}{
		{"routing override", `{"rules":[]}`, []proxy.Group{{Nodes: []proxy.Node{base}}}},
		{"no nodes", `{}`, nil},
		{"reserved", `{}`, []proxy.Group{{Nodes: []proxy.Node{node(`{"tag":"proxy","type":"trojan"}`)}}}},
		{"unknown field", `{}`, []proxy.Group{{Nodes: []proxy.Node{node(`{"tag":"leaf","type":"trojan","server":"host","server_port":443,"password":"secret","packet_encoding":"xudp"}`)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compose(json.RawMessage(tc.template), tc.groups, Options{})
			if err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
