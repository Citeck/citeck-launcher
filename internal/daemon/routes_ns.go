package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/form"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

// --- Namespace list ---

//nolint:nestif // listing namespaces requires mode-specific branching with active-namespace status overlay
func (d *Daemon) handleListNamespaces(w http.ResponseWriter, r *http.Request) {
	var result []api.NamespaceSummaryDto

	if config.IsDesktopMode() {
		act := d.active()
		wsID := act.workspaceID
		activeID := ""
		if act.nsConfig != nil {
			activeID = act.nsConfig.ID
		}
		rt := act.runtime
		rows, err := d.store.ListNamespaces(wsID)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		for _, row := range rows {
			summary := api.NamespaceSummaryDto{
				ID:          row.ID,
				WorkspaceID: wsID,
				Name:        row.Name,
				Status:      row.Status,
			}
			if summary.Status == "" {
				summary.Status = string(namespace.NsStatusStopped)
			}
			if cfg, cerr := d.loadNamespaceConfigFromStore(wsID, row.ID); cerr == nil {
				// Display the resolved concrete version for a symbolic "LATEST"
				// key (from the persisted cached bundle), so the list matches
				// the dashboard header instead of showing "...:LATEST".
				var cached *bundle.Def
				if st := loadNsStateFromStore(d.store, wsID, row.ID); st != nil {
					cached = st.CachedBundle
				}
				summary.BundleRef = namespace.ResolveDisplayBundleRef(cfg.BundleRef, cached)
			}
			if activeID == row.ID && rt != nil {
				summary.Status = string(rt.Status())
			}
			result = append(result, summary)
		}
	} else {
		// Server mode: single namespace
		act := d.active()
		wsID := act.workspaceID
		nsCfg := act.nsConfig
		rt := act.runtime
		if nsCfg != nil {
			status := string(namespace.NsStatusStopped)
			if rt != nil {
				status = string(rt.Status())
			}
			var cached *bundle.Def
			if st := loadNsStateFromStore(d.store, wsID, nsCfg.ID); st != nil {
				cached = st.CachedBundle
			}
			result = append(result, api.NamespaceSummaryDto{
				ID:          nsCfg.ID,
				WorkspaceID: wsID,
				Name:        nsCfg.Name,
				Status:      status,
				BundleRef:   namespace.ResolveDisplayBundleRef(nsCfg.BundleRef, cached),
			})
		}
	}

	if result == nil {
		result = []api.NamespaceSummaryDto{}
	}
	writeJSON(w, result)
}

func (d *Daemon) handleDeleteNamespace(w http.ResponseWriter, r *http.Request) {
	nsID := r.PathValue("id")
	if !validateID(nsID) {
		writeError(w, http.StatusBadRequest, "invalid namespace id")
		return
	}

	if !config.IsDesktopMode() {
		writeError(w, http.StatusBadRequest, "cannot delete namespace in server mode")
		return
	}

	act := d.active()
	activeID := ""
	if act.nsConfig != nil {
		activeID = act.nsConfig.ID
	}
	rt := act.runtime
	wsID := act.workspaceID

	// Don't allow deleting the active namespace while it is running.
	if activeID == nsID && rt != nil && rt.Status() != namespace.NsStatusStopped {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "cannot delete active namespace; stop it first")
		return
	}

	// Deleting the active (stopped) namespace: tear down its runtime FIRST so
	// its still-bound state persister cannot re-insert the row after we drop it
	// (which would resurrect a ghost card). Mirrors handleDeactivateNamespace,
	// and the row-delete below runs only after Shutdown drains the loop, so any
	// final persist during shutdown is overwritten by the delete.
	if activeID == nsID {
		if !d.reloadMu.TryLock() {
			writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
			return
		}
		defer d.reloadMu.Unlock()
		// Re-check under reloadMu: a concurrent activate (also reloadMu-gated)
		// may have changed the active namespace since the snapshot above. Only
		// tear down if THIS namespace is still active, so we never shut down a
		// different, now-current runtime.
		cur := d.active().nsConfig
		stillActive := cur != nil && cur.ID == nsID
		if stillActive {
			d.teardownActiveNamespaceForDelete(wsID, nsID)
		}
	}

	// Source of truth: drop the config + state row.
	if err := d.store.DeleteNamespace(wsID, nsID); err != nil {
		writeInternalError(w, err)
		return
	}
	// Best-effort: remove the on-disk rtfiles dir so generated bind-mount
	// files don't leak. Enumeration is row-based now, so a failure here
	// cannot resurrect a ghost entry.
	nsDir := config.NamespaceDir(wsID, nsID)
	if err := os.RemoveAll(nsDir); err != nil { //nolint:gosec // path from config.NamespaceDir
		slog.Warn("Failed to remove namespace rtfiles dir", "dir", nsDir, "err", err) //nolint:gosec // G706: nsDir from config.NamespaceDir(validated ids)
	}

	// Best-effort: remove the namespace's Docker resources (named DATA volumes,
	// network, any leftover containers) so a deleted namespace doesn't leak its
	// postgres/mongo/… volumes. Selected by label, so a non-active namespace is
	// cleaned too. Failures are logged inside PurgeNamespace and never block the
	// delete (the source-of-truth row is already gone).
	if dc := act.dockerClient; dc != nil {
		dc.PurgeNamespace(r.Context(), nsID, wsID)
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "namespace deleted"})
}

