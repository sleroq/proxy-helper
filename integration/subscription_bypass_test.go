package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type bypassObservation struct {
	config   string
	cache    string
	restarts int
}

func TestSubscriptionBypassActivatedBeforeFetch(t *testing.T) {
	for _, backend := range []string{"sing-box", "mihomo"} {
		for _, change := range []string{"added source", "changed URL file"} {
			t.Run(backend+"/"+change, func(t *testing.T) {
				checkSubscriptionBypassBeforeFetch(t, backend, change)
			})
		}
	}
}

func checkSubscriptionBypassBeforeFetch(t *testing.T, backend, change string) {
	t.Helper()
	f := newBypassFixture(t, backend)
	urlFile := filepath.Join(f.dir, "provider-url")
	if change == "changed URL file" {
		if err := os.WriteFile(urlFile, []byte(f.server.URL+"/one\n"), 0600); err != nil {
			t.Fatal(err)
		}
		f.write("declared.json", map[string]any{"subscriptions": []any{
			map[string]any{"id": "one", "url_file": urlFile}, f.source("two", "/two"),
		}})
	}
	f.run("", true, "update")

	observed := f.observeSubscriptionFetch(t, urlFile)
	id := "one"
	if change == "added source" {
		id = "new"
		f.run("", true, "subscription", "add", "--id", id, "--url-file", urlFile)
	}
	f.run("", true, "update", id)
	checkPreFetchState(t, <-observed, change)
	if strings.Count(f.read("restarts"), "restart\n") != 3 {
		t.Fatal("expected pre-fetch and final restarts")
	}
}

func checkPreFetchState(t *testing.T, got bypassObservation, change string) {
	t.Helper()
	if got.restarts != 2 || !strings.Contains(got.config, "localhost") {
		t.Fatalf("host rule was not active before fetch: %+v", got)
	}
	if strings.Contains(got.config, "token=private") {
		t.Fatal("routing configuration exposed the subscription URL")
	}
	if !strings.Contains(got.cache, `"one"`) || change == "added source" && strings.Contains(got.cache, `"new"`) {
		t.Fatal("pre-fetch activation modified the last-good cache")
	}
}

func newBypassFixture(t *testing.T, backend string) *fixture {
	t.Helper()
	var f *fixture
	if backend == "mihomo" {
		f = newMihomoFixture(t)
	} else {
		f = newFixture(t)
	}
	f.config["restart_command"] = []string{helper, "restart-ok", filepath.Join(f.dir, "restarts")}
	f.write("config.json", f.config)
	return f
}

func (f *fixture) observeSubscriptionFetch(t *testing.T, urlFile string) <-chan bypassObservation {
	t.Helper()
	observed := make(chan bypassObservation, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		config, _ := os.ReadFile(filepath.Join(f.dir, "state/config.json"))
		cache, _ := os.ReadFile(filepath.Join(f.dir, "state/cache.json"))
		restarts, _ := os.ReadFile(filepath.Join(f.dir, "restarts"))
		observed <- bypassObservation{string(config), string(cache), strings.Count(string(restarts), "restart\n")}
		_, _ = fmt.Fprint(w, "trojan://password@new.example:443#new")
	}))
	t.Cleanup(origin.Close)
	address, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	address.Host = "localhost:" + address.Port()
	if err := os.WriteFile(urlFile, []byte(address.String()+"/sub?token=private\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return observed
}

func TestDisableSubscriptionBypassBeforeFetch(t *testing.T) {
	for _, backend := range []string{"sing-box", "mihomo"} {
		t.Run(backend, func(t *testing.T) {
			f := newBypassFixture(t, backend)
			f.run("", true, "update")
			observed := make(chan bypassObservation, 1)
			proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				config, _ := os.ReadFile(filepath.Join(f.dir, "state/config.json"))
				restarts, _ := os.ReadFile(filepath.Join(f.dir, "restarts"))
				observed <- bypassObservation{config: string(config), restarts: strings.Count(string(restarts), "restart\n")}
				_, _ = fmt.Fprint(w, "trojan://password@new.example:443#new")
			}))
			defer proxyServer.Close()
			t.Setenv("HTTP_PROXY", proxyServer.URL)
			t.Setenv("http_proxy", "")
			t.Setenv("NO_PROXY", "")
			t.Setenv("no_proxy", "")
			f.write("declared.json", map[string]any{"subscriptions": []any{
				map[string]any{"id": "one", "url": "http://provider.invalid/sub"}, f.source("two", "/two"),
			}})
			f.run("", true, "--no-subscription-bypass", "update", "one")
			got := <-observed
			if got.restarts != 2 || strings.Contains(got.config, "ph-subscription-local") || strings.Contains(got.config, "IP-CIDR,127.0.0.1/32,DIRECT") {
				t.Fatalf("bypass was not disabled before fetch: %+v", got)
			}
		})
	}
}

