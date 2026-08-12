package daemon

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The export directory over HTTP: list, download, delete.
//
// This is the way an artifact leaves the box. The container writes into
// /citeck/export (a bind mount of rtfiles/export/<app>), and everything a
// human might want from it — a heap dump, a pg_dump, a thread dump — is on the
// daemon's host, possibly gigabytes large, possibly full of secrets. So:
//
//   - the directory is treated as FLAT. A name with a separator in it is
//     rejected outright rather than cleaned: the launcher generates none of
//     these names (the container does), and "clean it and hope" is how path
//     traversal gets in.
//   - the resolved path is checked against the app's export directory anyway,
//     because a symlink planted inside the container's writable mount would
//     otherwise point anywhere the daemon can read (it usually runs as root).
//   - downloads are streamed, never buffered: a heap dump is the size of the
//     heap that produced it.

// exportFileName validates one export file name and returns it. Names are
// single path elements — no directories, no traversal, no dotfiles.
func exportFileName(w http.ResponseWriter, raw string) (string, bool) {
	if raw == "" || raw == "." || raw == ".." ||
		strings.ContainsAny(raw, `/\`) || strings.HasPrefix(raw, ".") {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeInvalidRequest,
			fmt.Sprintf("invalid export file name %q", raw))
		return "", false
	}
	return raw, true
}

// exportPath resolves one file inside an app's export directory, after
// validating the app name and the file name. Returns "" when it has already
// written the error response.
func (d *Daemon) exportPath(w http.ResponseWriter, appName, file string) string {
	name, ok := exportFileName(w, file)
	if !ok {
		return ""
	}
	dir, ok := d.exportDir(w, appName)
	if !ok {
		return ""
	}
	full := filepath.Join(dir, name)
	// A symlink inside the export dir is the container's to create — resolve
	// before trusting the path. The daemon typically runs as root, so a link
	// to /etc/shadow would otherwise be served happily.
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("export file %q not found", name))
		return ""
	}
	if resolved != full && !isPathUnder(resolved, dir) {
		writeError(w, http.StatusForbidden, "export file resolves outside the export directory")
		return ""
	}
	return resolved
}

// exportDir resolves an app's export directory. The app must be known to the
// namespace — an export dir belongs to an app, and a name that is not one is a
// client error, not an empty listing.
func (d *Daemon) exportDir(w http.ResponseWriter, appName string) (string, bool) {
	if !validateAppName(w, appName) {
		return "", false
	}
	act := d.active()
	if _, ok := runtimeAppDef(act.runtime, appName); !ok {
		writeAppNotFound(w, appName)
		return "", false
	}
	if act.volumesBase == "" {
		writeErrorCode(w, http.StatusServiceUnavailable, api.ErrCodeNotConfigured,
			"no active namespace")
		return "", false
	}
	return namespace.ExportDirFor(act.volumesBase, appName), true
}

func (d *Daemon) handleListAppExports(w http.ResponseWriter, r *http.Request) {
	appName := r.PathValue("name")
	dir, ok := d.exportDir(w, appName)
	if !ok {
		return
	}

	files := make([]api.ExportFileDto, 0)
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No export dir yet means nothing has been exported — an empty list,
		// not an error. The directory is created on container start.
		writeJSON(w, files)
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		files = append(files, api.ExportFileDto{
			Name:     e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime().UTC().Format(time.RFC3339),
			HostPath: filepath.Join(dir, e.Name()),
		})
	}
	// Newest first: the reason anyone looks here is the thing that just
	// happened.
	sort.Slice(files, func(i, j int) bool {
		if files[i].Modified != files[j].Modified {
			return files[i].Modified > files[j].Modified
		}
		return files[i].Name < files[j].Name
	})
	writeJSON(w, files)
}

func (d *Daemon) handleGetAppExport(w http.ResponseWriter, r *http.Request) {
	path := d.exportPath(w, r.PathValue("name"), r.PathValue("file"))
	if path == "" {
		return
	}
	f, err := os.Open(path) //nolint:gosec // G304: path is a validated single name under the app's export dir, symlink-resolved
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "not a regular file")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	// Streamed, not buffered: this file can be as large as the heap that
	// produced it.
	if _, err := io.Copy(w, f); err != nil {
		// The header is already out, so the truncated body is all the client
		// gets — which is why the CLI verifies the size it received.
		//nolint:gosec // G706: both values passed the validation in exportPath above
		slog.Warn("Export download interrupted", "app", r.PathValue("name"), "file", info.Name(), "err", err)
	}
}

func (d *Daemon) handleDeleteAppExport(w http.ResponseWriter, r *http.Request) {
	path := d.exportPath(w, r.PathValue("name"), r.PathValue("file"))
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}
