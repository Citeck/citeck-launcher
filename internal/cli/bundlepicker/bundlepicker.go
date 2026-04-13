// Package bundlepicker provides a tabbed interactive picker for bundle
// versions. Each tab corresponds to one bundle repo; versions within a tab
// are listed newest-first. Used by `citeck install` and `citeck upgrade`.
//
// The picker assumes an interactive TTY. Callers must perform their own
// non-TTY checks before invoking Pick.
package bundlepicker

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Tab groups versions from one bundle repo.
type Tab struct {
	// ID is the bundle repo ID (e.g. "community", "enterprise").
	ID string
	// Name is the display name (e.g. "Community Bundles").
	Name string
	// Versions are the available versions for this tab, newest-first.
	Versions []Version
}

// Version describes a single selectable bundle version within a tab.
type Version struct {
	// Ref is the full bundle reference (e.g. "community:2026.1").
	Ref string
	// Label is the short version label (e.g. "2026.1").
	Label string
	// Current marks the version currently in use.
	Current bool
	// Latest marks the newest version in its tab.
	Latest bool
}

// KeyHints describes the footer key hints. Callers may translate these
// strings. Empty values are replaced with English defaults.
type KeyHints struct {
	SwitchTab string // e.g. "←/→ switch tab"
	Move      string // e.g. "↑/↓ move"
	Select    string // e.g. "Enter select"
	Cancel    string // e.g. "Esc cancel"
	Latest    string // e.g. "latest"
	Current   string // e.g. "current"
}

// defaultHints returns English defaults for any empty field in h.
func (h KeyHints) withDefaults() KeyHints {
	if h.SwitchTab == "" {
		h.SwitchTab = "←/→ switch tab"
	}
	if h.Move == "" {
		h.Move = "↑/↓ move"
	}
	if h.Select == "" {
		h.Select = "Enter select"
	}
	if h.Cancel == "" {
		h.Cancel = "Esc cancel"
	}
	if h.Latest == "" {
		h.Latest = "latest"
	}
	if h.Current == "" {
		h.Current = "current"
	}
	return h
}

// Pick shows the tabbed picker and blocks until the user selects a version
// or cancels. Returns the selected Version.Ref ("") and ok=true on select,
// or "" and ok=false when the user cancels (Esc/q/Ctrl+C).
//
// Only tabs with at least one version are shown. If tabs contains no
// versions at all, Pick returns ("", false, nil) immediately without
// drawing anything.
//
// Pick assumes an interactive TTY. Non-TTY detection is the caller's
// responsibility.
func Pick(title string, tabs []Tab, hints KeyHints) (ref string, ok bool, err error) {
	visible := make([]Tab, 0, len(tabs))
	for _, tab := range tabs {
		if len(tab.Versions) > 0 {
			visible = append(visible, tab)
		}
	}
	if len(visible) == 0 {
		return "", false, nil
	}

	m := model{
		title:  title,
		tabs:   visible,
		hints:  hints.withDefaults(),
		active: 0,
		cursor: initialCursor(visible[0]),
	}

	prog := tea.NewProgram(m,
		tea.WithOutput(teaOutput()),
		tea.WithInput(teaInput()),
		tea.WithAltScreen(), // restores prior screen on exit so picker hints
		// don't linger above the next prompt.
	)
	final, runErr := prog.Run()
	if runErr != nil {
		return "", false, fmt.Errorf("run bundle picker: %w", runErr)
	}
	fm, _ := final.(model)
	if !fm.chosen {
		return "", false, nil
	}
	tab := fm.tabs[fm.active]
	return tab.Versions[fm.cursor].Ref, true, nil
}

// initialCursor returns the index the cursor should start at for a tab:
// the "current" version if present, otherwise 0 (latest).
func initialCursor(tab Tab) int {
	for i, v := range tab.Versions {
		if v.Current {
			return i
		}
	}
	return 0
}

// ───────────────────────── model ─────────────────────────

type model struct {
	title  string
	tabs   []Tab
	hints  KeyHints
	active int  // index into tabs
	cursor int  // index into tabs[active].Versions
	chosen bool // user pressed Enter
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c", "esc", "q":
		return m, tea.Quit
	case "left", "h":
		if m.active > 0 {
			m.active--
			m.cursor = initialCursor(m.tabs[m.active])
		}
	case "right", "l":
		if m.active < len(m.tabs)-1 {
			m.active++
			m.cursor = initialCursor(m.tabs[m.active])
		}
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.tabs[m.active].Versions)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.tabs[m.active].Versions) - 1
	case "enter":
		m.chosen = true
		return m, tea.Quit
	}
	return m, nil
}

// ───────────────────────── view ─────────────────────────

var (
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#50fa7b"))
	// Active tab: dark fg on cyan bg with horizontal padding for breathing
	// room. Using a filled background (instead of underline) makes the
	// selection pop on both dark and light terminals.
	styleTabActive = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("#282a36")).
			Background(lipgloss.Color("#8be9fd")).
			Padding(0, 1)
	styleTabInactive = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#6272a4")).
				Padding(0, 1)
	styleCursor     = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6")).Bold(true)
	styleSelected   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Bold(true)
	styleNormal     = lipgloss.NewStyle().Foreground(lipgloss.Color("#bfbfbf"))
	styleMarker     = lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9"))
	styleCurrent    = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b"))
	styleSeparator  = lipgloss.NewStyle().Foreground(lipgloss.Color("#44475a"))
	styleHint       = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
)

// View implements tea.Model.
func (m model) View() string {
	var b []byte
	if m.title != "" {
		b = append(b, styleTitle.Render(m.title)...)
		b = append(b, '\n', '\n')
	}

	// Tab bar
	for i, tab := range m.tabs {
		name := tab.Name
		if name == "" {
			name = tab.ID
		}
		if i == m.active {
			b = append(b, styleTabActive.Render(name)...)
		} else {
			b = append(b, styleTabInactive.Render(name)...)
		}
		if i < len(m.tabs)-1 {
			b = append(b, styleSeparator.Render("│")...)
		}
	}
	b = append(b, '\n')
	b = append(b, styleSeparator.Render("──────────────────────────────────────────────────────")...)
	b = append(b, '\n', '\n')

	// Version list for active tab
	tab := m.tabs[m.active]
	for i, v := range tab.Versions {
		var line string
		if i == m.cursor {
			line = styleCursor.Render("  > ") + styleSelected.Render(v.Label)
		} else {
			line = "    " + styleNormal.Render(v.Label)
		}
		if v.Latest {
			line += "  " + styleMarker.Render("("+m.hints.Latest+")")
		}
		if v.Current {
			line += "  " + styleCurrent.Render("("+m.hints.Current+")")
		}
		b = append(b, line...)
		b = append(b, '\n')
	}

	// Footer
	b = append(b, '\n')
	footer := fmt.Sprintf("%s   %s   %s   %s",
		m.hints.SwitchTab, m.hints.Move, m.hints.Select, m.hints.Cancel)
	b = append(b, styleHint.Render(footer)...)
	b = append(b, '\n')
	return string(b)
}
