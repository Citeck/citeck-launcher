package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/snapshot"
)

func (d *Daemon) snapshotsDir() (string, error) {
	nsID := d.activeNsID()
	if nsID == "" {
		return "", fmt.Errorf("no namespace configured")
	}
	return filepath.Join(config.ResolveVolumesBase(d.workspaceID, nsID), "snapshots"), nil
}

func (d *Daemon) handleListSnapshots(w http.ResponseWriter, _ *http.Request) {
	dir, err := d.snapshotsDir()
	if err != nil {
		writeJSON(w, []api.SnapshotDto{})
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, []api.SnapshotDto{})
		return
	}

	result := make([]api.SnapshotDto, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, api.SnapshotDto{
			Name:      entry.Name(),
			CreatedAt: info.ModTime().Format(time.RFC3339),
			Size:      info.Size(),
		})
	}
	writeJSON(w, result)
}

func (d *Daemon) handleExportSnapshot(w http.ResponseWriter, r *http.Request) {
	if !d.snapshotMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeSnapshotInProgress, "another snapshot operation is in progress")
		return
	}
	// Validation under lock — unlock on early return
	d.configMu.RLock()
	rt := d.runtime
	d.configMu.RUnlock()
	if rt != nil && rt.Status() != namespace.NsStatusStopped {
		d.snapshotMu.Unlock()
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "namespace must be stopped before export")
		return
	}
	if d.dockerClient == nil {
		d.snapshotMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}

	// Determine output directory: query param or default snapshots dir
	dir := r.URL.Query().Get("output")
	if dir != "" {
		dir = filepath.Clean(dir)
		if !filepath.IsAbs(dir) {
			d.snapshotMu.Unlock()
			writeError(w, http.StatusBadRequest, "output path must be absolute")
			return
		}
	} else {
		var dirErr error
		dir, dirErr = d.snapshotsDir()
		if dirErr != nil {
			d.snapshotMu.Unlock()
			writeError(w, http.StatusBadRequest, dirErr.Error())
			return
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // snapshot dirs need 0o755 for container access
		d.snapshotMu.Unlock()
		writeInternalError(w, err)
		return
	}

	nsName := "namespace"
	d.configMu.RLock()
	if d.nsConfig != nil {
		nsName = sanitizeName(d.nsConfig.Name)
		if nsName == "" {
			nsName = d.nsConfig.ID
		}
	}
	d.configMu.RUnlock()
	fileName := fmt.Sprintf("%s_%s.zip", nsName, time.Now().Format("2006-01-02_15-04-05"))
	outputPath := filepath.Join(dir, fileName)

	nsID := d.activeNsID()
	// Lock ownership transferred to goroutine — unlocked when export completes
	d.bgWg.Go(func() {
		defer d.snapshotMu.Unlock()
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_export", Timestamp: time.Now().UnixMilli(),
			NamespaceID: nsID, After: fmt.Sprintf("exporting to %s", fileName),
		})
		meta, err := snapshot.Export(d.bgCtx, d.dockerClient, outputPath, d.volumesBase)
		if err != nil {
			slog.Error("Snapshot export failed", "err", err)
			d.broadcastEvent(api.EventDto{
				Type: "snapshot_error", Timestamp: time.Now().UnixMilli(),
				NamespaceID: nsID, After: err.Error(),
			})
			return
		}
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_complete", Timestamp: time.Now().UnixMilli(),
			NamespaceID: nsID, After: fmt.Sprintf("exported %d volumes to %s", len(meta.Volumes), fileName),
		})
	})

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Export started: %s", fileName),
	})
}

