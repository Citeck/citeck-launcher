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
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/git"
)

// workspaceRepoURL mirrors the default workspace repo URL hardcoded in
// internal/bundle/resolver.go (defaultBundlesRepo). It is duplicated here
// because the bundle package owns its own constant and exposes neither the
// URL nor a "force pull workspace" helper. The two values must stay in sync —
// keep this comment and update both sites when the upstream repo changes.
const (
	workspaceRepoURL    = "https://github.com/Citeck/launcher-workspace.git"
	workspaceRepoBranch = "main"
)

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
	repoDir := d.workspaceRepoLocalDir()
	gitCtx, gitCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer gitCancel()
	if err := git.CloneOrPullWithAuth(gitCtx, git.RepoOpts{
		URL: workspaceRepoURL, Branch: workspaceRepoBranch,
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
