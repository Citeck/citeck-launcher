// Package prompt provides a consistent bubbletea-based CLI prompt toolkit
// (Select, Input, Confirm, Note, Form). All primitives share the same
// Dracula-palette styling used by internal/cli/bundlepicker so every
// interactive wizard step has the same visual language.
//
// Primitives render inline (no altscreen) — once the user submits, the
// final view is left on the terminal as scrollback, matching the
// look-and-feel of the tabbed bundle picker.
package prompt

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Dracula palette — kept in sync with internal/cli/bundlepicker.
var (
	ColorTitle = lipgloss.Color("#50fa7b") // green
	// ColorFocused / ColorNormal are both the Dracula foreground — they
	// differ only by boldness at the style layer (StyleSelected is bold,
	// StyleNormal is not). An earlier "dim soft gray" for Normal read as
	// unreadable on many terminals, so we align with Dracula's canonical
	// foreground color instead.
	ColorFocused   = lipgloss.Color("#f8f8f2") // light
	ColorNormal    = lipgloss.Color("#f8f8f2") // same — differentiation is via bold
	ColorMuted     = lipgloss.Color("#9aa4cc") // footer hints — brighter than Dracula comment
	ColorCursor    = lipgloss.Color("#ff79c6") // pink
	ColorAccent    = lipgloss.Color("#8be9fd") // cyan
	ColorButtonBg  = lipgloss.Color("#ff79c6") // pink bg for focused button
	ColorButtonFg  = lipgloss.Color("#282a36") // dark fg on button
	ColorSeparator = lipgloss.Color("#44475a") // very dim
	ColorMarker    = lipgloss.Color("#bd93f9") // purple
	ColorError     = lipgloss.Color("#ff5555") // red
)

// Shared styles used across primitives.
var (
	StyleTitle = lipgloss.NewStyle().Bold(true).Foreground(ColorTitle)
	// StyleDesc intentionally uses ColorNormal (not ColorMuted) so the
	// auxiliary hint below a prompt title stays readable on dark terminals
	// with non-perfect contrast. StyleHint below is the muted one, used
	// only for the footer-key line that's meant to fade into the chrome.
	StyleDesc     = lipgloss.NewStyle().Foreground(ColorNormal)
	StyleHint     = lipgloss.NewStyle().Foreground(ColorMuted)
	StyleCursor   = lipgloss.NewStyle().Foreground(ColorCursor).Bold(true)
	StyleSelected = lipgloss.NewStyle().Foreground(ColorFocused).Bold(true)
	StyleNormal   = lipgloss.NewStyle().Foreground(ColorNormal)
	StyleMarker   = lipgloss.NewStyle().Foreground(ColorMarker)
	StyleError    = lipgloss.NewStyle().Foreground(ColorError).Bold(true)
	// StyleDone / StyleActive are the prefix markers to the left of a step
	// title. Active steps get a hollow circle in cyan, completed steps get
	// a green checkmark. Together they give a progress-tracker feel to the
	// wizard flow.
	StyleDone   = lipgloss.NewStyle().Foreground(ColorTitle).Bold(true)
	StyleActive = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true)
)

// Indent prefixes used by every primitive so active and completed steps
// share the same left column. ActivePrefix is 4 cells wide; StepIndent
// matches that width so sub-content (descriptions, input line, footer)
// lines up beneath the title.
const (
	ActivePrefix = " ○ "
	DonePrefix   = " ✓ "
	StepIndent   = "   "
)

// Button styles used by Confirm.
var (
	StyleButtonFocused = lipgloss.NewStyle().
				Foreground(ColorButtonFg).
				Background(ColorButtonBg).
				Bold(true).
				Padding(0, 3).
				MarginRight(1)
	StyleButtonBlurred = lipgloss.NewStyle().
				Foreground(ColorNormal).
				Padding(0, 3).
				MarginRight(1)
)

// Hints are the footer key hints shown below every prompt. Fields are
// exposed so i18n callers can override per locale.
type Hints struct {
	Move   string // e.g. "↑/↓ move"
	Select string // e.g. "Enter select"
	Cancel string // e.g. "Esc cancel"
	Toggle string // e.g. "←/→ toggle"  (Confirm only)
}

// WithDefaults fills unset fields with English defaults.
func (h Hints) WithDefaults() Hints {
	if h.Move == "" {
		h.Move = "↑/↓ move"
	}
	if h.Select == "" {
		h.Select = "Enter submit"
	}
	if h.Cancel == "" {
		h.Cancel = "Esc cancel"
	}
	if h.Toggle == "" {
		h.Toggle = "←/→ toggle"
	}
	return h
}

// HintsFromT builds localized Hints using the given translator function.
// The signature matches the project's `i18n.T(key, args...)` — pass
// `i18n.T` directly. Expects keys `hint.key.move/submit/cancel/toggle`.
// Returns a zero-value Hints{} (which Run() fills with English defaults)
// when the translator returns keys verbatim — i.e. i18n isn't loaded yet,
// e.g. during the language-selection step of the install wizard.
func HintsFromT(t func(key string, args ...string) string) Hints {
	h := Hints{
		Move:   t("hint.key.move"),
		Select: t("hint.key.submit"),
		Cancel: t("hint.key.cancel"),
		Toggle: t("hint.key.toggle"),
	}
	if strings.HasPrefix(h.Move, "hint.") {
		return Hints{}
	}
	return h
}
