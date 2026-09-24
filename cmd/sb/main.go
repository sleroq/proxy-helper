// sb is a short-lived controller, not a proxy or service supervisor.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strconv"
	"syscall"

	"github.com/sleroq/sb/internal/app"
	"github.com/sleroq/sb/singbox"
	"github.com/sleroq/sb/subscription"
)

const usage = `Usage: sb [--config PATH] COMMAND

  init                         Create standalone XDG configuration
  update [ID...]               Fetch subscriptions, validate, install, restart
  bypass on|off               Toggle direct-only routing
  tunnel off|on               Toggle template TUN inbound
  prepare                      Render from cache; no fetch or restart
  apply                        Render from cache and restart
  list | status                Inspect running selection and public manifest
  test [GROUP]                 Test automatic group latency (default auto)
  use [TAG]                    Select live (interactive picker with no tag)
  pin TAG                      Persist a leaf selection and select it live
  unpin                        Restore automatic/default selection
  config [--raw] | check       Inspect or validate installed config
  subscription list
  subscription add [--store NAME]       Read a source JSON object from stdin
  subscription delete ID
  subscription update [ID...]
  subscription enable|disable|reset ID
  subscription auto ID [--enabled BOOL] [--include-tag REGEX] [--exclude-tag REGEX]
                       [--include-server REGEX] [--exclude-server REGEX] [--clear]

Store/policy edits are staged; run sb apply or sb update to activate them.
`

func main() {
	if err := runWithSignals(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sb:", err)
		os.Exit(1)
	}
}

func runWithSignals(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, args)
}

func run(ctx context.Context, args []string) error {
	global := flag.NewFlagSet("sb", flag.ContinueOnError)
	path := global.String("config", "", "configuration path")
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args = global.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Print(usage)
		return nil
	}
	if *path == "" {
		p, err := app.DefaultConfig()
		if err != nil {
			return err
		}
		*path = p
	}
	if args[0] == "init" {
		if len(args) != 1 {
			return fmt.Errorf("init takes no arguments")
		}
		return app.Init(*path)
	}
	settings, err := app.LoadSettings(*path)
	if err != nil {
		return err
	}
	return execute(ctx, settings, args)
}

func execute(ctx context.Context, settings app.Settings, args []string) error {
	manager := app.Manager{Settings: settings}
	api := singbox.Clash{URL: settings.APIURL}
	command, rest := args[0], args[1:]
	switch command {
	case "update", "subscription", "prepare", "apply", "bypass", "tunnel":
		return mutate(ctx, manager, command, rest)
	case "use", "pin", "unpin", "test", "config", "check":
		return clientCommand(ctx, manager, api, command, rest)
	case "list", "status":
		if len(rest) != 0 {
			return fmt.Errorf("%s takes no arguments", command)
		}
		return inspect(ctx, manager, api, command)
	default:
		return fmt.Errorf("unknown command %q; run sb help", command)
	}
}

func mutate(ctx context.Context, manager app.Manager, command string, rest []string) error {
	switch command {
	case "bypass", "tunnel":
		if len(rest) != 1 || (rest[0] != "on" && rest[0] != "off") {
			return fmt.Errorf("usage: sb %s on|off", command)
		}
		return manager.SetMode(ctx, command, rest[0] == "on")
	case "update":
		return manager.Update(ctx, rest)
	case "subscription":
		return subscriptions(ctx, manager, rest)
	case "prepare", "apply":
		if len(rest) != 0 {
			return fmt.Errorf("%s takes no arguments", command)
		}
		if err := manager.Prepare(ctx); err != nil {
			return err
		}
		if command == "apply" {
			return manager.Restart(ctx)
		}
		return nil
	default:
		return fmt.Errorf("unknown mutation %q", command)
	}
}

