package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/license"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// newLicenseTestDaemon stands up a Daemon whose license.Service is backed by
// a real SQLite SecretService (unlocked with the default password) and mounts
// the routes — the same wiring the production daemon uses.
func newLicenseTestDaemon(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))

	d := &Daemon{store: store, secretService: svc, licenses: license.NewService(svc)}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux
}

// TestLicenseRoutes_CRUDHappyPath walks create (201) → list (entry visible
// with server-computed Valid flag) → delete (204) → list (empty).
func TestLicenseRoutes_CRUDHappyPath(t *testing.T) {
	_, mux := newLicenseTestDaemon(t)

	body := `{"id":"lic-1","tenant":"acme","priority":7,"issuedTo":"Acme Corp"}`
	req := httptest.NewRequest("POST", "/api/v1/licenses", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())

	req = httptest.NewRequest("GET", "/api/v1/licenses", http.NoBody)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var listed []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, "lic-1", listed[0]["id"])
	assert.Equal(t, "acme", listed[0]["tenant"])
	// A stub license has no real signature — the server-side validator must
	// mark it invalid rather than leaving the field to the UI.
	assert.Equal(t, false, listed[0]["valid"])

	req = httptest.NewRequest("DELETE", "/api/v1/licenses/lic-1", http.NoBody)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)

	req = httptest.NewRequest("GET", "/api/v1/licenses", http.NoBody)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())
}

// TestLicenseRoutes_CreateErrorPaths: malformed JSON and a missing license id
// are both 400s; nothing is persisted.
func TestLicenseRoutes_CreateErrorPaths(t *testing.T) {
	d, mux := newLicenseTestDaemon(t)

	t.Run("invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/licenses", strings.NewReader(`{broken`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid license JSON")
	})
	t.Run("missing id", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/licenses", strings.NewReader(`{"tenant":"acme"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "license id is required")
	})

	licenses, err := d.licenses.List()
	require.NoError(t, err)
	assert.Empty(t, licenses, "failed creates must not persist anything")
}

// TestLicenseRoutes_ListLockedStore: a SecretService locked with a custom
// master password makes List() fail — the route must surface a 500 instead of
// silently returning an empty list (which would hide enterprise licenses).
func TestLicenseRoutes_ListLockedStore(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	// Store a license under a CUSTOM master password, then re-open the
	// SecretService without unlocking — the meta row is visible but its value
	// is undecryptable, which is exactly the pre-unlock desktop state.
	seedSvc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, seedSvc.SetMasterPassword("user-master-pw", false))
	require.NoError(t, license.NewService(seedSvc).Add(license.Instance{ID: "lic-locked", Tenant: "acme"}))

	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.True(t, svc.IsLocked())

	d := &Daemon{store: store, secretService: svc, licenses: license.NewService(svc)}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	req := httptest.NewRequest("GET", "/api/v1/licenses", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestLicenseRoutes_Status covers GET /api/v1/licenses/status: an empty store
// reads as community (zero DTO), a stored-but-invalid license surfaces its
// tenant with enterprise=false (the "expired" indicator state), and a locked
// secret store is a 500 — same surfacing rule as the list endpoint.
func TestLicenseRoutes_Status(t *testing.T) {
	t.Run("empty store is community", func(t *testing.T) {
		_, mux := newLicenseTestDaemon(t)
		req := httptest.NewRequest("GET", "/api/v1/licenses/status", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var dto api.LicenseStatusDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		assert.Equal(t, api.LicenseStatusDto{}, dto)
	})

	t.Run("invalid license reports tenant without enterprise", func(t *testing.T) {
		d, mux := newLicenseTestDaemon(t)
		require.NoError(t, d.licenses.Add(license.Instance{
			ID: "lic-1", Tenant: "acme", IssuedTo: "Acme Corp",
			ValidUntil: license.Time{Time: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		}))
		req := httptest.NewRequest("GET", "/api/v1/licenses/status", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var dto api.LicenseStatusDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		assert.False(t, dto.Enterprise)
		assert.Equal(t, "acme", dto.Tenant)
		assert.Equal(t, "Acme Corp", dto.IssuedTo)
		assert.Equal(t, "2025-01-01", dto.ValidUntil)
		assert.Negative(t, dto.DaysLeft)
		assert.False(t, dto.ExpiringSoon, "expired is never 'expiring soon'")
	})

	t.Run("nil license service degrades to community", func(t *testing.T) {
		d := &Daemon{}
		req := httptest.NewRequest("GET", "/api/v1/licenses/status", http.NoBody)
		rec := httptest.NewRecorder()
		d.handleLicenseStatus(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"enterprise":false,"daysLeft":0,"expiringSoon":false}`, rec.Body.String())
	})

	t.Run("locked store is a 500", func(t *testing.T) {
		store, err := storage.NewSQLiteStore(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = store.Close() })
		seedSvc, err := storage.NewSecretService(store)
		require.NoError(t, err)
		require.NoError(t, seedSvc.SetMasterPassword("user-master-pw", false))
		require.NoError(t, license.NewService(seedSvc).Add(license.Instance{ID: "lic-locked", Tenant: "acme"}))

		svc, err := storage.NewSecretService(store)
		require.NoError(t, err)
		require.True(t, svc.IsLocked())

		d := &Daemon{store: store, secretService: svc, licenses: license.NewService(svc)}
		mux := http.NewServeMux()
		d.registerRoutes(mux)
		req := httptest.NewRequest("GET", "/api/v1/licenses/status", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// TestLicenseStatusToDTO pins the summary→wire mapping, in particular the
// server-computed ExpiringSoon flag (valid AND <= 14 days left).
func TestLicenseStatusToDTO(t *testing.T) {
	until := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	dto := licenseStatusToDTO(license.StatusSummary{
		Enterprise: true, Tenant: "acme", IssuedTo: "Acme Corp", ValidUntil: until, DaysLeft: 10,
	})
	assert.Equal(t, api.LicenseStatusDto{
		Enterprise: true, Tenant: "acme", IssuedTo: "Acme Corp",
		ValidUntil: "2026-09-01", DaysLeft: 10, ExpiringSoon: true,
	}, dto)

	dto = licenseStatusToDTO(license.StatusSummary{Enterprise: true, Tenant: "acme", ValidUntil: until, DaysLeft: 15})
	assert.False(t, dto.ExpiringSoon, "15 days left is outside the 14-day window")

	dto = licenseStatusToDTO(license.StatusSummary{Enterprise: false, Tenant: "acme", ValidUntil: until, DaysLeft: -3})
	assert.False(t, dto.ExpiringSoon, "invalid license is never 'expiring soon'")

	dto = licenseStatusToDTO(license.StatusSummary{})
	assert.Equal(t, api.LicenseStatusDto{}, dto, "community summary maps to the zero DTO")
}

// TestLicenseRoutes_DeleteMissingID: the handler rejects an empty id with 400
// (mux can't route an empty path segment, so the handler is invoked directly).
func TestLicenseRoutes_DeleteMissingID(t *testing.T) {
	d, _ := newLicenseTestDaemon(t)
	req := httptest.NewRequest("DELETE", "/api/v1/licenses/", http.NoBody)
	rec := httptest.NewRecorder()
	d.handleDeleteLicense(rec, req) // PathValue("id") is unset → ""
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "license id is required")
}
