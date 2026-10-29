package prompt

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func multiSelectModelWith(labels ...string) multiSelectModel[string] {
	opts := make([]Option[string], 0, len(labels))
	for _, l := range labels {
		opts = append(opts, Option[string]{Label: l, Value: "v-" + l})
	}
	return multiSelectModel[string]{
		title:   "T",
		options: opts,
		marked:  make([]bool, len(opts)),
		none:    "(none)",
		hints:   Hints{}.WithDefaults(),
	}
}

func step(t *testing.T, m multiSelectModel[string], keys ...string) (multiSelectModel[string], tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		var got tea.Model
		got, cmd = m.Update(keyMsg(k))
		m = got.(multiSelectModel[string])
	}
	return m, cmd
}

// Space marks the option under the cursor and marks it again to unmark;
// Enter submits whatever is marked, in list order, and Esc submits nothing.
func TestMultiSelect_MarkSubmitCancel(t *testing.T) {
	m := multiSelectModelWith("a", "b", "c")

	m, _ = step(t, m, "down", " ", "down", " ", "up", " ")
	if got := m.values(); len(got) != 1 || got[0] != "v-c" {
		t.Fatalf("values after mark b, mark c, unmark b: %v", got)
	}
	m, _ = step(t, m, "home", "x")
	if got := strings.Join(m.values(), ","); got != "v-a,v-c" {
		t.Fatalf("values in list order, not mark order: %q", got)
	}

	submitted, cmd := step(t, m, "enter")
	if !submitted.submitted || cmd == nil {
		t.Fatal("Enter submits and quits")
	}
	if got := strings.Join(submitted.values(), ","); got != "v-a,v-c" {
		t.Fatalf("submitted values: %q", got)
	}

	canceled, cmd := step(t, m, "esc")
	if canceled.submitted || cmd == nil {
		t.Fatal("Esc quits without submitting")
	}
}

// Nothing marked is a valid answer, and the final view says so in the
// caller's words rather than leaving the title alone on the line.
func TestMultiSelect_NothingMarkedIsAnAnswer(t *testing.T) {
	m := multiSelectModelWith("a", "b")
	m, _ = step(t, m, "enter")
	if !m.submitted {
		t.Fatal("Enter with nothing marked still submits")
	}
	if got := m.values(); len(got) != 0 {
		t.Fatalf("values: %v", got)
	}
	if !strings.Contains(m.View(), "(none)") {
		t.Fatalf("final view lacks the none word: %q", m.View())
	}
	m.marked[1] = true
	if v := m.View(); !strings.Contains(v, "b") || strings.Contains(v, "(none)") {
		t.Fatalf("final view with a mark: %q", v)
	}
}

func TestMultiSelect_ViewShowsBoxesHintsAndDescriptions(t *testing.T) {
	m := multiSelectModelWith("a", "b")
	m.options[1].Hint = "BETA · beta-app"
	m.options[1].Description = "Still cooking."
	m.marked[1] = true
	v := m.View()
	for _, want := range []string{"[ ] a", "[x] b", "BETA · beta-app", "Still cooking.", "Space mark", "Enter submit", "Esc cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "←/→") {
		t.Errorf("the Confirm toggle hint has no place here:\n%s", v)
	}
}

// Cursor movement clamps and keeps the viewport over the cursor, Select's rule.
func TestMultiSelect_NavigationAndViewport(t *testing.T) {
	m := multiSelectModelWith("a", "b", "c", "d", "e", "f")
	m.height = 3
	m, _ = step(t, m, "up", "end")
	if m.cursor != 5 || m.offset != 3 {
		t.Fatalf("end: cursor=%d offset=%d", m.cursor, m.offset)
	}
	m, _ = step(t, m, "down")
	if m.cursor != 5 {
		t.Fatalf("down clamps at the last option: %d", m.cursor)
	}
	m, _ = step(t, m, "home")
	if m.cursor != 0 || m.offset != 0 {
		t.Fatalf("home: cursor=%d offset=%d", m.cursor, m.offset)
	}
	if v := m.View(); !strings.Contains(v, "(1/6)") {
		t.Fatalf("clipped viewport shows its position:\n%s", v)
	}
}

// The Run constructor's inputs: Marked pre-marks by label, None is the final
// view's word, and the hints carry the mark key.
func TestMultiSelect_RunInputs(t *testing.T) {
	s := &MultiSelect[string]{Options: nil}
	if _, err := s.Run(); err == nil {
		t.Fatal("no options is an error, not an empty answer")
	}
	h := Hints{}.WithDefaults()
	if h.Mark != "Space mark" {
		t.Fatalf("default mark hint: %q", h.Mark)
	}
	h = HintsFromT(func(key string, _ ...string) string {
		return map[string]string{"hint.key.move": "m", "hint.key.submit": "s", "hint.key.cancel": "c", "hint.key.toggle": "t", "hint.key.mark": "k"}[key]
	})
	if h.Mark != "k" {
		t.Fatalf("localized mark hint: %q", h.Mark)
	}
}
