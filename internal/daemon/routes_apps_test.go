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
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// newAppsTestDaemon stands up a minimal Daemon with SQLite store + routes
// mounted on a fresh ServeMux. No docker client, no runtime — handlers that
// need either are expected to short-circuit on validation or NOT_CONFIGURED /
// APP_NOT_FOUND error paths, which is exactly what we want to exercise.
func newAppsTestDaemon(t *testing.T) *http.ServeMux {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	d := &Daemon{store: store, volumesBase: t.TempDir()}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return mux
}

// decodeErr is a small helper that decodes an api.ErrorDto and fails the test
// on bad JSON. Kept tiny to keep individual cases readable.
func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) api.ErrorDto {
	t.Helper()
	var out api.ErrorDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out),
		"non-JSON error body: %s", rec.Body.String())
	return out
}

// TestAppRoutes_InvalidNameReturns400 walks every handler that pulls
// {name} from the path and ensures the validateAppName gate rejects names
// that don't match safeIDPattern (so we can never reach findApp/runtime with
// a path-traversal or shell-metachar app name).
//
// The exact bad-name set is small but covers the common slip-ups: leading dot
// (would let an attacker target hidden files in mount lookups), embedded
// slash (would let the request escape the {name} segment), and space (would
// break the regex outright).
func TestAppRoutes_InvalidNameReturns400(t *testing.T) {
	mux := newAppsTestDaemon(t)

	// `bad` is URL-encoded so the mux still routes it to the {name} handler;
	// without encoding, ".." would be normalized away by net/http before the
	// handler ever sees it and we'd assert on the wrong thing.
	const bad = "%2E%2E" // ".." — escapes the path segment if validation is missing

	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/apps/" + bad + "/logs"},
		{"GET", "/api/v1/apps/" + bad + "/inspect"},
		{"POST", "/api/v1/apps/" + bad + "/restart"},
		{"POST", "/api/v1/apps/" + bad + "/stop"},
		{"POST", "/api/v1/apps/" + bad + "/start"},
		{"POST", "/api/v1/apps/" + bad + "/exec"},
		{"GET", "/api/v1/apps/" + bad + "/config"},
		{"PUT", "/api/v1/apps/" + bad + "/config"},
		{"POST", "/api/v1/apps/" + bad + "/config/reset"},
		{"PUT", "/api/v1/apps/" + bad + "/lock"},
		{"GET", "/api/v1/apps/" + bad + "/files"},
		{"POST", "/api/v1/apps/" + bad + "/files/reset"},
		{"GET", "/api/v1/apps/" + bad + "/files/foo.yml"},
		{"PUT", "/api/v1/apps/" + bad + "/files/foo.yml"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(c.method, c.path, http.NoBody)
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code,
				"body=%s", rec.Body.String())
			err := decodeErr(t, rec)
			assert.Equal(t, api.ErrCodeInvalidRequest, err.Code,
				"expected INVALID_REQUEST for invalid app name")
		})
	}
}

// TestAppRoutes_NoRuntimeReturnsNotFound covers every read-only handler that
// calls findApp() BEFORE requireRuntime(). Because findApp() returns nil when
// runtime is nil, these all surface as 404 APP_NOT_FOUND — the user-facing
// behavior is "app does not exist", which is the right thing to show before
// the namespace has been configured.
func TestAppRoutes_NoRuntimeReturnsNotFound(t *testing.T) {
	mux := newAppsTestDaemon(t)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/v1/apps/eapps/logs", ""},
		{"GET", "/api/v1/apps/eapps/inspect", ""},
		{"GET", "/api/v1/apps/eapps/config", ""},
		{"GET", "/api/v1/apps/eapps/files", ""},
		{"GET", "/api/v1/apps/eapps/files/some-file.yml", ""},
		{"PUT", "/api/v1/apps/eapps/files/some-file.yml", "payload"},
		{"POST", "/api/v1/apps/eapps/exec", `{"command":["echo","ok"]}`},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			var body *strings.Reader
			if c.body != "" {
				body = strings.NewReader(c.body)
			}
			var req *http.Request
			if body == nil {
				req = httptest.NewRequest(c.method, c.path, http.NoBody)
			} else {
				req = httptest.NewRequest(c.method, c.path, body)
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusNotFound, rec.Code,
				"body=%s", rec.Body.String())
			err := decodeErr(t, rec)
			assert.Equal(t, api.ErrCodeAppNotFound, err.Code)
		})
	}
}

