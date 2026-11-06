package daemon

import (
	"errors"
	"net/http"
	"strings"

	"fmt"
	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/i18n"
	"github.com/citeck/citeck-launcher/internal/msg"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

type snapshotBundleError struct{ refusal msg.Message }

func (e *snapshotBundleError) Error() string {
	return i18n.NewTranslator("en").Render(e.refusal)
}

func (d *Daemon) writeSnapshotBundleRefusal(w http.ResponseWriter, r *http.Request, err error) bool {
	var refusal *snapshotBundleError
	if !errors.As(err, &refusal) {
		return false
	}
	writeErrorCode(w, http.StatusConflict, api.ErrCodeSnapshotBundleIncompatible, d.translatorFor(r).Render(refusal.refusal))
	return true
}

func snapshotBundleVersion(act activeNamespace) string {
	if act.bundleDef != nil && act.bundleDef.Key.Version != "" {
		return act.bundleDef.Key.Version
	}
	if act.nsConfig != nil {
		return act.nsConfig.BundleRef.Key
	}
	return ""
}

// Workspace snapshots downloaded through the UI retain their configured ID as
// the local ZIP name. Recheck the requirement on restore, not only on download.
func snapshotNameRefusal(act activeNamespace, name string) msg.Message {
	def := bundle.FindSnapshot(act.workspaceConfig, strings.TrimSuffix(name, ".zip"))
	if def == nil {
		return msg.Message{}
	}
	return def.BundleVersionRefusal(snapshotBundleVersion(act))
}

func snapshotDownloadRefusal(act activeNamespace, req api.SnapshotDownloadDto) msg.Message {
	if act.workspaceConfig != nil {
		for _, def := range act.workspaceConfig.Snapshots {
			if def.URL == req.URL || def.ID == strings.TrimSuffix(req.Name, ".zip") {
				if refusal := def.BundleVersionRefusal(snapshotBundleVersion(act)); !refusal.Empty() {
					return refusal
				}
			}
		}
	}
	return msg.Message{}
}

// Read the target workspace's definitions even when creating in a workspace
// other than the active one. Other-workspace reads stay offline; the active
// workspace retains its existing cached-config recovery behavior.
func (d *Daemon) snapshotWorkspaceConfig(wsID string) (wsCfg *bundle.WorkspaceConfig, syncErr string) {
	if wsID == d.activeWorkspaceID() {
		return d.activeWorkspaceConfigForRead()
	}
	resolver := authenticatedBundleResolver(config.BundlesDataDir(wsID), d.secretReaderFunc()).
		WithWorkspaceRepo(lookupWorkspaceRepoOpts(d.store, d.secretService, wsID)).
		WithWorkspaceOverlay(workspaceConfigOverlay(d.store, wsID))
	resolver.SetOffline(true)
	ws := resolver.ResolveWorkspaceOnly()
	if err := resolver.WorkspaceSyncError(); err != nil {
		return nil, err.Error()
	}
	return ws, ""
}

// checkCreateBundleChoices validates bundle-dependent creation choices after
// template merge and resolution, before persistence or snapshot downloads.
func checkCreateBundleChoices(cfg *namespace.Config, resolved *bundle.ResolveResult) error {
	if err := checkCreateExperimentalFeatures(cfg, resolved.Bundle); err != nil {
		return err
	}
	if cfg.Snapshot == "" {
		return nil
	}
	def := bundle.FindSnapshot(resolved.Workspace, cfg.Snapshot)
	if def == nil {
		return &createNamespaceError{status: http.StatusBadRequest, code: api.ErrCodeInvalidConfig,
			message: fmt.Sprintf("snapshot %q not found in workspace config", cfg.Snapshot)}
	}
	if refusal := def.BundleVersionRefusal(resolved.Bundle.Key.Version); !refusal.Empty() {
		return &snapshotBundleError{refusal: refusal}
	}
	return nil
}
