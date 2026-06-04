package daemon

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/docker"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// Sentinel errors returned by SwitchWorkspace and mapped to HTTP codes by the
// activate handler.
var (
	errWorkspaceNotFound = errors.New("workspace not found")
	errWorkspaceBusy     = errors.New("namespace is running; stop it before switching workspaces")
)

// Multi-workspace endpoints (desktop only).
//
// All handlers reject server-mode requests with 404 + DESKTOP_ONLY so the
// route surface stays consistent across binaries — clients always see "not
// found" rather than getting a half-implemented response in server mode.

const (
	defaultWorkspaceRepoBranch = "main"
	defaultWorkspaceAuthType   = "NONE"
)

// validAuthTypes is the closed set of Kotlin-parity workspace auth types.
// Keeping this as a map makes membership checks tiny and avoids importing the
// secrets package here just for the constants.
var validAuthTypes = map[string]bool{
	"NONE":  true,
	"TOKEN": true,
}

// validateAuthType returns "" when the supplied value is acceptable (empty →
// default NONE applied later) or an error message for the bad-request branch.
func validateAuthType(t string) string {
	if t == "" {
		return ""
	}
	if !validAuthTypes[t] {
		return "invalid authType: must be NONE or TOKEN"
	}
	return ""
}

// validateRepoPullPeriod ensures the supplied ISO 8601 duration parses cleanly
// before it is persisted; empty is allowed (storage layer applies the default).
func validateRepoPullPeriod(p string) error {
	if p == "" {
		return nil
	}
	if _, err := storage.ParseISO8601Duration(p); err != nil {
		return fmt.Errorf("invalid repoPullPeriod: %w", err)
	}
	return nil
}

// defaultWorkspaceID is the implicit workspace ID Kotlin v1.x reserved for the
// no-config-needed bundles repo. Go inherits the convention: namespaces created
// without a workspace land under `default`, but the workspace itself is never
// written to the database unless the user explicitly customizes it.
const defaultWorkspaceID = "default"

// syntheticDefaultWorkspace returns the in-memory representation of the
// implicit "default" workspace. Used everywhere the DB lookup would miss it
// (fresh installs, migrations from Kotlin) so the UI's WorkspaceSelector
// always sees both `default` and any user-created workspaces.
func syntheticDefaultWorkspace() storage.WorkspaceDto {
	return storage.WorkspaceDto{
		ID:   defaultWorkspaceID,
		Name: defaultWorkspaceID,
	}
}

// listWorkspacesWithDefault returns the stored workspaces plus the implicit
// "default" entry when no explicit row exists for it. The default is prepended
// so it shows first in the picker (matches Kotlin v1.x ordering — default
// workspace is the entry point on a fresh install).
func (d *Daemon) listWorkspacesWithDefault() ([]storage.WorkspaceDto, error) {
	list, err := d.store.ListWorkspaces()
	if err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	for i := range list {
		if list[i].ID == defaultWorkspaceID {
			return list, nil
		}
	}
	return append([]storage.WorkspaceDto{syntheticDefaultWorkspace()}, list...), nil
}

// getWorkspaceWithDefault wraps store.GetWorkspace so callers receive a
// synthetic record for the implicit "default" workspace when no row exists.
// Returns (nil, nil) for unknown non-default IDs — same contract as the
// underlying store.
func (d *Daemon) getWorkspaceWithDefault(id string) (*storage.WorkspaceDto, error) {
	ws, err := d.store.GetWorkspace(id)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}
	if ws != nil {
		return ws, nil
	}
	if id == defaultWorkspaceID {
		synth := syntheticDefaultWorkspace()
		return &synth, nil
	}
	return nil, nil
}

// requireDesktop returns true and writes a 404 response when the daemon is not
// running in desktop mode. All workspace CRUD/activate handlers guard with it.
func (d *Daemon) requireDesktop(w http.ResponseWriter) bool {
	if !config.IsDesktopMode() {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeDesktopOnly,
			"multi-workspace endpoints are available in desktop mode only")
		return false
	}
	return true
}

