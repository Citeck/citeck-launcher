package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/citeck/citeck-launcher/internal/output"
)

// cliTheme is a shorthand for the shared Dracula huh theme.
var cliTheme = output.HuhTheme

// printStepHeader prints a numbered, colored step header with separator.
func printStepHeader(step int, title string) {
	fmt.Println()                                                                     //nolint:forbidigo // CLI step header
	fmt.Printf("  %s\n", output.Colorize(output.Cyan, fmt.Sprintf("── %d. %s ──", step, title))) //nolint:forbidigo // CLI step header
	fmt.Println()                                                                     //nolint:forbidigo // CLI step header
}

// displayWidth returns the terminal display width of a string,
// accounting for East Asian wide characters (CJK) that take 2 columns.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// isWideRune returns true for East Asian Wide characters (CJK ideographs, fullwidth forms).
func isWideRune(r rune) bool {
	return (r >= 0x1100 && r <= 0x115F) || // Hangul Jamo
		r == 0x2329 || r == 0x232A || // angle brackets
		(r >= 0x2E80 && r <= 0x303E) || // CJK Radicals, Kangxi, CJK Symbols
		(r >= 0x3040 && r <= 0x33BF) || // Hiragana, Katakana, Bopomofo, CJK Compat
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Unified Ext A
		(r >= 0x4E00 && r <= 0xA4CF) || // CJK Unified, Yi
		(r >= 0xA960 && r <= 0xA97C) || // Hangul Jamo Extended-A
		(r >= 0xAC00 && r <= 0xD7A3) || // Hangul Syllables
		(r >= 0xF900 && r <= 0xFAFF) || // CJK Compat Ideographs
		(r >= 0xFE30 && r <= 0xFE6B) || // CJK Compat Forms
		(r >= 0xFF01 && r <= 0xFF60) || // Fullwidth Forms
		(r >= 0xFFE0 && r <= 0xFFE6) || // Fullwidth Signs
		(r >= 0x20000 && r <= 0x2FFFD) || // CJK Ext B-F
		(r >= 0x30000 && r <= 0x3FFFD) // CJK Ext G+
}

// promptSelect shows a huh Select menu and returns the selected option string.
// On cancel/error, returns the first option.
func promptSelect(label string, options []string) string {
	huhOpts := make([]huh.Option[string], len(options))
	for i, opt := range options {
		huhOpts[i] = huh.NewOption(opt, opt)
	}

	var selected string
	if len(options) > 0 {
		selected = options[0]
	}

	_ = huh.NewSelect[string]().
		Title(label).
		Options(huhOpts...).
		Value(&selected).
		WithTheme(cliTheme).
		Run()
	return selected
}

// promptInput shows a huh Input prompt and returns the entered value.
// On cancel/error or empty input, returns defaultVal.
func promptInput(label, hint, defaultVal string) string {
	var value string
	input := huh.NewInput().
		Title(label).
		Value(&value).
		Placeholder(defaultVal)
	if hint != "" {
		input = input.Description(hint)
	}

	if err := input.WithTheme(cliTheme).Run(); err != nil {
		return defaultVal
	}
	if value == "" {
		return defaultVal
	}
	return strings.TrimSpace(value)
}

// promptPassword shows a huh Input with masked echo for password entry.
// On cancel/error or empty input, returns empty string.
func promptPassword(label string) string {
	var value string
	err := huh.NewInput().
		Title(label).
		Value(&value).
		EchoMode(huh.EchoModePassword).
		WithTheme(cliTheme).
		Run()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// promptConfirm shows a huh Confirm prompt and returns the result.
// Respects flagYes — returns defaultYes when --yes is set.
// On cancel/error, returns defaultYes.
func promptConfirm(label string, defaultYes bool) bool {
	if flagYes {
		return defaultYes
	}
	result := defaultYes
	err := huh.NewConfirm().
		Title(label).
		Value(&result).
		WithTheme(cliTheme).
		Run()
	if err != nil {
		return defaultYes
	}
	return result
}