// teardownActiveNamespaceForDelete shuts down the active namespace's runtime
// and clears its persisted selection so deleting the active (stopped)
// namespace cannot be undone by a late state persist (which would resurrect a
// ghost row). Mirrors handleDeactivateNamespace's teardown. Caller holds
// reloadMu; the row delete must run AFTER this returns (Shutdown drains the
// loop, so any final persist is then overwritten by the delete).
func (d *Daemon) teardownActiveNamespaceForDelete(wsID, nsID string) {
	// Drop the persisted selection so the next start lands on Welcome.
	if state, _ := d.store.GetState(); state != nil {
		delete(state.SelectedNs, wsID)
		if err := d.store.SetState(*state); err != nil {
			slog.Warn("Failed to clear namespace selection on delete", "ws", wsID, "ns", nsID, "err", err) //nolint:gosec // G706: validated ids
		}
	}

	d.configMu.Lock()
	old := d.clearActiveNamespaceLocked()
	d.configMu.Unlock()

	if old.runtime != nil {
		old.runtime.Shutdown() // drains the loop → no further persistState
	}
	if old.cloudCfgServer != nil {
		old.cloudCfgServer.Stop()
	}
	if old.acmeRenewal != nil {
		old.acmeRenewal.Stop()
	}
}

func (d *Daemon) handleGetQuickStarts(w http.ResponseWriter, _ *http.Request) {
	// No silent fallback (Kotlin 1.x parity): when the active workspace's
	// CUSTOM repo failed to sync and no cached config exists, Welcome must
	// surface the failure — not render the built-in fallback quick start that
	// leads to an infra-only namespace. The message carries the repo URL and
	// the git error text ("authentication required", …) for the UI heuristic.
	// activeWorkspaceConfigForRead re-resolves on a cached error, so adding the
	// repo token clears this on the next refresh without a restart.
	wsCfg, syncErr := d.activeWorkspaceConfigForRead()
	if syncErr != "" {
		writeErrorCode(w, http.StatusBadGateway, api.ErrCodeWsRepoSyncFailed, syncErr)
		return
	}

	var quickStarts []api.QuickStartDto
	if wsCfg != nil {
		latestCache := map[string]string{} // repo → resolved version (memoize per request)
		for _, qs := range wsCfg.QuickStartVariants {
			ref := resolveQuickStartBundleRef(wsCfg, qs)
			quickStarts = append(quickStarts, api.QuickStartDto{
				Name:      qs.Name,
				Template:  qs.Template,
				Snapshot:  qs.Snapshot,
				BundleRef: d.resolveDisplayQuickStartRef(ref, latestCache),
			})
		}
	}
	if quickStarts == nil {
		quickStarts = []api.QuickStartDto{}
	}
	writeJSON(w, quickStarts)
}

// resolveQuickStartBundleRef mirrors Kotlin WelcomeScreen.kt:prepareNsDataToCreate:
// QS bundleRef wins; otherwise the namespace template's bundleRef; otherwise
// `{firstBundleRepoId}:LATEST`. The actual git "LATEST" resolution is deferred
// to namespace-create time — here we just surface the symbolic ref so the
// Welcome screen subtitle matches what Kotlin used to render.
func resolveQuickStartBundleRef(wsCfg *bundle.WorkspaceConfig, qs bundle.QuickStartVariant) string {
	if !qs.Bundle.IsEmpty() {
		return qs.Bundle.String()
	}
	templateID := qs.Template
	for _, tmpl := range wsCfg.NamespaceTemplates {
		if templateID != "" && tmpl.ID != templateID {
			continue
		}
		if templateID == "" && tmpl.ID != "default" {
			continue
		}
		if raw, ok := tmpl.Config["bundleRef"]; ok {
			if s, ok := raw.(string); ok && s != "" {
				return s
			}
		}
		break
	}
	if len(wsCfg.BundleRepos) > 0 {
		return wsCfg.BundleRepos[0].ID + ":LATEST"
	}
	return ""
}

// resolveDisplayQuickStartRef substitutes a concrete version for a "repo:LATEST"
// quick-start ref so the Welcome button subtitle shows e.g. "community:2026.2"
// instead of "community:LATEST" (Kotlin parity: renderQuickStartButtons resolves
// via prepareNsDataToCreate → getLatestRepoBundle BEFORE rendering the label).
// latestCache memoizes one resolve per repo per request.
//
// On desktop this resolves ONLINE so a never-synced bundle repo (fresh
// workspace — Welcome renders before any namespace create) is cloned with the
// workspace git token, and a stale clone is refreshed. The freshness window is
// the repo's pullPeriod (default 1h): git.CloneOrPullWithAuth skips the pull and
// reads the local clone when it was synced within the period, so steady-state
// Welcome loads do no network I/O — matching the Kotlin 1.x GitRepoService
// pullPeriod model. Server mode never auto-pulls (resolveLatestBundleKey forces
// offline off-desktop) and keeps the symbolic ref until the repo is synced.
func (d *Daemon) resolveDisplayQuickStartRef(ref string, latestCache map[string]string) string {
	repo, key, ok := strings.Cut(ref, ":")
	if !ok || !strings.EqualFold(key, "LATEST") {
		return ref
	}
	if cached, seen := latestCache[repo]; seen {
		if cached == "" {
			return ref
		}
		return repo + ":" + cached
	}
	resolved, ok := d.resolveLatestBundleKey(d.activeWorkspaceID(), repo, false /* honor pullPeriod freshness */)
	if !ok {
		latestCache[repo] = ""
		return ref
	}
	latestCache[repo] = resolved
	return repo + ":" + resolved
}