//nolint:nestif // import handles both file upload and named snapshot paths with manual lock management
func (d *Daemon) handleImportSnapshot(w http.ResponseWriter, r *http.Request) {
	// Limit upload body to 2GB to prevent unbounded memory/disk usage
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)

	// Validate snapshot name/file BEFORE acquiring locks or checking namespace status.
	// This avoids unnecessary namespace disruption when input is invalid.
	snapshotName := r.URL.Query().Get("name")
	var zipPath string
	var tmpPath string // non-empty if we created a temp file that needs cleanup

	if snapshotName != "" {
		// Pre-validate name format and file existence (no lock needed)
		if !strings.HasSuffix(snapshotName, ".zip") || !validateID(strings.TrimSuffix(snapshotName, ".zip")) {
			writeError(w, http.StatusBadRequest, "invalid snapshot name")
			return
		}
		snapDir, err := d.snapshotsDir()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		zipPath = filepath.Join(snapDir, snapshotName)
		if _, err := os.Stat(zipPath); err != nil { //nolint:gosec // G703: zipPath built from sanitized snapshotName via validateID
			writeError(w, http.StatusNotFound, "snapshot not found")
			return
		}
	}

	if !d.snapshotMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeSnapshotInProgress, "another snapshot operation is in progress")
		return
	}
	// Validation under lock — unlock on early return
	d.configMu.RLock()
	rt := d.runtime
	d.configMu.RUnlock()
	if rt != nil && rt.Status() != namespace.NsStatusStopped {
		d.snapshotMu.Unlock()
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "namespace must be stopped before import")
		return
	}
	if d.dockerClient == nil {
		d.snapshotMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}

	if snapshotName == "" {
		// Accept file upload
		if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB in memory, Go spills to disk
			d.snapshotMu.Unlock()
			writeError(w, http.StatusBadRequest, "invalid multipart form")
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			d.snapshotMu.Unlock()
			writeError(w, http.StatusBadRequest, "file field required")
			return
		}
		defer file.Close()

		tmpFile, err := os.CreateTemp("", "citeck-snapshot-upload-*.zip")
		if err != nil {
			d.snapshotMu.Unlock()
			writeInternalError(w, err)
			return
		}

		if _, err := io.Copy(tmpFile, file); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpFile.Name())
			d.snapshotMu.Unlock()
			writeInternalError(w, err)
			return
		}
		_ = tmpFile.Close()
		zipPath = tmpFile.Name()
		tmpPath = tmpFile.Name() // goroutine will clean up
	}

	// Launch import in background and return 202 immediately.
	// Lock ownership transferred to goroutine.
	importPath := zipPath
	d.bgWg.Go(func() {
		defer d.snapshotMu.Unlock()
		if tmpPath != "" {
			defer os.Remove(tmpPath)
		}
		nsID := d.activeNsID()
		meta, err := snapshot.Import(d.bgCtx, d.dockerClient, importPath, d.volumesBase)
		if err != nil {
			slog.Error("Snapshot import failed", "err", err)
			d.broadcastEvent(api.EventDto{
				Type: "snapshot_error", Timestamp: time.Now().UnixMilli(),
				NamespaceID: nsID, After: err.Error(),
			})
			return
		}
		slog.Info("Snapshot import completed", "volumes", len(meta.Volumes)) //nolint:gosec // G706: meta.Volumes is internal count, not user-controlled
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_complete", Timestamp: time.Now().UnixMilli(),
			NamespaceID: nsID, After: fmt.Sprintf("imported %d volumes", len(meta.Volumes)),
		})
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.ActionResultDto{
		Success: true,
		Message: "Import started",
	})
}

func (d *Daemon) handleDownloadSnapshot(w http.ResponseWriter, r *http.Request) {
	var req api.SnapshotDownloadDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}

	// SSRF protection: validate URL scheme and resolved IP
	if err := validateSnapshotURL(req.URL); err != nil {
		writeErrorCode(w, http.StatusBadRequest, api.ErrCodeSSRFBlocked, fmt.Sprintf("blocked URL: %s", err.Error()))
		return
	}

	dir, err := d.snapshotsDir()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // snapshot dirs need 0o755 for container access
		writeInternalError(w, err)
		return
	}

	// Determine file name — sanitize to prevent path traversal
	fileName := req.Name
	if fileName == "" {
		fileName = safeSnapshotFileName(req.URL)
	}
	fileName = filepath.Base(fileName) // strip any directory components
	if fileName == "." || fileName == "/" || fileName == "" {
		writeError(w, http.StatusBadRequest, "invalid snapshot name")
		return
	}
	if !strings.HasSuffix(fileName, ".zip") {
		fileName += ".zip"
	}
	destPath := filepath.Join(dir, fileName)

	// Skip if already downloaded
	if _, err := os.Stat(destPath); err == nil {
		writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Snapshot %s already exists", fileName)})
		return
	}

	// Run download in background, report via SSE events
	nsID := d.activeNsID()
	d.bgWg.Go(func() {
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_download", Timestamp: time.Now().UnixMilli(),
			NamespaceID: nsID, After: fmt.Sprintf("downloading %s", fileName),
		})
		if err := snapshot.DownloadWithClient(d.bgCtx, ssrfSafeClient, req.URL, destPath, req.SHA256, nil); err != nil {
			slog.Error("Snapshot download failed", "url", req.URL, "err", err)
			d.broadcastEvent(api.EventDto{
				Type: "snapshot_error", Timestamp: time.Now().UnixMilli(),
				NamespaceID: nsID, After: err.Error(),
			})
			return
		}
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_complete", Timestamp: time.Now().UnixMilli(),
			NamespaceID: nsID, After: fmt.Sprintf("downloaded %s", fileName),
		})
	})

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Download started: %s", fileName),
	})
}