func clientCommand(ctx context.Context, manager app.Manager, api singbox.Clash, command string, rest []string) error {
	switch command {
	case "pin", "unpin":
		if command == "pin" && len(rest) != 1 {
			return fmt.Errorf("pin requires exactly one tag")
		}
		if command == "unpin" && len(rest) != 0 {
			return fmt.Errorf("unpin takes no arguments")
		}

		tag := ""
		if command == "pin" {
			tag = rest[0]
		}
		choice, err := manager.Pin(ctx, tag)
		if err != nil {
			return err
		}

		mode, err := manager.Settings.LoadMode()
		if err != nil {
			return err
		}
		if mode.Bypass {
			fmt.Println("pin saved; bypass remains direct-only; run sb bypass off to restore proxy selection")
			return nil
		}
		if err := api.Use(ctx, choice); err != nil {
			return fmt.Errorf("pin/config saved, but live selection could not be changed: %w", err)
		}
		fmt.Println("selected", choice)
		return nil
	case "use":
		manifest, err := manager.Manifest()
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if manifest.Mode == nil {
			mode, modeErr := manager.Settings.LoadMode()
			if modeErr != nil {
				return fmt.Errorf("installed mode unavailable: %w", modeErr)
			}
			if os.IsNotExist(err) {
				return fmt.Errorf("installed mode unavailable: no manifest")
			}
			manifest.Mode = &mode
		}
		if manifest.Mode.Bypass {
			return fmt.Errorf("direct-only bypass enabled; run sb bypass off before sb use")
		}
		if len(rest) > 1 {
			return fmt.Errorf("use accepts at most one tag")
		}
		if len(rest) == 0 {
			tag, err := pick(ctx, manager, api)
			if err != nil || tag == "" {
				return err
			}
			rest = []string{tag}
		}
		if err := api.Use(ctx, rest[0]); err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // SIGINT cancels the live change without an alarming API error.
			}
			return err
		}
		fmt.Println("selected", rest[0])
		return nil
	case "test":
		return test(ctx, api, manager.Settings.TestURL, rest)
	case "config":
		return config(manager, rest)
	case "check":
		if len(rest) != 0 {
			return fmt.Errorf("check takes no arguments")
		}
		child := exec.CommandContext(ctx, manager.Settings.SingBox, "check", "-c", manager.Settings.ConfigPath())
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		return child.Run()
	default:
		return fmt.Errorf("unknown client command %q", command)
	}
}

