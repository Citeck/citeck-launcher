package prompt

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Input collects a single text value. Supports password masking and a
// validation function that runs on submit.
//
// Usage:
//
//	inp := &prompt.Input{
//	    Title:       "Server hostname",
//	    Description: "Used in the platform URL",
//	    Placeholder: "example.com",
//	    Validate:    validateHostname,
//	}
//	host, err := inp.Run()
type Input struct {
	Title       string
	Description string
	Placeholder string
	// Value pre-fills the input.
	Value string
	// Password hides characters with •.
	Password bool
	// Validate runs on Enter. A non-nil error keeps the user in the field
	// with the error shown below.
	Validate func(string) error
	Hints    Hints
}

// Run blocks until the user submits or cancels. Returns ErrCanceled on
// cancel.
func (i *Input) Run() (string, error) {
	ti := textinput.New()
	ti.Placeholder = i.Placeholder
	if i.Value != "" {
		ti.SetValue(i.Value)
	}
	if i.Password {
		ti.EchoMode = textinput.EchoPassword
		ti.EchoCharacter = '•'
	}
	ti.Focus()
	ti.Prompt = ""   // we render our own "  > " prefix
	ti.CharLimit = 0 // no limit
	// Width MUST be set explicitly: the textinput default is 0, which in
	// bubbles v0.21 truncates the rendered placeholder/value viewport to
	// ~1 character when focused, so e.g. a "45.15.158.227" placeholder
	// appears as just "4". 64 fits every hostname / URL we'd prompt for.
	ti.Width = 64
	ti.TextStyle = StyleSelected
	ti.PromptStyle = StyleCursor
	ti.PlaceholderStyle = StyleHint

	m := inputModel{
		title:    i.Title,
		desc:     i.Description,
		hints:    i.Hints.WithDefaults(),
		ti:       ti,
		validate: i.Validate,
	}

	final, err := runModel(m)
	if err != nil {
		return "", err
	}
	fm := final.(inputModel)
	if !fm.submitted {
		return "", ErrCanceled
	}
	return fm.ti.Value(), nil
}

// ----- bubbletea model -----

type inputModel struct {
	title     string
	desc      string
	hints     Hints
	ti        textinput.Model
	validate  func(string) error
	errMsg    string
	submitted bool
}

func (m inputModel) Init() tea.Cmd { return textinput.Blink }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if m.validate != nil {
				if err := m.validate(m.ti.Value()); err != nil {
					m.errMsg = err.Error()
					return m, nil
				}
			}
			m.submitted = true
			return m, tea.Quit
		}
		// Any other key clears a stale validation error.
		m.errMsg = ""
	}

	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}

func (m inputModel) View() string {
	// Compact final view after submit.
	if m.submitted {
		val := m.ti.Value()
		var rendered string
		switch {
		case val != "" && m.ti.EchoMode == textinput.EchoPassword:
			rendered = StyleSelected.Render(strings.Repeat("•", 8))
		case val != "":
			rendered = StyleSelected.Render(val)
		case m.ti.Placeholder != "":
			// User submitted empty but a default is set — show the default
			// value in muted style to signal "fallback used" vs typed-value.
			rendered = StyleHint.Render(m.ti.Placeholder)
		default:
			rendered = StyleHint.Render("(empty)")
		}
		return StyleDone.Render(DonePrefix) + StyleTitle.Render(m.title) + "  " + rendered + "\n\n"
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

	// Input line: "   > <typing>"
	b.WriteString(StyleCursor.Render(StepIndent + "> "))
	b.WriteString(m.ti.View())
	b.WriteString("\n")

	if m.errMsg != "" {
		b.WriteString(StyleError.Render(StepIndent + m.errMsg))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	footer := fmt.Sprintf("%s%s   %s", StepIndent, m.hints.Select, m.hints.Cancel)
	b.WriteString(StyleHint.Render(footer))
	b.WriteString("\n")
	return b.String()
}
