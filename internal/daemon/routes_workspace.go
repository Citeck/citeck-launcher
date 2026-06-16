package daemon

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"runtime"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// Workspace repo URL/branch defaults are owned by the bundle package
// (see bundle.DefaultBundlesRepo / DefaultBundlesBranch). Reference them
// directly from there rather than redeclaring local constants.

// workspaceRepoSecretKey returns the conventional secret key for a workspace's
// git auth token (Kotlin parity: WorkspacesService.getRepoAuthId).
func workspaceRepoSecretKey(wsID string) string {
	return "ws:" + wsID + ":repo"
}

// migrateWorkspaceSecretLinks back-fills WorkspaceDto.SecretID for workspaces
// created before the secret-reference model (incl. Kotlin 1.x H2-migrated
// ones, whose tokens land as GIT_TOKEN secrets keyed "ws:<id>:repo"): an
// AuthType=TOKEN workspace with no explicit link but an existing legacy
// secret gets SecretID persisted so the UI picker shows the association.
// Idempotent and best-effort: a locked SecretService makes GetSecret fail and
// simply defers the back-fill to the next unlock (rebuildAuthCaches), and
// runtime token resolution falls back to the legacy key regardless — this
// migration is UI continuity, not a correctness requirement.
func migrateWorkspaceSecretLinks(store storage.Store, secretSvc secretValueReader) {
	if store == nil || secretSvc == nil {
		return
	}
	wss, err := store.ListWorkspaces()
	if err != nil {
		return
	}
	for _, ws := range wss {
		if ws.AuthType != "TOKEN" || ws.SecretID != "" {
			continue
		}
		legacyID := workspaceRepoSecretKey(ws.ID)
		if sec, secErr := secretSvc.GetSecret(legacyID); secErr != nil || sec == nil {
			continue // locked store or no legacy secret — nothing to link yet
		}
		ws.SecretID = legacyID
		if saveErr := store.SaveWorkspace(ws); saveErr != nil {
			slog.Warn("Failed to back-fill workspace secret link", "ws", ws.ID, "err", saveErr)
			continue
		}
		slog.Info("Linked legacy workspace repo secret", "ws", ws.ID, "secret", legacyID)
	}
}

// resolveActiveWorkspaceRepoOpts is the canonical entry point that maps the
// active workspace's stored settings into the bundle resolver's workspace
// repo options. Empty fields are left zero so the resolver can layer them on
// top of its hardcoded defaults.
func (d *Daemon) resolveActiveWorkspaceRepoOpts() bundle.WorkspaceRepoOpts {
	wsID := d.activeWorkspaceID()
	if d.store == nil || wsID == "" {
		return bundle.WorkspaceRepoOpts{}
	}
	ws, _ := d.store.GetWorkspace(wsID)
	if ws == nil {
		return bundle.WorkspaceRepoOpts{}
	}
	return d.workspaceRepoOptsFromDto(*ws)
}

// workspaceRepoOptsFromDto translates a stored WorkspaceDto into bundle opts,
// resolving the TOKEN secret when present. Exposed for the SwitchWorkspace
// path which needs to honor the *target* workspace, not the active one.
func (d *Daemon) workspaceRepoOptsFromDto(ws storage.WorkspaceDto) bundle.WorkspaceRepoOpts {
	return buildWorkspaceRepoOpts(ws, d.secretService)
}

// secretValueReader is the minimal interface buildWorkspaceRepoOpts needs from
// SecretService — accepts the live service or any test fake. Pre-startup
// callers (server bootstrap before *Daemon exists) can pass the same
// secretSvc handle they already have on hand.
type secretValueReader interface {
	GetSecret(id string) (*storage.Secret, error)
}

