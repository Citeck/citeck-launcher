package output

import (
	"encoding/json"
	"fmt"
	"os"
)

type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

var currentFormat = FormatText

func SetFormat(f Format) {
	currentFormat = f
	if f == FormatJSON {
		SetColorsEnabled(false)
	}
}

func GetFormat() Format {
	return currentFormat
}

func IsJSON() bool {
	return currentFormat == FormatJSON
}

// PrintJSON outputs the value as JSON to stdout.
func PrintJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// PrintText outputs text to stdout.
func PrintText(format string, args ...any) {
	if len(args) == 0 {
		fmt.Println(format)
	} else {
		fmt.Printf(format+"\n", args...)
	}
}

// PrintResult outputs data as JSON or text.
// textFn is called only in text mode.
func PrintResult(data any, textFn func()) {
	if IsJSON() {
		PrintJSON(data)
	} else {
		textFn()
	}
}

// Errf prints to stderr (for progress/errors in human mode).
func Errf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
