package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"log/slog"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/form"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/git"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/snapshot"
	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

// safeIDPattern allows only alphanumeric, hyphens, underscores, dots.
var safeIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// validateID checks that an ID is safe for use in file paths and SQL queries.
func validateID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && safeIDPattern.MatchString(id) &&
		!strings.Contains(id, "..") && !strings.ContainsAny(id, "/\\")
}

// sanitizeName converts a human name to a safe filesystem ID.
func sanitizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('-')
		}
	}
	id := b.String()
	if len(id) > 64 {
		id = id[:64]
	}
	return id
}

// --- Namespace list ---

func (d *Daemon) handleListNamespaces(w http.ResponseWriter, r *http.Request) {
	var result []api.NamespaceSummaryDto

	if config.IsDesktopMode() {
		namespaces, err := config.ListAllNamespaces()
		if err != nil {
			writeInternalError(w, err)
			return
		}
		for _, ns := range namespaces {
			summary := api.NamespaceSummaryDto{
				ID:          ns.NamespaceID,
				WorkspaceID: ns.WorkspaceID,
				Status:      string(namespace.NsStatusStopped),
			}
			// Load config to get name and bundle ref
			cfg, err := namespace.LoadNamespaceConfig(ns.ConfigPath)
			if err == nil {
				summary.Name = cfg.Name
				summary.BundleRef = cfg.BundleRef.String()
			}
			// Check if this is the active namespace
			d.configMu.RLock()
			if d.runtime != nil && d.nsConfig != nil && d.nsConfig.ID == ns.NamespaceID {
				summary.Status = string(d.runtime.Status())
			}
			d.configMu.RUnlock()
			result = append(result, summary)
		}
	} else {
		// Server mode: single namespace
		d.configMu.RLock()
		nsCfg := d.nsConfig
		d.configMu.RUnlock()
		if nsCfg != nil {
			status := string(namespace.NsStatusStopped)
			if d.runtime != nil {
				status = string(d.runtime.Status())
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
	d.configMu.RUnlock()
	if activeID == nsID && d.runtime != nil && d.runtime.Status() != namespace.NsStatusStopped {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "cannot delete active namespace; stop it first")
		return
	}

	if config.IsDesktopMode() {
		configPath := config.WorkspaceNamespaceConfigPath(d.workspaceID, nsID)
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			writeInternalError(w, err)
			return
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
				Name:     qs.Name,
				Template: qs.Template,
				Snapshot: qs.Snapshot,
			})
		}
	}
	if quickStarts == nil {
		quickStarts = []api.QuickStartDto{}
	}
	writeJSON(w, quickStarts)
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

// --- Namespace creation + Bundles ---

func (d *Daemon) handleCreateNamespace(w http.ResponseWriter, r *http.Request) {
	var req api.NamespaceCreateDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Server-side validation
	spec := form.GetSpec(form.NamespaceCreateFormID)
	if spec != nil {
		data := map[string]any{
			"name":     req.Name,
			"authType": req.AuthType,
			"host":     req.Host,
			"port":     float64(req.Port),
		}
		if fieldErrs := form.Validate(spec, data); len(fieldErrs) > 0 {
			fields := make([]api.FieldErrorDto, len(fieldErrs))
			for i, fe := range fieldErrs {
				fields[i] = api.FieldErrorDto{Key: fe.Key, Message: fe.Message}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(api.ValidationErrorDto{
				Error:  "validation failed",
				Fields: fields,
			})
			return
		}
	}

	// Generate namespace config — start from template if specified
	nsCfg := namespace.DefaultNamespaceConfig()

	// Apply namespace template if specified
	templateID := req.Template
	if templateID == "" {
		templateID = "default" // use default template if none specified
	}
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()
	if wsCfg != nil {
		for _, tmpl := range wsCfg.NamespaceTemplates {
			if tmpl.ID == templateID {
				// Apply template config as base by marshalling to YAML and re-parsing
				if len(tmpl.Config) > 0 {
					if tmplData, err := yaml.Marshal(tmpl.Config); err == nil {
						yaml.Unmarshal(tmplData, &nsCfg)
					}
				}
				// Set template ID for runtime use
				nsCfg.Template = templateID
				break
			}
		}
		// If no bundleRef from template or request, use first bundle repo + LATEST
		if nsCfg.BundleRef.IsEmpty() && req.BundleRepo == "" && len(wsCfg.BundleRepos) > 0 {
			nsCfg.BundleRef = bundle.BundleRef{Repo: wsCfg.BundleRepos[0].ID, Key: "LATEST"}
		}
	}

	nsCfg.Name = req.Name
	nsCfg.ID = sanitizeName(req.Name)
	if nsCfg.ID == "" {
		writeError(w, http.StatusBadRequest, "name produces empty ID after sanitization")
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
		nsCfg.BundleRef = bundle.BundleRef{Repo: req.BundleRepo, Key: req.BundleKey}
	}

	// Serialize to YAML
	data, err := namespace.MarshalNamespaceConfig(&nsCfg)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Write config file
	var configPath string
	if config.IsDesktopMode() {
		wsID := req.WorkspaceID
		if wsID == "" {
			wsID = d.workspaceID
		}
		if !validateID(wsID) {
			writeError(w, http.StatusBadRequest, "invalid workspace id")
			return
		}
		nsDir := config.NamespaceDir(wsID, nsCfg.ID)
		if err := os.MkdirAll(nsDir, 0o755); err != nil {
			writeInternalError(w, err)
			return
		}
		configPath = config.WorkspaceNamespaceConfigPath(wsID, nsCfg.ID)
	} else {
		configPath = config.NamespaceConfigPath()
	}

	// Atomic existence check — O_EXCL fails if file already exists (no TOCTOU race)
	excl, err := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceExists,
				fmt.Sprintf("namespace %q already exists", nsCfg.ID))
			return
		}
		writeInternalError(w, err)
		return
	}
	excl.Close()

	if err := fsutil.AtomicWriteFile(configPath, data, 0o644); err != nil {
		writeInternalError(w, err)
		return
	}

	// Trigger background snapshot download + import if specified
	if req.Snapshot != "" {
		wsID := req.WorkspaceID
		if wsID == "" {
			wsID = d.workspaceID
		}
		d.bgWg.Add(1)
		go func() {
			defer d.bgWg.Done()
			d.downloadAndImportSnapshot(req.Snapshot, wsID, nsCfg.ID)
		}()
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("namespace %q created", nsCfg.Name)})
}