// resolveLatestBundleKey resolves a repo's symbolic "LATEST" to its concrete
// latest version via the bundle resolver (Kotlin parity:
// BundlesService.getLatestRepoBundle). Returns ("", false) on any failure
// (empty repo, offline, repo not yet synced) so the caller keeps "LATEST".
func (d *Daemon) resolveLatestBundleKey(wsID, repo string, offline bool) (string, bool) {
	if repo == "" {
		return "", false
	}
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(wsID), makeTokenLookup(d.secretService)).
		WithWorkspaceRepo(lookupWorkspaceRepoOpts(d.store, d.secretService, wsID)).
		WithWorkspaceOverlay(workspaceConfigOverlay(d.store, wsID))
	// Server mode never auto-pulls git; desktop may pull to find the latest tag,
	// throttled by the repo's pullPeriod (a clone synced within the period is read
	// without network). offline=true forces a no-pull read even on desktop.
	if offline || !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	res, err := resolver.Resolve(bundle.Ref{Repo: repo, Key: "LATEST"})
	if err != nil || res == nil || res.Bundle == nil || res.Bundle.Key.Version == "" {
		slog.Debug("Resolve LATEST at create failed; keeping symbolic ref", "repo", repo, "err", err)
		return "", false
	}
	return res.Bundle.Key.Version, true
}

// --- Namespace activation ---

// handleActivateNamespace switches the active namespace within the current
// workspace. Desktop-only — server-mode has a single CLI-pinned namespace.
//
// Contract (Kotlin parity with NamespaceScreen.kt namespace-picker semantics):
//   - Refuse when no target namespace is specified.
//   - Refuse when the requested namespace doesn't exist on disk.
//   - Refuse when the currently active namespace is not STOPPED (the user
//     must stop running containers before switching).
//   - On success: tear down the current runtime, build the new namespace's
//     runtime via loadNamespace, atomically swap it into the daemon, and
//     persist the selection in LauncherState.SelectedNs[wsID]. The new
//     namespace is loaded in STOPPED state — the user clicks Start to run it.
func (d *Daemon) handleActivateNamespace(w http.ResponseWriter, r *http.Request) {
	if !d.requireDesktop(w) {
		return
	}
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	nsID := r.PathValue("id")
	if !validateID(nsID) {
		writeError(w, http.StatusBadRequest, "invalid namespace id")
		return
	}

	act := d.active()
	rt := act.runtime
	wsID := act.workspaceID
	curNs := ""
	if act.nsConfig != nil {
		curNs = act.nsConfig.ID
	}

	if curNs == nsID {
		writeJSON(w, api.ActionResultDto{Success: true, Message: "namespace already active"})
		return
	}
	if rt != nil && rt.Status() != namespace.NsStatusStopped {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning,
			"current namespace is not stopped; stop it before switching")
		return
	}

	// Target must exist in the current workspace.
	if _, ok, err := d.store.LoadNamespaceConfig(wsID, nsID); err != nil {
		writeInternalError(w, err)
		return
	} else if !ok {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeNamespaceNotFound,
			fmt.Sprintf("namespace %q not found in workspace %q", nsID, wsID))
		return
	}

	// Build the new namespace runtime BEFORE persisting the selection or
	// tearing down current state — if loading fails, the daemon stays on
	// the previous namespace and the user can retry without a restart.
	loaded, err := loadNamespace(loadNamespaceInput{
		Store:         d.store,
		SecretService: d.secretService,
		DockerClient:  nil, // build a fresh client scoped to this ns (loadNamespace)
		DaemonCfg:     d.daemonCfg,
		Licenses:      d.licenses,
		WorkspaceID:   wsID,
		NamespaceID:   nsID,
		Desktop:       d.desktop,
	})
	if err != nil {
		writeInternalError(w, fmt.Errorf("load namespace %q: %w", nsID, err))
		return
	}
	if loaded.NsConfig == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeNamespaceNotFound,
			fmt.Sprintf("namespace %q config could not be loaded", nsID))
		return
	}

	// Atomically swap in the new namespace state. The old runtime's
	// dispatcher / SSE / probes are torn down after the swap so external
	// observers see a clean handoff. Switched namespace is loaded but NOT
	// auto-started — user clicks Start (Kotlin parity).
	if err := d.installLoadedNamespace(loaded, wsID, nsID); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("switched to namespace %q", loaded.NsConfig.Name),
	})
}

