package daemon

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// handleMissingRegistryAuth lists auth-required registry hosts (from the active
// workspace's imageRepos) that have no resolvable credential yet — neither an
// explicit host binding nor a scope-matched secret. The Web UI calls this
// before starting a namespace and blocks the start until the user provides
// them, so a missing credential surfaces up front instead of stalling the
// namespace mid-pull.
func (d *Daemon) handleMissingRegistryAuth(w http.ResponseWriter, _ *http.Request) {
	missing := []string{}
	act := d.active()
	wsCfg := act.workspaceConfig
	if wsCfg == nil {
		writeJSON(w, missing)
		return
	}
	reposByHost := wsCfg.ImageReposByHost()
	bindings, _ := d.store.ListRegistryBindings(d.activeWorkspaceID())
	authByHost := buildRegistryAuthCache(reposByHost, d.secretReaderFunc(), bindings)

	// authRequiredHost reports whether a host is a declared auth-required
	// registry that currently has no resolvable credential.
	authRequiredHost := func(host string) bool {
		repo, declared := reposByHost[host]
		if !declared || repo.AuthType == "" {
			return false
		}
		_, hasCred := authByHost[host]
		return !hasCred
	}

	if act.runtime != nil {
		// Namespace is loaded — check only the hosts the apps that will
		// actually start pull from (active bundle minus detached apps), so a
		// declared-but-unused (or detached-only) auth registry never prompts.
		seen := map[string]bool{}
		for _, img := range act.runtime.StartableAppImages() {
			host := imageRegistryHost(img)
			if host == "" || seen[host] {
				continue
			}
			seen[host] = true
			if authRequiredHost(host) {
				missing = append(missing, host)
			}
		}
	} else {
		// No namespace loaded yet (e.g. before a fresh create) — fall back to
		// every declared auth-required registry.
		for host := range reposByHost {
			if authRequiredHost(host) {
				missing = append(missing, host)
			}
		}
	}
	sort.Strings(missing)
	writeJSON(w, missing)
}

// imageRegistryHost extracts the registry host from an image reference: the
// part before the first '/', but only when it looks like a host (contains '.'
// or ':' or is "localhost") — otherwise the image is a Docker Hub library
// reference with no explicit registry.
func imageRegistryHost(image string) string {
	slash := strings.Index(image, "/")
	if slash <= 0 {
		return ""
	}
	first := image[:slash]
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first
	}
	return ""
}

// handleListRegistryBindings returns the active workspace's image-registry
// host → secret-id bindings, so the credential picker can show (and preselect)
// the secret currently bound to a host.
func (d *Daemon) handleListRegistryBindings(w http.ResponseWriter, _ *http.Request) {
	if d.store == nil {
		writeJSON(w, map[string]string{})
		return
	}
	bindings, err := d.store.ListRegistryBindings(d.activeWorkspaceID())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if bindings == nil {
		bindings = map[string]string{}
	}
	writeJSON(w, bindings)
}

// handleSetRegistryBinding binds an image-registry host to a stored
// REGISTRY_AUTH secret for the active workspace (an empty secretId removes the
// binding). The auth caches are rebuilt and pull-failed apps retried so the
// reused credential takes effect immediately — no namespace restart.
func (d *Daemon) handleSetRegistryBinding(w http.ResponseWriter, r *http.Request) {
	if d.store == nil {
		writeError(w, http.StatusServiceUnavailable, "storage not available")
		return
	}
	var req api.RegistryBindingDto
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Host == "" {
		writeError(w, http.StatusBadRequest, "host is required")
		return
	}
	// A non-empty binding must reference an existing secret (an empty id
	// unbinds). validateSecretID rejects ids with path/traversal characters.
	if req.SecretID != "" {
		if !validateSecretID(req.SecretID) {
			writeError(w, http.StatusBadRequest, "invalid secretId")
			return
		}
		if _, err := d.secretReaderFunc().GetSecret(req.SecretID); err != nil {
			if errors.Is(err, storage.ErrSecretsLocked) {
				writeError(w, http.StatusLocked, "secrets are locked")
				return
			}
			writeErrorCode(w, http.StatusNotFound, api.ErrCodeSecretNotFound, "secret not found")
			return
		}
	}

	if err := d.store.SetRegistryBinding(d.activeWorkspaceID(), req.Host, req.SecretID); err != nil {
		writeInternalError(w, err)
		return
	}

	d.rebuildAuthCaches()

	writeJSON(w, api.ActionResultDto{Success: true, Message: "registry binding saved"})
}
