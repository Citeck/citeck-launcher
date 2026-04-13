package bundlepicker

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInitialCursor_PrefersCurrent(t *testing.T) {
	tab := Tab{
		Versions: []Version{
			{Ref: "r:2026.1", Label: "2026.1", Latest: true},
			{Ref: "r:2025.12", Label: "2025.12", Current: true},
			{Ref: "r:2025.11", Label: "2025.11"},
		},
	}
	if got := initialCursor(tab); got != 1 {
		t.Errorf("initialCursor=%d, want 1 (the current entry)", got)
	}
}

func TestInitialCursor_FallsBackToLatest(t *testing.T) {
	tab := Tab{
		Versions: []Version{
			{Ref: "r:2026.1", Label: "2026.1", Latest: true},
			{Ref: "r:2025.12", Label: "2025.12"},
		},
	}
	if got := initialCursor(tab); got != 0 {
		t.Errorf("initialCursor=%d, want 0 (no current → latest)", got)
	}
}

func TestPick_EmptyTabs_ReturnsFalseNoError(t *testing.T) {
	ref, ok, err := Pick("title", nil, KeyHints{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("ok=true on empty input, want false")
	}
	if ref != "" {
		t.Errorf("ref=%q on empty input, want empty", ref)
	}
}

func TestPick_TabsWithoutVersionsFilteredOut(t *testing.T) {
	tabs := []Tab{
		{ID: "empty", Name: "Empty"},
		{ID: "empty2", Name: "Empty2"},
	}
	ref, ok, err := Pick("title", tabs, KeyHints{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || ref != "" {
		t.Errorf("expected cancel when no visible tabs; got ref=%q ok=%v", ref, ok)
	}
}

// Model-level tests exercise the key handling without a real terminal.

func sampleTabs() []Tab {
	return []Tab{
		{
			ID: "community", Name: "Community",
			Versions: []Version{
				{Ref: "community:2026.1", Label: "2026.1", Latest: true},
				{Ref: "community:2025.12", Label: "2025.12", Current: true},
				{Ref: "community:2025.11", Label: "2025.11"},
			},
		},
		{
			ID: "enterprise", Name: "Enterprise",
			Versions: []Version{
				{Ref: "enterprise:2026.1", Label: "2026.1", Latest: true},
				{Ref: "enterprise:2025.12", Label: "2025.12"},
			},
		},
	}
}

// applyKey simulates a single key press.
func applyKey(m model, key string) model {
	km := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	// Map a few special keys to tea.KeyType values.
	switch key {
	case "up":
		km = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		km = tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		km = tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		km = tea.KeyMsg{Type: tea.KeyRight}
	case "enter":
		km = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		km = tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		km = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "home":
		km = tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		km = tea.KeyMsg{Type: tea.KeyEnd}
	}
	out, _ := m.Update(km)
	return out.(model)
}

func TestModel_InitialCursorOnActiveTab(t *testing.T) {
	tabs := sampleTabs()
	m := model{tabs: tabs, cursor: initialCursor(tabs[0])}
	if m.cursor != 1 {
		t.Errorf("cursor=%d, want 1 (current version in first tab)", m.cursor)
	}
}

func TestModel_RightSwitchesTab_AndResetsCursorToLatest(t *testing.T) {
	tabs := sampleTabs()
	m := model{tabs: tabs, active: 0, cursor: initialCursor(tabs[0])}
	m = applyKey(m, "right")
	if m.active != 1 {
		t.Errorf("active=%d, want 1", m.active)
	}
	// Enterprise tab has no current → cursor starts at 0 (latest).
	if m.cursor != 0 {
		t.Errorf("cursor=%d, want 0 on new tab (no current)", m.cursor)
	}
}

func TestModel_LeftAtFirstTabNoOp(t *testing.T) {
	tabs := sampleTabs()
	m := model{tabs: tabs, active: 0, cursor: 1}
	m = applyKey(m, "left")
	if m.active != 0 {
		t.Errorf("active=%d, want 0 (clamped)", m.active)
	}
}

func TestModel_DownBoundary(t *testing.T) {
	tabs := sampleTabs()
	m := model{tabs: tabs, active: 0, cursor: 2}
	m = applyKey(m, "down")
	if m.cursor != 2 {
		t.Errorf("cursor=%d, want 2 (clamped at bottom)", m.cursor)
	}
}

func TestModel_UpBoundary(t *testing.T) {
	tabs := sampleTabs()
	m := model{tabs: tabs, active: 0, cursor: 0}
	m = applyKey(m, "up")
	if m.cursor != 0 {
		t.Errorf("cursor=%d, want 0 (clamped)", m.cursor)
	}
}

func TestModel_EnterSetsChosen(t *testing.T) {
	tabs := sampleTabs()
	m := model{tabs: tabs, active: 0, cursor: 0}
	m = applyKey(m, "enter")
	if !m.chosen {
		t.Fatal("chosen=false after Enter")
	}
}

func TestModel_EscDoesNotChoose(t *testing.T) {
	tabs := sampleTabs()
	m := model{tabs: tabs}
	m = applyKey(m, "esc")
	if m.chosen {
		t.Fatal("chosen=true after Esc")
	}
}

func TestModel_ViewShowsLatestAndCurrent(t *testing.T) {
	tabs := sampleTabs()
	m := model{
		title: "Select version",
		tabs:  tabs,
		hints: KeyHints{}.withDefaults(),
	}
	out := m.View()
	for _, want := range []string{"Community", "Enterprise", "2026.1", "2025.12", "(latest)", "(current)"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Errorf("view missing %q\ngot:\n%s", want, stripANSI(out))
		}
	}
}

func TestModel_ViewCustomHints(t *testing.T) {
	tabs := sampleTabs()
	hints := KeyHints{Latest: "новейший", Current: "текущий"}
	m := model{tabs: tabs, hints: hints.withDefaults()}
	out := stripANSI(m.View())
	if !strings.Contains(out, "(новейший)") {
		t.Errorf("expected localized latest marker, got:\n%s", out)
	}
	if !strings.Contains(out, "(текущий)") {
		t.Errorf("expected localized current marker, got:\n%s", out)
	}
}

// stripANSI removes ANSI escape sequences (including CSI) from s so tests
// can assert on readable content.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip until final byte in range @–~ (0x40..0x7e).
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
