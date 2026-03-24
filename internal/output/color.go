package output

import "os"

const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Cyan   = "\033[36m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
)

var colorsEnabled = true

func init() {
	if os.Getenv("NO_COLOR") != "" {
		colorsEnabled = false
	}
}

func SetColorsEnabled(enabled bool) {
	colorsEnabled = enabled
}

func Colorize(color, text string) string {
	if !colorsEnabled {
		return text
	}
	return color + text + Reset
}

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

func ColorizeStatus(status string) string {
	c := StatusColor(status)
	if c == "" {
		return status
	}
	return Colorize(c, status)
}
