package subscription

import (
	"context"
	"encoding/base64"
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

func TestNativeFetchLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", nativeLimit+1))
	}))
	defer server.Close()
	_, err := (Native{}).Fetch(context.Background(), Source{ID: "test", URL: server.URL})
	if !errors.Is(err, errNativeTooLarge) {
		t.Fatalf("limit: %v", err)
	}
}
