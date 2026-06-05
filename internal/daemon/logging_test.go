package daemon

import (
	"log/slog"
	"os"
	"path/filepath"
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
	t.Setenv("CITECK_HOME", "") // force the desktop-home branch (no override)

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

	logPath := filepath.Join(home, ".citeck", "launcher", "log", "daemon.log")
	require.Equal(t, logPath, config.DaemonLogPath(), "desktop mode must resolve the desktop log path")

	data, err := os.ReadFile(logPath) //nolint:gosec // logPath is under t.TempDir()
	require.NoError(t, err, "desktop daemon must write daemon.log under the desktop home")
	require.Contains(t, string(data), "regression-marker-desktop-log")
}
