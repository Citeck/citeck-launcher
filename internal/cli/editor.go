package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// editorRunner runs the resolved editor against a file, wired to the terminal.
// A package var so tests substitute a fake that mutates (or ignores) the file.
var editorRunner = func(editor, path string) error {
	parts := strings.Fields(editor) // allow "code -w"-style values
	args := append(parts[1:], path)
	cmd := exec.Command(parts[0], args...) //nolint:gosec // G204: editor comes from the user's own env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// resolveEditor picks the editor: $CITECK_EDITOR → $VISUAL → $EDITOR → OS default.
func resolveEditor() string {
	for _, env := range []string{"CITECK_EDITOR", "VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}

// openInEditor writes initial to a temp file, opens it in the user's editor, and
// returns the edited bytes, whether they changed, and the temp file's path.
//
// The file is NOT removed here. It is the only copy of what the operator typed,
// and whether it may be thrown away depends on something openInEditor cannot
// see: whether the edit reached the daemon. So the path is handed back and the
// caller owns it — see edit_recovery.go. os.CreateTemp gives it mode 0600,
// which it must keep: an app config can carry env values.
//
// A path is returned even alongside an error, whenever one was created, so the
// caller can still decide; only a failed os.CreateTemp answers "".
func openInEditor(initial []byte, suffix string) (edited []byte, changed bool, tmp string, err error) {
	f, err := os.CreateTemp("", "citeck-edit-*"+suffix)
	if err != nil {
		return nil, false, "", fmt.Errorf("create temp file: %w", err)
	}
	path := f.Name()

	if _, err = f.Write(initial); err != nil {
		_ = f.Close()
		return nil, false, path, fmt.Errorf("write temp file: %w", err)
	}
	if err = f.Close(); err != nil {
		return nil, false, path, fmt.Errorf("close temp file: %w", err)
	}

	if err = editorRunner(resolveEditor(), path); err != nil {
		return nil, false, path, fmt.Errorf("run editor: %w", err)
	}

	edited, err = os.ReadFile(path) //nolint:gosec // G304: path is our own os.CreateTemp file, not user-controlled input
	if err != nil {
		return nil, false, path, fmt.Errorf("read edited file: %w", err)
	}
	return edited, !bytes.Equal(initial, edited), path, nil
}