// buildWorkspaceRepoOpts is the shared mapping used both by startup (before
// *Daemon is constructed) and by the live daemon for force-update / switch.
// Pass nil for secretSvc when the caller cannot resolve secrets yet — the
// returned struct then has no Token and the resolver falls through to
// unauthenticated clone, matching the pre-2.1 behavior.
//
// Token resolution priority:
//  1. ws.SecretID — an explicit reference to a REUSABLE secret (one token
//     shared by several workspaces). An explicit link is authoritative: it is
//     resolved regardless of AuthType, so a workspace whose dialog linked a
//     secret but left AuthType stale still authenticates.
//  2. Legacy per-workspace secret "ws:{id}:repo" (Kotlin getRepoAuthId
//     convention) — only when AuthType == "TOKEN" (back-compat).
//
// BASIC-style secrets (Username set) are supported gracefully: git token auth
// sends BasicAuth("oauth2", token), so only the secret's Value (the
// password/token half) is used — the stored Username is intentionally ignored.
func buildWorkspaceRepoOpts(ws storage.WorkspaceDto, secretSvc secretValueReader) bundle.WorkspaceRepoOpts {
	opts := bundle.WorkspaceRepoOpts{
		URL:    ws.RepoURL,
		Branch: ws.RepoBranch,
	}
	if dur, err := storage.ParseISO8601Duration(ws.RepoPullPeriod); err == nil {
		opts.PullPeriod = dur
	}
	if secretSvc == nil {
		return opts
	}
	switch {
	case ws.SecretID != "":
		if sec, err := secretSvc.GetSecret(ws.SecretID); err == nil && sec != nil {
			opts.Token = sec.Value
		}
	case ws.AuthType == "TOKEN":
		if sec, err := secretSvc.GetSecret(workspaceRepoSecretKey(ws.ID)); err == nil && sec != nil {
			opts.Token = sec.Value
		}
	}
	return opts
}

// lookupWorkspaceRepoOpts is the startup-time counterpart that accepts a Store
// directly. Returns a zero-valued struct when the workspace record is missing
// — the resolver then uses its hardcoded defaults.
func lookupWorkspaceRepoOpts(store storage.Store, secretSvc secretValueReader, wsID string) bundle.WorkspaceRepoOpts {
	if store == nil || wsID == "" {
		return bundle.WorkspaceRepoOpts{}
	}
	ws, _ := store.GetWorkspace(wsID)
	if ws == nil {
		return bundle.WorkspaceRepoOpts{}
	}
	return buildWorkspaceRepoOpts(*ws, secretSvc)
}

// handleWorkspaceUpdate force-pulls the active workspace repo (bypassing the
// PullPeriod throttle), re-resolves its config, and triggers a runtime reload so
// config changes are picked up immediately. Kotlin parity: "Force Update" RMB
// menu on the Welcome screen (WelcomeScreen.kt).
func (d *Daemon) handleWorkspaceUpdate(w http.ResponseWriter, _ *http.Request) {
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	if err := d.reresolveActiveWorkspace(); err != nil {
		slog.Warn("Force workspace update failed", "err", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Apply pulled config changes to a running namespace (no-op on Welcome).
	if act := d.active(); act.runtime != nil && act.nsConfig != nil {
		if err := d.doReload(); err != nil {
			slog.Warn("Force workspace update: reload failed", "err", err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("reload after pull failed: %v", err))
			return
		}
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "Workspace updated"})
}

// reresolveActiveWorkspace force-pulls (PullPeriod=0, bypassing the throttle)
// and re-resolves the active workspace's repo with the CURRENT git token, then
// updates the cached active-state workspaceConfig / wsSyncError. The caller MUST
// hold d.reloadMu. No-op for default-repo (lazy) or missing workspaces. Does NOT
// reload — callers needing a namespace reload (the Force Update button) do it
// separately.
//
// This is the single point that re-runs the workspace-repo resolution against
// the current secret store, so a repo first resolved before its token existed
// (or while the store was locked) is no longer stuck on a stale auth error.
func (d *Daemon) reresolveActiveWorkspace() error {
	wsID := d.activeWorkspaceID()
	if wsID == "" {
		return nil
	}
	ws, err := d.getWorkspaceWithDefault(wsID)
	if err != nil {
		return fmt.Errorf("lookup workspace: %w", err)
	}
	if ws == nil || ws.RepoURL == "" {
		return nil
	}

	opts := buildWorkspaceRepoOpts(*ws, d.secretService)
	opts.PullPeriod = 0 // force a fresh clone/pull with the current token
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(wsID), makeTokenLookup(d.secretReaderFunc())).
		WithWorkspaceRepo(opts)
	if !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	wsCfg := resolver.ResolveWorkspaceOnly()
	syncErr := resolver.WorkspaceSyncError()

	d.configMu.Lock()
	if a := d.activeLocked(); a.workspaceID == wsID {
		if syncErr != nil {
			a.wsSyncError = workspaceSyncErrorString(resolver)
		} else {
			a.workspaceConfig = wsCfg
			a.wsSyncError = ""
		}
	}
	d.configMu.Unlock()

	if syncErr != nil {
		return fmt.Errorf("workspace repo sync failed: %w", syncErr)
	}
	return nil
}

