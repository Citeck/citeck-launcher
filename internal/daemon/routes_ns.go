package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
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
		rows, err := d.store.ListNamespaces(d.workspaceID)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		d.configMu.RLock()
		activeID := ""
		if d.nsConfig != nil {
			activeID = d.nsConfig.ID
		}
		rt := d.runtime
		d.configMu.RUnlock()
		for _, row := range rows {
			summary := api.NamespaceSummaryDto{
				ID:          row.ID,
				WorkspaceID: d.workspaceID,
				Name:        row.Name,
				Status:      row.Status,
			}
			if summary.Status == "" {
				summary.Status = string(namespace.NsStatusStopped)
			}
			if cfg, cerr := d.loadNamespaceConfigFromStore(d.workspaceID, row.ID); cerr == nil {
				summary.BundleRef = cfg.BundleRef.String()
			}
			if activeID == row.ID && rt != nil {
				summary.Status = string(rt.Status())
			}
			result = append(result, summary)
		}
	} else {
		// Server mode: single namespace
		d.configMu.RLock()
		nsCfg := d.nsConfig
		rt := d.runtime
		d.configMu.RUnlock()
		if nsCfg != nil {
			status := string(namespace.NsStatusStopped)
			if rt != nil {
				status = string(rt.Status())
			}
			result = append(result, api.NamespaceSummaryDto{
				ID:          nsCfg.ID,
				WorkspaceID: d.workspaceID,
				Name:        nsCfg.Name,
				Status:      status,
				BundleRef:   nsCfg.BundleRef.String(),
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

	// Don't allow deleting the active namespace
	d.configMu.RLock()
	activeID := ""
	if d.nsConfig != nil {
		activeID = d.nsConfig.ID
	}
	rt := d.runtime
	d.configMu.RUnlock()
	if activeID == nsID && rt != nil && rt.Status() != namespace.NsStatusStopped {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "cannot delete active namespace; stop it first")
		return
	}

	if config.IsDesktopMode() {
		// Source of truth first: drop the config + state row.
		if err := d.store.DeleteNamespace(d.workspaceID, nsID); err != nil {
			writeInternalError(w, err)
			return
		}
		// Best-effort: remove the on-disk rtfiles dir so generated bind-mount
		// files don't leak. Enumeration is row-based now, so a failure here
		// cannot resurrect a ghost entry.
		nsDir := config.NamespaceDir(d.workspaceID, nsID)
		if err := os.RemoveAll(nsDir); err != nil { //nolint:gosec // path from config.NamespaceDir
			slog.Warn("Failed to remove namespace rtfiles dir", "dir", nsDir, "err", err)
		}
	} else {
		writeError(w, http.StatusBadRequest, "cannot delete namespace in server mode")
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "namespace deleted"})
}

func (d *Daemon) handleGetTemplates(w http.ResponseWriter, _ *http.Request) {
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()

	var templates []api.TemplateDto
	if wsCfg != nil {
		for _, t := range wsCfg.NamespaceTemplates {
			name := t.Name
			if name == "" {
				name = t.ID
			}
			templates = append(templates, api.TemplateDto{ID: t.ID, Name: name})
		}
	}
	if templates == nil {
		templates = []api.TemplateDto{}
	}
	writeJSON(w, templates)
}

func (d *Daemon) handleGetQuickStarts(w http.ResponseWriter, _ *http.Request) {
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()

	var quickStarts []api.QuickStartDto
	if wsCfg != nil {
		for _, qs := range wsCfg.QuickStartVariants {
			quickStarts = append(quickStarts, api.QuickStartDto{
				Name:      qs.Name,
				Template:  qs.Template,
				Snapshot:  qs.Snapshot,
				BundleRef: resolveQuickStartBundleRef(wsCfg, qs),
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

// --- Forms ---

func (d *Daemon) handleGetForm(w http.ResponseWriter, r *http.Request) {
	formID := r.PathValue("formId")
	spec := form.GetSpec(formID)
	if spec == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("form %q not found", formID))
		return
	}
	writeJSON(w, spec)
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

	d.configMu.RLock()
	rt := d.runtime
	wsID := d.workspaceID
	curNs := ""
	if d.nsConfig != nil {
		curNs = d.nsConfig.ID
	}
	d.configMu.RUnlock()

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
		DockerClient:  d.dockerClient,
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

	d.configMu.RLock()
	rt := d.runtime
	wsID := d.workspaceID
	d.configMu.RUnlock()

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
	// d.runtime == nil and treat the daemon as "no namespace loaded" — the
	// UI's Welcome screen path already handles that state.
	d.configMu.Lock()
	oldRuntime := d.runtime
	oldCloudCfgSrv := d.cloudCfgServer
	oldACME := d.acmeRenewal
	d.runtime = nil
	d.nsConfig = nil
	d.bundleDef = nil
	d.appDefs = nil
	d.cloudCfgServer = nil
	d.systemSecrets = namespace.SystemSecrets{}
	d.volumesBase = ""
	d.acmeRenewal = nil
	d.bundleError = ""
	d.configMu.Unlock()

	if oldRuntime != nil {
		oldRuntime.Shutdown()
	}
	if oldCloudCfgSrv != nil {
		oldCloudCfgSrv.Stop()
	}
	if oldACME != nil {
		oldACME.Stop()
	}

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: "namespace deactivated",
	})
}

// --- Namespace creation + Bundles ---

//nolint:gocyclo,nestif // namespace creation orchestrates validation, template resolution, config generation, and async snapshot import
func (d *Daemon) handleCreateNamespace(w http.ResponseWriter, r *http.Request) {
	var req api.NamespaceCreateDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Server-side validation. Host/port aren't exposed by the create dialog —
	// the user edits them via raw YAML — so only feed them to the validator
	// when the request actually carries a non-empty/non-zero value. Otherwise
	// the form spec's required-range check rejects the implicit port=0.
	spec := form.GetSpec(form.NamespaceCreateFormID)
	if spec != nil {
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
		if fieldErrs := form.Validate(spec, data); len(fieldErrs) > 0 {
			fields := make([]api.FieldErrorDto, len(fieldErrs))
			for i, fe := range fieldErrs {
				fields[i] = api.FieldErrorDto{Key: fe.Key, Message: fe.Message}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(api.ValidationErrorDto{
				Error:  "validation failed",
				Fields: fields,
			})
			return
		}
	}

	// Generate namespace config — start from template if specified
	nsCfg := namespace.DefaultNamespaceConfig()

	templateID := req.Template
	if templateID == "" {
		templateID = "default" // use default template if none specified
	}
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()
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
	// extremely unlikely collision with an existing on-disk slug. wsID
	// resolves the same way the create-config-path block below resolves it.
	createWsID := req.WorkspaceID
	if createWsID == "" {
		createWsID = d.workspaceID
	}
	for range 10 {
		candidate := generateEntityID()
		if candidate == "" {
			continue
		}
		if _, taken, _ := d.store.LoadNamespaceConfig(createWsID, candidate); taken {
			continue // taken
		}
		nsCfg.ID = candidate
		break
	}
	if nsCfg.ID == "" {
		writeInternalError(w, fmt.Errorf("failed to generate namespace id"))
		return
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
	}
	nsCfg.PgAdmin.Enabled = req.PgAdminEnabled
	if req.BundleRepo != "" && req.BundleKey != "" {
		nsCfg.BundleRef = bundle.Ref{Repo: req.BundleRepo, Key: req.BundleKey}
	}

	// Serialize to YAML
	data, err := namespace.MarshalNamespaceConfig(&nsCfg)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Resolve the target workspace.
	wsID := req.WorkspaceID
	if wsID == "" {
		wsID = d.workspaceID
	}
	if !validateID(wsID) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	// Collision check via the store (replaces the O_EXCL file create).
	if _, exists, lerr := d.store.LoadNamespaceConfig(wsID, nsCfg.ID); lerr != nil {
		writeInternalError(w, lerr)
		return
	} else if exists {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceExists,
			fmt.Sprintf("namespace %q already exists", nsCfg.ID))
		return
	}

	// Validate + persist via the single choke-point.
	if err := d.persistNamespaceConfig(wsID, nsCfg.ID, data); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidConfig, err.Error())
		return
	}

	// Always encrypt secrets with the default password on namespace creation
	if !d.secretService.IsEncrypted() {
		if encErr := d.secretService.SetMasterPassword(storage.DefaultMasterPassword, true); encErr != nil {
			slog.Error("Failed to set up secrets encryption during namespace creation", "err", encErr)
		} else {
			slog.Info("Secrets encrypted with default password during namespace creation")
		}
	}

	// Trigger background snapshot download + import if specified
	if req.Snapshot != "" {
		wsID := req.WorkspaceID
		if wsID == "" {
			wsID = d.workspaceID
		}
		d.bgWg.Go(func() {
			d.downloadAndImportSnapshot(req.Snapshot, wsID, nsCfg.ID)
		})
	}

	// Auto-activate the newly-created namespace in desktop mode when the
	// daemon has no current namespace loaded (Welcome / quick-start flow).
	// Without this, the UI's immediate postNamespaceStart() after createNamespace
	// fails with "no namespace configured" because the on-disk config was
	// written but never wired into d.runtime. We narrowly guard on no-current-ns
	// so a user creating a second namespace from an already-loaded workspace
	// keeps the current one active (they switch explicitly via the picker).
	if config.IsDesktopMode() {
		targetWsID := req.WorkspaceID
		if targetWsID == "" {
			targetWsID = d.workspaceID
		}
		d.configMu.RLock()
		hasCurrent := d.runtime != nil && d.nsConfig != nil
		activeWsID := d.workspaceID
		d.configMu.RUnlock()
		if !hasCurrent && targetWsID == activeWsID {
			if d.reloadMu.TryLock() {
				defer d.reloadMu.Unlock()
				loaded, loadErr := loadNamespace(loadNamespaceInput{
					Store:         d.store,
					SecretService: d.secretService,
					DockerClient:  d.dockerClient,
					DaemonCfg:     d.daemonCfg,
					Licenses:      d.licenses,
					WorkspaceID:   activeWsID,
					NamespaceID:   nsCfg.ID,
					Desktop:       d.desktop,
				})
				if loadErr != nil {
					slog.Warn("Auto-activate after create failed (load)", "nsID", nsCfg.ID, "err", loadErr)
				} else if loaded.NsConfig != nil {
					if err := d.installLoadedNamespace(loaded, activeWsID, nsCfg.ID); err != nil {
						slog.Warn("Auto-activate after create failed (install)", "nsID", nsCfg.ID, "err", err)
					} else {
						slog.Info("Auto-activated newly-created namespace", "nsID", nsCfg.ID)
					}
				}
			}
		}
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("namespace %q created", nsCfg.Name)})
}

