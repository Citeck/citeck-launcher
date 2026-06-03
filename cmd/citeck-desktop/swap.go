//go:build desktop

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/desktop"
	"github.com/citeck/citeck-launcher/internal/update"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// applySwapSettleDelay gives the just-responded daemon a moment to flush its
// HTTP response to the webview before we stop it for the swap.
const applySwapSettleDelay = 300 * time.Millisecond

// applyDaemonSwap performs the health-gated daemon swap on the wrapper side
// (Spec 2b). The staged (pending) payload is already chosen by SelectDaemonBinary
// (it is newer than our bundled version). On health-gate failure it marks the
// payload failed — so SelectDaemonBinary then returns the previous good / bundled
// binary — and restarts into that (rollback). Either way it reloads the webview so
// the UI reflects the now-running daemon and its /desktop/update/status.
func applyDaemonSwap(ctx context.Context, version string, window *application.WebviewWindow) {
	time.Sleep(applySwapSettleDelay)
	updatesDir := config.UpdatesDir()

	if err := supervisor.Restart(ctx, desktop.UpdateHealthTimeout); err != nil {
		slog.Error("Daemon update failed health-gate; rolling back", "version", version, "err", err)
		if merr := update.MarkState(updatesDir, version, update.StateFailed); merr != nil {
			slog.Error("Failed to mark update failed", "err", merr)
		}
		if rerr := supervisor.Restart(ctx, desktop.UpdateHealthTimeout); rerr != nil {
			slog.Error("Rollback restart also failed", "err", rerr)
		}
	} else {
		if merr := update.MarkState(updatesDir, version, update.StateGood); merr != nil {
			slog.Error("Failed to mark update good", "err", merr)
		}
		slog.Info("Daemon update applied", "version", version)
	}
	window.Reload() // re-request assets through the proxy → the now-running daemon
}
