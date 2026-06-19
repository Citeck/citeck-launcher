package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

// workspace-config editing — let the operator hand-edit workspace-v1.yml from
// the Welcome screen. The user's changes are stored as a structural delta (the
// same filemerge engine the per-app config editor uses) under a key-value state
// key and re-applied onto the git reference at every resolve via the resolver's
// workspace overlay seam. Mirrors the GET/PUT/reset shape of the app-config
// routes (see routes_apps.go).

// workspaceConfigFile is the logical filename handed to the filemerge engine so
// it picks the structural-YAML delta path (extension-driven).
const workspaceConfigFile = "workspace-v1.yml"

// wsConfigDeltaKey returns the key-value state key holding the manual
// workspace-config delta (a namespace.FileEdit JSON blob) for a workspace.
func wsConfigDeltaKey(wsID string) string {
	return "ws-config-delta:" + wsID
}

// workspaceConfigOverlay builds the resolver overlay closure for wsID: it loads
// the stored manual delta and re-applies it onto the raw git workspace-v1.yml.
// Returns nil when there's nothing it could overlay (no store / no wsID) so the
// resolver keeps its zero-overhead path. A missing or corrupt delta yields the
// git reference verbatim; only a genuine apply failure surfaces an error (which
// the resolver logs and falls back from).
func workspaceConfigOverlay(store storage.Store, wsID string) func(raw []byte) ([]byte, error) {
	if store == nil || wsID == "" {
		return nil
	}
	return func(raw []byte) ([]byte, error) {
		blob, err := store.GetStateValue(wsConfigDeltaKey(wsID))
		if err != nil || blob == "" {
			return raw, nil //nolint:nilerr // unreadable/absent delta → git reference verbatim
		}
		var edit namespace.FileEdit
		if jsonErr := json.Unmarshal([]byte(blob), &edit); jsonErr != nil {
			return raw, nil // corrupt delta → ignore, use git reference
		}
		merged, err := namespace.ApplyFileEdit(workspaceConfigFile, edit, raw, raw)
		if err != nil {
			return nil, fmt.Errorf("apply workspace config delta: %w", err)
		}
		return merged, nil
	}
}

// resolveWorkspaceBaselineRaw resolves the PRISTINE git workspace-v1.yml bytes
// for a workspace (NO overlay) — the baseline the user's delta is computed
// against and the editor diffs against. Returns nil when nothing resolved.
func (d *Daemon) resolveWorkspaceBaselineRaw(ws storage.WorkspaceDto) []byte {
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(ws.ID), makeTokenLookup(d.secretReaderFunc())).
		WithWorkspaceRepo(buildWorkspaceRepoOpts(ws, d.secretService))
	if !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	raw, _ := resolver.ResolveWorkspaceRaw()
	return raw
}

// refreshActiveWorkspaceConfigCache re-resolves the active workspace config WITH
// the overlay applied and updates the cached a.workspaceConfig so Welcome
// surfaces that read it (quick starts, etc.) reflect a just-saved delta. Unlike
// reresolveActiveWorkspace it does not force a git pull and works for the
// default workspace (which has no RepoURL) — the delta lives locally, so a plain
// re-resolve off the on-disk clone picks it up. Best-effort: a resolve miss
// leaves the cache as-is.
func (d *Daemon) refreshActiveWorkspaceConfigCache() {
	wsID := d.activeWorkspaceID()
	if wsID == "" || d.store == nil {
		return
	}
	ws, err := d.getWorkspaceWithDefault(wsID)
	if err != nil || ws == nil {
		return
	}
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(wsID), makeTokenLookup(d.secretReaderFunc())).
		WithWorkspaceRepo(buildWorkspaceRepoOpts(*ws, d.secretService)).
		WithWorkspaceOverlay(workspaceConfigOverlay(d.store, wsID))
	if !config.IsDesktopMode() {
		resolver.SetOffline(true)
	}
	wsCfg := resolver.ResolveWorkspaceOnly()
	d.configMu.Lock()
	if a := d.activeLocked(); a.workspaceID == wsID {
		a.workspaceConfig = wsCfg
	}
	d.configMu.Unlock()
}

