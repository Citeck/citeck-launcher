package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

type cachedRepoChoice struct {
	workspaceID, nsID, URL string
	URLs                   []string
}

func (d *Daemon) offlineGitContext(wsID string) (*storage.WorkspaceDto, *bundle.Resolver, *bundle.WorkspaceConfig, error) {
	if d.store == nil {
		return nil, nil, nil, fmt.Errorf("storage not available")
	}
	if wsID == "" {
		wsID = defaultWorkspaceID
	}
	ws, err := d.getWorkspaceWithDefault(wsID)
	if err != nil {
		return nil, nil, nil, err
	}
	if ws == nil {
		return nil, nil, nil, fmt.Errorf("workspace not found")
	}
	resolver := authenticatedBundleResolver(config.BundlesDataDir(ws.ID), d.secretReaderFunc()).
		WithWorkspaceRepo(buildWorkspaceRepoOpts(*ws, d.secretService)).
		WithWorkspaceOverlay(workspaceConfigOverlay(d.store, ws.ID))
	resolver.SetOffline(true)
	cfg := resolver.ResolveWorkspaceOnly()
	return ws, resolver, cfg, nil
}
func workspaceGitURL(ws *storage.WorkspaceDto) string {
	if ws.RepoURL != "" {
		return bundle.SafeGitURL(ws.RepoURL)
	}
	return bundle.DefaultBundlesRepo
}
func gitBundleForURL(cfg *bundle.WorkspaceConfig, repoURL string) *bundle.BundlesRepo {
	if cfg == nil {
		return nil
	}
	for i := range cfg.BundleRepos {
		repo := &cfg.BundleRepos[i]
		if repo.URL != "" && bundle.SafeGitURL(repo.URL) == repoURL {
			return repo
		}
	}
	return nil
}
func (d *Daemon) gitFailureDetails(wsID string, err error, force bool, cacheChoices ...[]string) *api.GitFailureDto {
	var failure *bundle.RepoSyncError
	if !errors.As(err, &failure) {
		return nil
	}
	details := &api.GitFailureDto{RepoURL: bundle.SafeGitURL(failure.URL), WorkspaceID: wsID, CacheAvailable: failure.CacheAvailable, CacheUpdatedAt: failure.LastSyncAt, Force: force, CachedRepos: nil}
	if len(cacheChoices) > 0 {
		details.CachedRepos = append([]string(nil), cacheChoices[0]...)
	}
	if failure.SecretID != nil {
		details.SecretID = *failure.SecretID
		return details
	}
	ws, _, cfg, lookupErr := d.offlineGitContext(wsID)
	if lookupErr != nil {
		return details
	}
	if workspaceGitURL(ws) == details.RepoURL {
		details.SecretID = ws.SecretID
		if details.SecretID == "" && ws.AuthType == "TOKEN" {
			details.SecretID = workspaceRepoSecretKey(ws.ID)
		}
	} else if repo := gitBundleForURL(cfg, details.RepoURL); repo != nil {
		token, id := makeGitCredentialLookup(d.secretReaderFunc())
		if strings.HasPrefix(repo.AuthType, "secret:") || (repo.AuthType != "" && token(repo.AuthType) != "") {
			details.SecretID = id(repo.AuthType)
		}
		if details.SecretID == "" {
			details.SecretID = id("GIT_TOKEN")
		}
	}
	return details
}
func (d *Daemon) writeGitError(w http.ResponseWriter, status int, code, wsID string, err error, cacheChoices ...[]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(api.ErrorDto{Error: http.StatusText(status), Code: code, Message: err.Error(), Git: d.gitFailureDetails(wsID, err, false, cacheChoices...)})
}

// cachedRepoRequest validates the one-request opt-in against configured repos
// and real local data; never trust a browser's cache-availability claim.
func (d *Daemon) cachedRepoRequest(r *http.Request, wsID string) ([]string, error) {
	requested := r.URL.Query()["cachedRepo"]
	if len(requested) == 0 {
		return nil, nil
	}
	if len(requested) > 32 {
		return nil, fmt.Errorf("too many cached repositories")
	}
	ws, resolver, cfg, err := d.offlineGitContext(wsID)
	if err != nil {
		return nil, err
	}
	var targets []string
	seen := map[string]bool{}
	for _, raw := range requested {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("invalid cached repository")
		}
		target := bundle.SafeGitURL(raw)
		if target == "" {
			return nil, fmt.Errorf("cached repository is required")
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		if cacheErr := validateCachedGitRepository(ws, resolver, cfg, target); cacheErr != nil {
			return nil, cacheErr
		}
		targets = append(targets, target)
	}
	for _, target := range targets {
		slog.Info("Using cached Git repository for one operation", "workspace", wsID, "repo", target) //nolint:gosec // G706: configured repository URL is parsed/sanitized above; no credential or control characters.
	}
	return targets, nil
}