func test(ctx context.Context, api singbox.Clash, testURL string, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("test accepts at most one group")
	}
	group := "auto"
	if len(args) == 1 {
		group = args[0]
	}
	results, err := api.Test(ctx, group, testURL)
	if err != nil {
		return err
	}
	tags := make([]string, 0, len(results))
	for tag := range results {
		tags = append(tags, tag)
	}
	slices.SortFunc(tags, func(a, b string) int {
		if results[a] != results[b] {
			return results[a] - results[b]
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	for _, tag := range tags {
		fmt.Printf("%d ms\t%s\n", results[tag], tag)
	}
	return nil
}

func config(manager app.Manager, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--raw") {
		return fmt.Errorf("usage: sb config [--raw]")
	}
	data, err := manager.Config(len(args) == 1)
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func inspect(ctx context.Context, manager app.Manager, api singbox.Clash, command string) error {
	manifest, err := manager.Manifest()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if command == "status" {
		if err := status(manager.Settings, manifest, os.IsNotExist(err)); err != nil {
			return err
		}
	}
	selected, err := api.Selector(ctx)
	if err != nil {
		if command == "status" {
			fmt.Println("live selector: unavailable")
			return nil
		}
		return err
	}
	if command == "status" {
		printPin(manifest)
		fmt.Println("selected:", selected.Now)
		return nil
	}
	printPin(manifest)
	printSelection(manifest, selected)
	return nil
}

func printPin(manifest app.Manifest) {
	switch {
	case manifest.PinnedTag == "":
		fmt.Println("pin: none")
	case manifest.PinActive:
		fmt.Println("pin:", manifest.PinnedTag, "(available; not necessarily selected live)")
	default:
		fmt.Println("pin:", manifest.PinnedTag, "(stale/inactive)")
	}
}

func printSelection(manifest app.Manifest, selected singbox.Selector) {
	sources := map[string]string{}
	for _, node := range manifest.Nodes {
		sources[node.Tag] = node.Source
	}
	for _, tag := range selected.All {
		marker := "  "
		if tag == selected.Now {
			marker = "* "
		}
		suffix := ""
		if sources[tag] != "" {
			suffix = " [" + sources[tag] + "]"
		}
		fmt.Println(marker + tag + suffix)
	}
}

func status(settings app.Settings, manifest app.Manifest, missing bool) error {
	mode, err := settings.LoadMode()
	switch {
	case err == nil:
		fmt.Printf("requested mode: bypass=%t tunnel_off=%t\n", mode.Bypass, mode.TunnelOff)
	case os.IsPermission(err):
		fmt.Println("requested mode: unavailable")
	default:
		return err
	}
	switch {
	case missing:
		fmt.Println("installed mode: missing")
	case manifest.Mode == nil:
		fmt.Println("installed mode: unknown (legacy manifest)")
	default:
		fmt.Printf("installed mode: bypass=%t tunnel_off=%t\n", manifest.Mode.Bypass, manifest.Mode.TunnelOff)
	}
	fmt.Println("active routing/interception: unverified (live selector is separate)")
	health, err := settings.LoadHealth()
	if err != nil {
		return err
	}
	if missing {
		fmt.Println("subscription has not been updated")
	} else {
		fmt.Println("last update:", manifest.UpdatedAt)
		fmt.Println("nodes:", len(manifest.Nodes))
	}
	installed := installedSources(manifest)
	ids := make([]string, 0, len(installed)+len(health))
	for id := range installed {
		ids = append(ids, id)
	}
	for id := range health {
		if _, ok := installed[id]; !ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		source, installedOK := installed[id]
		attempt := health[id]
		state := sourceState(source, attempt, installedOK)
		fmt.Printf("  %s: %s, nodes: %d, installed success: %s, last attempt: %s",
			id, state, source.NodeCount, source.UpdatedAt, attempt.AttemptedAt)
		if attempt.Error != "" {
			fmt.Printf(", failed: %s", attempt.Error)
		}
		fmt.Println()
	}
	return nil
}

func installedSources(manifest app.Manifest) map[string]app.SourceInfo {
	installed := map[string]app.SourceInfo{}
	for _, source := range manifest.Sources {
		installed[source.ID] = source
	}
	// A manifest from an older sb has nodes but no per-source summary.
	if len(manifest.Sources) == 0 {
		for _, node := range manifest.Nodes {
			source := installed[node.Source]
			source.ID = node.Source
			source.NodeCount++
			installed[node.Source] = source
		}
	}
	return installed
}

func sourceState(source app.SourceInfo, attempt app.SourceHealth, installed bool) string {
	switch {
	case attempt.Error != "" && (!installed || source.Unavailable):
		return "unavailable"
	case attempt.Error != "":
		return "stale"
	case attempt.AttemptedAt != "" && attempt.AttemptedAt != source.UpdatedAt:
		return "fetched, not installed"
	case source.Unavailable:
		return "unavailable"
	case !installed:
		return "not installed"
	default:
		return "successful"
	}
}

type patterns []string

func (p *patterns) String() string         { return fmt.Sprint([]string(*p)) }
func (p *patterns) Set(value string) error { *p = append(*p, value); return nil }

func subscriptions(ctx context.Context, m app.Manager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("subscription subcommand required; run sb help")
	}
	command, rest := args[0], args[1:]
	if command == "update" {
		return m.Update(ctx, rest)
	}
	if command == "list" {
		return listSubscriptions(m, rest)
	}
	err := m.Edit(func(c *subscription.Catalog) error { return editSubscription(c, command, rest) })
	if err == nil {
		fmt.Println("saved; run sb apply (cached) or sb update (fetch) to activate")
	}
	return err
}

func editSubscription(c *subscription.Catalog, command string, args []string) error {
	switch command {
	case "add":
		return addSubscription(c, args)
	case "delete", "enable", "disable", "reset":
		if len(args) != 1 {
			return fmt.Errorf("%s requires exactly one subscription ID", command)
		}
		switch command {
		case "delete":
			return c.Delete(args[0])
		case "reset":
			return c.Reset(args[0])
		default:
			return c.SetEnabled(args[0], command == "enable")
		}
	case "auto":
		return automatic(c, args)
	default:
		return fmt.Errorf("unknown subscription command %q", command)
	}
}

func listSubscriptions(m app.Manager, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("subscription list takes no arguments")
	}
	catalog, err := subscription.Load(m.Settings.Stores, m.Settings.OverridesFile)
	if err != nil {
		return err
	}
	for _, source := range catalog.Sources() {
		state := "enabled"
		if source.Disabled {
			state = "disabled"
		}
		automatic := "auto"
		if source.Auto != nil && !*source.Auto {
			automatic = "manual"
		}
		fmt.Printf("%s\t%s\t%s\n", source.ID, state, automatic)
	}
	return nil
}

