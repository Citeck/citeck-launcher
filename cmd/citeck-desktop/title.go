//go:build desktop

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/citeck/citeck-launcher/internal/desktop"
)

// titleVersionPoll is how often a booting daemon is asked for its version.
const titleVersionPoll = 500 * time.Millisecond

// refreshWindowTitle asks the RUNNING daemon for its version and sets the window
// title to match. The daemon can be a newer build than this wrapper — the
// desktop auto-update swaps the daemon binary but NOT the Wails wrapper — so the
// wrapper's own compile-time version is not authoritative for the title. The
// wrapper simply pulls the version over the daemon's unix socket; the daemon
// needs no knowledge of the wrapper.
//
// It WAITS for a daemon that is still booting (desktop.RunningDaemonVersion):
// every caller runs right after a daemon (re)start, when the daemon answers
// its API with 503 DAEMON_STARTING, and asking once is how the title kept the
// old version after an auto-update. Blocks up to desktop.TitleVersionTimeout,
// so call it from a goroutine. On failure the current title is kept. The
// SetTitle itself runs on the UI thread via InvokeAsync.
func refreshWindowTitle(socketClient *http.Client, window *application.WebviewWindow) {
	ctx, cancel := context.WithTimeout(context.Background(), desktop.TitleVersionTimeout)
	defer cancel()
	ver, err := desktop.RunningDaemonVersion(ctx, socketClient, "http://daemon", titleVersionPoll)
	if err != nil {
		slog.Warn("Window title: the daemon did not report its version; keeping the current title", "err", err)
		return
	}
	application.InvokeAsync(func() {
		window.SetTitle(fmt.Sprintf("Citeck Launcher v%s", ver))
	})
}
