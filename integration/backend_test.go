package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func managedFixture(t *testing.T) *fixture {
	t.Helper()
	f := newMihomoFixture(t)
	f.config["backends"] = map[string]any{
		"mihomo":   map[string]string{"template_file": "template.json", "binary": helper},
		"sing-box": map[string]string{"template_file": "singbox.json", "binary": helper},
	}
	f.write("singbox.json", map[string]any{"route": map[string]any{"final": "proxy"}})
	f.write("config.json", f.config)
	f.run("", true, "update")
	return f
}

func TestManagedBackendSelection(t *testing.T) {
	f := managedFixture(t)
	if output := f.run("", true, "backend", "list"); !strings.Contains(output, "* mihomo") || !strings.Contains(output, "sing-box") {
		t.Fatal(output)
	}
	f.run("", false, "backend", "select")
	f.run("", true, "backend", "select", "sing-box")
	if output := f.run("", true, "backend", "list"); !strings.Contains(output, "* sing-box") {
		t.Fatal(output)
	}
	f.run("", true, "check") // Resolves the persisted adapter, not the default.
	f.run("", false, "backend", "select", "unknown")

	// A removed active backend does not block repair through backend select.
	f.config["backend"] = "sing-box"
	f.write("config.json", f.config)
	if err := os.Remove(filepath.Join(f.dir, "state", "active-backend.json")); err != nil {
		t.Fatal(err)
	}
	f.run("", true, "backend", "select", "mihomo")
}

func TestManagedBackendValidationKeepsInstalledState(t *testing.T) {
	f := managedFixture(t)
	installed := f.read("state/config.json")
	restarts := f.read("restarts")
	f.write("singbox.json", map[string]any{"reject": true})
	f.run("", false, "backend", "select", "sing-box")
	if got := f.read("state/config.json"); got != installed {
		t.Fatal("validation replaced config")
	}
	if got := f.read("restarts"); got != restarts {
		t.Fatal("validation restarted service")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "state", "active-backend.json")); !os.IsNotExist(err) {
		t.Fatalf("active record created: %v", err)
	}
}

func TestManagedBackendFailedRestartRestoresState(t *testing.T) {
	f := managedFixture(t)
	installed := f.read("state/config.json")
	f.config["restart_command"] = []string{helper, "restart"}
	f.write("config.json", f.config)
	if output := f.run("", false, "backend", "select", "sing-box"); !strings.Contains(output, "rollback failed") {
		t.Fatal(output)
	}
	if got := f.read("state/config.json"); got != installed {
		t.Fatal("config not restored")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "state", "active-backend.json")); !os.IsNotExist(err) {
		t.Fatalf("missing active record not restored: %v", err)
	}
}

func TestManagedBackendReadinessRollback(t *testing.T) {
	f := managedFixture(t)
	installed := f.read("state/config.json")
	restarts := filepath.Join(f.dir, "restarts")
	// The old service answers until restart #1; the replacement stays unready.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if data, err := os.ReadFile(restarts); err == nil && strings.Count(string(data), "restart\n") == 2 {
			http.Error(w, "replacement not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"now":"auto","all":["auto"]}`)
	}))
	defer api.Close()
	f.config["api_url"] = api.URL
	f.write("config.json", f.config)
	if output := f.run("", false, "backend", "select", "sing-box"); !strings.Contains(output, "readiness") {
		t.Fatal(output)
	}
	if got := f.read("state/config.json"); got != installed {
		t.Fatal("config not restored")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "state", "active-backend.json")); !os.IsNotExist(err) {
		t.Fatalf("missing active record not restored: %v", err)
	}
	if count := strings.Count(f.read("restarts"), "restart\n"); count != 3 {
		t.Fatalf("restart count = %d; expected update, switch, rollback", count)
	}
}

func TestManagedBackendRestartReplacesServingClient(t *testing.T) {
	f := managedFixture(t)
	marker := filepath.Join(f.dir, "restarts")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile(marker)
		if strings.Count(string(data), "restart\n") < 2 {
			_, _ = fmt.Fprint(w, `{"now":"old","all":["old"]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"now":"auto","all":["auto"]}`)
	}))
	defer api.Close()
	response, err := http.Get(api.URL + "/proxies/proxy")
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Now string `json:"now"`
	}
	if err := json.NewDecoder(response.Body).Decode(&old); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if old.Now != "old" {
		t.Fatalf("old client not serving: %q", old.Now)
	}
	f.config["api_url"] = api.URL
	f.write("config.json", f.config)
	f.run("", true, "backend", "select", "sing-box")
	if count := strings.Count(f.read("restarts"), "restart\n"); count != 2 {
		t.Fatalf("restart count = %d", count)
	}
}