func (d *Daemon) handleListWorkspaces(w http.ResponseWriter, _ *http.Request) {
	if !d.requireDesktop(w) {
		return
	}
	list, err := d.listWorkspacesWithDefault()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	// Augment with namespace counts from the filesystem so the picker can show
	// "5 namespaces" without forcing a second round-trip per workspace.
	wsList, _ := config.ListWorkspaces()
	nsCount := make(map[string]int, len(wsList))
	for _, ws := range wsList {
		nsCount[ws.ID] = len(ws.Namespaces)
	}
	out := make([]api.WorkspaceDto, 0, len(list))
	for _, ws := range list {
		out = append(out, api.WorkspaceDto{
			ID:             ws.ID,
			Name:           ws.Name,
			RepoURL:        ws.RepoURL,
			RepoBranch:     ws.RepoBranch,
			RepoPullPeriod: ws.RepoPullPeriod,
			AuthType:       ws.AuthType,
			Active:         ws.ID == d.workspaceID,
			Namespaces:     nsCount[ws.ID],
		})
	}
	writeJSON(w, out)
}

func (d *Daemon) handleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	if !d.requireDesktop(w) {
		return
	}
	id := r.PathValue("id")
	if !validateID(id) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	ws, err := d.getWorkspaceWithDefault(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if ws == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeWorkspaceNotFound, "workspace not found")
		return
	}
	wsList, _ := config.ListWorkspaces()
	nsCount := 0
	for _, fws := range wsList {
		if fws.ID == id {
			nsCount = len(fws.Namespaces)
			break
		}
	}
	writeJSON(w, api.WorkspaceDto{
		ID:             ws.ID,
		Name:           ws.Name,
		RepoURL:        ws.RepoURL,
		RepoBranch:     ws.RepoBranch,
		RepoPullPeriod: ws.RepoPullPeriod,
		AuthType:       ws.AuthType,
		Active:         ws.ID == d.workspaceID,
		Namespaces:     nsCount,
	})
}

func (d *Daemon) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	if !d.requireDesktop(w) {
		return
	}
	var req api.WorkspaceCreateDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "repoUrl is required")
		return
	}
	id := req.ID
	if id == "" {
		// Opaque random slug (Kotlin parity: IdUtils.createStrId). The
		// user-facing Name is reference info only — collisions are
		// improbable but we still retry against existing IDs to be safe.
		for range 10 {
			candidate := generateEntityID()
			if candidate == "" {
				continue
			}
			ws, err := d.store.GetWorkspace(candidate)
			if err == nil && ws == nil && candidate != defaultWorkspaceID {
				id = candidate
				break
			}
		}
		if id == "" {
			writeInternalError(w, fmt.Errorf("failed to generate workspace id"))
			return
		}
	}
	if !validateID(id) {
		writeError(w, http.StatusBadRequest, "invalid workspace id (alphanumeric, dot, dash, underscore)")
		return
	}
	branch := req.RepoBranch
	if branch == "" {
		branch = defaultWorkspaceRepoBranch
	}
	if msg := validateAuthType(req.AuthType); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := validateRepoPullPeriod(req.RepoPullPeriod); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	authType := req.AuthType
	if authType == "" {
		authType = defaultWorkspaceAuthType
	}
	pullPeriod := req.RepoPullPeriod
	if pullPeriod == "" {
		pullPeriod = storage.DefaultRepoPullPeriod
	}

	if id == defaultWorkspaceID {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeWorkspaceExists,
			fmt.Sprintf("workspace %q is the reserved built-in default; pick a different id", id))
		return
	}
	existing, err := d.store.GetWorkspace(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if existing != nil {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeWorkspaceExists,
			fmt.Sprintf("workspace %q already exists", id))
		return
	}

	ws := storage.WorkspaceDto{
		ID:             id,
		Name:           req.Name,
		RepoURL:        req.RepoURL,
		RepoBranch:     branch,
		RepoPullPeriod: pullPeriod,
		AuthType:       authType,
	}
	if err := d.store.SaveWorkspace(ws); err != nil {
		writeInternalError(w, err)
		return
	}
	// Pre-create the workspace dir so subsequent namespace operations don't have
	// to race a mkdir; the repo subdir is created lazily by the git puller.
	if err := os.MkdirAll(config.WorkspaceDir(id), 0o755); err != nil { //nolint:gosec // workspace dir needs 0o755
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.WorkspaceDto{
		ID: ws.ID, Name: ws.Name, RepoURL: ws.RepoURL, RepoBranch: ws.RepoBranch,
		RepoPullPeriod: ws.RepoPullPeriod, AuthType: ws.AuthType,
		Active: ws.ID == d.workspaceID,
	})
}