func (d *Daemon) handleListBundles(w http.ResponseWriter, _ *http.Request) {
	d.configMu.RLock()
	wsCfg := d.workspaceConfig
	d.configMu.RUnlock()

	var result []api.BundleInfoDto
	if wsCfg != nil {
		for _, repo := range wsCfg.BundleRepos {
			// Scan the on-disk bundle directory for this repo
			bundlesDir := config.ResolveBundlesDir(d.workspaceID, repo.ID)
			if repo.Path != "" {
				bundlesDir = filepath.Join(bundlesDir, repo.Path)
			}
			versions := bundle.ListBundleVersions(bundlesDir)
			result = append(result, api.BundleInfoDto{Repo: repo.ID, Versions: versions})
		}
	}
	if result == nil {
		result = []api.BundleInfoDto{}
	}
	writeJSON(w, result)
}

// --- Secrets ---

func (d *Daemon) handleListSecrets(w http.ResponseWriter, _ *http.Request) {
	if d.store == nil {
		writeJSON(w, []api.SecretMetaDto{})
		return
	}

	secrets, err := d.store.ListSecrets()
	if err != nil {
		writeInternalError(w, err)
		return
	}

	result := make([]api.SecretMetaDto, len(secrets))
	for i, s := range secrets {
		result[i] = api.SecretMetaDto{
			ID:        s.ID,
			Name:      s.Name,
			Type:      string(s.Type),
			Scope:     s.Scope,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
		}
	}
	writeJSON(w, result)
}

func (d *Daemon) handleCreateSecret(w http.ResponseWriter, r *http.Request) {
	if d.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage not available")
		return
	}

	var req api.SecretCreateDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validateID(req.ID) {
		writeError(w, http.StatusBadRequest, "invalid secret id")
		return
	}
	if req.Value == "" {
		writeError(w, http.StatusBadRequest, "value is required")
		return
	}

	secret := storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:    req.ID,
			Name:  req.Name,
			Type:  storage.SecretType(req.Type),
			Scope: req.Scope,
		},
		Value: req.Value,
	}

	if err := d.store.SaveSecret(secret); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "secret saved"})
}

func (d *Daemon) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validateID(id) {
		writeError(w, http.StatusBadRequest, "invalid secret id")
		return
	}
	if d.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage not available")
		return
	}

	if err := d.store.DeleteSecret(id); err != nil {
		writeInternalError(w, err)
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "secret deleted"})
}