// handleDeactivateNamespace clears the workspace's namespace selection so the
// dashboard returns to Welcome and a daemon restart no longer auto-loads the
// previous namespace. Desktop-only — server-mode has a single CLI-pinned
// namespace and no Welcome screen.
//
// Refuses while the current namespace is not STOPPED (mirrors switch).
func (d *Daemon) handleDeactivateNamespace(w http.ResponseWriter, r *http.Request) {
	_ = r
	if !d.requireDesktop(w) {
		return
	}
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	act := d.active()
	rt := act.runtime
	wsID := act.workspaceID

	if rt != nil && rt.Status() != namespace.NsStatusStopped {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning,
			"current namespace is not stopped; stop it before exiting")
		return
	}

	// Persist: drop SelectedNs[wsID] so the next daemon start lands on
	// Welcome. Welcome is the canonical Empty state — there's no
	// first-namespace auto-pick on startup, so missing == Welcome.
	state, _ := d.store.GetState()
	if state == nil {
		state = &storage.LauncherState{}
	}
	if state.WorkspaceID == "" {
		state.WorkspaceID = wsID
	}
	delete(state.SelectedNs, wsID)
	if err := d.store.SetState(*state); err != nil {
		writeInternalError(w, fmt.Errorf("persist namespace selection: %w", err))
		return
	}

	// Tear down the current runtime under configMu. Subsequent API calls see
	// a nil active runtime and treat the daemon as "no namespace loaded" —
	// the UI's Welcome screen path already handles that state.
	d.configMu.Lock()
	old := d.clearActiveNamespaceLocked()
	d.configMu.Unlock()

	if old.runtime != nil {
		old.runtime.Shutdown()
	}
	if old.cloudCfgServer != nil {
		old.cloudCfgServer.Stop()
	}
	if old.acmeRenewal != nil {
		old.acmeRenewal.Stop()
	}

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: "namespace deactivated",
	})
}

// --- Namespace creation + Bundles ---

// createNamespaceError carries the HTTP mapping for a failed namespace create
// so handleCreateNamespace stays decode/validate/respond glue. An empty code
// renders via writeError (plain message), a non-empty one via writeErrorCode.
type createNamespaceError struct {
	status  int
	code    string
	message string
}

func (e *createNamespaceError) Error() string { return e.message }

// handleCreateNamespace is thin HTTP glue: decode → form-validate → delegate
// to the createNamespace service function → map typed errors to responses.
func (d *Daemon) handleCreateNamespace(w http.ResponseWriter, r *http.Request) {
	var req api.NamespaceCreateDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if fields := validateNamespaceCreateForm(req); len(fields) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.ValidationErrorDto{
			Error:  "validation failed",
			Fields: fields,
		})
		return
	}

	// A snapshot-backed create (download + volume restore) can run for
	// minutes, exceeding the server's WriteTimeout — lift the write deadline
	// for this request so the response isn't cut off mid-import (same
	// approach as the log-follow handler in routes_config.go). The Web UI
	// applies its own longer client timeout for snapshot-backed creates.
	// Lifted unconditionally: whether a snapshot applies is only known after
	// template resolution inside the service, and a deadline-free response
	// write is harmless for the fast no-snapshot path.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	nsCfg, err := d.createNamespace(req)
	if err != nil {
		var ce *createNamespaceError
		switch {
		case errors.As(err, &ce) && ce.code != "":
			writeErrorCode(w, ce.status, ce.code, ce.message)
		case errors.As(err, &ce):
			writeError(w, ce.status, ce.message)
		default:
			writeInternalError(w, err)
		}
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("namespace %q created", nsCfg.Name)})
}

// validateNamespaceCreateForm runs the namespace-create form spec against the
// request. Host/port aren't exposed by the create dialog — the user edits
// them via raw YAML — so they only feed the validator when the request
// actually carries a non-empty/non-zero value. Otherwise the form spec's
// required-range check rejects the implicit port=0.
func validateNamespaceCreateForm(req api.NamespaceCreateDto) []api.FieldErrorDto {
	spec := form.GetSpec(form.NamespaceCreateFormID)
	if spec == nil {
		return nil
	}
	data := map[string]any{
		"name":     req.Name,
		"authType": req.AuthType,
	}
	if req.Host != "" {
		data["host"] = req.Host
	}
	if req.Port > 0 {
		data["port"] = float64(req.Port)
	}
	fieldErrs := form.Validate(spec, data)
	if len(fieldErrs) == 0 {
		return nil
	}
	fields := make([]api.FieldErrorDto, len(fieldErrs))
	for i, fe := range fieldErrs {
		fields[i] = api.FieldErrorDto{Key: fe.Key, Message: fe.Message}
	}
	return fields
}

// createNamespace is the create-namespace service function: template merge,
// ID generation, persistence, create-time snapshot import, and desktop
// auto-activation. Field-level form validation is the handler's job; errors
// that map to specific HTTP responses come back as *createNamespaceError.
func (d *Daemon) createNamespace(req api.NamespaceCreateDto) (*namespace.Config, error) {
	// Resolve the target workspace.
	wsID := req.WorkspaceID
	if wsID == "" {
		wsID = d.activeWorkspaceID()
	}
	if !validateID(wsID) {
		return nil, &createNamespaceError{status: http.StatusBadRequest, message: "invalid workspace id"}
	}

	nsCfg, err := d.buildNamespaceConfigFromCreate(req, wsID)
	if err != nil {
		return nil, err
	}
	if err := d.persistNewNamespace(wsID, nsCfg); err != nil {
		return nil, err
	}

	// Server mode: bootstrap pre-initializes default-password encryption at
	// daemon startup (initSecretService), so this is a belt-and-braces retry
	// for the rare degraded case where that initialization failed. Desktop
	// mode must NOT initialize here: encryption is deliberately deferred
	// until the user adds their first user secret, at which point the UI
	// prompts for a real master password (ErrEncryptionNotSetUp flow) —
	// silently encrypting with the well-known default would bypass that.
	if !config.IsDesktopMode() && !d.secretService.IsEncrypted() {
		if encErr := d.secretService.SetMasterPassword(storage.DefaultMasterPassword, true); encErr != nil {
			slog.Error("Failed to set up secrets encryption during namespace creation", "err", encErr)
		} else {
			slog.Info("Secrets encrypted with default password during namespace creation")
		}
	}

	if nsCfg.Snapshot != "" {
		d.importCreateSnapshot(wsID, nsCfg)
	}
	d.autoActivateAfterCreate(wsID, nsCfg.ID)
	return nsCfg, nil
}

