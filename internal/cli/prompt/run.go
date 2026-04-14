package prompt

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrCanceled is returned when the user cancels a prompt via Esc or Ctrl+C.
var ErrCanceled = errors.New("prompt canceled")

// output returns the writer bubbletea should draw to. We use stderr so
// prompts remain interactive even when stdout is redirected (e.g. a test
// probe that captures stdout). Matches internal/cli/bundlepicker/io.go.
func output() io.Writer { return os.Stderr }

// runModel runs a bubbletea program with the shared inline-rendering
// configuration used by every prompt primitive. Returns the final model
// state so the caller can extract results.
//
// Key design choices:
//   - NO altscreen — content stays in scrollback, continuous wizard flow.
//   - Default input handling — bubbletea auto-uses /dev/tty with raw mode
//     so arrow keystrokes are intercepted and not leaked to the shell.
func runModel(m tea.Model) (tea.Model, error) {
	prog := tea.NewProgram(m, tea.WithOutput(output()))
	final, err := prog.Run()
	if err != nil {
		return nil, fmt.Errorf("run prompt: %w", err)
	}
	return final, nil
}
