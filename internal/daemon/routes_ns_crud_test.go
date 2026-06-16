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
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// newNsCrudTestDaemon stands up a Daemon with SQLite store + unlocked
// SecretService and mounted routes — enough for the namespace CRUD endpoints
// that don't need a live runtime or docker engine.
func newNsCrudTestDaemon(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))

	d := &Daemon{store: store, secretService: svc, activeNs: &activeNamespace{workspaceID: "wsMain"}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux
}

// TestCreateNamespace_ErrorPaths covers the handler-side validation glue:
// malformed body, form-spec field failures, and a path-unsafe workspace id.
func TestCreateNamespace_ErrorPaths(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	_, mux := newNsCrudTestDaemon(t)

	t.Run("invalid body", func(t *testing.T) {
		req := httptest.NewRequest("POST", api.Namespaces, strings.NewReader(`{nope`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid request body")
	})

	t.Run("form validation failure carries field errors", func(t *testing.T) {
		// name is Required:true in the namespace-create form spec.
		req := httptest.NewRequest("POST", api.Namespaces,
			strings.NewReader(`{"name":"","authType":"BASIC"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
		var body api.ValidationErrorDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "validation failed", body.Error)
		require.NotEmpty(t, body.Fields, "field-level errors must reach the client")
		assert.Equal(t, "name", body.Fields[0].Key)
	})

	t.Run("invalid workspace id", func(t *testing.T) {
		req := httptest.NewRequest("POST", api.Namespaces,
			strings.NewReader(`{"name":"X","authType":"BASIC","workspaceId":"bad/../ws"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid workspace id")
	})
}

// TestCreateNamespace_HappyPathDesktop drives a full create through the route
// and asserts the persisted row. The target workspace differs from the active
// one, so the desktop auto-activate step (which would need a docker engine)
// is skipped by its own guard.
func TestCreateNamespace_HappyPathDesktop(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)

	body := `{"name":"My Namespace","authType":"BASIC","users":["admin"],` +
		`"bundleRepo":"community","bundleKey":"2026.1","workspaceId":"ws-target"}`
	req := httptest.NewRequest("POST", api.Namespaces, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), `namespace \"My Namespace\" created`)

	rows, err := d.store.ListNamespaces("ws-target")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "My Namespace", rows[0].Name)
	assert.NotEmpty(t, rows[0].ID, "an opaque random ID must be generated")

	// The persisted YAML must round-trip through the config parser with the
	// request's bundle ref pinned (no LATEST resolution for a concrete key).
	cfg, err := d.loadNamespaceConfigFromStore("ws-target", rows[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "community", cfg.BundleRef.Repo)
	assert.Equal(t, "2026.1", cfg.BundleRef.Key)
	assert.Equal(t, "My Namespace", cfg.Name)

	// Namespace creation force-enables secret encryption (default password).
	assert.True(t, d.secretService.IsEncrypted())
}

// TestCreateNamespace_LatestUnsyncedRepoRefused: requesting bundleKey "LATEST"
// when the bundle repo has no synced versions must REFUSE (409
// BUNDLE_NOT_SYNCED) rather than persist a raw "LATEST" — the launcher never
// stores symbolic LATEST (it would auto-update between versions on reload).
// Server mode forces an offline resolve, so the empty bundle dir fails fast.
func TestCreateNamespace_LatestUnsyncedRepoRefused(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)

	body := `{"name":"X","authType":"BASIC","users":["admin"],` +
		`"bundleRepo":"community","bundleKey":"LATEST","workspaceId":"ws-target"}`
	req := httptest.NewRequest("POST", api.Namespaces, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), api.ErrCodeBundleNotSynced)

	rows, err := d.store.ListNamespaces("ws-target")
	require.NoError(t, err)
	assert.Empty(t, rows, "no namespace persisted with a raw LATEST bundle key")
}

// TestDeleteNamespace_Validation: bad id → 400, server mode → 400, desktop
// non-active delete drops the store row.
func TestDeleteNamespace_Validation(t *testing.T) {
	t.Run("invalid id", func(t *testing.T) {
		config.SetDesktopMode(true)
		t.Cleanup(config.ResetDesktopMode)
		_, mux := newNsCrudTestDaemon(t)
		req := httptest.NewRequest("DELETE", "/api/v1/namespaces/bad@id", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid namespace id")
	})

	t.Run("server mode refuses", func(t *testing.T) {
		config.ResetDesktopMode()
		t.Cleanup(config.ResetDesktopMode)
		_, mux := newNsCrudTestDaemon(t)
		req := httptest.NewRequest("DELETE", "/api/v1/namespaces/some-ns", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "cannot delete namespace in server mode")
	})

	t.Run("desktop deletes non-active namespace row", func(t *testing.T) {
		config.SetDesktopMode(true)
		t.Cleanup(config.ResetDesktopMode)
		t.Setenv("CITECK_HOME", t.TempDir())
		d, mux := newNsCrudTestDaemon(t)
		require.NoError(t, d.persistNamespaceConfig("wsMain", "nsdel",
			[]byte("id: nsdel\nname: Doomed\nproxy:\n  port: 80\n")))

		req := httptest.NewRequest("DELETE", "/api/v1/namespaces/nsdel", http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

		rows, err := d.store.ListNamespaces("wsMain")
		require.NoError(t, err)
		assert.Empty(t, rows, "the store row must be gone after delete")
	})
}

// TestNamespaceEdit_ErrorPaths: GET on an unknown id → 404
// NAMESPACE_NOT_FOUND; PUT with a malformed body → 400; PUT on an unknown id
// → 404 NAMESPACE_NOT_FOUND. The endpoint is id-scoped — it does NOT depend
// on an active namespace being loaded (Welcome edits rows without
// activating them).
func TestNamespaceEdit_ErrorPaths(t *testing.T) {
	_, mux := newNsCrudTestDaemon(t)

	t.Run("GET unknown namespace", func(t *testing.T) {
		req := httptest.NewRequest("GET", api.NamespaceEditPath("nope"), http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body api.ErrorDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, api.ErrCodeNamespaceNotFound, body.Code)
	})

	t.Run("PUT invalid body", func(t *testing.T) {
		req := httptest.NewRequest("PUT", api.NamespaceEditPath("nope"), strings.NewReader(`{broken`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid request body")
	})

	t.Run("PUT unknown namespace", func(t *testing.T) {
		req := httptest.NewRequest("PUT", api.NamespaceEditPath("nope"), strings.NewReader(`{"name":"X"}`))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body api.ErrorDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, api.ErrCodeNamespaceNotFound, body.Code)
	})
}

// TestNamespaceEdit_IDScopedContract pins the id-scoped edit contract the Web
// UI builds against:
//   - GET returns the AUTHORITATIVE stored values for namespace {id} — in
//     particular a stored symbolic "LATEST" bundle key comes back RAW, never
//     display-resolved to a concrete version.
//   - PUT applies the edit to namespace {id}'s stored YAML even when it is
//     NOT the active namespace (and when NO namespace is active at all — the
//     Welcome-screen case the old un-scoped handler 400'd on), persisting
//     without a reload.
//   - Partial-payload semantics: empty AuthType and nil Users leave the
//     stored authentication block unchanged (no wipe).
func TestNamespaceEdit_IDScopedContract(t *testing.T) {
	d, mux := newNsCrudTestDaemon(t)
	require.NoError(t, d.persistNamespaceConfig("wsMain", "nsedit", []byte(
		"id: nsedit\nname: Editable\nbundleRef: community:LATEST\n"+
			"authentication:\n  type: BASIC\n  users: [admin, alice]\nproxy:\n  port: 8085\n")))

	t.Run("GET returns raw stored values incl. symbolic LATEST", func(t *testing.T) {
		req := httptest.NewRequest("GET", api.NamespaceEditPath("nsedit"), http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
		var dto api.NamespaceEditDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		assert.Equal(t, "Editable", dto.Name)
		assert.Equal(t, "community", dto.BundleRepo)
		assert.Equal(t, "LATEST", dto.BundleKey, "stored LATEST must be returned RAW, not display-resolved")
		assert.Equal(t, "BASIC", dto.AuthType)
		assert.Equal(t, []string{"admin", "alice"}, dto.Users)
		assert.Equal(t, 8085, dto.Port)
		require.NotNil(t, dto.TLSEnabled, "GET must always fill tlsEnabled")
		require.NotNil(t, dto.PgAdminEnabled, "GET must always fill pgAdminEnabled")
		assert.False(t, *dto.TLSEnabled)
		// pgAdmin is enabled by DefaultNamespaceConfig when the stored YAML
		// doesn't mention it — GET reflects the effective stored value.
		assert.True(t, *dto.PgAdminEnabled)
	})

	t.Run("PUT without tls/pgAdmin preserves stored true values", func(t *testing.T) {
		// TLSEnabled / PgAdminEnabled are *bool on the DTO: the dialog omits
		// them, and an absent field must NOT zero a TLS-enabled namespace
		// (the historical bool fields silently wrote tls.enabled: false).
		require.NoError(t, d.persistNamespaceConfig("wsMain", "nstls", []byte(
			"id: nstls\nname: Secure\nbundleRef: community:LATEST\n"+
				"authentication:\n  type: BASIC\n  users: [admin]\n"+
				"proxy:\n  host: example.com\n  port: 443\n  tls:\n    enabled: true\n"+
				"pgAdmin:\n  enabled: true\n")))

		body := `{"name":"Still Secure","host":"","port":0}`
		req := httptest.NewRequest("PUT", api.NamespaceEditPath("nstls"), strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

		stored, err := d.loadNamespaceConfigFromStore("wsMain", "nstls")
		require.NoError(t, err)
		assert.Equal(t, "Still Secure", stored.Name)
		assert.True(t, stored.Proxy.TLS.Enabled, "absent tlsEnabled must preserve stored true")
		assert.True(t, stored.PgAdmin.Enabled, "absent pgAdminEnabled must preserve stored true")
		assert.Equal(t, "example.com", stored.Proxy.Host, "empty host means unchanged")
		assert.Equal(t, 443, stored.Proxy.Port, "zero port means unchanged")
	})

	t.Run("PUT with explicit false still applies tls/pgAdmin", func(t *testing.T) {
		body := `{"tlsEnabled":false,"pgAdminEnabled":false}`
		req := httptest.NewRequest("PUT", api.NamespaceEditPath("nstls"), strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

		stored, err := d.loadNamespaceConfigFromStore("wsMain", "nstls")
		require.NoError(t, err)
		assert.False(t, stored.Proxy.TLS.Enabled, "explicit false must apply")
		assert.False(t, stored.PgAdmin.Enabled, "explicit false must apply")
	})

	t.Run("PUT persists to the addressed (non-active) namespace", func(t *testing.T) {
		// No namespace is active (d.nsConfig == nil) — the edit must still
		// apply to nsedit's stored YAML, with no reload attempted.
		body := `{"name":"Renamed","bundleRepo":"community","bundleKey":"2026.1","port":8085}`
		req := httptest.NewRequest("PUT", api.NamespaceEditPath("nsedit"), strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

		stored, err := d.loadNamespaceConfigFromStore("wsMain", "nsedit")
		require.NoError(t, err)
		assert.Equal(t, "Renamed", stored.Name)
		assert.Equal(t, "2026.1", stored.BundleRef.Key)
		// Partial payload: authType empty + users absent → auth unchanged.
		assert.Equal(t, "BASIC", string(stored.Authentication.Type))
		assert.Equal(t, []string{"admin", "alice"}, stored.Authentication.Users)
	})
}

// TestNamespaceListEndpoints_EmptyShapes pins the "always a JSON array,
// never null" contract for list-shaped endpoints the dashboard polls.
func TestNamespaceListEndpoints_EmptyShapes(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	_, mux := newNsCrudTestDaemon(t)

	for _, path := range []string{api.Namespaces, api.QuickStarts, api.Bundles} {
		req := httptest.NewRequest("GET", path, http.NoBody)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, "%s body=%s", path, rec.Body.String())
		assert.JSONEq(t, "[]", rec.Body.String(), "%s must serialize an empty array", path)
	}
}

// TestNamespaceCreateDefaults_Fallbacks: with no workspace config the
// defaults endpoint still answers with the built-in BASIC/admin shape and an
// auto-numbered name (Kotlin defaultNameNum parity).
func TestNamespaceCreateDefaults_Fallbacks(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	_, mux := newNsCrudTestDaemon(t)

	req := httptest.NewRequest("GET", api.NamespaceCreateDefaults, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var dto api.NamespaceCreateDefaultsDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Equal(t, "Citeck #1", dto.Name)
	assert.Equal(t, "BASIC", dto.AuthType)
	// Kotlin AuthenticationProps.DEFAULT = setOf("admin", "fet").
	assert.Equal(t, []string{"admin", "fet"}, dto.Users)
}
