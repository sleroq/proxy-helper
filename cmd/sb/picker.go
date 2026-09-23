package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/sleroq/sb/internal/app"
	"github.com/sleroq/sb/singbox"
)

type pickerRow struct{ tag, source, server string }
type latencyMsg map[string]int
type pulseMsg struct{}
type picker struct {
	rows                              []pickerRow
	visible                           []int
	now, pin, choice, filter          string
	active, searching, probing, color bool
	cursor, top, width, height, frame int
	latency                           map[string]int
}

func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) || r == '\x1b' {
			return -1
		}
		return r
	}, s)
}

func (m picker) Init() tea.Cmd { return nil }
func (m picker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
	case latencyMsg:
		m.latency, m.probing = v, false
	case pulseMsg:
		if m.probing {
			m.frame++
			return m, tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return pulseMsg{} })
		}
	case tea.KeyPressMsg:
		key := v.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}

		if m.searching {
			switch key {
			case "esc":
				m.searching = false
				if m.filter != "" {
					m.filter = ""
					m.refilter()
					return m, nil
				}
				return m, tea.Quit
			case "enter":
				m.searching = false
			case "backspace":
				r := []rune(m.filter)
				if len(r) > 0 {
					m.filter = string(r[:len(r)-1])
					m.refilter()
				}
			case "ctrl+u":
				m.filter = ""
				m.refilter()
			default:
				if v.Key().Text != "" {
					m.filter += safeText(v.Key().Text)
					m.refilter()
				}
			}
			return m, nil
		}

		switch key {
		case "esc":
			if m.filter != "" {
				m.filter = ""
				m.refilter()
				return m, nil
			}
			return m, tea.Quit
		case "/":
			m.searching = true
		case "j", "down":
			m.move(1)
		case "k", "up":
			m.move(-1)
		case "pgdown":
			m.move(m.pageSize())
		case "pgup":
			m.move(-m.pageSize())
		case "ctrl+u":
			m.filter = ""
			m.refilter()
		case "enter":
			if len(m.visible) > 0 {
				m.choice = m.rows[m.visible[m.cursor]].tag
				return m, tea.Quit
			}
		}
	}
	return m, nil
}
func (m *picker) refilter() {
	m.visible = m.visible[:0]
	for i, r := range m.rows {
		q := strings.ToLower(m.filter)
		if strings.Contains(strings.ToLower(r.tag), q) || strings.Contains(strings.ToLower(r.source), q) || strings.Contains(strings.ToLower(r.server), q) {
			m.visible = append(m.visible, i)
		}
	}
	m.cursor, m.top = 0, 0
	for i, index := range m.visible {
		if m.rows[index].tag == m.now {
			m.cursor = i
			break
		}
	}
}
func (m picker) pageSize() int {
	if m.height < 10 {
		return max(1, m.height-4)
	}
	return m.height - 7
}
func (m *picker) move(delta int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor = max(0, min(len(m.visible)-1, m.cursor+delta))
	window := m.pageSize()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+window {
		m.top = m.cursor - window + 1
	}
}
func (m picker) View() tea.View {
	width := max(1, m.width)
	paint := func(s, color string, bold bool) string {
		style := lipgloss.NewStyle().Bold(bold)
		if m.color {
			style = style.Foreground(lipgloss.Color(color))
		}
		return style.Render(s)
	}

	clip := func(s string) string { return lipgloss.NewStyle().MaxWidth(width).Render(s) }
	muted := "#94a3b8"
	lines := []string{
		clip(paint("◆  SB", "#5eead4", true) + paint("  /  SELECT OUTBOUND", "#c4b5fd", true)),
		clip(paint("LIVE  ", muted, true) + paint(safeText(m.now), "#86efac", true) + paint("    PIN  ", muted, true) + paint(safeText(m.pin), "#fcd34d", true)),
	}

	if m.height >= 10 {
		lines = append(lines, "")
	}
	if m.searching {
		lines = append(lines, clip(paint("/ "+m.filter+"▏", "#c4b5fd", true)))
	} else {
		lines = append(lines, clip(paint("/ search    ↑↓ / j k navigate    enter select    esc quit", muted, false)))
	}
	if m.height >= 10 {
		lines = append(lines, clip(paint(fmt.Sprintf("%d of %d nodes", len(m.visible), len(m.rows)), muted, false)))
	}

	window := m.pageSize()
	top := min(m.top, max(0, len(m.visible)-window))
	for i := top; i < len(m.visible) && i < top+window; i++ {
		r := m.rows[m.visible[i]]
		marker := "  "
		if i == m.cursor {
			marker = paint("› ", "#5eead4", true)
		}
		tagColor := "#e2e8f0"
		if i == m.cursor {
			tagColor = "#c4b5fd"
		}
		line := marker + paint(safeText(r.tag), tagColor, i == m.cursor)
		if r.tag == m.now {
			line += paint("  ● live", "#86efac", false)
		}
		if r.tag == m.pin {
			line += paint("  ◆ pin", "#fcd34d", false)
		}
		if r.source != "" {
			line += paint(" · "+safeText(r.source), muted, false)
		}
		if r.server != "" {
			line += paint(" · "+safeText(r.server), muted, false)
		}
		if ms, ok := m.latency[r.tag]; ok {
			latencyColor := "#86efac"
			if ms > 300 {
				latencyColor = "#fda4af"
			} else if ms > 120 {
				latencyColor = "#fcd34d"
			}
			line += paint(fmt.Sprintf(" · %d ms", ms), latencyColor, false)
		}
		lines = append(lines, clip(line))
	}

	if len(m.visible) == 0 {
		lines = append(lines, "  No matching nodes")
	}
	if m.probing {
		lines = append(lines, clip(paint("  Measuring latency "+[]string{"◐", "◓", "◑", "◒"}[m.frame%4], "#5eead4", false)))
	} else {
		lines = append(lines, clip(paint("  Enter selects live only · pin TAG persists", muted, false)))
	}
	if len(lines) > m.height && m.height > 0 {
		lines = lines[:m.height]
	}

	view := tea.NewView(strings.Join(lines, "\n"))
	view.AltScreen = true
	return view
}

