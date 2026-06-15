package daemon

import (
	"errors"
	"net/http"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/storage"
)

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