// activeWorkspaceConfigForRead returns the active workspace config for the
// read-only Welcome surfaces (quick starts, namespace list, snapshots). When a
// sync error is cached it re-resolves once against the CURRENT secret store —
// the git token may have been added since the workspace was activated — so the
// read reflects current reality instead of a stale snapshot from activation
// time. A cached success returns as-is with no git I/O. Returns (config,
// wsSyncErrorString).
func (d *Daemon) activeWorkspaceConfigForRead() (cfg *bundle.WorkspaceConfig, syncErr string) {
	if act := d.active(); act.wsSyncError == "" {
		return act.workspaceConfig, ""
	}
	// A failure is cached — retry under the reload lock. If it's held (a reload
	// or another retry is in flight) return the cached value rather than block.
	if !d.reloadMu.TryLock() {
		act := d.active()
		return act.workspaceConfig, act.wsSyncError
	}
	defer d.reloadMu.Unlock()
	if act := d.active(); act.wsSyncError == "" { // fixed by a concurrent retry
		return act.workspaceConfig, ""
	}
	_ = d.reresolveActiveWorkspace() // updates the cached state; result read below
	act := d.active()
	return act.workspaceConfig, act.wsSyncError
}

// handleSystemOpenDir opens an allowlisted directory in the OS file manager.
// Server mode returns the resolved path without opening (Mode="server") so the
// UI can display it for manual access; desktop mode shells out to xdg-open /
// open / explorer.
func (d *Daemon) handleSystemOpenDir(w http.ResponseWriter, r *http.Request) {
	var req api.OpenDirRequestDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	dir, err := d.resolveOpenDirPath(req.Kind)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Server mode (no desktop): return the path so the UI can display / copy
	// it. We deliberately do NOT shell out to xdg-open on the headless server
	// — that would either no-op or fail noisily depending on DISPLAY state.
	if !config.IsDesktopMode() {
		writeJSON(w, api.OpenDirResponseDto{
			Opened:  false,
			Path:    dir,
			Mode:    "server",
			Message: "Path is on the daemon host; open it on that machine.",
		})
		return
	}

	if err := openLocalDir(dir); err != nil {
		slog.Warn("Open dir failed", "kind", req.Kind, "dir", dir, "err", err)
		writeJSON(w, api.OpenDirResponseDto{
			Opened:  false,
			Path:    dir,
			Mode:    "desktop",
			Message: err.Error(),
		})
		return
	}

	writeJSON(w, api.OpenDirResponseDto{Opened: true, Path: dir, Mode: "desktop"})
}

// resolveOpenDirPath maps the request "kind" to a server-side allowlisted
// directory. The set of allowed kinds is closed; any other value is rejected
// before any filesystem access happens.
func (d *Daemon) resolveOpenDirPath(kind string) (string, error) {
	switch kind {
	case "volumes":
		base := d.activeVolumesBase()
		if base == "" {
			return "", fmt.Errorf("no namespace configured")
		}
		return base, nil
	case "snapshots":
		dir, err := d.snapshotsDir()
		if err != nil {
			return "", err
		}
		// Ensure it exists so opening the snapshots folder on a namespace that
		// has never had a snapshot taken lands the user in an empty directory
		// rather than failing on a missing path.
		if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
			return "", fmt.Errorf("create snapshots dir: %w", mkErr)
		}
		return dir, nil
	default:
		return "", fmt.Errorf("unsupported open-dir kind: %q", kind)
	}
}

// openLocalDir shells out to the platform-native file-manager opener.
// Mirrors the desktop.OpenBrowser helper but for directories.
func openLocalDir(dir string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir) //nolint:gosec // G204: dir is from server-side allowlist, not user input
	case "windows":
		cmd = exec.Command("explorer", dir) //nolint:gosec // G204: dir is from server-side allowlist, not user input
	default:
		cmd = exec.Command("xdg-open", dir) //nolint:gosec // G204: dir is from server-side allowlist, not user input
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch file manager: %w", err)
	}
	return nil
}