func (d *Daemon) handleTestSecret(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validateID(id) {
		writeError(w, http.StatusBadRequest, "invalid secret id")
		return
	}
	if d.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage not available")
		return
	}

	secret, err := d.store.GetSecret(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Test connectivity based on secret type
	switch secret.Type {
	case storage.SecretGitToken:
		// Resolve the repo URL for this secret's scope from workspace config
		var repoURL string
		d.configMu.RLock()
		wsCfg := d.workspaceConfig
		d.configMu.RUnlock()
		if wsCfg != nil && secret.Scope != "" {
			for _, repo := range wsCfg.BundleRepos {
				if repo.AuthType == secret.Scope && repo.URL != "" {
					repoURL = repo.URL
					break
				}
			}
		}
		if repoURL == "" {
			writeJSON(w, api.ActionResultDto{Success: false, Message: "no repository configured for this secret scope"})
			return
		}
		if err := git.TestAuth(repoURL, secret.Value); err != nil {
			writeJSON(w, api.ActionResultDto{Success: false, Message: fmt.Sprintf("auth failed: %v", err)})
		} else {
			writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("authenticated to %s", repoURL)})
		}
	default:
		writeJSON(w, api.ActionResultDto{Success: true, Message: "secret exists"})
	}
}

// --- Diagnostics ---

func (d *Daemon) handleGetDiagnostics(w http.ResponseWriter, _ *http.Request) {
	var checks []api.DiagnosticCheckDto

	// Check 1: Docker reachable
	if d.dockerClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pingErr := d.dockerClient.Ping(ctx)
		cancel()
		if pingErr != nil {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Docker", Status: "error", Message: "Docker is not reachable: " + pingErr.Error(), Fixable: false,
			})
		} else {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Docker", Status: "ok", Message: "Docker is running", Fixable: false,
			})
		}
	}

	// Check 2: Socket exists
	socketPath := config.SocketPath()
	if _, err := os.Stat(socketPath); err != nil {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Socket", Status: "error", Message: "Daemon socket not found: " + socketPath, Fixable: true,
		})
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Socket", Status: "ok", Message: "Socket: " + socketPath, Fixable: false,
		})
	}

	// Check 3: Namespace config valid
	nsID := d.activeNsID()
	nsCfgPath := config.ResolveNamespaceConfigPath(d.workspaceID, nsID)
	if _, err := namespace.LoadNamespaceConfig(nsCfgPath); err != nil {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Config", Status: "warning", Message: "Namespace config: " + err.Error(), Fixable: false,
		})
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Config", Status: "ok", Message: "Namespace config is valid", Fixable: false,
		})
	}

	// Check 4: Disk space
	if freeGB, totalGB, err := diskSpace(config.HomeDir()); err == nil {
		pctUsed := 0.0
		if totalGB > 0 {
			pctUsed = (1 - freeGB/totalGB) * 100
		}
		msg := fmt.Sprintf("Disk: %.1f GB free of %.1f GB (%.0f%% used)", freeGB, totalGB, pctUsed)
		status := "ok"
		if freeGB < 1.0 {
			status = "error"
			msg = fmt.Sprintf("Disk critically low: %.1f GB free", freeGB)
		} else if freeGB < 5.0 {
			status = "warning"
			msg = fmt.Sprintf("Disk space low: %.1f GB free of %.1f GB", freeGB, totalGB)
		}
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Disk", Status: status, Message: msg, Fixable: false,
		})
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Disk", Status: "warning", Message: "Disk space check failed: " + err.Error(), Fixable: false,
		})
	}

	// Check 5: Runtime status
	if d.runtime != nil {
		status := d.runtime.Status()
		if status == namespace.NsStatusRunning {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Runtime", Status: "ok", Message: "Namespace is running", Fixable: false,
			})
		} else {
			checks = append(checks, api.DiagnosticCheckDto{
				Name: "Runtime", Status: "warning", Message: fmt.Sprintf("Namespace status: %s", status), Fixable: false,
			})
		}
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Runtime", Status: "warning", Message: "No namespace is loaded", Fixable: false,
		})
	}

	writeJSON(w, api.DiagnosticsDto{Checks: checks})
}

func (d *Daemon) handleDiagnosticsFix(w http.ResponseWriter, _ *http.Request) {
	fixed := 0
	failed := 0

	// Fix 1: stale socket cleanup
	socketPath := config.SocketPath()
	if _, err := os.Stat(socketPath); err == nil {
		// Check if socket is actually in use by trying to connect
		conn, dialErr := net.DialTimeout("unix", socketPath, 2*time.Second)
		if dialErr != nil {
			// Socket exists but nobody is listening — it's stale
			if err := os.Remove(socketPath); err != nil {
				failed++
			} else {
				fixed++
			}
		} else {
			conn.Close()
		}
	}

	msg := fmt.Sprintf("Fixed %d issue(s)", fixed)
	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}
	writeJSON(w, api.DiagFixResultDto{Fixed: fixed, Failed: failed, Message: msg})
}

