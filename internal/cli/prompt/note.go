package prompt

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Note displays a message and waits for the user to press Enter. Esc/Ctrl+C
// returns ErrCanceled so callers can distinguish intentional abort from
// confirmation.
//
// Usage:
//
//	if err := (&prompt.Note{
//	    Title: "Installing Citeck",
//	    Description: "Press Enter to continue...",
//	}).Run(); err != nil {
//	    return err
//	}
type Note struct {
	Title       string
	Description string
	// NextLabel overrides the default "▸ Enter" hint shown in the footer.
	NextLabel string
	Hints     Hints
}

// Run blocks until the user presses Enter (returns nil) or cancels
// (returns ErrCanceled).
func (n *Note) Run() error {
	m := noteModel{
		title: n.Title,
		desc:  n.Description,
		next:  n.NextLabel,
		hints: n.Hints.WithDefaults(),
	}
	final, err := runModel(m)
	if err != nil {
		return err
	}
	if !final.(noteModel).submitted {
		return ErrCanceled
	}
	return nil
}

// ----- bubbletea model -----

type noteModel struct {
	title     string
	desc      string
	next      string
	hints     Hints
	submitted bool
}

func (m noteModel) Init() tea.Cmd { return nil }

func (m noteModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "enter":
		m.submitted = true
		return m, tea.Quit
	}
	return m, nil
}

func (m noteModel) View() string {
	// Compact final view after submit.
	if m.submitted {
		return StyleDone.Render(DonePrefix) + StyleTitle.Render(m.title) + "\n\n"
	}

	var b strings.Builder
	if m.title != "" {
		b.WriteString(StyleActive.Render(ActivePrefix))
		b.WriteString(StyleTitle.Render(m.title))
		b.WriteString("\n\n")
	}
	if m.desc != "" {
		for line := range strings.SplitSeq(m.desc, "\n") {
			b.WriteString(StepIndent)
			b.WriteString(StyleNormal.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	next := m.next
	if next == "" {
		next = m.hints.Select
	}
	footer := StepIndent + "▸ " + next + "   " + m.hints.Cancel
	b.WriteString(StyleHint.Render(footer))
	b.WriteString("\n")
	return b.String()
}