// applyStoredDelta returns content = baseline + the stored delta for wsID. When
// no delta is stored (or it fails to apply) content == baseline.
func (d *Daemon) applyStoredDelta(wsID string, baseline []byte) []byte {
	if d.store == nil || baseline == nil {
		return baseline
	}
	blob, err := d.store.GetStateValue(wsConfigDeltaKey(wsID))
	if err != nil || blob == "" {
		return baseline
	}
	var edit namespace.FileEdit
	if jsonErr := json.Unmarshal([]byte(blob), &edit); jsonErr != nil {
		return baseline
	}
	merged, err := namespace.ApplyFileEdit(workspaceConfigFile, edit, baseline, baseline)
	if err != nil {
		return baseline
	}
	return merged
}

// handleGetWorkspaceConfig returns { content, baseline } for the workspace's
// raw YAML: baseline is the pristine git reference, content is baseline with the
// stored manual delta applied (what the user edits).
func (d *Daemon) handleGetWorkspaceConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws, err := d.getWorkspaceWithDefault(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	baseline := d.resolveWorkspaceBaselineRaw(*ws)
	content := d.applyStoredDelta(id, baseline)
	writeJSON(w, api.WorkspaceConfigDto{Content: string(content), Baseline: string(baseline)})
}

// handlePutWorkspaceConfig validates the submitted YAML, stores it as a delta
// over the git baseline (clearing the delta when it equals the baseline), then
// refreshes the cached active workspace config so Welcome updates immediately.
func (d *Daemon) handlePutWorkspaceConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws, err := d.getWorkspaceWithDefault(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	content, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	// Validate the body parses as YAML before storing (app-editor parity).
	var probe any
	if yamlErr := yaml.Unmarshal(content, &probe); yamlErr != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid YAML: %s", yamlErr.Error()))
		return
	}

	baseline := d.resolveWorkspaceBaselineRaw(*ws)
	if err := d.persistWorkspaceDelta(id, baseline, content); err != nil {
		if errors.Is(err, errInvalidDelta) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeInternalError(w, err)
		return
	}

	if id == d.activeWorkspaceID() {
		d.refreshActiveWorkspaceConfigCache()
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Workspace config updated"})
}

// errInvalidDelta marks a delta that could not be computed from the submitted
// content (a client error → 400), distinct from a storage failure (500).
var errInvalidDelta = errors.New("invalid workspace config")

// persistWorkspaceDelta stores the structural delta of content over the git
// baseline for wsID, or clears the stored delta when content equals the
// baseline (no change vs the git reference).
func (d *Daemon) persistWorkspaceDelta(wsID string, baseline, content []byte) error {
	if bytes.Equal(content, baseline) {
		if err := d.store.SetStateValue(wsConfigDeltaKey(wsID), ""); err != nil {
			return fmt.Errorf("clear workspace delta: %w", err)
		}
		return nil
	}
	edit, err := namespace.MakeFileEdit(workspaceConfigFile, baseline, content)
	if err != nil {
		return fmt.Errorf("%w: compute delta: %s", errInvalidDelta, err.Error())
	}
	blob, err := json.Marshal(edit)
	if err != nil {
		return fmt.Errorf("marshal workspace delta: %w", err)
	}
	if err := d.store.SetStateValue(wsConfigDeltaKey(wsID), string(blob)); err != nil {
		return fmt.Errorf("store workspace delta: %w", err)
	}
	return nil
}

// handleResetWorkspaceConfig clears the manual delta so the workspace reverts to
// the pristine git reference, then refreshes the cached active config.
func (d *Daemon) handleResetWorkspaceConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ws, err := d.getWorkspaceWithDefault(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if setErr := d.store.SetStateValue(wsConfigDeltaKey(id), ""); setErr != nil {
		writeInternalError(w, setErr)
		return
	}
	if id == d.activeWorkspaceID() {
		d.refreshActiveWorkspaceConfigCache()
	}
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Workspace config reset to git reference"})
}
