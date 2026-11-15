package integration_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sleroq/ph/singbox"
)

type subscriptionDNSConfig struct {
	DNS struct {
		Servers []map[string]any `json:"servers"`
	} `json:"dns"`
	Route struct {
		Default any `json:"default_domain_resolver"`
	} `json:"route"`
	Outbounds []map[string]any `json:"outbounds"`
}

func TestRealSubscriptionDNS(t *testing.T) {
	core := os.Getenv("PH_REAL_CORE")
	if core == "" {
		t.Skip("PH_REAL_CORE not set")
	}

	template, err := os.ReadFile("../tests/routing/sing-box.json")
	if err != nil {
		t.Fatal(err)
	}
	node := singbox.Outbound{
		"type": json.RawMessage(`"shadowsocks"`), "tag": json.RawMessage(`"node"`),
		"server": json.RawMessage(`"127.0.0.1"`), "server_port": json.RawMessage(`443`),
		"method": json.RawMessage(`"aes-128-gcm"`), "password": json.RawMessage(`"password"`),
	}
	mark := uint(0)
	if runtime.GOOS == "linux" {
		mark = 123
	}
	options := singbox.Options{
		SubscriptionHosts: []string{"192.0.2.1"}, RoutingMark: mark, Interval: "3m", URL: "https://example.com",
	}
	config, err := singbox.Compose(template, nil, []singbox.Outbound{node}, options)
	if err != nil {
		t.Fatal(err)
	}
	checkSubscriptionIPConfig(t, core, config, mark)
	checkSubscriptionDomainUpdate(t, core, config, mark)
	checkSubscriptionBypassMarkers(t)
}

func checkSubscriptionIPConfig(t *testing.T, core string, config []byte, mark uint) {
	t.Helper()
	checkSubscriptionCore(t, core, config)
	var root subscriptionDNSConfig
	decodeSubscriptionConfig(t, config, &root)
	if len(root.DNS.Servers) != 1 || root.Route.Default != nil {
		t.Fatal("IP-only bypass changed DNS")
	}
	direct := subscriptionOutbound(t, root.Outbounds, "ph-subscription-direct")
	if direct["domain_resolver"] != nil || mark != 0 && direct["routing_mark"] != float64(mark) {
		t.Fatal("IP bypass outbound not copied from direct")
	}
	has, err := singbox.HasSubscriptionBypass(config)
	if err != nil || !has {
		t.Fatalf("IP bypass detection: %v %v", has, err)
	}
}

func checkSubscriptionDomainUpdate(t *testing.T, core string, config []byte, mark uint) {
	t.Helper()
	config, changed, err := singbox.EnsureSubscriptionBypass(config, []string{"192.0.2.1", "sub.example"})
	if err != nil || !changed {
		t.Fatalf("domain update: %v %v", changed, err)
	}
	checkSubscriptionCore(t, core, config)
	var root subscriptionDNSConfig
	decodeSubscriptionConfig(t, config, &root)
	if root.Route.Default != "remote" || len(root.DNS.Servers) != 2 {
		t.Fatal("previous implicit resolver not preserved")
	}
	direct := subscriptionOutbound(t, root.Outbounds, "ph-subscription-direct")
	if direct["domain_resolver"] != "ph-subscription-local" || mark != 0 && direct["routing_mark"] != float64(mark) {
		t.Fatal("subscription outbound resolver or mark incorrect")
	}
	if subscriptionOutbound(t, root.Outbounds, "direct")["domain_resolver"] != nil {
		t.Fatal("ordinary direct outbound changed")
	}

	checkSubscriptionUpdateIdempotence(t, config)
}

func checkSubscriptionUpdateIdempotence(t *testing.T, config []byte) {
	t.Helper()
	again, changed, err := singbox.EnsureSubscriptionBypass(config, []string{"sub.example", "192.0.2.1"})
	if err != nil || changed || !bytes.Equal(again, config) {
		t.Fatalf("update not idempotent: %v %v", changed, err)
	}
}