// buildNamespaceConfigFromCreate translates the create request into a full
// namespace.Config: template merge (default or explicit QuickStart variant),
// request-field overlay, opaque-ID generation with collision retry, and
// symbolic-LATEST pinning.
//
//nolint:nestif // template selection mirrors the Kotlin create flow's branching
func (d *Daemon) buildNamespaceConfigFromCreate(req api.NamespaceCreateDto, wsID string) (*namespace.Config, error) {
	// Generate namespace config — start from template if specified
	nsCfg := namespace.DefaultNamespaceConfig()

	templateID := req.Template
	if templateID == "" {
		templateID = "default" // use default template if none specified
	}
	wsCfg := d.active().workspaceConfig
	if templateID == "default" {
		// Shared helper — keeps this path in lockstep with
		// handleNamespaceCreateDefaults so the form preview matches what
		// the server actually persists.
		applyDefaultTemplate(&nsCfg, wsCfg)
		nsCfg.Template = templateID
	} else if wsCfg != nil {
		// Non-default templates (QuickStart variants) — apply explicit template.
		for _, tmpl := range wsCfg.NamespaceTemplates {
			if tmpl.ID == templateID {
				if len(tmpl.Config) > 0 {
					if tmplData, err := yaml.Marshal(tmpl.Config); err == nil {
						_ = yaml.Unmarshal(tmplData, &nsCfg)
					}
				}
				nsCfg.Template = templateID
				break
			}
		}
		if nsCfg.BundleRef.IsEmpty() && req.BundleRepo == "" && len(wsCfg.BundleRepos) > 0 {
			nsCfg.BundleRef = bundle.Ref{Repo: wsCfg.BundleRepos[0].ID, Key: "LATEST"}
		}
	}

	nsCfg.Name = req.Name
	// Opaque random ID — the human Name and the on-disk ID are decoupled
	// (Kotlin parity: IdUtils.createStrId). Retry a few times to dodge an
	// extremely unlikely collision with an existing on-disk slug.
	for range 10 {
		candidate := generateEntityID()
		if candidate == "" {
			continue
		}
		if _, taken, _ := d.store.LoadNamespaceConfig(wsID, candidate); taken {
			continue // taken
		}
		nsCfg.ID = candidate
		break
	}
	if nsCfg.ID == "" {
		return nil, fmt.Errorf("failed to generate namespace id")
	}
	if req.AuthType != "" {
		nsCfg.Authentication.Type = namespace.AuthenticationType(req.AuthType)
	}
	if len(req.Users) > 0 {
		nsCfg.Authentication.Users = req.Users
	}
	if req.Host != "" {
		nsCfg.Proxy.Host = req.Host
	}
	if req.Port > 0 {
		nsCfg.Proxy.Port = req.Port
	}
	if req.TLSEnabled {
		nsCfg.Proxy.TLS.Enabled = true
		if req.TLSMode == "letsencrypt" {
			nsCfg.Proxy.TLS.LetsEncrypt = true
		}
		// self-signed cert is generated at daemon startup when certPath is empty and letsEncrypt is false
		applySelfSignedTLSDefaults(&nsCfg)
	}
	nsCfg.PgAdmin.Enabled = req.PgAdminEnabled
	if req.BundleRepo != "" && req.BundleKey != "" {
		nsCfg.BundleRef = bundle.Ref{Repo: req.BundleRepo, Key: req.BundleKey}
	}

	// Store the effective snapshot id in the config so the demo data is imported
	// before start (createNamespace) and re-imported on later restarts only when
	// absent (marker-guarded). req.Snapshot (Quick Start / create dialog) wins
	// over a template-provided snapshot already unmarshalled into nsCfg.Snapshot
	// (Kotlin parity: WelcomeScreen.kt:299 withSnapshot).
	if req.Snapshot != "" {
		nsCfg.Snapshot = req.Snapshot
	}

	// Resolve a symbolic "LATEST" bundle key to the concrete latest version and
	// PIN it. The launcher never persists a raw "LATEST" — that would silently
	// auto-update the namespace between bundle versions on reload, which we
	// don't want ("LATEST" is only a picker marker for the newest version). If
	// it can't be resolved the bundle repo isn't synced, so refuse to create a
	// broken namespace rather than store "LATEST" (Kotlin 1.x also never stored
	// it).
	if strings.EqualFold(nsCfg.BundleRef.Key, "LATEST") {
		resolved, ok := d.resolveLatestBundleKey(wsID, nsCfg.BundleRef.Repo, false /* create: pull to pin truly-latest */)
		if !ok {
			return nil, &createNamespaceError{
				status:  http.StatusConflict,
				code:    api.ErrCodeBundleNotSynced,
				message: fmt.Sprintf("bundle repo %q has no synced versions to pin — sync it (Force Update) before creating a namespace", nsCfg.BundleRef.Repo),
			}
		}
		nsCfg.BundleRef.Key = resolved
	}
	return &nsCfg, nil
}

