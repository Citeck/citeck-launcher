package prompt

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Confirm asks a Yes/No question. Returns the boolean answer or
// ErrCanceled on Esc/Ctrl+C.
//
// Usage:
//
//	ok, err := (&prompt.Confirm{
//	    Title:       "Apply changes?",
//	    Affirmative: "Да",
//	    Negative:    "Нет",
//	    Default:     true,
//	}).Run()
type Confirm struct {
	Title       string
	Description string
	// Affirmative/Negative are the button labels. Empty → "Yes"/"No".
	Affirmative string
	Negative    string
	// Default pre-selects the affirmative button when true.
	Default bool
	Hints   Hints
}

// Run blocks until the user picks Yes/No or cancels.
func (c *Confirm) Run() (bool, error) {
	aff, neg := c.Affirmative, c.Negative
	if aff == "" {
		aff = "Yes"
	}
	if neg == "" {
		neg = "No"
	}

	m := confirmModel{
		title: c.Title,
		desc:  c.Description,
		aff:   aff,
		neg:   neg,
		yes:   c.Default,
		hints: c.Hints.WithDefaults(),
	}

	final, err := runModel(m)
	if err != nil {
		return false, err
	}
	fm := final.(confirmModel)
	if !fm.submitted {
		return false, ErrCanceled
	}
	return fm.yes, nil
}

// ----- bubbletea model -----

type confirmModel struct {
	title     string
	desc      string
	aff       string
	neg       string
	yes       bool // currently highlighted side: true=affirmative
	submitted bool
	hints     Hints
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "left", "h":
		m.yes = true
	case "right", "l":
		m.yes = false
	case "tab":
		m.yes = !m.yes
	case "enter":
		m.submitted = true
		return m, tea.Quit
	}
	return m, nil
}

func (m confirmModel) View() string {
	// Compact final view after submit.
	if m.submitted {
		choice := m.neg
		if m.yes {
			choice = m.aff
		}
		return StyleDone.Render(DonePrefix) + StyleTitle.Render(m.title) +
			"  " + StyleSelected.Render(choice) + "\n\n"
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

	b.WriteString(StepIndent)
	if m.yes {
		b.WriteString(StyleButtonFocused.Render(m.aff))
		b.WriteString(StyleButtonBlurred.Render(m.neg))
	} else {
		b.WriteString(StyleButtonBlurred.Render(m.aff))
		b.WriteString(StyleButtonFocused.Render(m.neg))
	}
	b.WriteString("\n\n")

	footer := fmt.Sprintf("%s%s   %s   %s",
		StepIndent, m.hints.Toggle, m.hints.Select, m.hints.Cancel)
	b.WriteString(StyleHint.Render(footer))
	b.WriteString("\n")
	return b.String()
}