func (d *Daemon) handleUpdateWorkspace(w http.ResponseWriter, r *http.Request) {
	if !d.requireDesktop(w) {
		return
	}
	id := r.PathValue("id")
	if !validateID(id) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	if id == defaultWorkspaceID {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeWorkspaceExists,
			"workspace \"default\" is built-in and cannot be edited; its config is defined in code")
		return
	}
	var req api.WorkspaceUpdateDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	existing, err := d.store.GetWorkspace(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if existing == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeWorkspaceNotFound, "workspace not found")
		return
	}
	if msg := validateAuthType(req.AuthType); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := validateRepoPullPeriod(req.RepoPullPeriod); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name != "" {
		existing.Name = req.Name
	}
	if req.RepoURL != "" {
		existing.RepoURL = req.RepoURL
	}
	if req.RepoBranch != "" {
		existing.RepoBranch = req.RepoBranch
	}
	if req.RepoPullPeriod != "" {
		existing.RepoPullPeriod = req.RepoPullPeriod
	}
	if req.AuthType != "" {
		existing.AuthType = req.AuthType
	}
	if err := d.store.SaveWorkspace(*existing); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.WorkspaceDto{
		ID: existing.ID, Name: existing.Name, RepoURL: existing.RepoURL,
		RepoBranch: existing.RepoBranch, RepoPullPeriod: existing.RepoPullPeriod,
		AuthType: existing.AuthType, Active: existing.ID == d.workspaceID,
	})
}

func (d *Daemon) handleDeleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if !d.requireDesktop(w) {
		return
	}
	id := r.PathValue("id")
	if !validateID(id) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	if id == defaultWorkspaceID {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeWorkspaceInUse,
			"workspace \"default\" is built-in and cannot be deleted")
		return
	}
	existing, err := d.store.GetWorkspace(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if existing == nil {
		writeErrorCode(w, http.StatusNotFound, api.ErrCodeWorkspaceNotFound, "workspace not found")
		return
	}
	// Refuse to delete the active workspace. The user must switch first so the
	// daemon state stays consistent (docker client, runtime, configs).
	if d.workspaceID == id {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeWorkspaceInUse,
			"cannot delete the active workspace; switch first")
		return
	}
	if err := d.store.DeleteWorkspace(id); err != nil {
		writeInternalError(w, err)
		return
	}
	// Best-effort filesystem cleanup. Errors are logged but not surfaced — the
	// DB record is gone, so the workspace effectively doesn't exist anymore.
	wsDir := config.WorkspaceDir(id)
	if err := os.RemoveAll(wsDir); err != nil { //nolint:gosec // G703: id passed validateID, wsDir is server-side
		slog.Warn("Workspace delete: filesystem cleanup failed", //nolint:gosec // G706: id passed validateID
			"wsID", id, "dir", wsDir, "err", err)
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("workspace %q deleted", id)})
}