// TestAppRoutes_NoRuntimeReturnsNotConfigured covers the mutating endpoints
// that call requireRuntime BEFORE findApp. They must surface NOT_CONFIGURED
// so the UI can prompt the user to pick a namespace instead of showing a
// misleading "app not found" error.
func TestAppRoutes_NoRuntimeReturnsNotConfigured(t *testing.T) {
	mux := newAppsTestDaemon(t)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{"POST", "/api/v1/apps/eapps/restart", ""},
		{"POST", "/api/v1/apps/eapps/stop", ""},
		{"POST", "/api/v1/apps/eapps/start", ""},
		{"PUT", "/api/v1/apps/eapps/config", "name: eapps\n"},
		{"PUT", "/api/v1/apps/eapps/lock", `{"locked":true}`},
		{"POST", "/api/v1/apps/eapps/files/reset?path=foo.yml", ""},
		{"POST", api.AppsRetryPullFailed, ""},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			var req *http.Request
			if c.body == "" {
				req = httptest.NewRequest(c.method, c.path, http.NoBody)
			} else {
				req = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code,
				"body=%s", rec.Body.String())
			err := decodeErr(t, rec)
			assert.Equal(t, api.ErrCodeNotConfigured, err.Code,
				"expected NOT_CONFIGURED when runtime is nil")
		})
	}
}

// TestAppExec_InvalidBody verifies the JSON parsing guard. With runtime nil
// the handler would normally surface APP_NOT_FOUND, but invalid JSON is
// short-circuited only AFTER the findApp lookup — so this test uses the
// app-not-found path first and then a separate sub-test for bare-bones
// validation of the readJSON contract on a synthetic invocation.
//
// We can't test the "valid JSON but empty command" branch end-to-end without
// docker, but we CAN pin the contract that:
//  1. an unknown app yields 404 (even with a body) — already covered above
//  2. malformed JSON would yield 400 once an app does exist
//
// The handler order makes (2) unreachable in pure unit tests; this test
// documents that limitation.
func TestAppExec_InvalidBody_DocumentsHandlerOrder(t *testing.T) {
	mux := newAppsTestDaemon(t)

	// Confirm the documented order: without a real runtime/app, even
	// completely broken JSON results in APP_NOT_FOUND, NOT a 400. That's a
	// fingerprint of the current handler flow (findApp -> readJSON), and
	// changing it would require a separate test refresh.
	req := httptest.NewRequest("POST", "/api/v1/apps/eapps/exec",
		strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, api.ErrCodeAppNotFound, decodeErr(t, rec).Code)
}

