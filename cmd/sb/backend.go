package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/sleroq/sb/internal/app"
	"github.com/sleroq/sb/internal/backend"
)

func makeClient(s app.Settings, name string) (backend.Client, app.Settings, error) {
	template, binary, err := s.BackendPaths(name)
	if err != nil {
		return nil, s, err
	}
	s.TemplateFile = template
	cfg := backend.Config{
		Name: name, Converter: s.Converter, LegacyOutboundsFile: s.LegacyOutboundsFile,
		StaticOutboundsFile: s.StaticOutboundsFile, ExtraOutboundsFile: s.ExtraOutboundsFile,
		RoutingMark: s.RoutingMark,
	}
	if name == "mihomo" {
		cfg.Mihomo = binary
	} else {
		cfg.SingBox = binary
	}
	client, err := backend.New(cfg)
	return client, s, err
}

func configuredBackends(s app.Settings) []string {
	names := make([]string, 0, len(s.Backends))
	for name := range s.Backends {
		names = append(names, name)
	}
	if len(names) == 0 {
		name := s.Backend
		if name == "" {
			name = "sing-box"
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func backendCommand(ctx context.Context, s app.Settings, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sb backend list|select [NAME]")
	}
	names := configuredBackends(s)
	current, err := s.SelectedBackend()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("backend list takes no arguments")
		}
		for _, name := range names {
			mark := " "
			if name == current {
				mark = "*"
			}
			fmt.Printf("%s %s\n", mark, name)
		}
		return nil
	case "select":
		return selectBackend(ctx, s, names, current, args[1:])
	default:
		return fmt.Errorf("unknown backend command %q", args[0])
	}
}

func selectBackend(ctx context.Context, s app.Settings, names []string, current string, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("backend select accepts at most one name")
	}
	name := ""
	if len(args) == 1 {
		name = args[0]
	} else {
		var err error
		name, err = pickBackend(ctx, names, current)
		if err != nil || name == "" {
			return err
		}
	}
	if !slices.Contains(names, name) {
		return fmt.Errorf("backend %q is not configured", name)
	}
	if name == current {
		return nil
	}
	target, targetSettings, err := makeClient(s, name)
	if err != nil {
		return err
	}
	if err := app.Switch(ctx, s, app.Manager{Settings: targetSettings, Backend: target}); err != nil {
		return err
	}
	fmt.Println("selected backend", name)
	return nil
}

func pickBackend(ctx context.Context, names []string, current string) (string, error) {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return "", errors.New("backend select requires a name without a terminal")
	}
	rows := make([]pickerRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, pickerRow{tag: name})
	}
	p := picker{
		rows: rows, now: current, width: 80, height: 24, backend: true,
		color: os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
	}
	p.refilter()
	result, err := tea.NewProgram(p, tea.WithContext(ctx), tea.WithoutSignalHandler()).Run()
	if err != nil {
		return "", err
	}
	return result.(picker).choice, nil
}

func execBackend(s app.Settings, client backend.Client) error {
	binary, args := client.RunCommand(s.ConfigPath(), s.StateDir)
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return err
	}
	return syscall.Exec(resolved, append([]string{binary}, args...), os.Environ())
}