// SwitchWorkspace makes wsID the active workspace. The caller is responsible
// for stopping any running namespace beforehand; SwitchWorkspace itself
// refuses when r.runtime is in a non-stopped state to avoid orphaning
// containers (the docker client is workspace-scoped, so swapping it would
// hide the running containers from subsequent state queries).
//
// Side effects on success:
//   - Persist new wsID in launcher_state. The previous workspace's namespace
//     selection is preserved in SelectedNs[oldWsID] (Kotlin parity:
//     workspace-state/{wsId} → SELECTED_NS_PROP) so re-activating the old
//     workspace restores its namespace instead of dropping to Welcome.
//   - Update d.workspaceID and d.dockerClient.
//   - Auto-load the new workspace's last-selected namespace via loadNamespace
//     (Kotlin parity — switching workspace lands the user on their previously
//     active namespace, not Welcome). When no namespace was ever selected,
//     or the load fails, daemon falls back to a clean "no namespace" state
//     and the UI shows Welcome with the namespace picker.
func (d *Daemon) SwitchWorkspace(wsID string) error {
	if wsID == d.workspaceID {
		return nil // no-op
	}
	// getWorkspaceWithDefault so switching to the built-in "default" works
	// even when no row has ever been written to the DB.
	ws, err := d.getWorkspaceWithDefault(wsID)
	if err != nil {
		return fmt.Errorf("lookup workspace: %w", err)
	}
	if ws == nil {
		return errWorkspaceNotFound
	}

	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	d.configMu.Lock()
	rt := d.runtime
	oldWsID := d.workspaceID
	var oldNsID string
	if d.nsConfig != nil {
		oldNsID = d.nsConfig.ID
	}
	d.configMu.Unlock()
	if rt != nil && rt.Status() != namespace.NsStatusStopped {
		return errWorkspaceBusy
	}

	// Persist the switch. Errors here are logged but not fatal — the in-memory
	// state still reflects the new workspace. Record oldWsID's current ns
	// selection so re-activating the old workspace restores it.
	state, _ := d.store.GetState()
	if state == nil {
		state = &storage.LauncherState{}
	}
	if state.SelectedNs == nil {
		state.SelectedNs = make(map[string]string, 2)
	}
	if oldWsID != "" && oldNsID != "" {
		state.SelectedNs[oldWsID] = oldNsID
	}
	state.WorkspaceID = wsID
	if setErr := d.store.SetState(*state); setErr != nil {
		slog.Warn("Persist workspace switch failed", //nolint:gosec // G706: wsID validated by caller (handleActivateWorkspace) before SwitchWorkspace
			"wsID", wsID, "err", setErr)
	}

	// Determine the new workspace's target namespace BEFORE creating the
	// docker client so the client is scoped to it via labels (otherwise
	// GetContainers/GetVolumes return nothing and existing containers from
	// a prior daemon session look like they "disappeared"). Persisted
	// SelectedNs[wsID] wins; otherwise fall back to the first on-disk
	// namespace (handles workspaces migrated from Kotlin v1.x that never
	// had an explicit selection recorded).
	newNsID := state.SelectedNs[wsID]
	if newNsID == "" {
		rows, listErr := d.store.ListNamespaces(wsID)
		if listErr != nil {
			slog.Warn("Workspace switch: list namespaces failed", "wsID", wsID, "err", listErr) //nolint:gosec // G706: wsID validated by caller
		} else if len(rows) > 0 {
			newNsID = rows[0].ID
			slog.Info("Workspace switch: no persisted ns selection, falling back to first namespace", "wsID", wsID, "nsID", newNsID) //nolint:gosec // G706: wsID/newNsID from validated store list
		}
	}

	// Tear down the previous runtime + docker client. Shutdown is a no-op when
	// the runtime is already stopped; we still call it to drain dispatcher
	// workers and prevent goroutine leaks.
	if rt != nil {
		rt.Shutdown()
	}
	if d.dockerClient != nil {
		_ = d.dockerClient.Close()
	}

	// Recreate the docker client scoped to the new (workspace, namespace) pair
	// so container/volume queries via labels resolve correctly. nsID can be
	// "" when the workspace has no namespaces yet — the client still
	// constructs OK, just returns no matches until a namespace is loaded.
	newClient, err := docker.NewClient(wsID, newNsID)
	if err != nil {
		return fmt.Errorf("create docker client for workspace %q: %w", wsID, err)
	}

	// Tear down any ACME renewal from the previous workspace's namespace
	// before swapping in the new docker client + state.
	d.configMu.Lock()
	oldACME := d.acmeRenewal
	oldCloudCfg := d.cloudCfgServer
	d.workspaceID = wsID
	d.dockerClient = newClient
	d.runtime = nil
	d.nsConfig = nil
	d.bundleDef = nil
	d.appDefs = nil
	d.workspaceConfig = nil
	d.cloudCfgServer = nil
	d.systemSecrets = namespace.SystemSecrets{}
	d.volumesBase = ""
	d.acmeRenewal = nil
	d.configMu.Unlock()
	if oldACME != nil {
		oldACME.Stop()
	}
	if oldCloudCfg != nil {
		oldCloudCfg.Stop()
	}

	slog.Info("Workspace switched", "wsID", wsID) //nolint:gosec // G706: wsID validated by caller

	// Auto-load the new workspace's last-selected namespace if any. newNsID
	// was already resolved above (so the docker client could be scoped to
	// it via labels). Failure is non-fatal: daemon stays in the "no
	// namespace" state and the UI shows Welcome.
	if newNsID == "" {
		return nil
	}
	if _, ok, _ := d.store.LoadNamespaceConfig(wsID, newNsID); !ok {
		slog.Info("Workspace last-selected namespace missing, skipping auto-load", "wsID", wsID, "nsID", newNsID) //nolint:gosec // G706: wsID/newNsID validated
		return nil
	}
	loaded, loadErr := loadNamespace(loadNamespaceInput{
		Store:         d.store,
		SecretService: d.secretService,
		DockerClient:  d.dockerClient,
		DaemonCfg:     d.daemonCfg,
		Licenses:      d.licenses,
		WorkspaceID:   wsID,
		NamespaceID:   newNsID,
		Desktop:       d.desktop,
	})
	if loadErr != nil {
		slog.Warn("Workspace switch: auto-load namespace failed", "wsID", wsID, "nsID", newNsID, "err", loadErr) //nolint:gosec // G706: wsID/newNsID validated
		return nil                                                                                               //nolint:nilerr // workspace switch succeeded; namespace auto-load is best-effort
	}
	if loaded.NsConfig == nil {
		return nil
	}
	if err := d.installLoadedNamespace(loaded, wsID, newNsID); err != nil {
		slog.Warn("Workspace switch: install loaded namespace failed", "wsID", wsID, "nsID", newNsID, "err", err) //nolint:gosec // G706: wsID/newNsID validated
		return nil                                                                                                //nolint:nilerr // workspace switch succeeded; install failure is best-effort
	}
	slog.Info("Workspace switch: namespace auto-loaded", "wsID", wsID, "nsID", newNsID) //nolint:gosec // G706: wsID/newNsID validated
	return nil
}

func (d *Daemon) handleActivateWorkspace(w http.ResponseWriter, r *http.Request) {
	if !d.requireDesktop(w) {
		return
	}
	id := r.PathValue("id")
	if !validateID(id) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	if err := d.SwitchWorkspace(id); err != nil {
		if errors.Is(err, errWorkspaceNotFound) {
			writeErrorCode(w, http.StatusNotFound, api.ErrCodeWorkspaceNotFound, err.Error())
			return
		}
		if errors.Is(err, errWorkspaceBusy) {
			writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, err.Error())
			return
		}
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("workspace %q activated", id)})
}
