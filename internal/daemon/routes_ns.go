package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/form"
	"github.com/citeck/citeck-launcher/internal/fsutil"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"gopkg.in/yaml.v3"
)

// --- Namespace list ---

//nolint:nestif // listing namespaces requires mode-specific branching with active-namespace status overlay
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
		rt := d.runtime
		d.configMu.RUnlock()
		if nsCfg != nil {
			status := string(namespace.NsStatusStopped)
			if rt != nil {
				status = string(rt.Status())
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
	rt := d.runtime
	d.configMu.RUnlock()
	if activeID == nsID && rt != nil && rt.Status() != namespace.NsStatusStopped {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceRunning, "cannot delete active namespace; stop it first")
		return
	}

	if config.IsDesktopMode() {
		configPath := config.WorkspaceNamespaceConfigPath(d.workspaceID, nsID)
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) { //nolint:gosec // G703: path from config.WorkspaceNamespaceConfigPath, not user input
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

//nolint:gocyclo,nestif // namespace creation orchestrates validation, template resolution, config generation, and async snapshot import
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
			_ = json.NewEncoder(w).Encode(api.ValidationErrorDto{
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
				// Apply template config as base by marshaling to YAML and re-parsing
				if len(tmpl.Config) > 0 {
					if tmplData, err := yaml.Marshal(tmpl.Config); err == nil {
						_ = yaml.Unmarshal(tmplData, &nsCfg)
					}
				}
				// Set template ID for runtime use
				nsCfg.Template = templateID
				break
			}
		}
		// If no bundleRef from template or request, use first bundle repo + LATEST
		if nsCfg.BundleRef.IsEmpty() && req.BundleRepo == "" && len(wsCfg.BundleRepos) > 0 {
			nsCfg.BundleRef = bundle.Ref{Repo: wsCfg.BundleRepos[0].ID, Key: "LATEST"}
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
		nsCfg.BundleRef = bundle.Ref{Repo: req.BundleRepo, Key: req.BundleKey}
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
		if mkdirErr := os.MkdirAll(nsDir, 0o755); mkdirErr != nil { //nolint:gosec // namespace dirs need 0o755 for container access
			writeInternalError(w, mkdirErr)
			return
		}
		configPath = config.WorkspaceNamespaceConfigPath(wsID, nsCfg.ID)
	} else {
		configPath = config.NamespaceConfigPath()
	}

	// Atomic existence check — O_EXCL fails if file already exists (no TOCTOU race)
	excl, err := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // config files need 0o644 for readability
	if err != nil {
		if os.IsExist(err) {
			writeErrorCode(w, http.StatusConflict, api.ErrCodeNamespaceExists,
				fmt.Sprintf("namespace %q already exists", nsCfg.ID))
			return
		}
		writeInternalError(w, err)
		return
	}
	_ = excl.Close()

	if err := fsutil.AtomicWriteFile(configPath, data, 0o644); err != nil {
		_ = os.Remove(configPath) // remove empty placeholder from O_EXCL open
		writeInternalError(w, err)
		return
	}

	// Always encrypt secrets with the default password on namespace creation
	if !d.secretService.IsEncrypted() {
		if encErr := d.secretService.SetMasterPassword("citeck", true); encErr != nil {
			slog.Error("Failed to set up secrets encryption during namespace creation", "err", encErr)
		} else {
			slog.Info("Secrets encrypted with default password during namespace creation")
		}
	}

	// Trigger background snapshot download + import if specified
	if req.Snapshot != "" {
		wsID := req.WorkspaceID
		if wsID == "" {
			wsID = d.workspaceID
		}
		d.bgWg.Go(func() {
			d.downloadAndImportSnapshot(req.Snapshot, wsID, nsCfg.ID)
		})
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
			bundlesDir := d.resolveBundleDir(repo)
			versions := bundle.ListBundleVersions(bundlesDir)
			result = append(result, api.BundleInfoDto{Repo: repo.ID, Versions: versions})
		}
	}
	if result == nil {
		result = []api.BundleInfoDto{}
	}
	writeJSON(w, result)
}

// resolveBundleDir returns the on-disk directory for a bundle repo.
// Checks local workspace repo first (offline import at data/repo/), then cloned bundles dir.
func (d *Daemon) resolveBundleDir(repo bundle.BundlesRepo) string {
	if repo.Path != "" {
		localRepo := filepath.Join(config.DataDir(), "repo", repo.Path)
		if _, err := os.Stat(localRepo); err == nil {
			return localRepo
		}
	}
	bundlesDir := config.ResolveBundlesDir(d.workspaceID, repo.ID)
	if repo.Path != "" {
		bundlesDir = filepath.Join(bundlesDir, repo.Path)
	}
	return bundlesDir
}