func addSubscription(c *subscription.Catalog, args []string) error {
	flags := flag.NewFlagSet("subscription add", flag.ContinueOnError)
	store := flags.String("store", "", "writable store")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("add reads source JSON from stdin, not arguments")
	}
	var source subscription.Source
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return fmt.Errorf("invalid source JSON on stdin")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected exactly one source JSON object")
	}
	return c.Put(source, *store, false)
}

func automatic(c *subscription.Catalog, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: sb subscription auto ID --enabled BOOL | --include-tag REGEX | " +
			"--exclude-tag REGEX | --include-server REGEX | --exclude-server REGEX | --clear")
	}
	id := args[0]
	flags := flag.NewFlagSet("subscription auto", flag.ContinueOnError)
	enabled := flags.String("enabled", "", "enable automatic selection")
	clear := flags.Bool("clear", false, "clear filters")
	var includeTags, excludeTags, includeServers, excludeServers patterns
	flags.Var(&includeTags, "include-tag", "tag inclusion regular expression (repeatable)")
	flags.Var(&excludeTags, "exclude-tag", "tag exclusion regular expression (repeatable)")
	flags.Var(&includeServers, "include-server", "server inclusion regular expression (repeatable)")
	flags.Var(&excludeServers, "exclude-server", "server exclusion regular expression (repeatable)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected automatic selection arguments")
	}
	policy, err := currentPolicy(c, id)
	if err != nil {
		return err
	}
	if *clear {
		policy = subscription.Policy{}
	}
	if err := setAutomatic(&policy, *enabled); err != nil {
		return err
	}
	applyFilters(&policy, includeTags, excludeTags, includeServers, excludeServers)
	return c.SetPolicy(id, policy)
}

func applyFilters(policy *subscription.Policy, includeTags, excludeTags, includeServers, excludeServers patterns) {
	if includeTags != nil {
		policy.IncludeTags = includeTags
	}
	if excludeTags != nil {
		policy.ExcludeTags = excludeTags
	}
	if includeServers != nil {
		policy.IncludeServers = includeServers
	}
	if excludeServers != nil {
		policy.ExcludeServers = excludeServers
	}
}

func currentPolicy(c *subscription.Catalog, id string) (subscription.Policy, error) {
	for _, source := range c.Sources() {
		if source.ID == id {
			return source.Policy, nil
		}
	}
	return subscription.Policy{}, fmt.Errorf("unknown subscription %s", id)
}

func setAutomatic(policy *subscription.Policy, enabled string) error {
	if enabled == "" {
		return nil
	}
	value, err := strconv.ParseBool(enabled)
	if err != nil {
		return fmt.Errorf("enabled must be true or false")
	}
	policy.Auto = &value
	return nil
}
