package prompt

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// DefaultSelectHeight is the standard scrollable-viewport cap for Select
// prompts with many options. Call sites pass this to Select.Height so a
// single grep-able constant controls the viewport size across the CLI.
const DefaultSelectHeight = 12

// Option is one entry in a Select list.
type Option[T any] struct {
	// Label is the display text.
	Label string
	// Value is the typed payload returned when this option is selected.
	Value T
	// Hint is optional trailing markup (e.g. "(current)", "(latest)").
	Hint string
}

// Select shows a list of options and returns the chosen one's Value.
// The prompt renders inline (no altscreen).
//
// Usage:
//
//	sel := &prompt.Select[string]{
//	    Title:       "Pick a color",
//	    Description: "Use arrows to move.",
//	    Options: []prompt.Option[string]{
//	        {Label: "Red", Value: "red"},
//	        {Label: "Green", Value: "green"},
//	    },
//	    Hints: prompt.Hints{}.WithDefaults(),
//	}
//	choice, err := sel.Run()
type Select[T any] struct {
	Title       string
	Description string
	Options     []Option[T]
	// Default pre-selects the option whose Label matches this value. If
	// no option matches, cursor starts at 0.
	Default string
	// Height caps the scrollable viewport. 0 = show all options inline.
	Height int
	Hints  Hints
}

// Run blocks until the user submits or cancels. Returns ErrCanceled on
// cancel.
func (s *Select[T]) Run() (T, error) {
	var zero T
	if len(s.Options) == 0 {
		return zero, fmt.Errorf("select: no options")
	}

	cursor := 0
	if s.Default != "" {
		for i, o := range s.Options {
			if o.Label == s.Default {
				cursor = i
				break
			}
		}
	}

	m := selectModel[T]{
		title:   s.Title,
		desc:    s.Description,
		options: s.Options,
		hints:   s.Hints.WithDefaults(),
		cursor:  cursor,
		height:  s.Height,
	}
	// Seed offset so a Default past the viewport is visible on the FIRST
	// render — otherwise the cursor scrolls into view only after the user
	// presses a navigation key.
	m.recomputeOffset()

	final, err := runModel(m)
	if err != nil {
		return zero, err
	}
	fm := final.(selectModel[T])
	if !fm.chosen {
		return zero, ErrCanceled
	}
	return fm.options[fm.cursor].Value, nil
}

// ----- bubbletea model -----

type selectModel[T any] struct {
	title   string
	desc    string
	options []Option[T]
	hints   Hints
	cursor  int
	height  int  // 0 = all inline; >0 = capped scrolling viewport
	offset  int  // scroll offset, only used when height > 0
	chosen  bool // Enter was pressed
}

func (m selectModel[T]) Init() tea.Cmd { return nil }

func (m selectModel[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	case "enter":
		m.chosen = true
		return m, tea.Quit
	}
	m.recomputeOffset()
	return m, nil
}

// recomputeOffset adjusts the scroll offset so the cursor is always in
// view when a Height cap is in effect.
func (m *selectModel[T]) recomputeOffset() {
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

func (m selectModel[T]) View() string {
	// Compact final view: one-line summary left in scrollback after submit
	// so the next wizard step is visually separated from this completed one.
	if m.chosen {
		return StyleDone.Render(DonePrefix) + StyleTitle.Render(m.title) +
			"  " + StyleSelected.Render(m.options[m.cursor].Label) + "\n\n"
	}

	var b strings.Builder
	if m.title != "" {
		b.WriteString(StyleActive.Render(ActivePrefix))
		b.WriteString(StyleTitle.Render(m.title))
		b.WriteString("\n")
	}
	if m.desc != "" {
		b.WriteString(StepIndent)
		b.WriteString(StyleDesc.Render(m.desc))
		b.WriteString("\n")
	}
	if m.title != "" || m.desc != "" {
		b.WriteString("\n")
	}

	// Determine visible slice.
	start, end := 0, len(m.options)
	if m.height > 0 && m.height < len(m.options) {
		start = m.offset
		end = m.offset + m.height
		end = min(end, len(m.options))
	}

	for i := start; i < end; i++ {
		o := m.options[i]
		var line string
		if i == m.cursor {
			line = StyleCursor.Render(StepIndent+"> ") + StyleSelected.Render(o.Label)
		} else {
			line = StepIndent + "  " + StyleNormal.Render(o.Label)
		}
		if o.Hint != "" {
			line += "  " + StyleMarker.Render(o.Hint)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	// Scroll hint when viewport is clipped.
	if m.height > 0 && m.height < len(m.options) {
		b.WriteString(StyleHint.Render(fmt.Sprintf("%s(%d/%d)", StepIndent, m.cursor+1, len(m.options))))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	footer := fmt.Sprintf("%s%s   %s   %s",
		StepIndent, m.hints.Move, m.hints.Select, m.hints.Cancel)
	b.WriteString(StyleHint.Render(footer))
	b.WriteString("\n")
	return b.String()
}
