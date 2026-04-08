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