// handleLinkGitSecret persists an explicit reference, not a credential value.
// Bundle links use the existing workspace YAML delta's authType key.
func (d *Daemon) handleLinkGitSecret(w http.ResponseWriter, r *http.Request) {
	release, ok := d.tryLongOp(w, r, tolerateNothing)
	if !ok {
		return
	}
	defer release()
	if !d.requireDesktop(w) {
		return
	}
	if !d.reloadMu.TryLock() {
		writeError(w, http.StatusConflict, "reload already in progress")
		return
	}
	defer d.reloadMu.Unlock()
	wsID := r.PathValue("id")
	if !validateID(wsID) {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	var req struct {
		RepoURL  string `json:"repoUrl"`
		SecretID string `json:"secretId"`
	}
	if err := readJSON(r, &req); err != nil || !validateSecretID(req.SecretID) {
		writeError(w, http.StatusBadRequest, "invalid Git secret link")
		return
	}
	secret, err := d.secretReaderFunc().GetSecret(req.SecretID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Git secret is unavailable or locked")
		return
	}
	if secret.Type != storage.SecretGitToken {
		writeError(w, http.StatusBadRequest, "a Git token secret is required")
		return
	}
	ws, resolver, cfg, err := d.offlineGitContext(wsID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target := bundle.SafeGitURL(req.RepoURL)
	if target == "" {
		writeError(w, http.StatusBadRequest, "repository URL is required")
		return
	}
	if linkErr := d.persistGitSecretLink(ws, resolver, cfg, target, req.SecretID); linkErr != nil {
		if errors.Is(linkErr, errInvalidDelta) {
			writeError(w, http.StatusBadRequest, linkErr.Error())
		} else {
			writeInternalError(w, linkErr)
		}
		return
	}

	slog.Info("Git repository secret linked", "workspace", wsID, "repo", target, "secret", req.SecretID) //nolint:gosec // G706: workspace/secret ids validated and repository URL sanitized; references only.
	writeJSON(w, api.ActionResultDto{Success: true, Message: "Git repository secret linked"})
}

func gitCredentialReadState(err error) string {
	if errors.Is(err, storage.ErrSecretsLocked) {
		return "locked"
	}
	if err == nil {
		return "missing"
	}
	return "unavailable"
}

func explicitGitRefreshError(resolver *bundle.Resolver, required bool) error {
	if !required {
		return nil
	}
	if err := resolver.GitSyncError(); err != nil {
		return fmt.Errorf("git update failed: %w", err)
	}
	return nil
}

func (d *Daemon) persistBundleGitSecret(wsID, target, secretID string, resolver *bundle.Resolver) error {
	// The baseline resolver is offline; secret recovery must not require the
	// very credentials it is trying to repair.
	baseline, _ := resolver.ResolveWorkspaceRaw()
	content := baseline
	var err error
	if overlay := workspaceConfigOverlay(d.store, wsID); overlay != nil {
		content, err = overlay(baseline)
		if err != nil {
			return fmt.Errorf("prepare Git secret binding: %w", err)
		}
	}
	var document map[string]any
	if parseErr := yaml.Unmarshal(content, &document); parseErr != nil {
		return fmt.Errorf("%w: cached workspace is invalid", errInvalidDelta)
	}
	repos, _ := document["bundleRepos"].([]any)
	changed := false
	for _, item := range repos {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		repoURL, _ := entry["url"].(string)
		if repoURL != "" && bundle.SafeGitURL(repoURL) == target {
			entry["authType"] = "secret:" + secretID
			changed = true
		}
	}
	if !changed {
		return fmt.Errorf("%w: cached repository definition is unavailable", errInvalidDelta)
	}
	updated, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("prepare Git secret binding: %w", err)
	}
	if deltaErr := d.persistWorkspaceDelta(wsID, baseline, updated); deltaErr != nil {
		return fmt.Errorf("prepare Git secret binding: %w", deltaErr)
	}
	return nil
}

func (d *Daemon) persistGitSecretLink(ws *storage.WorkspaceDto, resolver *bundle.Resolver, cfg *bundle.WorkspaceConfig, target, secretID string) error {
	if workspaceGitURL(ws) == target {
		ws.SecretID = secretID
		ws.AuthType = "TOKEN"
		if saveErr := d.store.SaveWorkspace(*ws); saveErr != nil {
			return fmt.Errorf("save workspace Git secret link: %w", saveErr)
		}
		return nil
	}
	if gitBundleForURL(cfg, target) == nil {
		return fmt.Errorf("%w: repository is not configured in this workspace", errInvalidDelta)
	}
	return d.persistBundleGitSecret(ws.ID, target, secretID, resolver)
}

func validateCachedGitRepository(ws *storage.WorkspaceDto, resolver *bundle.Resolver, cfg *bundle.WorkspaceConfig, target string) error {
	if workspaceGitURL(ws) == target {
		data, dir := resolver.ResolveWorkspaceRaw()
		if len(data) == 0 {
			return fmt.Errorf("cached workspace is unavailable")
		}
		if !bundle.CachedRepoMatches(dir, target) {
			return fmt.Errorf("cached workspace belongs to another repository or has an unreadable origin")
		}
		return nil
	}
	repo := gitBundleForURL(cfg, target)
	if repo == nil {
		return fmt.Errorf("repository is not configured in this workspace")
	}
	dir := bundle.ResolveBundleRepoDir(config.BundlesDataDir(ws.ID), "", *repo)
	cloneRoot := filepath.Join(config.BundlesDataDir(ws.ID), "bundles", repo.ID)
	if !bundle.CachedRepoMatches(cloneRoot, target) {
		return fmt.Errorf("cached bundle clone belongs to another repository or has an unreadable origin")
	}
	if len(bundle.ListBundleVersions(dir)) == 0 {
		return fmt.Errorf("cached bundle repository is unavailable")
	}
	return nil
}
