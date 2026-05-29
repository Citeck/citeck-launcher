package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// Regression for the missing-ensureI18n bug: text CLI commands like `citeck
// snapshot list` used to print "snapshot.list.empty" verbatim instead of the
// translated message, because root.go's PersistentPreRun never initialized
// i18n. Only the wizard / setup paths called ensureI18n() explicitly.
//
// Approach: run a side-effecting command (`version`) that exercises
// PersistentPreRun without needing a daemon socket, against a freshly-built
// root cmd whose i18n state we reset to nil first. After PersistentPreRun
// fires, lookups must resolve real strings, not bare keys.
func TestRootCmd_PersistentPreRunInitializesI18n(t *testing.T) {
	// Use a fresh HOME so LoadDaemonConfig defaults to "en" without seeing
	// any locale a parallel test wrote to the real config dir.
	tmpHome := t.TempDir()
	confDir := filepath.Join(tmpHome, "conf")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CITECK_HOME", tmpHome)

	// Force a cold start: clear any state left by other tests in this package.
	i18n.ResetForTest()
	if got := i18n.T("snapshot.list.empty"); got != "snapshot.list.empty" {
		t.Fatalf("precondition violated: i18n appears already initialized, got %q", got)
	}

	root := NewRootCmd(BuildInfo{Version: "test"})
	root.SetArgs([]string{"version"})
	// version writes to stdout; redirect to /dev/null to keep test output clean.
	root.SetOut(os.NewFile(0, os.DevNull))
	root.SetErr(os.NewFile(0, os.DevNull))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute version: %v", err)
	}

	// The PersistentPreRun must have wired the dictionary up. Pick a key only
	// present in the locale JSON — failing this means a future change has
	// dropped ensureI18n() from PersistentPreRun and bare keys leak again.
	got := i18n.T("snapshot.list.empty")
	if got == "snapshot.list.empty" {
		t.Errorf("snapshot.list.empty still returns the bare key — PersistentPreRun forgot to call ensureI18n()")
	}
	if got == "" {
		t.Errorf("snapshot.list.empty resolved to empty string — i18n bundle missing")
	}
}
