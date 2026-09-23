// These tests exercise the compiled CLI through real processes, HTTP and disk.
// No production functions are called directly.
package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

var binary, helper string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sb-e2e-*")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "sb")
	helper = filepath.Join(dir, "helper")
	for _, build := range [][2]string{{binary, "../cmd/sb"}, {helper, "./testdata/helper"}} {
		cmd := exec.Command("go", "build", "-o", build[0], build[1])
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type fixture struct {
	t           *testing.T
	dir         string
	config      map[string]any
	server      *httptest.Server
	mu          sync.Mutex
	bodies      map[string]string
	requests    map[string]int
	selected    string
	blockedPath string
	entered     chan struct{}
	release     chan struct{}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), bodies: map[string]string{}, requests: map[string]int{}, selected: "auto"}

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests[r.URL.Path]++
		blocked := f.blockedPath == r.URL.Path && (r.URL.Path != "/proxies/proxy" || r.Method == http.MethodPut)
		entered := f.entered
		release := f.release
		selected := f.selected
		body, exists := f.bodies[r.URL.Path]
		f.mu.Unlock()
		if blocked {
			select {
			case entered <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		switch {
		case r.URL.Path == "/proxies/proxy":
			if r.Method == http.MethodPut {
				var body struct {
					Name string `json:"name"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					w.WriteHeader(400)
					return
				}
				f.mu.Lock()
				f.selected = body.Name
				f.mu.Unlock()
				w.WriteHeader(204)
				return
			}
			all := []string{"auto", "a", "b"}
			if _, err := os.Stat(filepath.Join(f.dir, "static.json")); err == nil {
				all = append(all, "auto-custom")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"now": selected, "all": all})
		case strings.HasPrefix(r.URL.Path, "/group/"):
			_ = json.NewEncoder(w).Encode(map[string]int{"a": 80, "b": 20})
		default:
			if !exists {
				http.NotFound(w, r)
				return
			}
			_, _ = fmt.Fprint(w, body)
		}
	}))
	t.Cleanup(f.server.Close)

	f.config = map[string]any{
		"template_file": "template.json", "state_dir": "state", "api_url": f.server.URL,
		"sing_box": helper, "converter": helper, "overrides_file": "overrides.json",
		"stores": []any{map[string]any{"name": "declared", "path": "declared.json", "writable": false}, map[string]any{"name": "local", "path": "local.json", "writable": true}},
	}
	f.write("template.json", map[string]any{"route": map[string]any{"final": "proxy"}})
	f.write("declared.json", map[string]any{"subscriptions": []any{f.source("one", "/one"), f.source("two", "/two")}})
	f.write("config.json", f.config)
	f.setBody("/one", `[{"type":"shadowsocks","tag":"a","server":"one.example",
		"password":"secret-one","server_port":443,"future_number":9007199254740993}]`)
	f.setBody("/two", `[{"type":"shadowsocks","tag":"b","server":"two.example",
		"password":"secret-two","server_port":443}]`)
	return f
}

func (f *fixture) source(id, path string) map[string]any {
	return map[string]any{"id": id, "url": f.server.URL + path + "?token=private"}
}
func (f *fixture) write(name string, value any) {
	f.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, name), data, 0600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) run(input string, wantOK bool, args ...string) string {
	f.t.Helper()
	command := exec.Command(binary, append([]string{"--config", filepath.Join(f.dir, "config.json")}, args...)...)
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if (err == nil) != wantOK {
		f.t.Fatalf("sb %v: %v\n%s", args, err, output)
	}
	if strings.Contains(string(output), "token=private") || strings.Contains(string(output), "secret-one") {
		f.t.Fatalf("credential leaked: %s", output)
	}
	return string(output)
}
func (f *fixture) read(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}
func (f *fixture) setBody(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[path] = body
}

func (f *fixture) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[path]
}

func TestUsePicker(t *testing.T) {
	f := newFixture(t)
	f.run("", true, "update")
	if output := f.run("", false, "use"); !strings.Contains(output, "use requires a tag without a terminal") {
		t.Fatal(output)
	}
	f.run("", true, "use", "a")

	for _, tc := range []struct{ name, keys, want string }{
		{"cancel", "\x03", "a"},
		{"filter", "/b\r\r", "b"},
		{"navigation", "k\r", "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--config", filepath.Join(f.dir, "config.json"), "use")
			terminal, err := pty.Start(cmd)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = terminal.Close() }()
			output := make(chan string, 1)
			go func() { data, _ := io.ReadAll(terminal); output <- string(data) }()
			time.Sleep(200 * time.Millisecond)
			if _, err := terminal.Write([]byte(tc.keys)); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			if ctx.Err() != nil {
				t.Fatal("picker did not exit promptly")
			}
			if err != nil {
				t.Fatal(err)
			}
			screen := <-output
			if !strings.Contains(screen, "\x1b[?1049l") {
				t.Fatalf("alternate screen not restored: %q", screen)
			}
			f.mu.Lock()
			got := f.selected
			f.mu.Unlock()
			if got != tc.want {
				t.Fatalf("selection = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPickerBlockedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, path, input string
		signal            bool
	}{
		{"latency ctrl+c", "/group/auto/delay", "\x03", false},
		{"selection SIGINT", "/proxies/proxy", "\r", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.run("", true, "update")

			f.mu.Lock()
			f.blockedPath, f.entered, f.release = tc.path, make(chan struct{}, 1), make(chan struct{})
			defer close(f.release)
			f.mu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--config", filepath.Join(f.dir, "config.json"), "use")
			terminal, err := pty.Start(cmd)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = terminal.Close() }()
			output := make(chan string, 1)
			go func() { data, _ := io.ReadAll(terminal); output <- string(data) }()

			if tc.signal {
				// Let the picker render before selecting its default leaf.
				time.Sleep(150 * time.Millisecond)
				if _, err := terminal.Write([]byte(tc.input)); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-f.entered:
			case <-ctx.Done():
				t.Fatal("request never blocked")
			}
			if tc.signal {
				if err := cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			} else if _, err := terminal.Write([]byte(tc.input)); err != nil {
				t.Fatal(err)
			}

			err = cmd.Wait()
			screen := <-output
			if err != nil || ctx.Err() != nil {
				t.Fatalf("interrupted picker: %v (context: %v): %q", err, ctx.Err(), screen)
			}
			if !strings.Contains(screen, "\x1b[?1049l") {
				t.Fatalf("alternate screen not restored: %q", screen)
			}
			f.mu.Lock()
			selected := f.selected
			f.mu.Unlock()
			if selected != "auto" {
				t.Fatalf("selection changed to %q", selected)
			}
		})
	}
}

func TestPickerAutoPrefixedExtraLeaf(t *testing.T) {
	f := newFixture(t)
	f.write("static.json", []any{map[string]any{
		"type": "shadowsocks", "tag": "auto-custom", "server": "extra.example",
		"server_port": 443, "password": "secret",
	}})
	f.config["static_outbounds_file"] = "static.json"
	f.write("config.json", f.config)
	f.run("", true, "update")
	if public := f.read("state/subscription.json"); strings.Contains(public, "extra.example") || strings.Contains(public, "secret") {
		t.Fatal("extra outbound details leaked into public manifest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--config", filepath.Join(f.dir, "config.json"), "use")
	terminal, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = terminal.Close() }()
	output := make(chan string, 1)
	go func() { data, _ := io.ReadAll(terminal); output <- string(data) }()
	time.Sleep(150 * time.Millisecond)
	_, _ = terminal.Write([]byte("/auto-custom\r\r"))
	if err := cmd.Wait(); err != nil || ctx.Err() != nil {
		t.Fatalf("select extra leaf: %v (context: %v)", err, ctx.Err())
	}
	if screen := <-output; !strings.Contains(screen, "auto-custom") {
		t.Fatalf("leaf missing from picker: %q", screen)
	}
	f.mu.Lock()
	selected := f.selected
	f.mu.Unlock()
	if selected != "auto-custom" {
		t.Fatalf("selected %q", selected)
	}
}

func TestPinPersistence(t *testing.T) {
	f := newFixture(t)
	f.run("", true, "update")
	f.run("", true, "use", "b")
	if strings.Contains(f.read("state/config.json"), `"default": "b"`) {
		t.Fatal("use persisted")
	}
	f.run("", false, "pin", "auto")
	if _, err := os.Stat(filepath.Join(f.dir, "state", "pin.json")); !os.IsNotExist(err) {
		t.Fatal("invalid pin saved", err)
	}
	f.run("", true, "pin", "b")
	if !strings.Contains(f.read("state/config.json"), `"default": "b"`) {
		t.Fatal("pin not installed")
	}
	if info, err := os.Stat(filepath.Join(f.dir, "state", "pin.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("pin permissions: %v", err)
	}
	f.run("", true, "prepare")
	if !strings.Contains(f.read("state/config.json"), `"default": "b"`) {
		t.Fatal("prepare lost pin")
	}
	f.run("", true, "subscription", "disable", "two")
	f.run("", true, "apply")
	if !strings.Contains(f.read("state/subscription.json"), `"pinnedTag": "b"`) || !strings.Contains(f.run("", true, "status"), "stale/inactive") {
		t.Fatal("stale pin not reported")
	}
	if strings.Contains(f.read("state/config.json"), `"default": "b"`) {
		t.Fatal("stale pin selected")
	}
	f.run("", true, "unpin")
	if !strings.Contains(f.read("state/config.json"), `"default": "auto"`) {
		t.Fatal("unpin did not restore default")
	}
}

func TestCLIWorkflow(t *testing.T) {
	f := newFixture(t)
	if !t.Run("initial update", func(t *testing.T) {
		f.t = t
		f.workflowUpdate(t)
	}) {
		return
	}
	if !t.Run("commands", func(t *testing.T) {
		f.t = t
		f.workflowCommands(t)
	}) {
		return
	}
	if !t.Run("subscription policies", func(t *testing.T) {
		f.t = t
		f.workflowPolicies(t)
	}) {
		return
	}
	t.Run("writable store symlink", func(t *testing.T) {
		f.t = t
		f.workflowSymlink(t)
	})
}

func (f *fixture) workflowUpdate(t *testing.T) {
	f.run("", true, "update")
	config := f.read("state/config.json")
	if !strings.Contains(config, "9007199254740993") || !strings.Contains(config, "auto-one") {
		t.Fatal("composition lost unknown fields or source groups")
	}
	for _, name := range []string{"cache.json", "config.json"} {
		stat, err := os.Stat(filepath.Join(f.dir, "state", name))
		if err != nil || stat.Mode().Perm() != 0600 {
			t.Fatalf("private permissions %s: %v", name, err)
		}
	}
	manifest := f.read("state/subscription.json")
	if strings.Contains(manifest, "password") {
		t.Fatal("public manifest contains credentials")
	}
}

func (f *fixture) workflowCommands(t *testing.T) {
	if output := f.run("", true, "list"); !strings.Contains(output, "a [one]") {
		t.Fatal(output)
	}
	if output := f.run("", true, "status"); !strings.Contains(output, "nodes: 2") {
		t.Fatal(output)
	}
	if output := f.run("", true, "test"); strings.Index(output, "20 ms") > strings.Index(output, "80 ms") {
		t.Fatal(output)
	}
	f.run("", true, "use", "b")
	if output := f.run("", true, "status"); !strings.Contains(output, "selected: b") {
		t.Fatal(output)
	}
	if output := f.run("", true, "config"); !strings.Contains(output, "<redacted>") {
		t.Fatal(output)
	}
	f.run("", true, "check")
}

func (f *fixture) workflowPolicies(t *testing.T) {
	before := f.count("/two")
	f.run("", true, "subscription", "update", "one")
	if f.count("/two") != before {
		t.Fatal("targeted update fetched unrelated source")
	}
	f.run("", true, "subscription", "auto", "one", "--exclude-server", "one\\.example")
	f.run("", true, "apply")
	config := f.read("state/config.json")
	if strings.Contains(config, "auto-one") || !strings.Contains(config, `"tag": "a"`) {
		t.Fatal("automatic exclusion removed manual node or retained empty group")
	}
	f.run("", true, "subscription", "auto", "two", "--enabled", "false")
	f.run("", true, "apply")
	if strings.Contains(f.read("state/config.json"), `"type": "urltest"`) {
		t.Fatal("empty auto group emitted")
	}
	f.run("", false, "subscription", "delete", "one")
	f.run("", true, "subscription", "disable", "one")
	f.run("", true, "apply")
	if strings.Contains(f.read("state/config.json"), `"tag": "a"`) {
		t.Fatal("disabled source retained")
	}
	f.run("", true, "subscription", "reset", "one")
	f.run("", true, "apply")
	if !strings.Contains(f.read("state/config.json"), "auto-one") {
		t.Fatal("reset did not restore declared policy")
	}
}

func (f *fixture) workflowSymlink(t *testing.T) {
	// Editing a symlink replaces the target, not the link itself.
	f.write("editable.json", map[string]any{"subscriptions": []any{}})
	if err := os.Symlink("editable.json", filepath.Join(f.dir, "local.json")); err != nil {
		t.Fatal(err)
	}
	source, _ := json.Marshal(f.source("three", "/three"))
	f.run(string(source), true, "subscription", "add")
	link, err := os.Readlink(filepath.Join(f.dir, "local.json"))
	if err != nil || link != "editable.json" {
		t.Fatal("store symlink replaced")
	}
	f.setBody("/three", `[{"type":"shadowsocks","tag":"c","server":"three.example"}]`)
	f.run("", true, "update", "three")
	f.run("", true, "subscription", "delete", "three")
	f.run("", true, "apply")
	if strings.Contains(f.read("state/cache.json"), `"three"`) || strings.Contains(f.read("state/health.json"), `"three"`) {
		t.Fatal("deleted source retained after apply")
	}
}

func TestFailedUpdatePreservesInstalledState(t *testing.T) {
	for _, failure := range []string{"collision", "core"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			f.run("", true, "update")
			before := map[string]string{}
			for _, name := range []string{"config.json", "cache.json", "subscription.json"} {
				before[name] = f.read("state/" + name)
			}
			switch failure {
			case "collision":
				f.setBody("/two", `[{"type":"shadowsocks","tag":"a","server":"two.example"}]`)
			case "core":
				f.write("template.json", map[string]any{"reject": true})
			}
			f.run("", false, "update")
			for name, want := range before {
				if f.read("state/"+name) != want {
					t.Fatalf("failed update modified %s", name)
				}
			}
			if status := f.run("", true, "status"); !strings.Contains(status, "fetched, not installed") {
				t.Fatal("candidate failure was reported as installed: ", status)
			}
		})
	}
}

func TestPartialAndAllFailedUpdates(t *testing.T) {
	f := newFixture(t)
	f.run("", true, "update")

	if !t.Run("partial refresh", func(t *testing.T) {
		f.t = t
		f.partialRefresh(t)
	}) {
		return
	}
	if !t.Run("all failed refresh", func(t *testing.T) {
		f.t = t
		f.allFailedRefresh(t)
	}) {
		return
	}
	t.Run("targeted recovery and metadata", func(t *testing.T) {
		f.t = t
		f.targetedRecovery(t)
	})
}

func (f *fixture) partialRefresh(t *testing.T) {
	old := f.read("state/cache.json")
	f.setBody("/one", `[{"type":"shadowsocks","tag":"new","server":"new.example"}]`)
	f.mu.Lock()
	delete(f.bodies, "/two")
	f.mu.Unlock()
	if output := f.run("", false, "update"); !strings.Contains(output, "updated: [one]; stale: [two]") {
		t.Fatal(output)
	}
	if f.read("state/cache.json") == old ||
		!strings.Contains(f.read("state/config.json"), "new.example") ||
		!strings.Contains(f.read("state/config.json"), "two.example") {
		t.Fatal("mixed snapshot not installed")
	}
}

func (f *fixture) allFailedRefresh(t *testing.T) {
	installed := f.read("state/config.json")
	f.mu.Lock()
	delete(f.bodies, "/one")
	f.mu.Unlock()
	f.config["restart_command"] = []string{helper, "restart"}
	f.write("config.json", f.config)
	if output := f.run("", false, "update"); strings.Contains(output, "restart failed") ||
		!strings.Contains(output, "no subscription fetch succeeded") {
		t.Fatal("all-failed refresh restarted: ", output)
	}
	delete(f.config, "restart_command")
	f.write("config.json", f.config)
	if f.read("state/config.json") != installed {
		t.Fatal("all failed update installed config")
	}
	if status := f.run("", true, "status"); !strings.Contains(status, "one: stale") || !strings.Contains(status, "two: stale") {
		t.Fatal(status)
	}
}

func (f *fixture) targetedRecovery(t *testing.T) {
	f.setBody("/one", `[{"type":"shadowsocks","tag":"newer","server":"newer.example"}]`)
	f.run("", true, "update", "one")
	if status := f.run("", true, "status"); !strings.Contains(status, "two: stale") {
		t.Fatal("targeted update erased other health: ", status)
	}
	for _, name := range []string{"health.json", "subscription.json"} {
		data := f.read("state/" + name)
		if strings.Contains(data, "token=private") || strings.Contains(data, "secret-one") {
			t.Fatal("public metadata leaked credentials")
		}
		if stat, err := os.Stat(filepath.Join(f.dir, "state", name)); err != nil || stat.Mode().Perm() != 0644 {
			t.Fatalf("public permissions %s: %v", name, err)
		}
	}
}

func TestAllFailedFirstBootDoesNotInstall(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.dir, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	f.write("state/config.json", map[string]any{"existing": true})
	before := f.read("state/config.json")
	f.mu.Lock()
	delete(f.bodies, "/one")
	delete(f.bodies, "/two")
	f.mu.Unlock()
	if output := f.run("", false, "update"); !strings.Contains(output, "unavailable: [one two]") {
		t.Fatal(output)
	}
	if f.read("state/config.json") != before {
		t.Fatal("first-boot failure replaced installed configuration")
	}
	if status := f.run("", true, "status"); !strings.Contains(status, "one: unavailable") || !strings.Contains(status, "two: unavailable") {
		t.Fatal(status)
	}
}

func TestUpdateWithNoSubscriptionsAndStaticOutbound(t *testing.T) {
	f := newFixture(t)
	f.write("declared.json", map[string]any{"subscriptions": []any{}})
	f.write("static.json", json.RawMessage(`[{"type":"shadowsocks","tag":"static","server":"static.example"}]`))
	f.config["static_outbounds_file"] = "static.json"
	f.write("config.json", f.config)
	f.run("", true, "update")
	if !strings.Contains(f.read("state/config.json"), "static.example") {
		t.Fatal("static-only config not installed")
	}
}

func TestOversizeResponse(t *testing.T) {
	f := newFixture(t)
	f.setBody("/one", strings.Repeat("x", (8<<20)+1))
	if output := f.run("", false, "update"); !strings.Contains(f.read("state/health.json"), "subscription response too large") || !strings.Contains(output, "unavailable: [one]") {
		t.Fatal(output)
	}
	f.run("", true, "prepare")
}

func TestManySourcesAndLegacyMigration(t *testing.T) {
	f := newFixture(t)
	var sources []any
	for i := range 12 {
		id := fmt.Sprintf("source%d", i)
		path := "/" + id
		sources = append(sources, f.source(id, path))
		f.setBody(path, fmt.Sprintf(`[{"tag":"node%d","type":"shadowsocks","server":"host%d.example"}]`, i, i))
	}
	f.write("declared.json", map[string]any{"subscriptions": sources})
	f.run("", true, "update")
	var manifest struct {
		Nodes []struct{ Tag, Source string }
	}
	if err := json.Unmarshal([]byte(f.read("state/subscription.json")), &manifest); err != nil {
		t.Fatal(err)
	}
	for i, node := range manifest.Nodes {
		if node.Source != fmt.Sprintf("source%d", i) || node.Tag != fmt.Sprintf("node%d", i) {
			t.Fatal("source ordering corrupted")
		}
	}

	g := newFixture(t)
	if err := os.MkdirAll(filepath.Join(g.dir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	g.write("state/subscription-outbounds.json", json.RawMessage(`[
		{"tag":"a","type":"shadowsocks","server":"one.example"},
		{"tag":"b","type":"shadowsocks","server":"two.example"}
	]`))
	g.write("state/subscription-sources.json", map[string]any{"one": []string{"a"}, "two": []string{"b"}})
	g.run("", true, "prepare")
	if g.count("/one") != 0 {
		t.Fatal("migration fetched subscriptions")
	}
	if !strings.Contains(g.read("state/cache.json"), `"one"`) {
		t.Fatal("migration missing source")
	}
}

func TestUpdateBootstrapsLegacyCacheWithoutSourceMapping(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.dir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	f.write("state/subscription-outbounds.json", json.RawMessage(`[
		{"tag":"old","type":"shadowsocks","server":"old.example"}
	]`))
	f.write("state/config.json", map[string]any{"existing": true})
	previous := f.read("state/config.json")
	if output := f.run("", false, "prepare"); !strings.Contains(output, "has no cached nodes") {
		t.Fatal(output)
	}
	if f.count("/one") != 0 || f.read("state/config.json") != previous {
		t.Fatal("offline preparation fetched or replaced existing config")
	}
	f.setBody("/two", "invalid subscription")
	f.run("", false, "update")
	if f.read("state/config.json") == previous || !strings.Contains(f.read("state/cache.json"), `"unavailable": true`) {
		t.Fatal("partial bootstrap did not install viable source and unavailable entry")
	}
	f.run("", true, "prepare")
	f.setBody("/two", `[{"type":"shadowsocks","tag":"b","server":"two.example"}]`)
	f.run("", true, "update")
	f.run("", true, "prepare")
	if strings.Contains(f.read("state/cache.json"), "old.example") {
		t.Fatal("unattributed legacy nodes were imported")
	}
	if !strings.Contains(f.read("state/subscription-outbounds.json"), "old.example") {
		t.Fatal("legacy source file was modified")
	}
}

func TestRestartFailureAndCLIValidation(t *testing.T) {
	f := newFixture(t)
	f.config["restart_command"] = []string{helper, "restart"}
	f.write("config.json", f.config)
	if output := f.run("", false, "update"); !strings.Contains(output, "configuration installed, but service restart failed") {
		t.Fatal(output)
	}
	f.run("", true, "prepare") // update released the lock despite restart failure.
	f.run("", false, "list", "ignored")
	f.run("", false, "subscription", "auto", "one", "--include-tag", "[")
	f.run("", false, "subscription", "update", "unknown")
	f.write("local.json", map[string]any{"subscriptions": []any{f.source("one", "/one")}})
	f.run("", false, "subscription", "list")
}

func TestRestartCanPrepareWithoutLockConflict(t *testing.T) {
	f := newFixture(t)
	f.config["restart_command"] = []string{binary, "--config", filepath.Join(f.dir, "config.json"), "prepare"}
	f.write("config.json", f.config)
	f.run("", true, "update")
	if f.count("/one") != 1 || f.count("/two") != 1 {
		t.Fatal("restart fetched subscriptions instead of preparing from cache")
	}
}

func TestStandaloneInit(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(binary, "init")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "sb", "config.json")); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(binary, "init")
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("init overwrote existing config")
	}
}

func TestRealConverterAndCore(t *testing.T) {
	core, converter := os.Getenv("SB_REAL_CORE"), os.Getenv("SB_REAL_CONVERTER")
	if core == "" || converter == "" {
		t.Skip("set SB_REAL_CORE and SB_REAL_CONVERTER for actual core/converter integration")
	}
	f := newFixture(t)
	f.config["sing_box"], f.config["converter"] = core, converter
	if template := os.Getenv("SB_REAL_TEMPLATE"); template != "" {
		f.config["template_file"] = template
	}
	f.write("config.json", f.config)
	// Nix declares URL-file references, not URL values. Exercise that exact
	// boundary with a relative file reference and CRLF-terminated provider URL.
	if err := os.WriteFile(filepath.Join(f.dir, "provider-url"), []byte(f.server.URL+"/one?token=private\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.write("declared.json", map[string]any{"subscriptions": []any{
		map[string]any{"id": "one", "url_file": "provider-url", "prefix": "one-"},
		f.source("two", "/two"),
	}})
	credentials := base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:integration-password"))
	f.setBody("/one", "ss://"+credentials+"@127.0.0.1:8388#real-one\n")
	f.setBody("/two", "ss://"+credentials+"@127.0.0.1:8389#real-two\n")
	f.run("", true, "update")
	if !strings.Contains(f.read("state/config.json"), "one-real-one") {
		t.Fatal("converter prefix was not applied")
	}
	f.run("", true, "check")
	f.run("", true, "subscription", "auto", "one", "--enabled", "false")
	f.run("", true, "subscription", "auto", "two", "--enabled", "false")
	f.run("", true, "prepare")
	f.run("", true, "check")
}
