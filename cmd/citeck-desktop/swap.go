//go:build desktop

package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// applySwapSettleDelay gives the just-responded daemon a moment to flush its
// HTTP response to the webview before we stop it for the swap.
const applySwapSettleDelay = 300 * time.Millisecond

// applyDaemonSwap performs the health-gated daemon swap on the wrapper side.
// The decision itself lives in desktop.ApplyDaemonSwap (testable without a
// window); here we only give the just-responded daemon a moment to flush its
// response, and then point the UI at whatever daemon is running afterwards.
func applyDaemonSwap(ctx context.Context, version string, window *application.WebviewWindow, socketClient *http.Client) {
	time.Sleep(applySwapSettleDelay)

	if !desktop.ApplyDaemonSwap(ctx, supervisor, config.UpdatesDir(), version) {
		// Neither the new daemon nor the rollback came up: nothing is listening
		// on the socket, so reloading the webview would only swap the update
		// dialog for a proxy error page. The dialog polls /desktop/update/status
		// itself and ends in "restart the launcher", which is the truth here.
		slog.Error("No daemon is answering after the update attempt; leaving the window alone", "version", version)
		return
	}
	reloadWebview(window) // re-request assets through the proxy → the now-running daemon
	// Whether the swap stuck or rolled back, sync the title to whatever daemon
	// version is actually running now.
	refreshWindowTitle(socketClient, window)
}
