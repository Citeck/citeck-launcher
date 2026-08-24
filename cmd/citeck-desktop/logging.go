//go:build desktop

package main

import (
	"io"
	"log"
	"log/slog"
	"os"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/fsutil"
)

// Wrapper log rotation. Much smaller than the daemon's 50 MiB budget — this file
// holds startup/shutdown bookkeeping and supervisor transitions, not request or
// container logs, so a run contributes kilobytes.
const (
	launcherLogMaxBytes = 5 * 1024 * 1024
	launcherLogMaxFiles = 3
)

// setupWrapperLogging points slog and the stdlib logger at
// <LogDir>/launcher.log, additionally teeing to stderr when the process
// actually has one.
//
// Until 2.11.x the wrapper logged to stderr and nowhere else, which was
// survivable only because the Windows build was accidentally a console
// application: the user got a second black window they never asked for, and
// that window was the ONLY place the wrapper's log existed. Linking it
// -H windowsgui removes the window — and with it stderr — so the log needs a
// real home first, otherwise a startup failure (a held single-instance lock, a
// daemon binary that will not spawn) becomes an app that does nothing at all
// with no trace anywhere. The same applies, less visibly, to a Linux .desktop
// launch or a macOS Finder launch.
//
// Must be called AFTER config.SetDesktopMode(true) — every path helper branches
// on it, so an earlier call would resolve the server-mode log directory.
func setupWrapperLogging() {
	logDir := config.LogDir()
	if err := os.MkdirAll(logDir, 0o755); err != nil { //nolint:gosec // G301: log dir needs 0o755
		// Nothing to log to yet; stderr is the only option and may be dead.
		log.Printf("cannot create log dir %s: %v", logDir, err)
		return
	}

	writer := fsutil.NewRotatingWriter(config.LauncherLogPath(), launcherLogMaxBytes, launcherLogMaxFiles)

	// io.MultiWriter aborts on the FIRST writer that errors, so a dead stderr
	// would silently swallow every record before it reached the file. Include
	// stderr only once it is known to be usable.
	var dest io.Writer = writer
	if stderrUsable() {
		dest = io.MultiWriter(os.Stderr, writer)
	}

	slog.SetDefault(slog.New(fsutil.NewCleanLogHandler(dest, slog.LevelInfo)))
	// log.Fatal from main() and any dependency still on the stdlib logger.
	log.SetOutput(dest)
	log.SetFlags(0)
}

// stderrUsable reports whether os.Stderr refers to a real handle. Under
// -H windowsgui the process is started with no standard handles: GetStdHandle
// yields NULL, os.Stderr wraps that, and every write fails. Stat is the cheapest
// portable probe — it fails on the invalid handle and succeeds on any real
// file, pipe or terminal, which also covers the supervisor's piped case.
func stderrUsable() bool {
	if os.Stderr == nil {
		return false
	}
	_, err := os.Stderr.Stat()
	return err == nil
}
