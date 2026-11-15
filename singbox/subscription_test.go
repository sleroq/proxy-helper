package singbox

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSubscriptionBypass(t *testing.T) {
	template := json.RawMessage(`{
 "outbounds":[{"type":"direct","tag":"direct"}],
 "dns":{"servers":[{"type":"local","tag":"existing"}],"rules":[{"domain_suffix":["example"],"server":"existing"}]},
 "route":{"rules":[{"action":"hijack-dns","protocol":"dns"},{"outbound":"proxy"}]},
 "experimental":{"large":900719925474099312345}
 }`)
	config, changed, err := EnsureSubscriptionBypass(template, []string{"sub.example", "192.0.2.1", "2001:db8::1"})
	if err != nil || !changed {
		t.Fatalf("ensure: changed=%v err=%v", changed, err)
	}

	var root struct {
		DNS struct {
			Servers []json.RawMessage `json:"servers"`
			Rules   []json.RawMessage `json:"rules"`
		} `json:"dns"`
		Route struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(config, &root); err != nil {
		t.Fatal(err)
	}

	want := []string{
		`"action":"sniff"`, `"domain":["sub.example"]`,
		`"ip_cidr":["192.0.2.1/32"]`, `"ip_cidr":["2001:db8::1/128"]`,
		`"action":"hijack-dns"`, `"outbound":"proxy"`,
	}
	assertRouteRules(t, root.Route.Rules, want)
	if len(root.DNS.Servers) != 2 || len(root.DNS.Rules) != 2 || !bytes.Contains(root.DNS.Rules[0], []byte(`"sub.example"`)) || !bytes.Contains(config, []byte("900719925474099312345")) {
		t.Fatalf("preservation: %s", config)
	}

}

func assertRouteRules(t *testing.T, rules []json.RawMessage, want []string) {
	t.Helper()
	if len(rules) != len(want) {
		t.Fatalf("got %d route rules, want %d", len(rules), len(want))
	}
	for i, part := range want {
		var compact bytes.Buffer
		if err := json.Compact(&compact, rules[i]); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(compact.Bytes(), []byte(part)) {
			t.Errorf("rule %d: %s", i, rules[i])
		}
	}
}

func TestSubscriptionBypassUpdates(t *testing.T) {
	hosts := []string{"sub.example", "192.0.2.1", "2001:db8::1"}
	base := json.RawMessage(`{"outbounds":[{"type":"direct","tag":"direct"}]}`)
	config, changed, err := EnsureSubscriptionBypass(base, hosts)
	if err != nil || !changed {
		t.Fatalf("initial ensure: %v %v", changed, err)
	}

	again, changed, err := EnsureSubscriptionBypass(config, hosts)
	if err != nil || changed || !bytes.Equal(again, config) {
		t.Fatalf("idempotency: %v %v", changed, err)
	}

}

func TestSubscriptionBypassAddsHosts(t *testing.T) {
	base := json.RawMessage(`{"outbounds":[{"type":"direct","tag":"direct"}]}`)
	again, _, err := EnsureSubscriptionBypass(base, []string{"sub.example"})
	if err != nil {
		t.Fatal(err)
	}

	added, changed, err := EnsureSubscriptionBypass(again, []string{"new.example"})
	if err != nil || !changed || !bytes.Contains(added, []byte("sub.example")) || !bytes.Contains(added, []byte("new.example")) {
		t.Fatalf("addition: %v %v %s", changed, err, added)
	}
	_, changed, err = EnsureSubscriptionBypass(added, []string{"new.example", "sub.example"})
	if err != nil || changed {
		t.Fatalf("addition idempotency: %v %v", changed, err)
	}
}

func TestSubscriptionBypassInvalidAndEmpty(t *testing.T) {
	for _, config := range []string{`[]`, `{"route":[]}`, `{"dns":[]}`, `{"dns":{"servers":[{"tag":"ph-subscription-local","type":"https"}]}}`} {
		if _, _, err := EnsureSubscriptionBypass(json.RawMessage(config), []string{"example.com"}); err == nil {
			t.Errorf("accepted %s", config)
		}
	}
	config := json.RawMessage(`{"route":{"final":"proxy"}}`)
	result, changed, err := EnsureSubscriptionBypass(config, nil)
	if err != nil || changed || !bytes.Equal(result, config) {
		t.Fatalf("empty: %v %v", changed, err)
	}
}

func TestSubscriptionBypassRealCore(t *testing.T) {
	core := os.Getenv("PH_REAL_CORE")
	if core == "" {
		t.Skip("PH_REAL_CORE not set")
	}
	template := json.RawMessage(`{
 "inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":2080}],
 "route":{"final":"proxy","rules":[{"protocol":"dns","action":"hijack-dns"}]}
 }`)
	config, err := Compose(template, nil, nil, Options{Bypass: true})
	if err != nil {
		t.Fatal(err)
	}
	config, _, err = EnsureSubscriptionBypass(config, []string{"example.com", "192.0.2.1", "2001:db8::1"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(core, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("core check: %v: %s\n%s", err, output, config)
	}
}

func TestHasSubscriptionBypass(t *testing.T) {
	base := json.RawMessage(`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
	for _, hosts := range [][]string{nil, {"192.0.2.1"}, {"example.com"}} {
		config, _, err := EnsureSubscriptionBypass(base, hosts)
		if err != nil {
			t.Fatal(err)
		}
		got, err := HasSubscriptionBypass(config)
		if err != nil || got != (len(hosts) != 0) {
			t.Fatalf("hosts %v: got %v, err %v", hosts, got, err)
		}
	}
	if _, err := HasSubscriptionBypass(json.RawMessage(`[]`)); err == nil {
		t.Fatal("accepted invalid config")
	}
}

func TestComposeSubscriptionHosts(t *testing.T) {
	template := json.RawMessage(`{
 "route":{"rules":[{"outbound":"proxy"},{"domain":["sub.example"],"outbound":"direct"}]}
 }`)
	node := Outbound{"type": field("direct"), "tag": field("leaf")}
	config, err := Compose(template, nil, []Outbound{node}, Options{SubscriptionHosts: []string{"sub.example"}})
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Route struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal(config, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Route.Rules) != 4 || !bytes.Contains(root.Route.Rules[0], []byte("sniff")) || !bytes.Contains(root.Route.Rules[1], []byte("sub.example")) {
		t.Fatalf("priority: %s", config)
	}
}

func TestComposeWithoutSubscriptionBypass(t *testing.T) {
	template := json.RawMessage(`{"route":{"final":"proxy"}}`)
	node := Outbound{"type": field("direct"), "tag": field("leaf")}

	empty, err := Compose(template, nil, []Outbound{node}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	has, err := HasSubscriptionBypass(empty)
	if err != nil || has {
		t.Fatalf("default: %v %v", has, err)
	}
	bypass, err := Compose(template, nil, nil, Options{Bypass: true, SubscriptionHosts: []string{"sub.example"}})
	if err != nil {
		t.Fatal(err)
	}
	has, err = HasSubscriptionBypass(bypass)
	if err != nil || has {
		t.Fatalf("whole bypass: %v %v", has, err)
	}
}