func TestSubscriptionBypassRequiresRestartBeforeNewHost(t *testing.T) {
	f := newFixture(t)
	f.run("", true, "update")
	config := f.read("state/config.json")
	f.write("local.json", map[string]any{"subscriptions": []any{
		map[string]any{"id": "new", "url": "http://new.example.invalid/sub"},
	}})
	if output := f.run("", false, "update", "new"); !strings.Contains(output, "require restart_command") {
		t.Fatal(output)
	}
	if f.read("state/config.json") != config || f.count("/one") != 1 {
		t.Fatal("failed pre-fetch activation changed config or fetched sources")
	}
}

func TestSubscriptionBypassRejectsRestartThatReplacesRules(t *testing.T) {
	f := newFixture(t)
	f.config["restart_command"] = []string{binary, "--config", filepath.Join(f.dir, "config.json"), "prepare"}
	f.write("config.json", f.config)
	f.run("", true, "update")
	output := f.run("", false, "--no-subscription-bypass", "update")
	if !strings.Contains(output, "restart did not preserve subscription bypass configuration") {
		t.Fatal(output)
	}
	if f.count("/one") != 1 {
		t.Fatal("fetch proceeded after restart replaced the requested rules")
	}
}

func TestSubscriptionBypassRestartFailureStopsFetch(t *testing.T) {
	f := newFixture(t)
	f.config["restart_command"] = []string{helper, "restart-ok", filepath.Join(f.dir, "restarts")}
	f.write("config.json", f.config)
	f.run("", true, "update")
	cache := f.read("state/cache.json")
	f.write("local.json", map[string]any{"subscriptions": []any{
		map[string]any{"id": "new", "url": "http://new.example.invalid/sub"},
	}})
	f.config["restart_command"] = []string{helper, "restart"}
	f.write("config.json", f.config)
	if output := f.run("", false, "update", "new"); !strings.Contains(output, "service restart failed") {
		t.Fatal(output)
	}
	if f.read("state/cache.json") != cache || !strings.Contains(f.read("state/config.json"), "new.example.invalid") {
		t.Fatal("restart failure did not preserve cache and installed routing")
	}
	f.config["restart_command"] = []string{helper, "restart-ok", filepath.Join(f.dir, "restarts")}
	f.write("config.json", f.config)
	f.run("", false, "update", "new") // The nonexistent host still cannot be fetched.
	if strings.Count(f.read("restarts"), "restart\n") != 2 {
		t.Fatal("retry fetched without reactivating the previously installed rules")
	}
}

func TestSubscriptionBypassPreservesSourceFailures(t *testing.T) {
	f := newFixture(t)
	f.config["restart_command"] = []string{helper, "restart-ok", filepath.Join(f.dir, "restarts")}
	f.write("config.json", f.config)
	f.run("", true, "update")
	f.write("local.json", map[string]any{"subscriptions": []any{
		map[string]any{"id": "broken", "url_file": filepath.Join(f.dir, "missing")},
	}})
	output := f.run("", false, "update")
	if !strings.Contains(output, "updated: [one two]") || !strings.Contains(output, "unavailable: [broken]") {
		t.Fatal(output)
	}
	var cache map[string]json.RawMessage
	if err := json.Unmarshal([]byte(f.read("state/cache.json")), &cache); err != nil || cache["one"] == nil || cache["broken"] == nil {
		t.Fatalf("partial refresh lost sources: %v", err)
	}
}