// persistNewNamespace serializes the config, re-checks the ID for a collision
// via the store (replaces the historical O_EXCL file create), and writes
// through the persistNamespaceConfig validation choke-point.
func (d *Daemon) persistNewNamespace(wsID string, nsCfg *namespace.Config) error {
	data, err := namespace.MarshalNamespaceConfig(nsCfg)
	if err != nil {
		return fmt.Errorf("marshal namespace config: %w", err)
	}
	if _, exists, lerr := d.store.LoadNamespaceConfig(wsID, nsCfg.ID); lerr != nil {
		return fmt.Errorf("check namespace id collision: %w", lerr)
	} else if exists {
		return &createNamespaceError{status: http.StatusConflict, code: api.ErrCodeNamespaceExists,
			message: fmt.Sprintf("namespace %q already exists", nsCfg.ID)}
	}
	if persistErr := d.persistNamespaceConfig(wsID, nsCfg.ID, data); persistErr != nil {
		return &createNamespaceError{status: http.StatusBadRequest, code: api.ErrCodeInvalidConfig,
			message: persistErr.Error()}
	}
	return nil
}

// importCreateSnapshot imports the configured snapshot synchronously BEFORE
// the namespace is activated and started, so volumes are populated before any
// container mounts them (Kotlin parity: NamespacesService.kt:131-143 imports
// in the create handler). Inline — not a background goroutine — to remove the
// race where Quick Start's immediate start brought containers up on empty
// volumes while the import was still running. This create-time path is the
// ONLY place a snapshot is imported on namespace creation; there is no
// boot-time auto-import (a `snapshot:` config field is just a record of which
// snapshot the namespace was created from, not a trigger).
func (d *Daemon) importCreateSnapshot(wsID string, nsCfg *namespace.Config) {
	// The import creates volumes scoped to the NEW namespace, so it needs a
	// client scoped to (wsID, nsCfg.ID) — NOT the active dockerClient, which is scoped
	// to whatever namespace was last active. Server mode has a single
	// namespace (the active client is correct); desktop builds a transient client
	// just for the import (the runtime gets its own via loadNamespace in the
	// auto-activate step).
	importClient := d.activeDockerClient()
	if config.IsDesktopMode() {
		if fc, fcErr := docker.NewClient(wsID, nsCfg.ID); fcErr != nil {
			slog.Warn("Create: failed to build docker client for snapshot import", "nsID", nsCfg.ID, "err", fcErr)
		} else {
			defer fc.Close()
			importClient = fc
		}
	}
	d.downloadAndImportSnapshot(importClient, nsCfg.Snapshot, wsID, nsCfg.ID)
}

// autoActivateAfterCreate activates the newly-created namespace in desktop
// mode when the daemon has no current namespace loaded (Welcome / quick-start
// flow). Without this, the UI's immediate postNamespaceStart() after
// createNamespace fails with "no namespace configured" because the on-disk
// config was written but never wired into the active runtime. Narrowly guarded on
// no-current-ns so a user creating a second namespace from an already-loaded
// workspace keeps the current one active (they switch explicitly via the
// picker). Best-effort: failures are logged, never surfaced as create errors.
func (d *Daemon) autoActivateAfterCreate(wsID, nsID string) {
	if !config.IsDesktopMode() {
		return
	}
	act := d.active()
	hasCurrent := act.runtime != nil && act.nsConfig != nil
	activeWsID := act.workspaceID
	if hasCurrent || wsID != activeWsID {
		return
	}
	if !d.reloadMu.TryLock() {
		return
	}
	defer d.reloadMu.Unlock()
	loaded, loadErr := loadNamespace(loadNamespaceInput{
		Store:         d.store,
		SecretService: d.secretService,
		// nil → loadNamespace builds the runtime client scoped to
		// nsID. Never inject the active dockerClient: it is scoped to the
		// previously-active namespace (the wrong-namespace bug).
		DockerClient: nil,
		DaemonCfg:    d.daemonCfg,
		Licenses:     d.licenses,
		WorkspaceID:  activeWsID,
		NamespaceID:  nsID,
		Desktop:      d.desktop,
	})
	if loadErr != nil {
		slog.Warn("Auto-activate after create failed (load)", "nsID", nsID, "err", loadErr)
		return
	}
	if loaded.NsConfig == nil {
		return
	}
	if err := d.installLoadedNamespace(loaded, activeWsID, nsID); err != nil {
		slog.Warn("Auto-activate after create failed (install)", "nsID", nsID, "err", err)
		return
	}
	slog.Info("Auto-activated newly-created namespace", "nsID", nsID)
}

