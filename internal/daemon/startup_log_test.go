package daemon

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/require"
)

// The startup line is the only per-boot fingerprint a daemon.log carries. It
// named the socket, the web UI, the TCP listen address and the pid — and not
// the version, so a log spanning several boots could not say which release any
// of them was. On desktop that is worse than an inconvenience: the running
// daemon is either the bundled executable or a staged auto-update payload out
// of UpdatesDir, which is exactly the question an update rollback raises.

func attrMap(t *testing.T, attrs []any) map[string]string {
	t.Helper()
	require.Zero(t, len(attrs)%2, "slog attrs must be key/value pairs: %v", attrs)
	out := make(map[string]string, len(attrs)/2)
	for i := 0; i < len(attrs); i += 2 {
		key, ok := attrs[i].(string)
		require.True(t, ok, "attr key %v is not a string", attrs[i])
		out[key] = fmt.Sprintf("%v", attrs[i+1])
	}
	return out
}

// TestStartupLogCarriesTheVersion is the whole point: the release identity.
func TestStartupLogCarriesTheVersion(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())

	got := attrMap(t, startupLogAttrs(
		StartOptions{Version: "2.12.0"},
		"/run/citeck/citeck.sock",
		config.DefaultDaemonConfig(),
		"/opt/citeck/bin/citeck-server",
	))

	require.Equal(t, "2.12.0", got["version"], "the startup line must name the running release")
	require.Equal(t, "/run/citeck/citeck.sock", got["socket"], "existing attrs must survive")
	require.Contains(t, got, "pid")
}

// TestStartupLogSaysWhichBinaryIsRunning: bundled vs staged payload, and where
// the payload is — the question a desktop dump with several boots cannot
// otherwise answer.
func TestStartupLogSaysWhichBinaryIsRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)
	staged := filepath.Join(config.UpdatesDir(), "2.13.0", "citeck")

	stagedAttrs := attrMap(t, startupLogAttrs(
		StartOptions{Version: "2.13.0"}, "/sock", config.DefaultDaemonConfig(), staged))
	require.Equal(t, "true", stagedAttrs["staged"], "a binary under UpdatesDir is a staged payload")
	require.Equal(t, staged, stagedAttrs["binary"], "the payload path must be in the line")

	bundledAttrs := attrMap(t, startupLogAttrs(
		StartOptions{Version: "2.12.0"}, "/sock", config.DefaultDaemonConfig(),
		filepath.Join(home, "bin", "citeck-launcher")))
	require.Equal(t, "false", bundledAttrs["staged"], "a binary outside UpdatesDir is the bundled one")
}

// TestStartupLogOmitsTheBinaryWhenItIsUnknown: os.Executable() can fail, and a
// blank path plus a confident staged=false would be a claim we cannot make.
func TestStartupLogOmitsTheBinaryWhenItIsUnknown(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())

	got := attrMap(t, startupLogAttrs(
		StartOptions{Version: "2.12.0"}, "/sock", config.DefaultDaemonConfig(), ""))

	require.NotContains(t, got, "binary")
	require.NotContains(t, got, "staged")
	require.Equal(t, "2.12.0", got["version"], "the version is known even when the path is not")
}