// safeSnapshotFileName extracts a safe filename from a download URL.
// Strips query parameters and path components.
func safeSnapshotFileName(rawURL string) string {
	if idx := strings.LastIndex(rawURL, "/"); idx >= 0 {
		name := rawURL[idx+1:]
		if qIdx := strings.Index(name, "?"); qIdx >= 0 {
			name = name[:qIdx]
		}
		name = filepath.Base(name)
		if name != "" && name != "." && name != "/" {
			return name
		}
	}
	return "snapshot.zip"
}

// downloadAndImportSnapshot downloads a snapshot in the background and imports it into the namespace.
//
//nolint:nestif // download+import orchestration requires nested SHA256 verification and retry logic
func (d *Daemon) downloadAndImportSnapshot(snapshotID, wsID, nsID string) {
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()

	snapDef := bundle.FindSnapshot(wsCfg, snapshotID)
	if snapDef == nil {
		slog.Warn("Snapshot not found in workspace config", "id", snapshotID)
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_error", Timestamp: time.Now().UnixMilli(),
			NamespaceID: nsID, After: fmt.Sprintf("snapshot %q not found", snapshotID),
		})
		return
	}

	// Use the new namespace's volumes base, not the active namespace
	volumesBase := config.ResolveVolumesBase(wsID, nsID)
	dir := filepath.Join(volumesBase, "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // snapshot dirs need 0o755 for container access
		slog.Error("Create snapshots dir", "err", err)
		return
	}

	fileName := safeSnapshotFileName(snapDef.URL)
	if !strings.HasSuffix(fileName, ".zip") {
		fileName += ".zip"
	}
	destPath := filepath.Join(dir, fileName)

	d.broadcastEvent(api.EventDto{
		Type: "snapshot_download", Timestamp: time.Now().UnixMilli(),
		NamespaceID: nsID, After: fmt.Sprintf("downloading %s", fileName),
	})

	// Check if file already exists and matches expected hash
	needsDownload := true
	if _, err := os.Stat(destPath); err == nil {
		if snapDef.SHA256 != "" {
			if actual, err := snapshot.FileSHA256(destPath); err == nil && strings.EqualFold(actual, snapDef.SHA256) {
				needsDownload = false
			} else {
				_ = os.Remove(destPath) // stale or corrupted — re-download
			}
		} else {
			needsDownload = false // no hash to verify, trust existing file
		}
	}

	if needsDownload {
		progress := func(received, total int64) {
			pct := int64(0)
			if total > 0 {
				pct = received * 100 / total
			}
			d.broadcastEvent(api.EventDto{
				Type: "snapshot_progress", Timestamp: time.Now().UnixMilli(),
				NamespaceID: nsID, After: fmt.Sprintf("%d%%", pct),
			})
		}

		// Retry loop — up to 3 attempts with 3-second delay
		const maxRetries = 3
		var downloadErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			downloadErr = snapshot.DownloadWithClient(d.bgCtx, ssrfSafeClient, snapDef.URL, destPath, snapDef.SHA256, progress)
			if downloadErr == nil {
				break
			}
			slog.Warn("Snapshot download attempt failed", "attempt", attempt, "err", downloadErr)
			if attempt < maxRetries {
				time.Sleep(3 * time.Second)
			}
		}
		if downloadErr != nil {
			slog.Error("Snapshot download failed after retries", "url", snapDef.URL, "err", downloadErr)
			d.broadcastEvent(api.EventDto{
				Type: "snapshot_error", Timestamp: time.Now().UnixMilli(),
				NamespaceID: nsID, After: downloadErr.Error(),
			})
			return
		}
	}

	// Import the snapshot
	if d.dockerClient == nil {
		slog.Error("Docker client not available for snapshot import")
		return
	}

	d.broadcastEvent(api.EventDto{
		Type: "snapshot_import", Timestamp: time.Now().UnixMilli(),
		NamespaceID: nsID, After: "importing snapshot",
	})

	meta, err := snapshot.Import(d.bgCtx, d.dockerClient, destPath, volumesBase)
	if err != nil {
		slog.Error("Snapshot import failed", "err", err)
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_error", Timestamp: time.Now().UnixMilli(),
			NamespaceID: nsID, After: err.Error(),
		})
		return
	}

	slog.Info("Snapshot imported for new namespace", "ns", nsID, "volumes", len(meta.Volumes))
	d.broadcastEvent(api.EventDto{
		Type: "snapshot_complete", Timestamp: time.Now().UnixMilli(),
		NamespaceID: nsID, After: fmt.Sprintf("imported %d volumes", len(meta.Volumes)),
	})
}

