package daemon

import (
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"gopkg.in/yaml.v3"
)

// namespaceConfigFromCreateTemplate applies the target workspace's template
// before request overlays and bundle-dependent validation.
//
//nolint:nestif // template selection mirrors the Kotlin create flow's branching
func (d *Daemon) namespaceConfigFromCreateTemplate(req api.NamespaceCreateDto, wsID string) (namespace.Config, error) {
	// Generate namespace config — start from template if specified
	nsCfg := namespace.DefaultNamespaceConfig()

	templateID := req.Template
	if templateID == "" {
		templateID = "default" // use default template if none specified
	}
	wsCfg := d.active().workspaceConfig
	if wsID != d.activeWorkspaceID() {
		var syncErr string
		wsCfg, syncErr = d.snapshotWorkspaceConfig(wsID)
		if syncErr != "" {
			return nsCfg, &createNamespaceError{status: http.StatusBadGateway, code: api.ErrCodeWsRepoSyncFailed, message: syncErr}
		}
	}
	if templateID == "default" {
		// Shared helper — keeps this path in lockstep with
		// handleNamespaceCreateDefaults so the form preview matches what
		// the server actually persists.
		applyDefaultTemplate(&nsCfg, wsCfg)
		nsCfg.Template = templateID
	} else if wsCfg != nil {
		// Non-default templates (QuickStart variants) — apply explicit template.
		for _, tmpl := range wsCfg.NamespaceTemplates {
			if tmpl.ID == templateID {
				if len(tmpl.Config) > 0 {
					if tmplData, err := yaml.Marshal(tmpl.Config); err == nil {
						_ = yaml.Unmarshal(tmplData, &nsCfg)
					}
				}
				nsCfg.Template = templateID
				break
			}
		}
		if nsCfg.BundleRef.IsEmpty() && req.BundleRepo == "" && len(wsCfg.BundleRepos) > 0 {
			nsCfg.BundleRef = bundle.Ref{Repo: wsCfg.BundleRepos[0].ID, Key: "LATEST"}
		}
	}

	return nsCfg, nil
}