// TestAppLockToggle_InvalidJSON pins the body-validation gate on
// handleAppLockToggle. Like the other mutating endpoints, requireRuntime
// runs FIRST, so this test only exercises the no-runtime branch — the actual
// "invalid JSON" 400 cannot be reached without a configured runtime + app.
func TestAppLockToggle_NoRuntimeRejectsBeforeJSONParse(t *testing.T) {
	mux := newAppsTestDaemon(t)

	req := httptest.NewRequest("PUT", "/api/v1/apps/eapps/lock",
		strings.NewReader(`{not-json}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	// requireRuntime runs first → NOT_CONFIGURED, never reaches JSON parser.
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, api.ErrCodeNotConfigured, decodeErr(t, rec).Code)
}

// TestResetAppFile_MissingPathQuery covers the explicit "path is required"
// branch — unique to handleResetAppFile because the path travels via
// ?path= rather than the URL segment. Even though requireRuntime runs first,
// we keep this test as a regression anchor for the parameter contract.
func TestResetAppFile_NoRuntime(t *testing.T) {
	mux := newAppsTestDaemon(t)

	// No path query — but requireRuntime fires first, so the assertion is
	// NOT_CONFIGURED, not "path query parameter is required". Changing the
	// order would change this assertion.
	req := httptest.NewRequest("POST", "/api/v1/apps/eapps/files/reset", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, api.ErrCodeNotConfigured, decodeErr(t, rec).Code)
}

// TestAppLogs_TailParamCappedAndDefaulted exercises the parseTailParam
// helper as exposed through the logs handler. Out-of-range and non-numeric
// inputs must NOT crash — they should fall back to the default (100) or be
// clamped to the max (10000). Because we have no docker client, the handler
// will return 404 once it reaches findApp; we only care that we got there
// without panicking on a hostile tail= value.
func TestAppLogs_HostileTailParamDoesNotCrash(t *testing.T) {
	mux := newAppsTestDaemon(t)

	cases := []string{
		"abc",       // non-numeric
		"-5",        // negative
		"999999999", // way over cap
		"",          // missing
		"100%3Brm",  // URL-encoded shell-ish string — doesn't parse, no harm done
	}
	for _, tail := range cases {
		t.Run("tail="+tail, func(t *testing.T) {
			path := "/api/v1/apps/eapps/logs?tail=" + tail
			req := httptest.NewRequest("GET", path, http.NoBody)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			// Without a runtime the handler returns 404 APP_NOT_FOUND.
			// The point of this test is: it MUST return a normal error,
			// not panic or write a malformed response.
			require.Equal(t, http.StatusNotFound, rec.Code,
				"body=%s", rec.Body.String())
		})
	}
}

// TestAppsRetryPullFailed_RoutedAsStaticBeforeNameTemplate verifies the
// route comment in server.go: the static "/apps/retry-pull-failed" path is
// registered before the {name}-templated paths, so it never collides with
// e.g. an app literally named "retry-pull-failed". Without runtime it
// surfaces NOT_CONFIGURED, which proves the static handler ran (a
// templated-route fallback would have returned 405 or 404).
func TestAppsRetryPullFailed_RoutesToStaticHandler(t *testing.T) {
	mux := newAppsTestDaemon(t)

	req := httptest.NewRequest("POST", api.AppsRetryPullFailed, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code,
		"static retry-pull-failed handler should run and trip requireRuntime")
	assert.Equal(t, api.ErrCodeNotConfigured, decodeErr(t, rec).Code)
}

// TestPutAppConfig_NoRuntimeRejectsBeforeYAMLParse mirrors the lock-toggle
// test: requireRuntime sits in front of the YAML decode, so even a body that
// would otherwise be rejected as "invalid YAML" surfaces NOT_CONFIGURED.
// Pinning this prevents an accidental reorder from silently changing the
// error code the UI relies on.
func TestPutAppConfig_NoRuntimeRejectsBeforeYAMLParse(t *testing.T) {
	mux := newAppsTestDaemon(t)

	req := httptest.NewRequest("PUT", "/api/v1/apps/eapps/config",
		strings.NewReader("::: not valid yaml :::"))
	req.Header.Set("Content-Type", "text/yaml")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, api.ErrCodeNotConfigured, decodeErr(t, rec).Code)
}

// TestResetAppConfig_NoRuntimeReturnsNotFound is the odd one out among the
// mutating handlers: handleResetAppConfig does NOT call requireRuntime, only
// findApp. Because findApp returns nil when runtime is nil, the response is
// 404 APP_NOT_FOUND — NOT 400 NOT_CONFIGURED. This is documented behavior
// (the handler never reaches the runtime.ResetAppDef call), but it's worth
// pinning so a future refactor that adds requireRuntime doesn't silently
// flip the error code on UI clients.
func TestResetAppConfig_NoRuntimeReturnsNotFound(t *testing.T) {
	mux := newAppsTestDaemon(t)

	req := httptest.NewRequest("POST", "/api/v1/apps/eapps/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, api.ErrCodeAppNotFound, decodeErr(t, rec).Code)
}

// TestIsAppBindMount covers the four cases isAppBindMount has to discriminate
// between: exact file mount, file nested under a directory mount, traversal
// escape via `../`, and a sibling-path miss. The mux-level test below catches
// the most common attack shape (a raw `..` in the URL), but isAppBindMount is
// also reached from POST /files/{path} where the path comes from the JSON
// body and bypasses the mux's normalisation — so the function itself has to
// reject escapes on its own.
func TestIsAppBindMount(t *testing.T) {
	app := &namespace.AppRuntime{
		Def: appdef.ApplicationDef{
			Volumes: []string{
				"./proxy/lua_oidc_full_access.lua:/usr/local/openresty/lualib/x.lua",
				"./app/eapps/props:/opt/citeck/props",
			},
		},
	}
	cases := []struct {
		name    string
		relPath string
		want    bool
	}{
		{"exact file mount", "./proxy/lua_oidc_full_access.lua", true},
		{"file under dir mount", "./app/eapps/props/application-launcher.yml", true},
		{"nested file under dir mount", "./app/eapps/props/sub/deeper/x.yml", true},
		{"dir mount itself is a match", "./app/eapps/props", true},
		{"traversal escape rejected", "./app/eapps/props/../../etc/shadow", false},
		{"sibling directory rejected", "./app/eapps/other/y.yml", false},
		{"sibling under root rejected", "./app/other", false},
		{"unrelated path rejected", "./somewhere/else.yml", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isAppBindMount(app, tc.relPath)
			assert.Equal(t, tc.want, got, "relPath=%q", tc.relPath)
		})
	}
}

// TestGetAppFile_PathTraversalRejectedByMux confirms that the standard
// net/http mux cleans dot-segments before invoking the handler — so a raw
// "../../etc/passwd" in the {path...} wildcard cannot escape the volumes
// base directory. This is a property of the framework, not the handler, but
// pinning it makes accidental switches to a different mux (chi, gorilla)
// safe to spot in review.
func TestGetAppFile_PathTraversalNeutralizedByMux(t *testing.T) {
	mux := newAppsTestDaemon(t)

	req := httptest.NewRequest("GET",
		"/api/v1/apps/eapps/files/../../etc/passwd", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	// http.ServeMux will redirect dot-segment paths to the canonical form
	// (307/308 in modern Go, 301 historically) — the handler is never
	// invoked with the unsafe path, so the redirect target is the cleaned
	// "/api/v1/apps/etc/passwd" which doesn't match any registered route.
	assert.True(t,
		rec.Code == http.StatusMovedPermanently ||
			rec.Code == http.StatusTemporaryRedirect ||
			rec.Code == http.StatusPermanentRedirect ||
			rec.Code == http.StatusNotFound ||
			rec.Code == http.StatusBadRequest,
		"got %d body=%s — expected redirect/404/400, NOT a file read",
		rec.Code, rec.Body.String())
}
