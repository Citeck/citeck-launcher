package prompt

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ----- Select model -----

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func selectModelWith(opts []Option[string], cursor, height int) selectModel[string] {
	return selectModel[string]{
		title:   "T",
		options: opts,
		hints:   Hints{}.WithDefaults(),
		cursor:  cursor,
		height:  height,
	}
}

func TestSelect_Navigation(t *testing.T) {
	opts := []Option[string]{{Label: "a", Value: "a"}, {Label: "b", Value: "b"}, {Label: "c", Value: "c"}}
	m := selectModelWith(opts, 0, 0)

	got, _ := m.Update(keyMsg("down"))
	if sm := got.(selectModel[string]); sm.cursor != 1 {
		t.Errorf("down: cursor=%d want 1", sm.cursor)
	}
	m = got.(selectModel[string])
	got, _ = m.Update(keyMsg("down"))
	got, _ = got.(selectModel[string]).Update(keyMsg("down")) // should clamp at len-1
	if sm := got.(selectModel[string]); sm.cursor != 2 {
		t.Errorf("down clamp: cursor=%d want 2", sm.cursor)
	}

	got, _ = got.(selectModel[string]).Update(keyMsg("home"))
	if sm := got.(selectModel[string]); sm.cursor != 0 {
		t.Errorf("home: cursor=%d want 0", sm.cursor)
	}
	got, _ = got.(selectModel[string]).Update(keyMsg("end"))
	if sm := got.(selectModel[string]); sm.cursor != 2 {
		t.Errorf("end: cursor=%d want 2", sm.cursor)
	}
	got, _ = got.(selectModel[string]).Update(keyMsg("up"))
	got, _ = got.(selectModel[string]).Update(keyMsg("up"))
	got, _ = got.(selectModel[string]).Update(keyMsg("up")) // clamp at 0
	if sm := got.(selectModel[string]); sm.cursor != 0 {
		t.Errorf("up clamp: cursor=%d want 0", sm.cursor)
	}
}

func TestSelect_EnterChooses(t *testing.T) {
	opts := []Option[string]{{Label: "a", Value: "a"}, {Label: "b", Value: "b"}}
	m := selectModelWith(opts, 1, 0)
	got, cmd := m.Update(keyMsg("enter"))
	if sm := got.(selectModel[string]); !sm.chosen {
		t.Error("chosen not set after Enter")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd on Enter")
	}
}

func TestSelect_EscQuitsWithoutChoosing(t *testing.T) {
	opts := []Option[string]{{Label: "a", Value: "a"}}
	m := selectModelWith(opts, 0, 0)
	got, cmd := m.Update(keyMsg("esc"))
	if sm := got.(selectModel[string]); sm.chosen {
		t.Error("chosen set on Esc (should not be)")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd on Esc")
	}
}

func TestSelect_RecomputeOffset_CursorStaysInViewport(t *testing.T) {
	opts := make([]Option[string], 20)
	for i := range opts {
		opts[i] = Option[string]{Label: "x", Value: "x"}
	}
	// cursor past height → offset adjusts so cursor is visible
	m := selectModelWith(opts, 15, 5)
	m.recomputeOffset()
	// expected offset: cursor(15) minus height(5) plus 1 = 11
	if m.offset != 15-5+1 {
		t.Errorf("offset=%d want %d", m.offset, 15-5+1)
	}
	// cursor back at 0 → offset 0
	m.cursor = 0
	m.recomputeOffset()
	if m.offset != 0 {
		t.Errorf("offset after cursor=0: %d want 0", m.offset)
	}
	// height=0 → no scrolling, offset always 0
	m.height = 0
	m.cursor = 10
	m.recomputeOffset()
	if m.offset != 0 {
		t.Errorf("offset with height=0: %d want 0", m.offset)
	}
}

// ----- Input model -----

func TestInput_ValidateBlocksSubmit(t *testing.T) {
	ti := inputModel{
		title: "t",
		hints: Hints{}.WithDefaults(),
		validate: func(s string) error {
			if s == "" {
				return errors.New("required")
			}
			return nil
		},
	}
	ti.ti.SetValue("")
	got, cmd := ti.Update(keyMsg("enter"))
	im := got.(inputModel)
	if im.submitted {
		t.Error("submitted with validation error")
	}
	if im.errMsg == "" {
		t.Error("errMsg empty after failed validate")
	}
	if cmd != nil {
		t.Error("expected no Quit cmd on failed validate")
	}
}

func TestInput_StaleErrorClearsOnNextKey(t *testing.T) {
	ti := inputModel{
		title:  "t",
		hints:  Hints{}.WithDefaults(),
		errMsg: "previous error",
	}
	got, _ := ti.Update(keyMsg("x"))
	if im := got.(inputModel); im.errMsg != "" {
		t.Errorf("errMsg=%q want empty after typing", im.errMsg)
	}
}

// ----- Confirm model -----

func TestConfirm_ToggleViaArrows(t *testing.T) {
	m := confirmModel{aff: "Y", neg: "N", hints: Hints{}.WithDefaults(), yes: true}
	got, _ := m.Update(keyMsg("right"))
	if cm := got.(confirmModel); cm.yes {
		t.Error("right: yes should be false")
	}
	got, _ = got.(confirmModel).Update(keyMsg("left"))
	if cm := got.(confirmModel); !cm.yes {
		t.Error("left: yes should be true")
	}
	got, _ = got.(confirmModel).Update(keyMsg("tab"))
	if cm := got.(confirmModel); cm.yes {
		t.Error("tab from yes: should toggle to no")
	}
}

func TestConfirm_EnterSubmits(t *testing.T) {
	m := confirmModel{aff: "Y", neg: "N", hints: Hints{}.WithDefaults(), yes: false}
	got, cmd := m.Update(keyMsg("enter"))
	cm := got.(confirmModel)
	if !cm.submitted {
		t.Error("submitted not set on Enter")
	}
	if cm.yes {
		t.Error("yes should still be false after Enter without toggling")
	}
	if cmd == nil {
		t.Error("expected Quit cmd")
	}
}

// ----- HintsFromT -----

func TestHintsFromT_FallbackWhenI18nNotLoaded(t *testing.T) {
	// Simulate i18n.T returning the key verbatim (not loaded yet).
	verbatim := func(key string, _ ...string) string { return key }
	h := HintsFromT(verbatim)
	if h != (Hints{}) {
		t.Errorf("expected zero Hints, got %+v", h)
	}
}

func TestHintsFromT_ReturnsTranslated(t *testing.T) {
	translated := func(key string, _ ...string) string {
		switch key {
		case "hint.key.move":
			return "↑↓ move"
		case "hint.key.submit":
			return "Enter"
		case "hint.key.cancel":
			return "Esc"
		case "hint.key.toggle":
			return "←→"
		}
		return key
	}
	h := HintsFromT(translated)
	if !strings.Contains(h.Move, "move") || !strings.Contains(h.Cancel, "Esc") {
		t.Errorf("translations not applied: %+v", h)
	}
}
