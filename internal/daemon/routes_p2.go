package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/niceteck/citeck-launcher/internal/api"
	"github.com/niceteck/citeck-launcher/internal/bundle"
	"github.com/niceteck/citeck-launcher/internal/config"
	"github.com/niceteck/citeck-launcher/internal/form"
	"github.com/niceteck/citeck-launcher/internal/namespace"
	"github.com/niceteck/citeck-launcher/internal/snapshot"
	"github.com/niceteck/citeck-launcher/internal/storage"
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

// --- Phase E1: Namespace list ---

func (d *Daemon) handleListNamespaces(w http.ResponseWriter, r *http.Request) {
	var result []api.NamespaceSummaryDto

	if config.IsDesktopMode() {
		namespaces, err := config.ListAllNamespaces()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
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
			if d.runtime != nil && d.nsConfig != nil && d.nsConfig.ID == ns.NamespaceID {
				summary.Status = string(d.runtime.Status())
			}
			result = append(result, summary)
		}
	} else {
		// Server mode: single namespace
		if d.nsConfig != nil {
			status := string(namespace.NsStatusStopped)
			if d.runtime != nil {
				status = string(d.runtime.Status())
			}
			result = append(result, api.NamespaceSummaryDto{
				ID:          d.nsConfig.ID,
				WorkspaceID: d.workspaceID,
				Name:        d.nsConfig.Name,
				Status:      status,
				BundleRef:   d.nsConfig.BundleRef.String(),
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
	if d.nsConfig != nil && d.nsConfig.ID == nsID && d.runtime != nil && d.runtime.Status() != namespace.NsStatusStopped {
		writeError(w, http.StatusConflict, "cannot delete active namespace; stop it first")
		return
	}

	if config.IsDesktopMode() {
		configPath := config.WorkspaceNamespaceConfigPath(d.workspaceID, nsID)
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		writeError(w, http.StatusBadRequest, "cannot delete namespace in server mode")
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: "namespace deleted"})
}

func (d *Daemon) handleGetTemplates(w http.ResponseWriter, _ *http.Request) {
	d.configMu.Lock()
	wsCfg := d.workspaceConfig
	d.configMu.Unlock()

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
	d.configMu.Lock()
	wsCfg := d.workspaceConfig
	d.configMu.Unlock()

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

// --- Phase 3E: Forms ---

func (d *Daemon) handleGetForm(w http.ResponseWriter, r *http.Request) {
	formID := r.PathValue("formId")
	spec := form.GetSpec(formID)
	if spec == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("form %q not found", formID))
		return
	}
	writeJSON(w, spec)
}

// --- Phase E3: Namespace creation + Bundles ---

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
			writeError(w, http.StatusBadRequest, fields[0].Message)
			return
		}
	}

	// Generate namespace config
	nsCfg := namespace.DefaultNamespaceConfig()
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
	}
	nsCfg.PgAdmin.Enabled = req.PgAdminEnabled
	if req.BundleRepo != "" && req.BundleKey != "" {
		nsCfg.BundleRef = bundle.BundleRef{Repo: req.BundleRepo, Key: req.BundleKey}
	}

	// Serialize to YAML
	data, err := namespace.MarshalNamespaceConfig(&nsCfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		configPath = config.WorkspaceNamespaceConfigPath(wsID, nsCfg.ID)
	} else {
		configPath = config.NamespaceConfigPath()
	}

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, api.ActionResultDto{Success: true, Message: fmt.Sprintf("namespace %q created", nsCfg.Name)})
}

func (d *Daemon) handleListBundles(w http.ResponseWriter, _ *http.Request) {
	d.configMu.Lock()
	wsCfg := d.workspaceConfig
	d.configMu.Unlock()

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

// --- Phase F1: Secrets ---

func (d *Daemon) handleListSecrets(w http.ResponseWriter, _ *http.Request) {
	if d.store == nil {
		writeJSON(w, []api.SecretMetaDto{})
		return
	}

	secrets, err := d.store.ListSecrets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeJSON(w, api.ActionResultDto{Success: false, Message: "connectivity test for git tokens is not yet implemented"})
	default:
		writeJSON(w, api.ActionResultDto{Success: true, Message: "secret exists"})
	}
}

