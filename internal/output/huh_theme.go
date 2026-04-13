package output

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

// HuhTheme is the shared huh TUI theme used across all CLI and setup
// prompts. Based on Dracula with tweaks for dark terminal readability.
var HuhTheme = newHuhTheme()

func newHuhTheme() *huh.Theme {
	t := huh.ThemeDracula()
	// Description text is too dim (#6272a4) on dark terminals — lighten it.
	light := lipgloss.AdaptiveColor{Dark: "#bfbfbf"}
	t.Focused.Description = t.Focused.Description.Foreground(light)
	t.Blurred.Description = t.Blurred.Description.Foreground(light)
	t.Group.Description = t.Focused.Description

	// Button colors: Dracula's default uses yellow fg on purple bg — on many
	// terminals the selected/focused button ends up light-on-light, making
	// the label unreadable. Override with Dracula pink (#ff79c6) bg + dark
	// (#282a36) fg for the focused button, and a dim fg on transparent bg
	// for the unfocused button.
	darkFg := lipgloss.Color("#282a36")
	pinkBg := lipgloss.Color("#ff79c6")
	dimFg := lipgloss.Color("#bfbfbf")
	focused := lipgloss.NewStyle().
		Foreground(darkFg).
		Background(pinkBg).
		Bold(true).
		Padding(0, 3).
		MarginRight(1)
	blurred := lipgloss.NewStyle().
		Foreground(dimFg).
		Padding(0, 3).
		MarginRight(1)
	t.Focused.FocusedButton = focused
	t.Focused.BlurredButton = blurred
	t.Blurred.FocusedButton = focused
	t.Blurred.BlurredButton = blurred
	return t
}

// ConfirmYes and ConfirmNo hold localized button labels for huh.Confirm.
// Set by the CLI i18n init; default to English.
var (
	ConfirmYes = "Yes"
	ConfirmNo  = "No"
)

// MaxSelectOptions is the soft cap above which a huh.NewSelect should be
// scrollable. Below it we leave .Height() unset so huh sizes the viewport
// to the full options list and never scrolls — this avoids a long-standing
// huh bug where setting Height makes the viewport collapse to a single row
// when the cursor reaches the last option.
const MaxSelectOptions = 12

// SelectHeight returns the value to pass to huh.NewSelect[T].Height(), or 0
// when .Height() should be left unset so the entire options list renders
// inline.
//
// Background: huh's Update() loop unconditionally calls updateViewportHeight()
// on every message, and that function sets viewport.YOffset = s.selected
// whenever a Height has been set (see huh@v1.0.0/field_select.go around
// lines 329, 542-543). After the user navigates to the last option, the
// viewport's top is forcibly aligned to that option, hiding every row above
// it — the visible list collapses to just the cursor row. The "no Height"
// branch of updateViewportHeight does not touch YOffset, so for short
// lists the safest fix is to skip Height() entirely.
//
// For longer lists we still need a bounded viewport, so beyond MaxSelectOptions
// we return a capped height. In that mode huh's collapse-on-last bug can
// still trigger, but it is the lesser evil compared to a list that overflows
// the terminal.
//
// Call sites should use ApplySelectHeight to apply the height conditionally.
func SelectHeight(optionCount int) int {
	if optionCount <= MaxSelectOptions {
		return 0
	}
	return MaxSelectOptions
}

// ApplySelectHeight applies SelectHeight(optionCount) to the given huh.Select
// builder, leaving Height unset when the option count fits inline. Returns
// the same builder for fluent chaining.
func ApplySelectHeight[T comparable](sel *huh.Select[T], optionCount int) *huh.Select[T] {
	if h := SelectHeight(optionCount); h > 0 {
		return sel.Height(h)
	}
	return sel
}

// NewConfirm creates a huh.Confirm with localized Yes/No buttons and the shared theme.
func NewConfirm() *huh.Confirm {
	c := huh.NewConfirm().
		Affirmative(ConfirmYes).
		Negative(ConfirmNo)
	c.WithTheme(HuhTheme)
	return c
}

// huhQuitKeyMap returns a fresh huh KeyMap with Quit bound to BOTH Esc and
// Ctrl+C. huh's default KeyMap only binds Ctrl+C; standalone field.Run()
// and plain form.Run() therefore ignore Esc even though our UI hints say
// "Esc cancel". We install this keymap at the form level via RunField /
// RunForm so the hint matches the actual behavior.
func huhQuitKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("esc", "ctrl+c"))
	return km
}

// RunField wraps a huh field in a one-group form with the shared theme and a
// key map that binds BOTH Esc and Ctrl+C to quit, then runs it.
//
// Use this for ALL standalone `*huh.Input`, `*huh.Select`, `*huh.Confirm`,
// `*huh.Note` prompts instead of calling `.Run()` directly on the field —
// otherwise Esc is silently ignored.
//
// On user cancel, the returned error wraps huh.ErrUserAborted; callers can
// detect it via errors.Is(err, huh.ErrUserAborted).
func RunField(field huh.Field) error {
	err := huh.NewForm(huh.NewGroup(field)).
		WithTheme(HuhTheme).
		WithKeyMap(huhQuitKeyMap()).
		Run()
	if err != nil {
		return fmt.Errorf("huh run field: %w", err)
	}
	return nil
}

// RunForm runs a huh.Form with the shared theme and Esc+Ctrl+C quit key map.
// Replaces the common `huh.NewForm(...).WithTheme(HuhTheme).Run()` pattern.
func RunForm(form *huh.Form) error {
	if err := form.WithTheme(HuhTheme).WithKeyMap(huhQuitKeyMap()).Run(); err != nil {
		return fmt.Errorf("huh run form: %w", err)
	}
	return nil
}
