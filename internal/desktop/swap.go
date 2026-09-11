package desktop

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/update"
)

// daemonRestarter is the Supervisor slice the swap needs. Narrow on purpose:
// the swap's decisions — which budget each restart gets, what is written to the
// manifest, whether the UI may be reloaded — are then testable without a daemon
// binary, a socket or a window.
type daemonRestarter interface {
	Restart(ctx context.Context, healthTimeout time.Duration) error
}

// ApplyDaemonSwap performs the health-gated daemon swap. The staged (pending)
// payload has already been chosen by SelectDaemonBinary, so restarting is what
// puts it in service. On a failed gate the payload is marked failed — which is
// what makes SelectDaemonBinary hand back the previous good / bundled binary —
// and a second restart rolls into it.
//
// It returns whether the caller may point the UI at the daemon: false means no
// restart came up, so a webview reload would only replace the update dialog
// with a proxy error page.
func ApplyDaemonSwap(ctx context.Context, sup daemonRestarter, updatesDir, version string) (daemonAnswering bool) {
	err := sup.Restart(ctx, UpdateHealthTimeout)
	if err == nil {
		if merr := update.MarkState(updatesDir, version, update.StateGood); merr != nil {
			slog.Error("Failed to mark update good", "err", merr)
		}
		slog.Info("Daemon update applied", "version", version)
		return true
	}
	slog.Error("Daemon update failed health-gate; rolling back", "version", version, "err", err)

	if merr := update.MarkState(updatesDir, version, update.StateFailed); merr != nil {
		slog.Error("Failed to mark update failed", "err", merr)
	}
	// The rollback target is a binary that is KNOWN good and may be an OLDER
	// release whose socket appears only at the end of its boot, so it gets the
	// generous budget — see RollbackHealthTimeout.
	if rerr := sup.Restart(ctx, RollbackHealthTimeout); rerr != nil {
		slog.Error("Rollback restart also failed", "err", rerr)
		return false
	}
	return true
}

// IsDaemonStartingBody reports whether a proxied response is the daemon's boot
// refusal (503 + api.ErrCodeDaemonStarting).
//
// The wrapper's webview gate opens on a timer as well as on readiness ("after
// 30s, proxy anyway"), so a document request can reach a daemon that is still
// booting — and rendering that JSON refusal in the window would replace the
// loading page with unexplained text and no retry. The webview keeps its
// auto-refreshing loading page instead.
func IsDaemonStartingBody(statusCode int, body []byte) bool {
	if statusCode != http.StatusServiceUnavailable {
		return false
	}
	var e api.ErrorDto
	if json.Unmarshal(body, &e) != nil {
		return false
	}
	return e.Code == api.ErrCodeDaemonStarting
}
