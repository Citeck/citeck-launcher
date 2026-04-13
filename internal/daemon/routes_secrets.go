package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/git"
	"github.com/citeck/citeck-launcher/internal/h2migrate"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// --- Secrets ---

func (d *Daemon) handleListSecrets(w http.ResponseWriter, _ *http.Request) {
	if d.store == nil {
		writeJSON(w, []api.SecretMetaDto{})
		return
	}

	secrets, err := d.secretReaderFunc().ListSecrets()
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

	if err := d.secretWriterFunc().SaveSecret(secret); err != nil {
		if errors.Is(err, storage.ErrSecretsLocked) {
			writeError(w, http.StatusLocked, "secrets are locked")
			return
		}
		writeInternalError(w, err)
		return
	}

	d.rebuildAuthCaches()

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

	d.rebuildAuthCaches()

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

	secret, err := d.secretReaderFunc().GetSecret(id)
	if err != nil {
		if errors.Is(err, storage.ErrSecretsLocked) {
			writeError(w, http.StatusLocked, "secrets are locked")
			return
		}
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

// --- Migration (master password for Kotlin secrets) ---

// handleGetMigrationStatus returns whether encrypted secrets from Kotlin need to be unlocked,
// and the current encryption/lock state.
func (d *Daemon) handleGetMigrationStatus(w http.ResponseWriter, _ *http.Request) {
	if d.store == nil {
		writeJSON(w, map[string]any{"hasPendingSecrets": false, "encrypted": false, "locked": false})
		return
	}

	blob, err := d.store.GetSecretBlob()
	hasBlob := err == nil && blob != ""

	encrypted := false
	locked := false
	if d.secretService != nil {
		encrypted = d.secretService.IsEncrypted()
		locked = d.secretService.IsLocked()
	}

	hasSecrets := false
	if secrets, listErr := d.secretReaderFunc().ListSecrets(); listErr == nil {
		hasSecrets = len(secrets) > 0
	}

	writeJSON(w, map[string]any{
		"hasPendingSecrets": hasBlob,
		"encrypted":         encrypted,
		"locked":            locked,
		"hasSecrets":        hasSecrets,
	})
}

// handleSubmitMasterPassword decrypts the Kotlin secrets blob and imports individual secrets.
func (d *Daemon) handleSubmitMasterPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		slog.Warn("Master password request failed to parse", //nolint:gosec // G706: logged values are HTTP metadata, not secrets
			"err", err, "contentType", r.Header.Get("Content-Type"),
			"contentLength", r.ContentLength, "hasBody", r.Body != nil)
		writeError(w, http.StatusBadRequest, "password required")
		return
	}

	blob, err := d.store.GetSecretBlob()
	if err != nil || blob == "" {
		writeError(w, http.StatusNotFound, "no pending secrets to decrypt")
		return
	}

	decrypted, err := h2migrate.DecryptSecretBlob(blob, req.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	count, err := h2migrate.ImportDecryptedSecrets(decrypted, d.store)
	if err != nil {
		writeInternalError(w, err)
		return
	}

	// Clear the encrypted blob — secrets are now stored individually
	if err := d.store.PutSecretBlob(""); err != nil {
		slog.Error("Failed to clear secret blob after import", "err", err)
	}

	// Encrypt imported secrets — secrets must never stay plaintext on disk.
	// Server mode: default password. Desktop mode: use the Kotlin master password.
	if !d.secretService.IsEncrypted() {
		encPassword := "citeck"
		encIsDefault := true
		if config.IsDesktopMode() {
			encPassword = req.Password
			encIsDefault = false
		}
		if encErr := d.secretService.SetMasterPassword(encPassword, encIsDefault); encErr != nil {
			slog.Error("Failed to encrypt secrets after import", "err", encErr)
		} else {
			slog.Info("Secrets encrypted after import", "isDefault", encIsDefault)
		}
	}

	d.rebuildAuthCaches()

	slog.Info("Master password accepted, secrets imported and encrypted", "count", count)
	writeJSON(w, api.ActionResultDto{
		Success: true,
		Message: fmt.Sprintf("%d secrets imported and encrypted", count),
	})
}

// --- Secrets Encryption ---

// handleGetSecretsStatus returns the encryption and lock state of secrets.
func (d *Daemon) handleGetSecretsStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"encrypted": d.secretService.IsEncrypted(),
		"locked":    d.secretService.IsLocked(),
	})
}

// handleUnlockSecrets derives the key from the password and unlocks encrypted secrets.
func (d *Daemon) handleUnlockSecrets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil || req.Password == "" {
		writeError(w, http.StatusBadRequest, "password required")
		return
	}

	if err := d.secretService.Unlock(req.Password); err != nil {
		if errors.Is(err, storage.ErrCorruptedKeystore) {
			writeInternalError(w, err)
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	d.rebuildAuthCaches()

	slog.Info("Secrets unlocked successfully")
	writeJSON(w, api.ActionResultDto{Success: true, Message: "secrets unlocked"})
}

// handleSetupPassword sets up encryption for the first time.
// Server mode: always uses the default password "citeck" (auto-unlock on startup).
// Desktop mode: accepts user-provided password from the request body.
func (d *Daemon) handleSetupPassword(w http.ResponseWriter, r *http.Request) {
	password := "citeck"
	isDefault := true

	if config.IsDesktopMode() {
		var req struct {
			Password string `json:"password"`
		}
		if err := readJSON(r, &req); err != nil || req.Password == "" {
			writeError(w, http.StatusBadRequest, "password required")
			return
		}
		password = req.Password
		isDefault = false
	}

	if err := d.secretService.SetMasterPassword(password, isDefault); err != nil {
		if errors.Is(err, storage.ErrAlreadyEncrypted) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeInternalError(w, err)
		return
	}

	d.rebuildAuthCaches()

	slog.Info("Secrets encryption configured", "isDefault", isDefault)
	writeJSON(w, api.ActionResultDto{Success: true, Message: "encryption configured"})
}