func checkSubscriptionBypassMarkers(t *testing.T) {
	t.Helper()
	collision := json.RawMessage(`{"outbounds":[{"type":"direct","tag":"direct"},` +
		`{"type":"block","tag":"ph-subscription-direct"}]}`)
	if _, _, err := singbox.EnsureSubscriptionBypass(collision, []string{"192.0.2.1"}); err == nil {
		t.Fatal("accepted reserved outbound collision")
	}
	reserved := singbox.Outbound{"type": json.RawMessage(`"direct"`), "tag": json.RawMessage(`"ph-subscription-direct"`)}
	if _, err := singbox.Compose(json.RawMessage(`{}`), nil, []singbox.Outbound{reserved}, singbox.Options{}); err == nil {
		t.Fatal("accepted a node using the reserved subscription outbound tag")
	}
	legacy := json.RawMessage(`{"dns":{"servers":[{"type":"local","tag":"ph-subscription-local"}]}}`)
	if has, err := singbox.HasSubscriptionBypass(legacy); err != nil || !has {
		t.Fatalf("legacy detection: %v %v", has, err)
	}
}

func checkSubscriptionCore(t *testing.T, core string, config []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(core, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("core check: %v: %s", err, output)
	}
}

func decodeSubscriptionConfig(t *testing.T, config []byte, dest any) {
	t.Helper()
	if err := json.Unmarshal(config, dest); err != nil {
		t.Fatal(err)
	}
}

func subscriptionOutbound(t *testing.T, outbounds []map[string]any, tag string) map[string]any {
	t.Helper()
	for _, outbound := range outbounds {
		if outbound["tag"] == tag {
			return outbound
		}
	}
	t.Fatalf("missing outbound %s", tag)
	return nil
}

func TestRealSubscriptionDNSInstallUpdate(t *testing.T) {
	core := os.Getenv("PH_REAL_CORE")
	if core == "" {
		t.Skip("PH_REAL_CORE not set")
	}
	f := newFixture(t)
	f.config["sing_box"] = core
	f.config["restart_command"] = []string{helper, "restart-ok", filepath.Join(f.dir, "restarts")}
	f.write("config.json", f.config)
	f.write("template.json", map[string]any{
		"dns":   map[string]any{"servers": []any{map[string]any{"type": "udp", "tag": "remote", "server": "198.18.0.1"}}},
		"route": map[string]any{"final": "proxy"},
	})
	f.write("declared.json", map[string]any{"subscriptions": []any{f.source("one", "/one")}})
	f.run("", true, "update")
	origin, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	origin.Host = "localhost:" + origin.Port()
	source := map[string]any{"id": "one", "url": origin.String() + "/one"}
	f.write("declared.json", map[string]any{"subscriptions": []any{source}})
	f.run("", true, "update")
	installed := []byte(f.read("state/config.json"))
	checkSubscriptionCore(t, core, installed)
	if !bytes.Contains(installed, []byte("ph-subscription-direct")) || !bytes.Contains(installed, []byte("ph-subscription-local")) {
		t.Fatal("installed update missing subscription bypass")
	}
	if got := bytes.Count([]byte(f.read("restarts")), []byte("restart\n")); got != 3 {
		t.Fatalf("expected pre-fetch and final restarts, got %d", got)
	}
}

func TestRealSubscriptionDNSResolverPreservation(t *testing.T) {
	core := os.Getenv("PH_REAL_CORE")
	if core == "" {
		t.Skip("PH_REAL_CORE not set")
	}
	for _, tc := range []struct{ name, server, resolver, want string }{
		{"untagged", `{"type":"udp","server":"198.18.0.1"}`, "", "ph-subscription-previous-dns"},
		{"explicit", `{"type":"udp","tag":"remote","server":"198.18.0.1"}`, `,"default_domain_resolver":"remote"`, "remote"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := json.RawMessage(`{"outbounds":[{"type":"direct","tag":"direct","routing_mark":123}],` +
				`"dns":{"servers":[` + tc.server + `]},"route":{"final":"direct"` + tc.resolver + `}}`)
			result, changed, err := singbox.EnsureSubscriptionBypass(config, []string{"sub.example"})
			if err != nil || !changed {
				t.Fatalf("ensure: %v %v", changed, err)
			}
			var root map[string]any
			decodeSubscriptionConfig(t, result, &root)
			route := root["route"].(map[string]any)
			if route["default_domain_resolver"] != tc.want {
				t.Fatal("resolver not preserved")
			}
			outbounds := root["outbounds"].([]any)
			if outbounds[1].(map[string]any)["routing_mark"] != float64(123) {
				t.Fatal("routing mark not copied")
			}
			if runtime.GOOS != "linux" { // routing marks are Linux-only
				for _, outbound := range outbounds {
					delete(outbound.(map[string]any), "routing_mark")
				}
				result, err = json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
			}
			checkSubscriptionCore(t, core, result)
		})
	}
}
