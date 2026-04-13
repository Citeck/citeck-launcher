package bundlepicker

import (
	"io"
	"os"
)

// teaOutput returns the writer bubbletea should draw to. We use stderr so
// the picker remains interactive even when stdout is redirected (e.g. in
// a test probe that captures stdout). This mirrors what huh does.
func teaOutput() io.Writer { return os.Stderr }