// handleGetNamespaceEdit returns the typed subset of namespace.yml consumed
// by the Web UI's "edit namespace" form. The form drives a focused field set
// (name, bundle, auth, host, port, TLS, pgAdmin); fields outside the DTO are
// preserved on PUT so power users editing the raw YAML are not surprised by
// silent rewrites.
func (d *Daemon) handleGetNamespaceEdit(w http.ResponseWriter, _ *http.Request) {
	d.configMu.RLock()
	nsCfg := d.nsConfig
	d.configMu.RUnlock()
	if nsCfg == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	users := nsCfg.Authentication.Users
	if users == nil {
		users = []string{}
	}
	dto := api.NamespaceEditDto{
		Name:           nsCfg.Name,
		BundleRepo:     nsCfg.BundleRef.Repo,
		BundleKey:      nsCfg.BundleRef.Key,
		AuthType:       string(nsCfg.Authentication.Type),
		Users:          users,
		Host:           nsCfg.Proxy.Host,
		Port:           nsCfg.Proxy.Port,
		TLSEnabled:     nsCfg.Proxy.TLS.Enabled,
		PgAdminEnabled: nsCfg.PgAdmin.Enabled,
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
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	wsID := d.workspaceID
	d.configMu.RUnlock()

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

// handlePutNamespaceEdit applies the typed edit form back onto namespace.yml.
// Loads the on-disk config (so non-form fields like webapps, snapshot, email,
// S3 etc. survive), patches the form fields, validates, atomically writes,
// then triggers a doReload() so the change takes effect immediately.
func (d *Daemon) handlePutNamespaceEdit(w http.ResponseWriter, r *http.Request) {
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

	d.configMu.RLock()
	nsCfg := d.nsConfig
	d.configMu.RUnlock()
	if nsCfg == nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeNotConfigured, "no namespace configured")
		return
	}
	nsID := nsCfg.ID

	current, err := d.loadNamespaceConfigFromStore(d.workspaceID, nsID)
	if err != nil {
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
	current.Proxy.TLS.Enabled = req.TLSEnabled
	current.PgAdmin.Enabled = req.PgAdminEnabled

	if valErr := namespace.ValidateNamespaceConfig(current); valErr != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidConfig, valErr.Error())
		return
	}

	data, err := namespace.MarshalNamespaceConfig(current)
	if err != nil {
		writeInternalError(w, fmt.Errorf("marshal namespace config: %w", err))
		return
	}
	if err := d.persistNamespaceConfig(d.workspaceID, nsID, data); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidConfig, err.Error())
		return
	}

	// Reload so the change is picked up live. Failure to reload is reported
	// but the YAML is already on disk — the user can retry via UI.
	if err := d.doReload(); err != nil {
		writeInternalError(w, fmt.Errorf("reload after edit: %w", err))
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "namespace updated"})
}

func (d *Daemon) handleListBundles(w http.ResponseWriter, _ *http.Request) {
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()

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
	if config.IsDesktopMode() && d.workspaceID != "" {
		dataDir = config.WorkspaceDir(d.workspaceID)
	}
	wsRepoDir := filepath.Join(dataDir, "bundles", "workspace")
	return bundle.ResolveBundleRepoDir(dataDir, wsRepoDir, repo)
}
