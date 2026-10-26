package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/require"
)

// TestSetupDaemonLogging_DesktopWritesToDesktopPath is a regression test for the
// bug where the desktop daemon (a child of the SERVER binary launched with
// --desktop) wrote NO daemon.log: the log writer was initialized before desktop
// mode was applied, so it targeted the server path (/opt/citeck/log), failed to
// open for an unprivileged user, and silently dropped every line. Logging must
// land in the desktop home's log dir.
func TestSetupDaemonLogging_DesktopWritesToDesktopPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCALAPPDATA", home) // the desktop home on Windows is keyed off this one
	t.Setenv("CITECK_HOME", "")    // force the desktop-home branch (no override)

	// Isolate the process-global logging state and restore it afterwards.
	prevLogger := slog.Default()
	logInitOnce = sync.Once{}
	globalLogWriter = nil
	config.ResetDesktopMode()
	t.Cleanup(func() {
		if globalLogWriter != nil {
			_ = globalLogWriter.Close()
		}
		logInitOnce = sync.Once{}
		globalLogWriter = nil
		config.ResetDesktopMode()
		slog.SetDefault(prevLogger)
	})

	setupDaemonLogging(StartOptions{Desktop: true})
	slog.Info("regression-marker-desktop-log")

	// The desktop home is platform-specific (~/.citeck/launcher on Linux,
	// ~/Library/Application Support/Citeck/launcher on macOS, %LOCALAPPDATA%
	// on Windows), so the expectation is not a literal: the log must live under
	// the test's HOME — never under the server's /opt/citeck, which is the bug —
	// and at the launcher's own logs/daemon.log inside it.
	logPath := config.DaemonLogPath()
	require.True(t, strings.HasPrefix(logPath, home+string(filepath.Separator)),
		"desktop mode must resolve the log path under the desktop home %q, got %q", home, logPath)
	require.True(t, strings.HasSuffix(logPath, filepath.Join("launcher", "logs", "daemon.log")),
		"desktop log path must be the launcher's logs/daemon.log, got %q", logPath)

	data, err := os.ReadFile(logPath) //nolint:gosec // logPath is under t.TempDir()
	require.NoError(t, err, "desktop daemon must write daemon.log under the desktop home")
	require.Contains(t, string(data), "regression-marker-desktop-log")
}