func pick(ctx context.Context, manager app.Manager, api singbox.Clash) (string, error) {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return "", errors.New("use requires a tag without a terminal")
	}

	selected, err := api.Selector(ctx)
	if err != nil {
		return "", err
	}
	manifest, err := manager.Manifest()
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	metadata := make(map[string]app.NodeInfo, len(manifest.Nodes)+len(manifest.ExtraNodes))
	groups := make(map[string]bool, len(manifest.Sources))
	for _, node := range manifest.Nodes {
		metadata[node.Tag] = node
		if node.Automatic {
			groups["auto-"+node.Source] = true
		}
	}
	for _, node := range manifest.ExtraNodes {
		metadata[node.Tag] = node
	}

	m := picker{
		now: selected.Now, pin: manifest.PinnedTag, active: manifest.PinActive,
		width: 80, height: 24,
		color:   os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		probing: slices.Contains(selected.All, "auto"),
	}
	if m.pin == "" {
		m.pin = "none"
	} else if !m.active {
		m.pin += " (inactive)"
	}
	for _, tag := range selected.All {
		if tag == "auto" || tag == "direct" || tag == "proxy" || groups[tag] {
			continue
		}
		node := metadata[tag]
		m.rows = append(m.rows, pickerRow{tag: tag, source: node.Source, server: node.Server})
	}
	m.refilter()

	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// A separate goroutine keeps the HTTP probe out of Bubble Tea's command lifecycle;
	// quitting never waits for a slow server to finish its response.
	program := tea.NewProgram(m, tea.WithContext(ctx), tea.WithoutSignalHandler())
	if m.probing {
		go func() {
			result, _ := api.Test(probeCtx, "auto", manager.Settings.TestURL)
			if probeCtx.Err() == nil {
				program.Send(latencyMsg(result))
			}
		}()
		go func() {
			select {
			case <-probeCtx.Done():
			case <-time.After(120 * time.Millisecond):
				if probeCtx.Err() == nil {
					program.Send(pulseMsg{})
				}
			}
		}()
	}

	result, err := program.Run()
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, tea.ErrInterrupted) {
			return "", nil
		}
		return "", err
	}
	return result.(picker).choice, nil
}
