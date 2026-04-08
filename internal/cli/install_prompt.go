package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// isTTYOut returns true if stdout is a terminal.
func isTTYOut() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
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
