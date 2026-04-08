package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/citeck/citeck-launcher/internal/output"
	"golang.org/x/term"
)

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

// isTTYOut returns true if stdout is a terminal.
func isTTYOut() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // G115: file descriptor fits in int
}

// clearLines moves cursor up n lines and clears each line (TTY only).
func clearLines(n int) {
	for range n {
		fmt.Print("\033[A\033[2K") //nolint:forbidigo // ANSI clear
	}
}

// promptNumber shows a numbered list and returns the selected option string.
// In TTY mode: erases list + input line after selection, prints "  label: selected".
// In non-TTY mode: prints label + selected value.
func promptNumber(scanner *bufio.Scanner, label string, options []string, defaultIdx int) string { //nolint:unparam // defaultIdx is part of the public API
	tty := isTTYOut()

	// Print numbered options
	for i, opt := range options {
		marker := "  "
		if i == defaultIdx {
			marker = "* "
		}
		fmt.Printf("  %s%d) %s\n", marker, i+1, opt) //nolint:forbidigo // CLI prompt
	}
	lines := len(options)

	// Print prompt line
	fmt.Printf("  %s [%d]: ", label, defaultIdx+1) //nolint:forbidigo // CLI prompt
	lines++ // input line

	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())

	selected := defaultIdx
	if val != "" {
		if idx, err := strconv.Atoi(val); err == nil && idx >= 1 && idx <= len(options) {
			selected = idx - 1
		}
	}

	// ANSI cleanup: erase options + input line, print summary
	if tty {
		clearLines(lines)
		fmt.Printf("  %s: %s\n", label, options[selected]) //nolint:forbidigo // CLI summary
	}

	return options[selected]
}

// promptText shows a text prompt with hint and default.
// In TTY mode: erases input line, prints "  label: value".
func promptText(scanner *bufio.Scanner, label, hint, defaultVal string) string {
	tty := isTTYOut()

	lines := 0
	if hint != "" {
		fmt.Printf("  %s\n", hint) //nolint:forbidigo // CLI hint
		lines++
	}

	if defaultVal != "" {
		fmt.Printf("  %s [%s]: ", label, defaultVal) //nolint:forbidigo // CLI prompt
	} else {
		fmt.Printf("  %s: ", label) //nolint:forbidigo // CLI prompt
	}
	lines++ // input line

	scanner.Scan()
	val := strings.TrimSpace(scanner.Text())
	if val == "" {
		val = defaultVal
	}

	if tty {
		clearLines(lines)
		fmt.Printf("  %s: %s\n", label, val) //nolint:forbidigo // CLI summary
	}

	return val
}

// promptYesNo shows a yes/no prompt.
// In TTY mode: erases input line, prints "  label: Yes/No".
func promptYesNo(scanner *bufio.Scanner, label, hint string, defaultYes bool) bool {
	tty := isTTYOut()

	lines := 0
	if hint != "" {
		fmt.Printf("  %s\n", hint) //nolint:forbidigo // CLI hint
		lines++
	}

	def := "Y/n"
	if !defaultYes {
		def = "y/N"
	}
	fmt.Printf("  %s [%s]: ", label, def) //nolint:forbidigo // CLI prompt
	lines++

	scanner.Scan()
	val := strings.TrimSpace(strings.ToLower(scanner.Text()))

	result := defaultYes
	if val != "" {
		result = val == "y" || val == "yes"
	}

	if tty {
		clearLines(lines)
		answer := "No"
		if result {
			answer = "Yes"
		}
		fmt.Printf("  %s: %s\n", label, answer) //nolint:forbidigo // CLI summary
	}

	return result
}