// handleGetNamespaceEdit returns the typed subset of namespace.yml consumed
// by the Web UI's "edit namespace" form, for the namespace addressed by the
// {id} path segment (NOT necessarily the active one — Welcome edits rows
// without activating them). Values are the AUTHORITATIVE stored ones, loaded
// from the namespace's persisted namespace.yml: in particular the bundle key
// comes back RAW (a stored "LATEST" is returned as "LATEST", never the
// display-resolved concrete version). The form drives a focused field set
// (name, bundle, auth, host, port, TLS, pgAdmin); fields outside the DTO are
// preserved on PUT so power users editing the raw YAML are not surprised by
// silent rewrites.
func (d *Daemon) handleGetNamespaceEdit(w http.ResponseWriter, r *http.Request) {
	nsID := r.PathValue("id")
	if !validateID(nsID) {
		writeError(w, http.StatusBadRequest, "invalid namespace id")
		return
	}
	nsCfg, err := d.loadNamespaceConfigFromStore(d.activeWorkspaceID(), nsID)
	if err != nil {
		if errors.Is(err, errNamespaceNotFound) {
			writeErrorCode(w, http.StatusNotFound, api.ErrCodeNamespaceNotFound,
				fmt.Sprintf("namespace %q not found", nsID))
			return
		}
		writeInternalError(w, fmt.Errorf("load namespace config: %w", err))
		return
	}
	users := nsCfg.Authentication.Users
	if users == nil {
		users = []string{}
	}
	tlsEnabled := nsCfg.Proxy.TLS.Enabled
	pgAdminEnabled := nsCfg.PgAdmin.Enabled
	dto := api.NamespaceEditDto{
		Name:           nsCfg.Name,
		BundleRepo:     nsCfg.BundleRef.Repo,
		BundleKey:      nsCfg.BundleRef.Key,
		AuthType:       string(nsCfg.Authentication.Type),
		Users:          users,
		Host:           nsCfg.Proxy.Host,
		Port:           nsCfg.Proxy.Port,
		TLSEnabled:     &tlsEnabled,
		PgAdminEnabled: &pgAdminEnabled,
	}
	writeJSON(w, dto)
}

// handleNamespaceCreateDefaults computes pre-filled form values for the
// "Create namespace" dialog (Kotlin 1.x parity — NamespacesService.toFormData
// when entity is null). Returns:
//   - Name: auto-generated "Citeck #N" where N is the first integer that
//     doesn't collide with an existing namespace name in the workspace.
//   - BundleRepo / BundleKey: from the "default" namespace template's bundleRef;
//     empty parts fall back to first BundleRepo + "LATEST". "LATEST" stays
//     symbolic — git resolution happens at namespace-create time on the server.
//   - AuthType / Users: from the template's authentication block, falling back
//     to namespace.DefaultNamespaceConfig() (BASIC + ["admin"]).
//
// Desktop-only — the create dialog isn't reachable in server mode. We still
// answer with sane fallbacks so the endpoint never 404s the frontend.
func (d *Daemon) handleNamespaceCreateDefaults(w http.ResponseWriter, _ *http.Request) {
	act := d.active()
	wsCfg := act.workspaceConfig
	wsID := act.workspaceID

	nsCfg := namespace.DefaultNamespaceConfig()
	applyDefaultTemplate(&nsCfg, wsCfg)

	users := nsCfg.Authentication.Users
	if users == nil {
		users = []string{}
	}
	dto := api.NamespaceCreateDefaultsDto{
		Name:       d.nextDefaultNamespaceName(wsID),
		BundleRepo: nsCfg.BundleRef.Repo,
		BundleKey:  nsCfg.BundleRef.Key,
		AuthType:   string(nsCfg.Authentication.Type),
		Users:      users,
	}
	writeJSON(w, dto)
}

// nextDefaultNamespaceName scans existing namespaces (desktop: all in the
// active workspace; server: the single CLI-pinned one) and returns the first
// "Citeck #N" name that isn't taken. Mirrors the Kotlin defaultNameNum loop.
func (d *Daemon) nextDefaultNamespaceName(wsID string) string {
	used := d.collectUsedNamespaceNames(wsID)
	num := len(used) + 1
	for {
		candidate := fmt.Sprintf("Citeck #%d", num)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
		num++
	}
}

func (d *Daemon) collectUsedNamespaceNames(wsID string) map[string]struct{} {
	used := map[string]struct{}{}
	if !config.IsDesktopMode() {
		return used
	}
	rows, err := d.store.ListNamespaces(wsID)
	if err != nil {
		return used
	}
	for _, row := range rows {
		if row.Name != "" {
			used[row.Name] = struct{}{}
		}
	}
	return used
}

// applyDefaultTemplate overlays the workspace's "default" namespace template
// onto cfg (matching the create endpoint's template-application logic) and
// applies the "first repo + LATEST" bundle fallback. Centralized so the
// /create-defaults endpoint stays in lockstep with handleCreateNamespace.
func applyDefaultTemplate(cfg *namespace.Config, wsCfg *bundle.WorkspaceConfig) {
	if wsCfg == nil {
		return
	}
	for _, tmpl := range wsCfg.NamespaceTemplates {
		if tmpl.ID != "default" {
			continue
		}
		if len(tmpl.Config) > 0 {
			if tmplData, err := yaml.Marshal(tmpl.Config); err == nil {
				_ = yaml.Unmarshal(tmplData, cfg)
			}
		}
		break
	}
	if cfg.BundleRef.IsEmpty() && len(wsCfg.BundleRepos) > 0 {
		cfg.BundleRef = bundle.Ref{Repo: wsCfg.BundleRepos[0].ID, Key: "LATEST"}
	}
}

