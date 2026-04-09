package output

import (
	"fmt"
	"os"
)

// IsTTY reports whether stdout is a terminal.
func IsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ClearLines moves cursor up n lines and clears each one (ANSI escape).
func ClearLines(n int) {
	for range n {
		fmt.Print("\033[A\033[2K") //nolint:forbidigo // ANSI cursor control
	}
}
