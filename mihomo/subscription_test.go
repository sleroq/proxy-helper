package mihomo

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sleroq/ph/proxy"
)

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	_ = json.Compact(&b, raw)
	return b.String()
}

func TestSubscriptionBypass(t *testing.T) {
	template := json.RawMessage(`{
		"dns": {
			"enable": true, "nameserver": ["1.1.1.1"], "direct-nameserver": ["8.8.8.8"],
			"direct-nameserver-follow-policy": false,
			"nameserver-policy": {"rule-set:ads":"rcode://success","+.example.com":"8.8.8.8","sub.example.com":"1.1.1.1"}
		},
		"log-level": "warning"
	}`)
	leaf := node(`{"tag":"leaf","type":"trojan","server":"host","server_port":443,"password":"secret"}`)
	groups := []proxy.Group{{Nodes: []proxy.Node{leaf}}}
	hosts := []string{"sub.example.com", "192.0.2.1", "2001:db8::1"}
	config, err := Compose(template, groups, Options{SubscriptionHosts: hosts})
	if err != nil {
		t.Fatal(err)
	}

	checkSubscriptionComposition(t, config)
	checkSubscriptionIdempotency(t, config, hosts)
	checkSubscriptionHostChange(t, config)
}

func checkSubscriptionComposition(t *testing.T, config json.RawMessage) {
	t.Helper()
	var got struct {
		Rules    []string                   `json:"rules"`
		DNS      map[string]json.RawMessage `json:"dns"`
		Proxies  []json.RawMessage          `json:"proxies"`
		LogLevel string                     `json:"log-level"`
	}
	if err := json.Unmarshal(config, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DOMAIN,sub.example.com,DIRECT", "IP-CIDR,192.0.2.1/32,DIRECT,no-resolve",
		"IP-CIDR6,2001:db8::1/128,DIRECT,no-resolve", "MATCH,proxy",
	}
	if !reflect.DeepEqual(got.Rules, want) {
		t.Fatalf("rules: %v", got.Rules)
	}
	if len(got.Proxies) != 1 || got.LogLevel != "warning" || compact(got.DNS["nameserver"]) != `["1.1.1.1"]` || string(got.DNS["direct-nameserver-follow-policy"]) != "true" {
		t.Fatalf("lost template or nodes: %s", config)
	}
	if compact(got.DNS["nameserver-policy"]) != `{"sub.example.com":"system","rule-set:ads":"rcode://success","+.example.com":"8.8.8.8"}` {
		t.Fatalf("policy: %s", got.DNS["nameserver-policy"])
	}
}

func checkSubscriptionIdempotency(t *testing.T, config json.RawMessage, hosts []string) {
	t.Helper()
	has, err := HasSubscriptionBypass(config)
	if err != nil || !has {
		t.Fatalf("bypass detection: %v %v", has, err)
	}
	same, changed, err := EnsureSubscriptionBypass(config, hosts)
	if err != nil || changed || !bytes.Equal(same, config) {
		t.Fatalf("not idempotent: changed=%v err=%v", changed, err)
	}
}

func checkSubscriptionHostChange(t *testing.T, config json.RawMessage) {
	t.Helper()
	updated, changed, err := EnsureSubscriptionBypass(config, []string{"new.example.com"})
	if err != nil || !changed {
		t.Fatalf("update: changed=%v err=%v", changed, err)
	}
	for _, entry := range []string{"DOMAIN,new.example.com,DIRECT", "DOMAIN,sub.example.com,DIRECT", `"leaf"`} {
		if !bytes.Contains(updated, []byte(entry)) {
			t.Fatalf("update lost %s: %s", entry, updated)
		}
	}
}

func TestSubscriptionBypassRealCore(t *testing.T) {
	core := os.Getenv("PH_REAL_MIHOMO")
	if core == "" {
		t.Skip("PH_REAL_MIHOMO not set")
	}
	template := json.RawMessage(`{"dns":{"enable":true,"nameserver":["1.1.1.1"]}}`)
	leaf := node(`{"tag":"leaf","type":"trojan","server":"host.example","server_port":443,"password":"secret"}`)
	groups := []proxy.Group{{Nodes: []proxy.Node{leaf}}}
	hosts := []string{"sub.example.com", "192.0.2.1", "2001:db8::1"}
	config, err := Compose(template, groups, Options{SubscriptionHosts: hosts})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(core, "-t", "-f", path, "-d", dir).CombinedOutput(); err != nil {
		t.Fatalf("core check: %v: %s\n%s", err, output, config)
	}
}

func TestSubscriptionBypassDisabledDNS(t *testing.T) {
	for _, dns := range []string{``, `,"dns":{"enable":false,"nameserver-policy":{"+.example.com":"1.1.1.1"}}`} {
		config := json.RawMessage(`{"rules":["MATCH,proxy"]` + dns + `}`)
		has, err := HasSubscriptionBypass(config)
		if err != nil || has {
			t.Fatalf("unexpected bypass: %v %v", has, err)
		}
		same, changed, err := EnsureSubscriptionBypass(config, nil)
		if err != nil || changed || !bytes.Equal(config, same) {
			t.Fatalf("empty hosts: %v %v", changed, err)
		}
		updated, changed, err := EnsureSubscriptionBypass(config, []string{"sub.example.com"})
		if err != nil || !changed {
			t.Fatalf("host: %v %v", changed, err)
		}
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal(config, &before)
		_ = json.Unmarshal(updated, &after)
		if compact(before["dns"]) != compact(after["dns"]) {
			t.Fatalf("disabled DNS changed: %s", updated)
		}
	}
}
