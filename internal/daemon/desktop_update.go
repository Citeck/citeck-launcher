package daemon

import (
	"context"
	"log/slog"
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
	if _, err := d.updateSvc.CheckLatest(r.Context()); err != nil {
		// Quiet on the WIRE, not in the log. This route is only ever pressed by
		// a person, so it is one line per click — and it is exactly the line a
		// user's "the update never appeared" report needs, which otherwise
		// survived nowhere (the periodic check logs its own failure at DEBUG).
		slog.Warn("Update check failed", "err", err)
	}
	writeJSON(w, d.updateSvc.Status())
}

// handleUpdateChangelog returns the (current, latest] changelog in ?locale=.
func (d *Daemon) handleUpdateChangelog(w http.ResponseWriter, r *http.Request) {
	locale := r.URL.Query().Get("locale")
	notes, err := d.updateSvc.Changelog(r.Context(), locale)
	if err != nil {
		slog.Warn("Update changelog fetch failed", "err", err)
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
	retry := r.URL.Query().Get("retry") == "true"
	var stageOpts []update.StageOption
	if retry {
		stageOpts = append(stageOpts, update.UserRetry())
	}
	version, err := d.updateSvc.Stage(r.Context(), stageOpts...)
	if err != nil {
		// Every exit below logs its reason. A user whose update failed and was
		// rolled back had nothing in daemon.log but `POST …/apply → 500` and a
		// duration, so the cause had to be inferred from the requests around it.
		// WARN, not ERROR: a refused or failed apply leaves the installation
		// exactly as it was — that is what the staging order is for.
		//nolint:gosec // G706: `retry` is a bool from a == comparison, not request text
		slog.Warn("Update apply failed", "retry", retry, "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sock := os.Getenv("CITECK_WRAPPER_SOCK")
	if sock == "" {
		slog.Warn("Update apply cannot reach the desktop wrapper: CITECK_WRAPPER_SOCK is unset", "version", version)
		writeError(w, http.StatusServiceUnavailable, "no desktop wrapper to apply update")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := newWrapperClient(sock).call(ctx, verbUpdateApply, map[string]any{"version": version}); err != nil {
		slog.Warn("Update apply could not hand the payload to the wrapper", "version", version, "err", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, map[string]any{"applying": true, "version": version})
}
