package integration_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSubscriptionProxyEnvironment(t *testing.T) {
	for _, variable := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Run(variable, func(t *testing.T) {
			testSubscriptionProxy(t, variable)
		})
	}
}

func TestSubscriptionBypassIgnoresProxyEnvironment(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	f := newFixture(t)
	requests := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		http.Error(w, "unexpected proxy", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	origin, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	origin.Host = "localhost.:" + origin.Port()
	origin.Path = "/one"
	f.write("declared.json", map[string]any{"subscriptions": []any{
		map[string]any{"id": "one", "url": origin.String()},
	}})
	f.run("", true, "update")
	if f.count("/one") != 1 {
		t.Fatal("subscription did not reach origin directly")
	}
	select {
	case <-requests:
		t.Fatal("subscription reached environment proxy")
	default:
	}
}

func testSubscriptionProxy(t *testing.T, variable string) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy", "REQUEST_METHOD"} {
		t.Setenv(key, "")
	}
	f := newFixture(t)
	body := "trojan://password@node.example:443#node"

	requests := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.Host
		if r.Method == http.MethodConnect {
			// Stop at CONNECT: this test verifies proxy selection, not TLS trust.
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = fmt.Fprint(w, body)
	}))
	defer proxy.Close()
	t.Setenv(variable, proxy.URL)

	scheme, want := "http", "GET provider.invalid"
	https := strings.EqualFold(variable, "HTTPS_PROXY")
	if https {
		scheme, want = "https", "CONNECT provider.invalid:443"
	}
	f.write("declared.json", map[string]any{"subscriptions": []any{
		map[string]any{"id": "main", "url": scheme + "://provider.invalid/subscription?token=private"},
	}})
	f.run("", !https, "--no-subscription-bypass", "update")

	select {
	case got := <-requests:
		if got != want {
			t.Fatalf("proxy request = %q, want %q", got, want)
		}
	default:
		t.Fatal("subscription request did not reach the environment proxy")
	}
}
