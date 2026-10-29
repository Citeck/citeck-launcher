package prompt

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// MultiSelect shows a checklist and returns the Values of the marked options,
// in list order. It renders inline like Select; Space (or x) marks the option
// under the cursor, Enter submits whatever is marked — possibly nothing.
//
// Usage:
//
//	chosen, err := (&prompt.MultiSelect[string]{
//	    Title:   "Experimental modules",
//	    Options: []prompt.Option[string]{{Label: "Scanner", Value: "scanner"}},
//	    Hints:   prompt.Hints{}.WithDefaults(),
//	}).Run()
type MultiSelect[T any] struct {
	Title       string
	Description string
	Options     []Option[T]
	// Marked pre-marks the options whose Label is listed. Nothing is marked
	// by default: the wizard uses this for choices a reflexive Enter must
	// not make.
	Marked []string
	// None is what the compact final view shows when nothing was marked.
	// Empty → "(none)".
	None string
	// Height caps the scrollable viewport. 0 = show all options inline.
	Height int
	Hints  Hints
}

// Run blocks until the user submits or cancels. Returns ErrCanceled on
// cancel.
func (s *MultiSelect[T]) Run() ([]T, error) {
	if len(s.Options) == 0 {
		return nil, fmt.Errorf("multiselect: no options")
	}
	none := s.None
	if none == "" {
		none = "(none)"
	}
	m := multiSelectModel[T]{
		title:   s.Title,
		desc:    s.Description,
		options: s.Options,
		marked:  make([]bool, len(s.Options)),
		none:    none,
		hints:   s.Hints.WithDefaults(),
		height:  s.Height,
	}
	for i, o := range s.Options {
		for _, label := range s.Marked {
			if o.Label == label {
				m.marked[i] = true
			}
		}
	}

	final, err := runModel(m)
	if err != nil {
		return nil, err
	}
	fm := final.(multiSelectModel[T])
	if !fm.submitted {
		return nil, ErrCanceled
	}
	return fm.values(), nil
}

// ----- bubbletea model -----

type multiSelectModel[T any] struct {
	title     string
	desc      string
	options   []Option[T]
	marked    []bool
	none      string
	hints     Hints
	cursor    int
	height    int // 0 = all inline; >0 = capped scrolling viewport
	width     int // terminal cells, refreshed on resize
	offset    int // scroll offset, only used when height > 0
	submitted bool
}

func (m multiSelectModel[T]) Init() tea.Cmd { return nil }

func (m multiSelectModel[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		return m, nil
	}
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.options)-1 {
			m.cursor++
		}
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = len(m.options) - 1
	case " ", "x":
		m.marked[m.cursor] = !m.marked[m.cursor]
	case "enter":
		m.submitted = true
		return m, tea.Quit
	}
	m.recomputeOffset()
	return m, nil
}

// values are the marked options' payloads in list order.
func (m multiSelectModel[T]) values() []T {
	out := make([]T, 0, len(m.options))
	for i, o := range m.options {
		if m.marked[i] {
			out = append(out, o.Value)
		}
	}
	return out
}

// labels are the marked options' labels in list order, for the final view.
func (m multiSelectModel[T]) labels() []string {
	out := make([]string, 0, len(m.options))
	for i, o := range m.options {
		if m.marked[i] {
			out = append(out, o.Label)
		}
	}
	return out
}

// recomputeOffset keeps the cursor in view under a Height cap (Select's rule).
func (m *multiSelectModel[T]) recomputeOffset() {
	if m.height <= 0 {
		m.offset = 0
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
}

func (m multiSelectModel[T]) View() string {
	// Compact final view: one-line summary left in scrollback after submit.
	if m.submitted {
		chosen := m.none
		if labels := m.labels(); len(labels) > 0 {
			chosen = strings.Join(labels, ", ")
		}
		return StyleDone.Render(DonePrefix) + StyleTitle.Render(m.title) +
			"  " + StyleSelected.Render(chosen) + "\n\n"
	}

	var b strings.Builder
	if m.title != "" {
		b.WriteString(StyleActive.Render(ActivePrefix))
		b.WriteString(StyleTitle.Render(m.title))
		b.WriteString("\n")
	}
	if m.desc != "" {
		b.WriteString(m.wrapIndented(m.desc, StepIndent))
		b.WriteString("\n")
	}
	if m.title != "" || m.desc != "" {
		b.WriteString("\n")
	}

	start, end := 0, len(m.options)
	if m.height > 0 && m.height < len(m.options) {
		start = m.offset
		end = min(m.offset+m.height, len(m.options))
	}

	for i := start; i < end; i++ {
		o := m.options[i]
		box := "[ ] "
		if m.marked[i] {
			box = "[x] "
		}
		var line string
		if i == m.cursor {
			line = StyleCursor.Render(StepIndent+"> ") + StyleSelected.Render(box+o.Label)
		} else {
			line = StepIndent + "  " + StyleNormal.Render(box+o.Label)
		}
		if o.Hint != "" {
			line += "  " + StyleMarker.Render(o.Hint)
		}
		b.WriteString(line)
		b.WriteString("\n")
		if o.Description != "" {
			// Under the checkbox, in the hint color, so a module's own words
			// sit with its row rather than in one long preamble.
			b.WriteString(m.wrapIndented(o.Description, StepIndent+"      "))
			b.WriteString("\n")
		}
	}

	if m.height > 0 && m.height < len(m.options) {
		b.WriteString(StyleHint.Render(fmt.Sprintf("%s(%d/%d)", StepIndent, m.cursor+1, len(m.options))))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	footer := fmt.Sprintf("%s%s   %s   %s   %s",
		StepIndent, m.hints.Move, m.hints.Mark, m.hints.Select, m.hints.Cancel)
	b.WriteString(StyleHint.Render(footer))
	b.WriteString("\n")
	return b.String()
}

// wrapIndented renders text to the terminal width with every line prefixed by
// indent, the way Select wraps its description.
func (m multiSelectModel[T]) wrapIndented(text, indent string) string {
	style := StyleHint
	if indent == StepIndent {
		style = StyleDesc
	}
	if m.width > len(indent) {
		style = style.Width(m.width - len(indent))
	}
	return indent + strings.ReplaceAll(style.Render(text), "\n", "\n"+indent)
}
