package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/git"
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

// resolveActiveRepoOpts returns the URL/branch/token to use when force-pulling
// the workspace repo. Falls back to the hardcoded defaults when no store entry
// or the active workspace ID isn't set. The PullPeriod is ignored by callers
// that bypass throttling (force-pull); other callers should prefer
// resolveActiveWorkspaceRepoOpts.
func (d *Daemon) resolveActiveRepoOpts() (url, branch, token string) {
	opts := d.resolveActiveWorkspaceRepoOpts()
	if opts.URL != "" {
		url = opts.URL
	} else {
		url = bundle.DefaultBundlesRepo
	}
	if opts.Branch != "" {
		branch = opts.Branch
	} else {
		branch = bundle.DefaultBundlesBranch
	}
	return url, branch, opts.Token
}

// resolveActiveWorkspaceRepoOpts is the canonical entry point that maps the
// active workspace's stored settings into the bundle resolver's workspace
// repo options. Empty fields are left zero so the resolver can layer them on
// top of its hardcoded defaults.
func (d *Daemon) resolveActiveWorkspaceRepoOpts() bundle.WorkspaceRepoOpts {
	if d.store == nil || d.workspaceID == "" {
		return bundle.WorkspaceRepoOpts{}
	}
	ws, _ := d.store.GetWorkspace(d.workspaceID)
	if ws == nil {
		return bundle.WorkspaceRepoOpts{}
	}
	return d.workspaceRepoOptsFromDto(*ws)
}

// workspaceRepoOptsFromDto translates a stored WorkspaceDto into bundle opts,
// resolving the TOKEN secret when present. Exposed for the SwitchWorkspace
// path which needs to honor the *target* workspace, not d.workspaceID.
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
func buildWorkspaceRepoOpts(ws storage.WorkspaceDto, secretSvc secretValueReader) bundle.WorkspaceRepoOpts {
	opts := bundle.WorkspaceRepoOpts{
		URL:    ws.RepoURL,
		Branch: ws.RepoBranch,
	}
	if dur, err := storage.ParseISO8601Duration(ws.RepoPullPeriod); err == nil {
		opts.PullPeriod = dur
	}
	if ws.AuthType == "TOKEN" && secretSvc != nil {
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

// workspaceRepoLocalDir returns the on-disk location of the cloned default
// workspace repo. Mirrors the layout used by bundle.resolveWorkspace().
func (d *Daemon) workspaceRepoLocalDir() string {
	bundlesDataDir := config.DataDir()
	if config.IsDesktopMode() {
		bundlesDataDir = filepath.Join(config.HomeDir(), "ws", d.workspaceID)
	}
	return filepath.Join(bundlesDataDir, "bundles", "workspace")
}

// handleWorkspaceUpdate force-pulls the default workspace repo (bypassing the
// PullPeriod throttle) and triggers a runtime reload so config changes are
// picked up immediately. Kotlin parity: "Force Update" RMB menu on the
// Welcome screen (WelcomeScreen.kt).
func (d *Daemon) handleWorkspaceUpdate(w http.ResponseWriter, _ *http.Request) {
	if !d.reloadMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeReloadInProgress, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()

	// Force-pull the workspace repo with PullPeriod=0 to bypass the throttle.
	// resolveActiveRepoOpts shares the same source-of-truth as the bundle
	// resolver's workspace clone path, so a private repo configured for
	// startup auto-pull stays consistent with this manual force-pull.
	repoURL, repoBranch, repoToken := d.resolveActiveRepoOpts()
	repoDir := d.workspaceRepoLocalDir()
	gitCtx, gitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer gitCancel()
	if err := git.CloneOrPullWithAuth(gitCtx, git.RepoOpts{
		URL: repoURL, Branch: repoBranch, Token: repoToken,
		DestDir: repoDir, PullPeriod: 0,
	}); err != nil {
		slog.Warn("Force workspace update: git pull failed", "err", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("workspace pull failed: %v", err))
		return
	}

	// Only trigger a reload if a namespace is configured; otherwise the pull
	// alone is enough — the welcome screen will pick up new workspace data on
	// its next refresh.
	d.configMu.RLock()
	hasNamespace := d.runtime != nil && d.nsConfig != nil
	d.configMu.RUnlock()

	if hasNamespace {
		if err := d.doReload(); err != nil {
			slog.Warn("Force workspace update: reload after pull failed", "err", err)
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("reload after pull failed: %v", err))
			return
		}
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "Workspace updated"})
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
		d.configMu.RLock()
		base := d.volumesBase
		d.configMu.RUnlock()
		if base == "" {
			return "", fmt.Errorf("no namespace configured")
		}
		return base, nil
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
