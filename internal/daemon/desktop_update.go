package daemon

import (
	"context"
	"net/http"
	"os"
	"time"

	"github.com/citeck/citeck-launcher/internal/update"
)

// verbUpdateApply mirrors desktop.VerbUpdateApply. The daemon cannot import
// internal/desktop (import cycle), so the literal is duplicated here exactly as
// in desktop_focus.go / desktop_tray.go.
const verbUpdateApply = "update.apply"

// handleUpdateStatus returns the current updater snapshot (desktop-only).
func (d *Daemon) handleUpdateStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, d.updateSvc.Status())
}

// handleUpdateCheck forces a `latest` re-check, then returns the snapshot. The
// check error (offline etc.) is intentionally NOT surfaced as an HTTP error —
// it is reflected in Status().Error so the UI stays quiet (spec: silent offline).
func (d *Daemon) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	_, _ = d.updateSvc.CheckLatest(r.Context())
	writeJSON(w, d.updateSvc.Status())
}

// handleUpdateChangelog returns the (current, latest] changelog in ?locale=.
func (d *Daemon) handleUpdateChangelog(w http.ResponseWriter, r *http.Request) {
	locale := r.URL.Query().Get("locale")
	notes, err := d.updateSvc.Changelog(r.Context(), locale)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, notes)
}

// handleUpdateApply stages the latest payload (download + verify + extract,
// fully before any swap) and then asks the wrapper to perform the health-gated
// swap via the update.apply control verb. The verb is async on the wrapper side
// (it returns immediately and swaps in the background), so this handler returns
// promptly; the wrapper reloads the webview when the swap settles.
//
// ?retry=true is the user pressing "Try again" on a release that already failed
// its health-gate — the only way past the blacklist that keeps the machine from
// looping on a broken release (see update.UserRetry). It defaults to off, so
// every other caller of this route keeps that loop guard.
func (d *Daemon) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	var stageOpts []update.StageOption
	if r.URL.Query().Get("retry") == "true" {
		stageOpts = append(stageOpts, update.UserRetry())
	}
	version, err := d.updateSvc.Stage(r.Context(), stageOpts...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sock := os.Getenv("CITECK_WRAPPER_SOCK")
	if sock == "" {
		writeError(w, http.StatusServiceUnavailable, "no desktop wrapper to apply update")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := newWrapperClient(sock).call(ctx, verbUpdateApply, map[string]any{"version": version}); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"applying": true, "version": version})
}
