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
	list, err := d.store.ListWorkspaces()
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
	ws, err := d.store.GetWorkspace(id)
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
		id = sanitizeName(req.Name)
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
//   - Clear nsConfig / bundleDef / appDefs / workspaceConfig / runtime so the
//     UI sees a clean slate. The caller (UI) is expected to fetch
//     /api/v1/namespaces and let the user pick one, which loads via the
//     existing reload path.
//
// The current implementation does not auto-load a namespace from the new
// workspace — that keeps this function side-effect-light and reuses the
// existing namespace selection UX.
func (d *Daemon) SwitchWorkspace(wsID string) error {
	if wsID == d.workspaceID {
		return nil // no-op
	}
	ws, err := d.store.GetWorkspace(wsID)
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

	// Tear down the previous runtime + docker client. Shutdown is a no-op when
	// the runtime is already stopped; we still call it to drain dispatcher
	// workers and prevent goroutine leaks.
	if rt != nil {
		rt.Shutdown()
	}
	if d.dockerClient != nil {
		_ = d.dockerClient.Close()
	}

	// Recreate the docker client scoped to the new workspace. In server mode
	// this code path is unreachable (requireDesktop guards), so we always
	// include the workspace label.
	newClient, err := docker.NewClient(wsID, "")
	if err != nil {
		return fmt.Errorf("create docker client for workspace %q: %w", wsID, err)
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
	if err := d.store.SetState(*state); err != nil {
		slog.Warn("Persist workspace switch failed", //nolint:gosec // G706: wsID validated by caller (handleActivateWorkspace) before SwitchWorkspace
			"wsID", wsID, "err", err)
	}

	d.configMu.Lock()
	d.workspaceID = wsID
	d.dockerClient = newClient
	d.runtime = nil
	d.nsConfig = nil
	d.bundleDef = nil
	d.appDefs = nil
	d.workspaceConfig = nil
	d.systemSecrets = namespace.SystemSecrets{}
	d.volumesBase = ""
	d.configMu.Unlock()

	slog.Info("Workspace switched", "wsID", wsID) //nolint:gosec // G706: wsID validated by caller
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
