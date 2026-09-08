package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/citeck/citeck-launcher/internal/cli/prompt"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/output"
)

// abortOnCancel bails out of the install wizard cleanly when the user
// presses Esc or Ctrl+C in any prompt helper that doesn't propagate
// errors (promptSelect/promptInput/promptPassword/promptConfirm return
// bare values, not (value, error)). We exit with the conventional
// SIGINT code (130) so the shell's $? matches a real ^C.
//
// The ErrInstallCancelled-returning paths (welcome Note, bundlepicker,
// anything using errors.Is(err, prompt.ErrCanceled) → return) end up
// going through the RunE handler in newInstallCmd which also maps to
// os.Exit(130) — both paths exit the same way so shell scripts wrapping
// `citeck install` see a consistent "user canceled" signal.
//
// os.Exit is safe here because install.go hasn't acquired any resources
// at prompt time that need cleanup — no daemon running, no open file
// handles, no locks. Later stages (TLS cert generation, systemd install,
// daemon start) happen after all prompts are answered.
func abortOnCancel(err error) {
	if err == nil || !errors.Is(err, prompt.ErrCanceled) {
		return
	}
	ensureI18n()
	msg := i18n.T("install.canceled")
	if strings.HasPrefix(msg, "install.") {
		msg = "Install canceled."
	}
	fmt.Fprintln(os.Stderr, msg) //nolint:forbidigo // terminal exit message
	os.Exit(130)
}

// printDoneTitle renders a completed-step title line matching the
// compact-final view used by every prompt primitive — green check mark,
// same left indent. For wizard steps that execute synchronously with no
// user prompt (config save, systemd install), this is how they align
// visually with the preceding prompt-based steps.
func printDoneTitle(title string) {
	fmt.Println() //nolint:forbidigo // CLI separator
	fmt.Printf("%s%s\n",
		prompt.StyleDone.Render(prompt.DonePrefix),
		prompt.StyleTitle.Render(title),
	) //nolint:forbidigo // CLI title line
	fmt.Println() //nolint:forbidigo // CLI separator
}

// displayWidth returns the terminal display width of a string, accounting for
// East Asian wide characters (CJK) that take 2 columns. It is the table
// formatter's measure (output.DisplayWidth) so a wizard line and a table row
// cannot disagree about how wide the same string is.
func displayWidth(s string) int { return output.DisplayWidth(s) }

// padRight pads s with trailing spaces so its display width equals targetWidth.
// If s is already wider than targetWidth the string is returned unchanged.
func padRight(s string, targetWidth int) string {
	gap := targetWidth - displayWidth(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// i18nHints returns localized key hints for prompt primitives. Falls back
// to English defaults when i18n is not yet loaded (e.g. the language
// selection step). Thin shim over prompt.HintsFromT to keep the local
// helper name used throughout this file.
func i18nHints() prompt.Hints { return prompt.HintsFromT(i18n.T) }

// promptSelect shows a select menu and returns the chosen option string.
// On cancel/error, returns the first option.
func promptSelect(label string, options []string) string {
	if len(options) == 0 {
		return ""
	}
	opts := make([]prompt.Option[string], len(options))
	for i, o := range options {
		opts[i] = prompt.Option[string]{Label: o, Value: o}
	}

	res, err := (&prompt.Select[string]{
		Title:   label,
		Options: opts,
		Height:  prompt.DefaultSelectHeight,
		Hints:   i18nHints(),
	}).Run()
	abortOnCancel(err)
	if err != nil {
		return options[0]
	}
	return res
}

// promptInput shows a text input and returns the entered value.
// On cancel/error or empty input, returns defaultVal.
func promptInput(label, hint, defaultVal string) string {
	// Only pass `hint` through as Description — keyboard hints live in the
	// footer now, so we don't default to "hint.input" like huh did.
	v, err := (&prompt.Input{
		Title:       label,
		Description: hint,
		Placeholder: defaultVal,
		Hints:       i18nHints(),
	}).Run()
	abortOnCancel(err)
	if err != nil {
		return defaultVal
	}
	if v == "" {
		return defaultVal
	}
	return strings.TrimSpace(v)
}

// promptPassword shows a masked text input for password entry.
// On cancel/error or empty input, returns empty string.
func promptPassword(label string) string {
	v, err := (&prompt.Input{
		Title:    label,
		Password: true,
		Hints:    i18nHints(),
	}).Run()
	abortOnCancel(err)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// promptConfirm shows a Yes/No prompt and returns the result.
// Respects flagYes — returns defaultYes when --yes is set.
// On cancel/error, returns defaultYes.
func promptConfirm(label string, defaultYes bool) bool {
	if flagYes {
		return defaultYes
	}
	res, err := (&prompt.Confirm{
		Title:       label,
		Affirmative: output.ConfirmYes,
		Negative:    output.ConfirmNo,
		Default:     defaultYes,
		Hints:       i18nHints(),
	}).Run()
	abortOnCancel(err)
	if err != nil {
		return defaultYes
	}
	return res
}