func (d *Daemon) snapshotsDir() (string, error) {
	nsID := d.activeNsID()
	if nsID == "" {
		return "", fmt.Errorf("no namespace configured")
	}
	return filepath.Join(config.ResolveVolumesBase(d.workspaceID, nsID), "snapshots"), nil
}

func (d *Daemon) activeNsID() string {
	d.configMu.RLock()
	defer d.configMu.RUnlock()
	if d.nsConfig != nil {
		return d.nsConfig.ID
	}
	return ""
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

	var result []api.SnapshotDto
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
	if result == nil {
		result = []api.SnapshotDto{}
	}
	writeJSON(w, result)
}

func (d *Daemon) handleExportSnapshot(w http.ResponseWriter, r *http.Request) {
	if !d.snapshotMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeSnapshotInProgress, "another snapshot operation is in progress")
		return
	}
	// Validation under lock — unlock on early return
	if d.runtime != nil && d.runtime.Status() != namespace.NsStatusStopped {
		d.snapshotMu.Unlock()
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "namespace must be stopped before export")
		return
	}
	if d.dockerClient == nil {
		d.snapshotMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}

	dir, err := d.snapshotsDir()
	if err != nil {
		d.snapshotMu.Unlock()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	d.bgWg.Add(1)
	// Lock ownership transferred to goroutine — unlocked when export completes
	go func() {
		defer d.snapshotMu.Unlock()
		defer d.bgWg.Done()
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
	}()

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Export started: %s", fileName),
	})
}

func (d *Daemon) handleImportSnapshot(w http.ResponseWriter, r *http.Request) {
	// Limit upload body to 2GB to prevent unbounded memory/disk usage
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)

	if !d.snapshotMu.TryLock() {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeSnapshotInProgress, "another snapshot operation is in progress")
		return
	}
	// Validation under lock — unlock on early return
	if d.runtime != nil && d.runtime.Status() != namespace.NsStatusStopped {
		d.snapshotMu.Unlock()
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "namespace must be stopped before import")
		return
	}
	if d.dockerClient == nil {
		d.snapshotMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}

	// Accept multipart file upload or a snapshot name query parameter
	snapshotName := r.URL.Query().Get("name")
	var zipPath string
	var tmpPath string // non-empty if we created a temp file that needs cleanup

	if snapshotName != "" {
		// Import from existing snapshot file
		if !strings.HasSuffix(snapshotName, ".zip") || !validateID(strings.TrimSuffix(snapshotName, ".zip")) {
			d.snapshotMu.Unlock()
			writeError(w, http.StatusBadRequest, "invalid snapshot name")
			return
		}
		snapDir, err := d.snapshotsDir()
		if err != nil {
			d.snapshotMu.Unlock()
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		zipPath = filepath.Join(snapDir, snapshotName)
		if _, err := os.Stat(zipPath); err != nil {
			d.snapshotMu.Unlock()
			writeError(w, http.StatusNotFound, "snapshot not found")
			return
		}
	} else {
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
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			d.snapshotMu.Unlock()
			writeInternalError(w, err)
			return
		}
		tmpFile.Close()
		zipPath = tmpFile.Name()
		tmpPath = tmpFile.Name() // goroutine will clean up
	}

	// Launch import in background and return 202 immediately.
	// Lock ownership transferred to goroutine.
	importPath := zipPath
	d.bgWg.Add(1)
	go func() {
		defer d.snapshotMu.Unlock()
		defer d.bgWg.Done()
		if tmpPath != "" {
			defer os.Remove(tmpPath)
		}
		meta, err := snapshot.Import(d.bgCtx, d.dockerClient, importPath, d.volumesBase)
		if err != nil {
			slog.Error("Snapshot import failed", "err", err)
			d.broadcastEvent(api.EventDto{
				Type: "snapshot_import", After: "failed",
			})
			return
		}
		slog.Info("Snapshot import completed", "volumes", len(meta.Volumes))
		d.broadcastEvent(api.EventDto{
			Type: "snapshot_import", After: "completed",
		})
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(api.ActionResultDto{
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
	d.bgWg.Add(1)
	go func() {
		defer d.bgWg.Done()
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
	}()

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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
				os.Remove(destPath) // stale or corrupted — re-download
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

	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		writeError(w, http.StatusConflict, "target name already exists")
		return
	}

	if err := os.Rename(oldPath, newPath); err != nil {
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
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupHost(ctx, host)
			if err != nil {
				return nil, err
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