// --- Phase F2: Diagnostics ---

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
	nsID := "default"
	if d.nsConfig != nil {
		nsID = d.nsConfig.ID
	}
	nsCfgPath := config.ResolveNamespaceConfigPath(d.workspaceID, nsID)
	if _, err := namespace.LoadNamespaceConfig(nsCfgPath); err != nil {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Config", Status: "warn", Message: "Namespace config: " + err.Error(), Fixable: false,
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
			status = "warn"
			msg = fmt.Sprintf("Disk space low: %.1f GB free of %.1f GB", freeGB, totalGB)
		}
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Disk", Status: status, Message: msg, Fixable: false,
		})
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Disk", Status: "warn", Message: "Disk space check failed: " + err.Error(), Fixable: false,
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
				Name: "Runtime", Status: "warn", Message: fmt.Sprintf("Namespace status: %s", status), Fixable: false,
			})
		}
	} else {
		checks = append(checks, api.DiagnosticCheckDto{
			Name: "Runtime", Status: "warn", Message: "No namespace is loaded", Fixable: false,
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

// --- Phase F3: Snapshots ---

func (d *Daemon) snapshotsDir() string {
	return fmt.Sprintf("%s/snapshots", config.ResolveVolumesBase(d.workspaceID, d.activeNsID()))
}

func (d *Daemon) activeNsID() string {
	if d.nsConfig != nil {
		return d.nsConfig.ID
	}
	return "default"
}

func (d *Daemon) handleListSnapshots(w http.ResponseWriter, _ *http.Request) {
	dir := d.snapshotsDir()
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
	if d.runtime != nil && d.runtime.Status() != namespace.NsStatusStopped {
		writeError(w, http.StatusConflict, "namespace must be stopped before export")
		return
	}
	if d.dockerClient == nil {
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}

	dir := d.snapshotsDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	nsName := "namespace"
	if d.nsConfig != nil {
		nsName = sanitizeName(d.nsConfig.Name)
		if nsName == "" {
			nsName = d.nsConfig.ID
		}
	}
	fileName := fmt.Sprintf("%s_%s.zip", nsName, time.Now().Format("2006-01-02_15-04-05"))
	outputPath := fmt.Sprintf("%s/%s", dir, fileName)

	ctx := r.Context()
	meta, err := snapshot.Export(ctx, d.dockerClient, outputPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Exported %d volumes to %s", len(meta.Volumes), fileName),
	})
}

func (d *Daemon) handleImportSnapshot(w http.ResponseWriter, r *http.Request) {
	if d.runtime != nil && d.runtime.Status() != namespace.NsStatusStopped {
		writeError(w, http.StatusConflict, "namespace must be stopped before import")
		return
	}
	if d.dockerClient == nil {
		writeError(w, http.StatusServiceUnavailable, "docker client not available")
		return
	}

	// Accept multipart file upload or a snapshot name query parameter
	snapshotName := r.URL.Query().Get("name")
	var zipPath string

	if snapshotName != "" {
		// Import from existing snapshot file
		if !strings.HasSuffix(snapshotName, ".zip") || !validateID(strings.TrimSuffix(snapshotName, ".zip")) {
			writeError(w, http.StatusBadRequest, "invalid snapshot name")
			return
		}
		zipPath = fmt.Sprintf("%s/%s", d.snapshotsDir(), snapshotName)
		if _, err := os.Stat(zipPath); err != nil {
			writeError(w, http.StatusNotFound, "snapshot not found")
			return
		}
	} else {
		// Accept file upload
		if err := r.ParseMultipartForm(512 << 20); err != nil { // 512MB max
			writeError(w, http.StatusBadRequest, "invalid multipart form")
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field required")
			return
		}
		defer file.Close()

		tmpFile, err := os.CreateTemp("", "citeck-snapshot-upload-*.zip")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer os.Remove(tmpFile.Name())

		if _, err := io.Copy(tmpFile, file); err != nil {
			tmpFile.Close()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		tmpFile.Close()
		zipPath = tmpFile.Name()
	}

	ctx := r.Context()
	meta, err := snapshot.Import(ctx, d.dockerClient, zipPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("Imported %d volumes", len(meta.Volumes)),
	})
}
