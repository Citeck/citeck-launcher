//go:build desktop

package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/citeck/citeck-launcher/internal/api"
)

// refreshWindowTitle asks the RUNNING daemon for its version and sets the window
// title to match. The daemon can be a newer build than this wrapper — the
// desktop auto-update swaps the daemon binary but NOT the Wails wrapper — so the
// wrapper's own compile-time version is not authoritative for the title. The
// wrapper simply pulls the version over the daemon's unix socket; the daemon
// needs no knowledge of the wrapper. Best-effort: on any failure the current
// title is kept. Safe to call from any goroutine (the SetTitle runs on the UI
// thread via InvokeAsync).
func refreshWindowTitle(socketClient *http.Client, window *application.WebviewWindow) {
	req, err := http.NewRequest(http.MethodGet, "http://daemon"+api.DaemonStatus, http.NoBody)
	if err != nil {
		return
	}
	resp, err := socketClient.Do(req)
	if err != nil {
		slog.Debug("Window title: daemon status fetch failed", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	var st api.DaemonStatusDto
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return
	}
	ver := strings.TrimPrefix(st.Version, "dev-")
	if ver == "" {
		return
	}
	application.InvokeAsync(func() {
		window.SetTitle(fmt.Sprintf("Citeck Launcher v%s", ver))
	})
}
