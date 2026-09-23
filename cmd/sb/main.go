// sb is a short-lived controller, not a proxy or service supervisor.
package main

import (
	"context"
	"encoding/json"
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
  prepare                      Render from cache; no fetch or restart
  apply                        Render from cache and restart
  list | status                Inspect running selection and public manifest
  test [GROUP]                 Test automatic group latency (default auto)
  use TAG                      Select an outbound
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sb:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	global := flag.NewFlagSet("sb", flag.ContinueOnError)
	path := global.String("config", "", "configuration path")
	global.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := global.Parse(args); err != nil {
		if err == flag.ErrHelp {
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
	manager := app.Manager{Settings: settings}
	api := singbox.Clash{URL: settings.APIURL}
	command, rest := args[0], args[1:]
	switch command {
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
	case "use":
		if len(rest) != 1 {
			return fmt.Errorf("use requires exactly one tag")
		}
		if err := api.Use(ctx, rest[0]); err != nil {
			return err
		}
		fmt.Println("selected", rest[0])
		return nil
	case "test":
		if len(rest) > 1 {
			return fmt.Errorf("test accepts at most one group")
		}
		group := "auto"
		if len(rest) == 1 {
			group = rest[0]
		}
		results, err := api.Test(ctx, group, settings.TestURL)
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
	case "config":
		if len(rest) > 1 || (len(rest) == 1 && rest[0] != "--raw") {
			return fmt.Errorf("usage: sb config [--raw]")
		}
		data, err := manager.Config(len(rest) == 1)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	case "check":
		if len(rest) != 0 {
			return fmt.Errorf("check takes no arguments")
		}
		child := exec.CommandContext(ctx, settings.SingBox, "check", "-c", settings.ConfigPath())
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		return child.Run()
	case "list", "status":
		if len(rest) != 0 {
			return fmt.Errorf("%s takes no arguments", command)
		}
		manifest, err := manager.Manifest()
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if command == "status" {
			health, healthErr := settings.LoadHealth()
			if healthErr != nil {
				return healthErr
			}
			if os.IsNotExist(err) {
				fmt.Println("subscription has not been updated")
			} else {
				fmt.Println("last update:", manifest.UpdatedAt)
				fmt.Println("nodes:", len(manifest.Nodes))
			}
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
				state := "successful"
				switch {
				case attempt.Error != "" && (!installedOK || source.Unavailable):
					state = "unavailable"
				case attempt.Error != "":
					state = "stale"
				case attempt.AttemptedAt != "" && attempt.AttemptedAt != source.UpdatedAt:
					state = "fetched, not installed"
				case source.Unavailable:
					state = "unavailable"
				case !installedOK:
					state = "not installed"
				}
				fmt.Printf("  %s: %s, nodes: %d, installed success: %s, last attempt: %s", id, state, source.NodeCount, source.UpdatedAt, attempt.AttemptedAt)
				if attempt.Error != "" {
					fmt.Printf(", failed: %s", attempt.Error)
				}
				fmt.Println()
			}
		}
		selected, err := api.Selector(ctx)
		if err != nil {
			return err
		}
		if command == "status" {
			fmt.Println("selected:", selected.Now)
			return nil
		}
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
		return nil
	default:
		return fmt.Errorf("unknown command %q; run sb help", command)
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
		if len(rest) != 0 {
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
	err := m.Edit(func(c *subscription.Catalog) error {
		switch command {
		case "add":
			flags := flag.NewFlagSet("subscription add", flag.ContinueOnError)
			store := flags.String("store", "", "writable store")
			if err := flags.Parse(rest); err != nil {
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
			if err := decoder.Decode(&trailing); err != io.EOF {
				return fmt.Errorf("expected exactly one source JSON object")
			}
			return c.Put(source, *store, false)
		case "delete", "enable", "disable", "reset":
			if len(rest) != 1 {
				return fmt.Errorf("%s requires exactly one subscription ID", command)
			}
			switch command {
			case "delete":
				return c.Delete(rest[0])
			case "reset":
				return c.Reset(rest[0])
			default:
				return c.SetEnabled(rest[0], command == "enable")
			}
		case "auto":
			return automatic(c, rest)
		default:
			return fmt.Errorf("unknown subscription command %q", command)
		}
	})
	if err == nil {
		fmt.Println("saved; run sb apply (cached) or sb update (fetch) to activate")
	}
	return err
}

func automatic(c *subscription.Catalog, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: sb subscription auto ID --enabled BOOL | --include-tag REGEX | --exclude-tag REGEX | --include-server REGEX | --exclude-server REGEX | --clear")
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
	var policy subscription.Policy
	found := false
	for _, source := range c.Sources() {
		if source.ID == id {
			policy = source.Policy
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown subscription %s", id)
	}
	if *clear {
		policy = subscription.Policy{}
	}
	if *enabled != "" {
		value, err := strconv.ParseBool(*enabled)
		if err != nil {
			return fmt.Errorf("enabled must be true or false")
		}
		policy.Auto = &value
	}
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
	return c.SetPolicy(id, policy)
}
