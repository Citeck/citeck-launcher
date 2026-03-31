package output

import "os"

// Reset and related constants are ANSI escape codes for terminal colors.
const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Cyan   = "\033[36m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
)

var colorsEnabled = os.Getenv("NO_COLOR") == ""

// SetColorsEnabled controls whether terminal color codes are emitted.
func SetColorsEnabled(enabled bool) {
	colorsEnabled = enabled
}

// Colorize wraps text with ANSI color codes if colors are enabled.
func Colorize(color, text string) string {
	if !colorsEnabled {
		return text
	}
	return color + text + Reset
}

// StatusColor returns the ANSI color code for the given status string.
func StatusColor(status string) string {
	switch status {
	case "RUNNING", "HEALTHY", "ok":
		return Green
	case "FAILED", "UNHEALTHY", "PULL_FAILED", "START_FAILED", "STOPPING_FAILED", "error":
		return Red
	case "STARTING", "PULLING", "DEPS_WAITING", "READY_TO_PULL", "READY_TO_START", "warning":
		return Yellow
	case "STOPPED":
		return Dim
	default:
		return ""
	}
}

// ColorizeStatus returns the status string wrapped in its corresponding color.
func ColorizeStatus(status string) string {
	c := StatusColor(status)
	if c == "" {
		return status
	}
	return Colorize(c, status)
}
