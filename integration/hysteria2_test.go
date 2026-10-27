package integration_test

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestHysteria2SubscriptionUpdate(t *testing.T) {
	for _, backend := range []string{"sing-box", "mihomo"} {
		t.Run(backend, func(t *testing.T) {
			var f *fixture
			if backend == "mihomo" {
				f = newMihomoFixture(t)
			} else {
				f = newFixture(t)
				if core := os.Getenv("PH_REAL_CORE"); core != "" {
					f.config["sing_box"] = core
					f.write("config.json", f.config)
				}
			}
			f.run("", true, "update")
			body := "trojan://password@one.example:443#a\n" +
				"hysteria2://user:p%40ss@127.0.0.1:8443/?sni=example.com&insecure=1&alpn=h3&obfs=salamander&obfs-password=integration-obfs#Warsaw\n" +
				"hy2://password@127.0.0.1#Warsaw-fallback\n"
			f.setBody("/one", base64.StdEncoding.EncodeToString([]byte(body)))
			f.run("", true, "subscription", "update", "one")
			for _, name := range []string{"Warsaw", "Warsaw-fallback"} {
				for _, path := range []string{"state/cache.json", "state/config.json", "state/subscription.json"} {
					if !strings.Contains(f.read(path), name) {
						t.Fatalf("updated Hysteria 2 node %q missing from %s", name, path)
					}
				}
			}
			f.run("", true, "check")
			f.run("", true, "prepare")
			if !strings.Contains(f.read("state/config.json"), "Warsaw") {
				t.Fatal("offline preparation lost cached Hysteria 2 nodes")
			}
		})
	}
}
