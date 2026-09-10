package daemon

import (
	"log/slog"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// attachToggleRegenApps lists apps whose attach/detach state changes OTHER
// apps' generated configuration — the proxy's upstream targets (ONLYOFFICE_TARGET,
// AI_TARGET) and the AI↔STT sidecar wiring (CITECK_AI_CALLRECORDING_STT_SIDECARURL
// + dependsOn). Toggling one of these at runtime must regenerate the whole
// namespace so the dependent apps pick up / drop the wiring — a plain single-app
// start/stop only moves that one container and leaves the proxy / AI stale.
//
// Kotlin parity: NamespaceGenerator's static dependsOnDetachedApps set
// {ONLYOFFICE, AI, STT_SIDECAR} consulted by NamespaceRuntime.detachedAppsChanged.
// v1.4.1 changelog: "Trigger namespace regeneration when ai or stt-sidecar is
// attached/detached at runtime, so toggling them in the UI takes effect without
// recreating the namespace."
var attachToggleRegenApps = map[string]bool{
	appdef.AppOnlyoffice: true,
	appdef.AppAi:         true,
	appdef.AppSttSidecar: true,
}

// regenOnAttachToggle reports whether attaching/detaching the named app must
// trigger a namespace regeneration (re-run Generate with the new detached set).
func regenOnAttachToggle(name string) bool { return attachToggleRegenApps[name] }

// regenAfterAttachToggleAsync regenerates the namespace off the request
// goroutine after a cross-wiring app (see attachToggleRegenApps) is attached or
// detached, so the proxy's upstream targets and AI↔STT wiring reflect the new
// detached set. doReload re-runs Generate using the runtime's current
// ManualStoppedApps(), which StopApp/StartApp have already updated synchronously.
//
// Uses the same reloadMu/TryLock coalescing as the other async reload paths:
// doReload holds reloadMu and does slow resolve/generate I/O, so it must not
// block the HTTP handler, and a reload already in progress satisfies the
// regeneration intent (it re-generates from the same updated detached set).
//
// Update & Start is the one path that must NOT coalesce this way — it is the
// only caller passing refreshImages=true, so folding it into an in-flight
// refreshImages=false reload would silently drop the :snapshot digest refresh
// that IS the action. See updateAndStartAsync, which waits on reloadMu instead.
//
// It is gated at PASS level on the long-operation lock, exactly like the
// Update & Start pass and for the same reason: the spawning handler
// (handleAppStart / handleAppStop) has released the lock by the time this runs,
// so a migration can take it in between — and this goroutine re-runs Generate,
// which seeds dependency pins and rewrites the runtime files a migration is
// reading and rewriting. It is not a benign reload. A lost race is SAFE to
// skip: the attach/detach is already persisted in ManualStoppedApps, so the
// next reload or start regenerates from it; the WARN is there because nothing
// else would tell the operator the proxy is briefly stale, and it says when the
// wiring comes back. This is the SAME staleness window the reloadMu coalescing
// below has always had — and it is the ordinary case for the documented
// memory-relief recipe (`citeck stop onlyoffice attorneys ecom …`), where each
// toggle after the first finds the previous one still regenerating.
func (d *Daemon) regenAfterAttachToggleAsync(app, action string) {
	go func() {
		if !d.longOp.TryLock(longOpUpdatePass) {
			//nolint:gosec // G706: app is validated by validateAppName and gated to the constant attachToggleRegenApps set; action is a caller literal
			slog.Warn("Attach-toggle regeneration skipped: "+d.longOp.Holder().busyEnglish()+
				"; the proxy / AI wiring is regenerated on the next reload or start",
				"app", app, "action", action)
			return
		}
		defer d.longOp.Unlock()
		if !d.reloadMu.TryLock() {
			//nolint:gosec // G706: app is validated by validateAppName and gated to the constant attachToggleRegenApps set; action is a caller literal
			slog.Info("Attach-toggle regeneration coalesced into in-progress reload", "app", app, "action", action)
			return
		}
		defer d.reloadMu.Unlock()
		// invokeReload, not doReload: the daemon-wide reload seam, so a test can
		// observe that this pass ran AND that it held the lock while it did.
		if err := d.invokeReload(); err != nil {
			//nolint:gosec // G706: app is validated by validateAppName and gated to the constant attachToggleRegenApps set; action is a caller literal
			slog.Warn("Attach-toggle regeneration failed", "app", app, "action", action, "err", err)
		}
	}()
}
