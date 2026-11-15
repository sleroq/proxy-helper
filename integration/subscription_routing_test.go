package integration_test

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This exercises real mixed-proxy routing, not host TUN interception.
func TestRealSubscriptionDirectRouting(t *testing.T) {
	for _, tc := range []struct {
		name, core string
	}{
		{"sing-box", os.Getenv("PH_REAL_CORE")},
		{"mihomo", os.Getenv("PH_REAL_MIHOMO")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.core == "" {
				t.Skip("real core not configured")
			}
			checkRealSubscriptionRouting(t, tc.name, tc.core)
		})
	}
}

func checkRealSubscriptionRouting(t *testing.T, name, core string) {
	t.Helper()
	f := newFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	if name == "mihomo" {
		f.config["backend"] = name
		f.config["mihomo"] = core
		f.write("template.json", map[string]any{"mixed-port": port, "bind-address": "127.0.0.1"})
	} else {
		f.config["sing_box"] = core
		f.write("template.json", map[string]any{
			"inbounds": []any{map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": port}},
			"dns": map[string]any{"servers": []any{map[string]any{
				"type": "udp", "tag": "remote", "server": "127.0.0.1", "server_port": 1,
			}}},
			"route": map[string]any{"final": "proxy"},
		})
	}
	f.write("config.json", f.config)
	origin, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	origin.Host = "localhost:" + origin.Port()
	origin.Path = "/one"
	f.write("declared.json", map[string]any{"subscriptions": []any{
		map[string]any{"id": "one", "url": origin.String()},
	}})
	credentials := base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:password"))
	f.setBody("/one", "ss://"+credentials+"@127.0.0.1:1#node")
	f.run("", true, "update")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	config := filepath.Join(f.dir, "state", "config.json")
	args := []string{"run", "-c", config}
	if name == "mihomo" {
		args = []string{"-f", config, "-d", filepath.Join(f.dir, "state")}
	}
	command := exec.CommandContext(ctx, core, args...)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
	waitForProxy(t, address)

	proxyURL, _ := url.Parse("http://" + address)
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	checkProxyRoutes(t, f, client, origin)
}

func checkProxyRoutes(t *testing.T, f *fixture, client *http.Client, origin *url.URL) {
	t.Helper()
	response, err := client.Get(origin.String())
	if err != nil {
		t.Fatalf("direct host request: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(body), "#node") {
		t.Fatalf("direct host response: status=%d error=%v", response.StatusCode, err)
	}

	// The same endpoint by its unlisted IP must still use the unreachable proxy.
	before := f.count("/one")
	response, err = client.Get(f.server.URL + "/one")
	if err == nil {
		_ = response.Body.Close()
	}
	if f.count("/one") != before {
		t.Fatal("unlisted host bypassed the proxy")
	}
}

func waitForProxy(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("real core did not start its mixed proxy")
}