// handlePutNamespaceEdit applies the typed edit form back onto the
// namespace.yml of the namespace addressed by the {id} path segment. Loads
// the on-disk config (so non-form fields like webapps, snapshot, email, S3
// etc. survive), patches the form fields (empty AuthType / nil Users mean
// "leave unchanged" — a partial payload never wipes stored values),
// validates, atomically writes, then triggers a doReload() ONLY when {id} is
// the active namespace; edits to non-active namespaces just persist. The
// historical handler always patched the ACTIVE namespace's config — editing
// a non-active row from Welcome corrupted the active one, and with no active
// namespace the edit 400'd.
func (d *Daemon) handlePutNamespaceEdit(w http.ResponseWriter, r *http.Request) {
	nsID := r.PathValue("id")
	if !validateID(nsID) {
		writeError(w, http.StatusBadRequest, "invalid namespace id")
		return
	}
	var req api.NamespaceEditDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	wsID := d.activeWorkspaceID()
	current, err := d.loadNamespaceConfigFromStore(wsID, nsID)
	if err != nil {
		if errors.Is(err, errNamespaceNotFound) {
			writeErrorCode(w, http.StatusNotFound, api.ErrCodeNamespaceNotFound,
				fmt.Sprintf("namespace %q not found", nsID))
			return
		}
		writeInternalError(w, fmt.Errorf("load namespace config: %w", err))
		return
	}

	// Apply form patch. Name is preserved if blank (kotlin-style merge — the
	// form treats empty strings as "no change" for purely cosmetic fields).
	if req.Name != "" {
		current.Name = req.Name
	}
	if req.BundleRepo != "" && req.BundleKey != "" {
		current.BundleRef = bundle.Ref{Repo: req.BundleRepo, Key: req.BundleKey}
	}
	if req.AuthType != "" {
		current.Authentication.Type = namespace.AuthenticationType(req.AuthType)
	}
	if req.Users != nil {
		current.Authentication.Users = req.Users
	}
	if req.Host != "" {
		current.Proxy.Host = req.Host
	}
	if req.Port > 0 {
		current.Proxy.Port = req.Port
	}
	if req.TLSEnabled != nil {
		current.Proxy.TLS.Enabled = *req.TLSEnabled
		if *req.TLSEnabled {
			applySelfSignedTLSDefaults(current)
		}
	}
	if req.PgAdminEnabled != nil {
		current.PgAdmin.Enabled = *req.PgAdminEnabled
	}

	if valErr := namespace.ValidateNamespaceConfig(current); valErr != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidConfig, valErr.Error())
		return
	}

	data, err := namespace.MarshalNamespaceConfig(current)
	if err != nil {
		writeInternalError(w, fmt.Errorf("marshal namespace config: %w", err))
		return
	}
	if err := d.persistNamespaceConfig(wsID, nsID, data); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidConfig, err.Error())
		return
	}

	// Reload only when the edited namespace is the active one, so the change
	// is picked up live. Non-active namespaces have nothing running — the
	// persisted YAML is authoritative and applies on next activation/start.
	// Reload failure is reported but the YAML is already on disk — the user
	// can retry via UI.
	if nsID == d.activeNsID() {
		if err := d.doReload(); err != nil {
			writeInternalError(w, fmt.Errorf("reload after edit: %w", err))
			return
		}
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "namespace updated"})
}

func (d *Daemon) handleListBundles(w http.ResponseWriter, _ *http.Request) {
	wsCfg := d.active().workspaceConfig

	var result []api.BundleInfoDto
	if wsCfg != nil {
		for _, repo := range wsCfg.BundleRepos {
			bundlesDir := d.resolveBundleDir(repo)
			versions := bundle.ListBundleVersions(bundlesDir)
			result = append(result, api.BundleInfoDto{Repo: repo.ID, Versions: versions})
		}
	}
	if result == nil {
		result = []api.BundleInfoDto{}
	}
	writeJSON(w, result)
}

// handleBundleRepoPull force-pulls a single configured bundle repo (by ID) so
// its versions appear without resolving a namespace against it. Backs the
// namespace edit dialog's per-repo refresh button (e.g. selecting "release" or
// "alf-develop" then refreshing, which were never cloned by the active ref).
func (d *Daemon) handleBundleRepoPull(w http.ResponseWriter, r *http.Request) {
	repoID := r.PathValue("repoId")
	act := d.active()
	if act.workspaceConfig == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no workspace config")
		return
	}
	// force=true (explicit ↻) bypasses the PullPeriod throttle; otherwise the
	// pull (triggered on repo selection) honors the throttle — re-pull only
	// after the period elapses, and clone unconditionally when nothing is on
	// disk yet. No background pulling either way.
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(act.workspaceID), makeTokenLookup(d.secretReaderFunc())).
		WithWorkspaceRepo(d.resolveActiveWorkspaceRepoOpts()).
		WithWorkspaceOverlay(workspaceConfigOverlay(d.store, act.workspaceID))
	if r.URL.Query().Get("force") == "true" {
		resolver = resolver.WithForcePull()
	}
	if _, err := resolver.SyncBundleRepo(act.workspaceConfig, repoID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "bundle repo synced"})
}

// resolveBundleDir returns the on-disk directory for a bundle repo.
// Delegates to the shared ResolveBundleRepoDir which handles offline import,
// workspace repo, and cloned repo priorities. In desktop mode bundles live
// under ~/.citeck/launcher/ws/{wsID}/, mirroring the path the namespace
// loader (namespace_loader.go) uses to feed `bundle.NewResolverWithAuth` —
// without this branch `versions[]` came back empty in desktop mode and the
// bundle dropdown in the namespace-edit dialog only showed the currently
// selected key as a stale fallback.
func (d *Daemon) resolveBundleDir(repo bundle.BundlesRepo) string {
	dataDir := config.DataDir()
	if wsID := d.activeWorkspaceID(); config.IsDesktopMode() && wsID != "" {
		dataDir = config.WorkspaceDir(wsID)
	}
	wsRepoDir := filepath.Join(dataDir, "bundles", "workspace")
	return bundle.ResolveBundleRepoDir(dataDir, wsRepoDir, repo)
}
