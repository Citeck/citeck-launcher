package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/storage"
)

func testDaemon(t *testing.T, store storage.Store) *Daemon {
	t.Helper()
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	return &Daemon{store: store, secretService: svc}
}

// secretsTestMux stands up a daemon with an unlocked (master-password-set)
// SecretService over SQLite and mounts the routes, so the {id}-templated
// secrets endpoints are exercised through real mux path matching.
func secretsTestMux(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	d := testDaemon(t, store)
	require.NoError(t, d.secretService.SetMasterPassword("test-master", false))
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux
}

func putSecret(t *testing.T, mux *http.ServeMux, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/v1/secrets/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestHandleUpdateSecret_PartialKeepsValue: the write-only contract — an
// empty/absent value keeps the stored one while name/scope/username update.
func TestHandleUpdateSecret_PartialKeepsValue(t *testing.T) {
	d, mux := secretsTestMux(t)
	require.NoError(t, d.secretService.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "git-token-1", Name: "Old name", Type: storage.SecretGitToken, Scope: "global"},
		Value:      "glpat-original",
	}))

	rec := putSecret(t, mux, "git-token-1", `{"name":"Renamed","scope":"team-a"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var meta api.SecretMetaDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &meta))
	assert.Equal(t, "git-token-1", meta.ID)
	assert.Equal(t, "Renamed", meta.Name)
	assert.Equal(t, "team-a", meta.Scope)
	assert.Equal(t, string(storage.SecretGitToken), meta.Type, "type is immutable through the edit endpoint")

	got, err := d.secretService.GetSecret("git-token-1")
	require.NoError(t, err)
	assert.Equal(t, "glpat-original", got.Value, "empty value in PUT must keep the stored value (write-only edit)")
	assert.Equal(t, "Renamed", got.Name)
}

// TestHandleUpdateSecret_UpdatesValueWhenProvided: a non-empty value replaces
// the stored one.
func TestHandleUpdateSecret_UpdatesValueWhenProvided(t *testing.T) {
	d, mux := secretsTestMux(t)
	require.NoError(t, d.secretService.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "git-token-2", Name: "Token", Type: storage.SecretGitToken},
		Value:      "glpat-old",
	}))

	rec := putSecret(t, mux, "git-token-2", `{"value":"glpat-new"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	got, err := d.secretService.GetSecret("git-token-2")
	require.NoError(t, err)
	assert.Equal(t, "glpat-new", got.Value)
	assert.Equal(t, "Token", got.Name, "fields absent from the PUT stay unchanged")
}

// TestHandleUpdateSecret_NotFound: editing a missing secret → 404 with the
// machine-readable SECRET_NOT_FOUND code.
func TestHandleUpdateSecret_NotFound(t *testing.T) {
	_, mux := secretsTestMux(t)

	rec := putSecret(t, mux, "no-such-secret", `{"name":"X"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)

	var errResp api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
	assert.Equal(t, api.ErrCodeSecretNotFound, errResp.Code)
}

// TestHandleUpdateSecret_NeverReturnsValue: the response body must not leak
// the secret value — neither the old one nor a freshly submitted one.
func TestHandleUpdateSecret_NeverReturnsValue(t *testing.T) {
	d, mux := secretsTestMux(t)
	require.NoError(t, d.secretService.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "git-token-3", Name: "Token", Type: storage.SecretGitToken},
		Value:      "glpat-old-value",
	}))

	rec := putSecret(t, mux, "git-token-3", `{"value":"glpat-new-value"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "glpat-old-value")
	assert.NotContains(t, rec.Body.String(), "glpat-new-value")

	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	_, hasValue := raw["value"]
	assert.False(t, hasValue, "SecretMetaDto must not carry a value field")
}

func TestHandleGetMigrationStatus_HasPendingSecrets(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	// Store a secret blob to simulate pending migration
	require.NoError(t, store.PutSecretBlob("some-encrypted-blob-data"))

	d := testDaemon(t, store)

	req := httptest.NewRequest("GET", "/api/v1/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleGetMigrationStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	assert.Equal(t, true, result["hasPendingSecrets"])
}

func TestHandleGetMigrationStatus_NoPendingSecrets(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	// No secret blob stored
	d := testDaemon(t, store)

	req := httptest.NewRequest("GET", "/api/v1/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleGetMigrationStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	assert.Equal(t, false, result["hasPendingSecrets"])
}

func TestHandleGetMigrationStatus_EmptyBlob(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	// Store empty blob (cleared after successful import)
	require.NoError(t, store.PutSecretBlob(""))

	d := testDaemon(t, store)

	req := httptest.NewRequest("GET", "/api/v1/migration/status", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleGetMigrationStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&result))
	assert.Equal(t, false, result["hasPendingSecrets"],
		"empty blob should be treated as no pending secrets")
}

func TestHandleSubmitMasterPassword_EmptyPassword(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	d := testDaemon(t, store)

	body := strings.NewReader(`{"password":""}`)
	req := httptest.NewRequest("POST", "/api/v1/migration/master-password", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.handleSubmitMasterPassword(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var errResp api.ErrorDto
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp.Message, "password required")
}

func TestHandleSubmitMasterPassword_NoBlob(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	d := testDaemon(t, store)

	body := strings.NewReader(`{"password":"some-password"}`)
	req := httptest.NewRequest("POST", "/api/v1/migration/master-password", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.handleSubmitMasterPassword(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	var errResp api.ErrorDto
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp.Message, "no pending secrets")
}

func TestHandleSubmitMasterPassword_WrongPassword(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	// Store a blob that is not decryptable with the given password.
	// This is an arbitrary base64 string — DecryptSecretBlob will fail to decrypt it.
	require.NoError(t, store.PutSecretBlob("dGhpcyBpcyBub3QgYSB2YWxpZCBlbmNyeXB0ZWQgYmxvYg=="))

	d := testDaemon(t, store)

	body := strings.NewReader(`{"password":"wrong-password"}`)
	req := httptest.NewRequest("POST", "/api/v1/migration/master-password", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	d.handleSubmitMasterPassword(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	var errResp api.ErrorDto
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&errResp))
	assert.Contains(t, errResp.Message, "invalid password")
}

func TestHandleSubmitMasterPassword_InvalidJSON(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	d := testDaemon(t, store)

	body := strings.NewReader(`not json`)
	req := httptest.NewRequest("POST", "/api/v1/migration/master-password", body)
	rec := httptest.NewRecorder()
	d.handleSubmitMasterPassword(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestHandleResetSecrets_ClearsPendingBlob: "drop all secrets" must also drop
// the not-yet-migrated Kotlin blob, so the migration unlock dialog stops
// re-appearing. The source H2 store is untouched (read-only) — this only
// clears our own copy.
func TestHandleResetSecrets_ClearsPendingBlob(t *testing.T) {
	d, mux := secretsTestMux(t)

	require.NoError(t, d.secretService.SaveSecret(storage.Secret{
		SecretMeta: storage.SecretMeta{ID: "git-token-1", Name: "tok", Type: storage.SecretGitToken, Scope: "global"},
		Value:      "glpat-x",
	}))
	require.NoError(t, d.store.PutSecretBlob("pending-kotlin-blob"))

	req := httptest.NewRequest("POST", api.SecretsReset, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	blob, err := d.store.GetSecretBlob()
	require.NoError(t, err)
	assert.Empty(t, blob, "reset must clear the pending Kotlin blob")

	secrets, err := d.store.ListSecrets()
	require.NoError(t, err)
	assert.Empty(t, secrets, "reset must wipe stored secrets")

	statusReq := httptest.NewRequest("GET", api.MigrationStatus, http.NoBody)
	statusRec := httptest.NewRecorder()
	mux.ServeHTTP(statusRec, statusReq)
	require.Equal(t, http.StatusOK, statusRec.Code)
	var status struct {
		HasPendingSecrets bool `json:"hasPendingSecrets"`
	}
	require.NoError(t, json.NewDecoder(statusRec.Body).Decode(&status))
	assert.False(t, status.HasPendingSecrets, "migration prompt must not return after reset")
}