func (d *Daemon) handleWorkspaceSnapshots(w http.ResponseWriter, _ *http.Request) {
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()

	if wsCfg == nil || len(wsCfg.Snapshots) == 0 {
		writeJSON(w, []bundle.SnapshotDef{})
		return
	}
	writeJSON(w, wsCfg.Snapshots)
}

func (d *Daemon) handleRenameSnapshot(w http.ResponseWriter, r *http.Request) {
	oldName := r.PathValue("name")
	if oldName == "" || !strings.HasSuffix(oldName, ".zip") {
		writeError(w, http.StatusBadRequest, "invalid snapshot name")
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "missing new name")
		return
	}

	// Validate names to prevent path traversal
	oldBase := strings.TrimSuffix(oldName, ".zip")
	if !safeIDPattern.MatchString(oldBase) {
		writeError(w, http.StatusBadRequest, "invalid snapshot name")
		return
	}

	dir, err := d.snapshotsDir()
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Ensure new name ends with .zip and validate
	newName := body.Name
	if !strings.HasSuffix(newName, ".zip") {
		newName += ".zip"
	}
	newBase := strings.TrimSuffix(newName, ".zip")
	if !safeIDPattern.MatchString(newBase) {
		writeError(w, http.StatusBadRequest, "invalid new name")
		return
	}

	oldPath := filepath.Join(dir, oldName)
	newPath := filepath.Join(dir, newName)

	if _, err := os.Stat(oldPath); os.IsNotExist(err) { //nolint:gosec // G703: oldPath built from dir + oldName where oldBase is validated by safeIDPattern above
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if _, err := os.Stat(newPath); err == nil { //nolint:gosec // G703: newPath built from dir + newName where newBase is validated by safeIDPattern above
		writeError(w, http.StatusConflict, "target name already exists")
		return
	}

	if err := os.Rename(oldPath, newPath); err != nil { //nolint:gosec // G703: both paths validated via safeIDPattern above
		writeInternalError(w, err)
		return
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("Renamed to %s", newName)})
}

// validateSnapshotURL checks that a URL is safe for server-side download (SSRF protection).
// Only http/https schemes are allowed, and the resolved IP must not be private/loopback/link-local.
func validateSnapshotURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q not allowed (http/https only)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname")
	}

	// Resolve hostname to IPs and check each
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed: %w", err)
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			continue
		}
		if isBlockedIP(ip) {
			return fmt.Errorf("resolved IP %s is blocked (private/loopback/link-local)", ipStr)
		}
	}
	return nil
}

// ssrfSafeClient is a shared HTTP client whose DialContext validates resolved IPs
// against SSRF blocked ranges. Prevents DNS rebinding attacks where a hostname
// resolves to a public IP at validation time but a private IP at connection time.
var ssrfSafeClient = &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("split host:port %q: %w", addr, err)
			}
			ips, err := net.DefaultResolver.LookupHost(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve host %q: %w", host, err)
			}
			for _, ipStr := range ips {
				ip := net.ParseIP(ipStr)
				if ip != nil && isBlockedIP(ip) {
					return nil, fmt.Errorf("SSRF blocked: resolved IP %s for %s", ipStr, host)
				}
			}
			if len(ips) > 0 {
				dialer := &net.Dialer{Timeout: 30 * time.Second}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0], port))
			}
			return nil, fmt.Errorf("no valid IPs for %s", host)
		},
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// isBlockedIP returns true if the IP is in a range that should not be accessed via SSRF.
func isBlockedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// Cloud metadata endpoint (169.254.169.254 — already covered by IsLinkLocalUnicast, explicit for clarity)
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	return false
}
