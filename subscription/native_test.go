package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeParse(t *testing.T) {
	lines := strings.Join([]string{
		"bad",
		"ss://YWVzLTEyOC1nY206cGFzcw@example.com:443#same",
		"ss://YWVzLTEyOC1nY206cGFzcw@example.org:443#same",
		"trojan://pass@example.net:443#other",
		"vless://uuid@example.com:443?security=reality&pbk=key&sid=abc#vless",
		"",
	}, "\n")
	n := Native{}
	nodes, err := n.Parse(strings.NewReader(lines), Source{Prefix: "pre-", ExcludeProtocols: "trojan"})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 || nodes[0].String("tag") != "pre-same" || nodes[1].String("tag") != "pre-same-2" {
		t.Fatalf("nodes: %v", nodes)
	}
	testNativeEncoded(t, n, lines)
}

func testNativeEncoded(t *testing.T, n Native, lines string) {
	t.Helper()
	encoded := base64.RawURLEncoding.EncodeToString([]byte(lines))
	nodes, err := n.Parse(strings.NewReader(encoded), Source{ExcludeNodeNames: "same"})
	if err != nil || len(nodes) != 2 {
		t.Fatalf("encoded: %v %v", nodes, err)
	}
	vmessJSON := `{"add":"example.com","port":"443","id":"uuid","ps":"vm",` +
		`"net":"ws","host":"example.com","path":"/ws","tls":"tls"}`
	vmess := base64.StdEncoding.EncodeToString([]byte(vmessJSON))
	nodes, err = n.Parse(strings.NewReader("vmess://"+vmess), Source{})
	if err != nil || len(nodes) != 1 || nodes[0].String("type") != "vmess" {
		t.Fatalf("vmess: %v %v", nodes, err)
	}
	if _, err = n.Parse(strings.NewReader("bad"), Source{}); err == nil {
		t.Fatal("expected no nodes")
	}
}

func TestNativeURIFingerprint(t *testing.T) {
	for _, tt := range []struct {
		name        string
		uri         string
		fingerprint string
	}{
		{"Reality", "vless://uuid@example.com:443?security=reality&pbk=key&fp=chrome", "chrome"},
		{"TLS", "vless://uuid@example.com:443?security=tls&fp=firefox", "firefox"},
		{"Trojan", "trojan://password@example.com:443?fp=safari", "safari"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nodes, err := (Native{}).Parse(strings.NewReader(tt.uri), Source{})
			if err != nil {
				t.Fatal(err)
			}

			var tls struct {
				Enabled bool `json:"enabled"`
				UTLS    struct {
					Enabled     bool   `json:"enabled"`
					Fingerprint string `json:"fingerprint"`
				} `json:"utls"`
			}
			if err := json.Unmarshal(nodes[0]["tls"], &tls); err != nil {
				t.Fatal(err)
			}
			if !tls.Enabled || !tls.UTLS.Enabled || tls.UTLS.Fingerprint != tt.fingerprint {
				t.Fatalf("TLS fingerprint not preserved: %+v", tls)
			}
		})
	}
}

func TestNativeFetchLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", responseLimit+1))
	}))
	defer server.Close()
	_, err := (Native{}).Fetch(context.Background(), Source{ID: "test", URL: server.URL})
	if !errors.Is(err, errResponseTooLarge) {
		t.Fatalf("limit: %v", err)
	}
}
