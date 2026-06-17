package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// downloadNameUnsafe matches characters not allowed in a saved download
// filename — everything outside [A-Za-z0-9._-] is replaced with '_'.
var downloadNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// userDownloadsDir resolves the OS "Downloads" folder (~/Downloads on Linux,
// macOS and Windows), falling back to the home dir when it does not exist. It
// never creates the folder so it can't leave a stray dir with odd permissions.
func userDownloadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, "Downloads")
	if info, statErr := os.Stat(dir); statErr == nil && info.IsDir() {
		return dir, nil
	}
	return home, nil
}

// handleDesktopSaveDownload writes a UI-provided text payload (e.g. a log dump)
// into the user's Downloads folder. The WebKitGTK webview has no download
// manager, so the browser <a download> path is a no-op there — the desktop UI
// posts here instead and then offers an "open folder" action. The filename is
// reduced to a sanitized base name so it can only ever land inside Downloads.
func (d *Daemon) handleDesktopSaveDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	// Decode with a larger ceiling than readJSON's shared 1 MiB: a saved log dump
	// (up to 5000 lines of Java stack traces) routinely exceeds 1 MiB and must
	// not be silently truncated/rejected. 32 MiB is a generous local-only bound.
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := downloadNameUnsafe.ReplaceAllString(filepath.Base(req.Filename), "_")
	if name == "" || name == "." || name == ".." {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}
	dir, err := userDownloadsDir()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(req.Content), 0o600); err != nil { //nolint:gosec // G304: path = Downloads dir + sanitized base name (no separators)
		writeInternalError(w, err)
		return
	}
	slog.Info("Saved log download to Downloads")
	writeJSON(w, map[string]string{"path": path, "dir": dir})
}

// handleDesktopOpenDownloads reveals the Downloads folder in the OS file manager
// via the wrapper's shell.openPath verb. Server mode (no wrapper socket) → 503.
func (d *Daemon) handleDesktopOpenDownloads(w http.ResponseWriter, r *http.Request) {
	sock := os.Getenv("CITECK_WRAPPER_SOCK")
	if sock == "" {
		http.Error(w, "no wrapper socket configured", http.StatusServiceUnavailable)
		return
	}
	dir, err := userDownloadsDir()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	// Verb string duplicated from desktop.VerbShellOpenPath (daemon cannot import
	// internal/desktop — import cycle), same as desktop_focus.go / desktop_tray.go.
	if err := newWrapperClient(sock).call(ctx, "shell.openPath", map[string]any{"path": dir}); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
