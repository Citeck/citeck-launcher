package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// newStoppedRuntimeDaemon stands up a Daemon whose active namespace has a real
// Runtime that has never been started — the state a namespace is in right after
// it is created, and the state EVERY stopped namespace returns to when the
// launcher restarts. A fresh Runtime reports STOPPED and its app registry is
// empty, which is the whole point: the registry is filled by doStart.
func newStoppedRuntimeDaemon(t *testing.T) *http.ServeMux {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "ns1"
	rt := namespace.NewRuntime(&cfg, nil, t.TempDir())
	require.Equal(t, namespace.NsStatusStopped, rt.Status(),
		"a Runtime that was never started must report STOPPED — this fixture depends on it")

	d := &Daemon{store: store, activeNs: &activeNamespace{
		workspaceID: "wsMain", nsConfig: &cfg, runtime: rt, volumesBase: t.TempDir(),
	}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return mux
}

// The play button in the app table and `citeck start <app>` used to answer
// APP_NOT_FOUND here — about an app the table on screen is listing — because
// the runtime's registry is built by doStart and nothing else. The refusal has
// to come BEFORE the registry is consulted, or the operator is told the app
// does not exist rather than what to do about it.
func TestPerAppStartOnAStoppedNamespaceIsRefusedWithSomethingActionable(t *testing.T) {
	mux := newStoppedRuntimeDaemon(t)

	for _, path := range []string{"/api/v1/apps/postgres/start", "/api/v1/apps/postgres/restart"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
			err := decodeErr(t, rec)
			assert.Equal(t, api.ErrCodeNamespaceNotRunning, err.Code,
				"APP_NOT_FOUND here names the wrong problem: the app is in the table, the namespace is not running")
			assert.NotEmpty(t, err.Message)
			assert.NotContains(t, err.Message, "apps.msg.",
				"the message must be rendered, not the i18n key")
		})
	}
}

// The refusal is localized like every other operator sentence the daemon
// builds — the CLI prints it and the Web UI shows it verbatim.
func TestPerAppStartRefusalSpeaksTheRequestedLanguage(t *testing.T) {
	mux := newStoppedRuntimeDaemon(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/postgres/start", http.NoBody)
	req.Header.Set(api.LocaleHeader, "ru")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, decodeErr(t, rec).Message, "Неймспейс")
}

// The allowing side of the same rule. It cannot be reached through the handler
// in a unit test (a Runtime only reaches RUNNING with a Docker client), so it
// is asserted where the decision lives. STOPPING is refused on purpose: the
// loop is alive but winding down, and a start racing the stop chain is the
// case stepAllApps already refuses to step.
func TestPerAppLifecycleIsAllowedExactlyWhileTheLoopRuns(t *testing.T) {
	allowed := map[namespace.NsRuntimeStatus]bool{
		namespace.NsStatusRunning:  true,
		namespace.NsStatusStarting: true,
		namespace.NsStatusStalled:  true,
		namespace.NsStatusStopped:  false,
		namespace.NsStatusStopping: false,
		"":                         false,
	}
	for status, want := range allowed {
		assert.Equal(t, want, perAppLifecycleAllowed(status), "status %q", status)
	}
}

// The exception, and the reason the gate takes the app name: attaching a
// DETACHED app is a persisted change of intent — plus a regeneration when the
// app gates the composition (rag deciding whether qdrant exists) — and it
// works with the loop down, because the app starts with the namespace. The
// runtime has to KNOW the app for that: a namespace never started in this
// process has an empty registry and nothing to attach.
func TestAttachingADetachedAppIsAllowedWhileTheNamespaceIsStopped(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "ns1"
	rt := namespace.NewRuntime(&cfg, nil, t.TempDir())
	rt.InjectAppsForTest(&namespace.AppRuntime{
		Name:   "rag",
		Status: namespace.AppStatusStopped,
		Def:    appdef.ApplicationDef{Name: "rag"},
	})
	rt.SetManualStoppedApps(map[string]bool{"rag": true})
	d := &Daemon{store: store, activeNs: &activeNamespace{
		workspaceID: "wsMain", nsConfig: &cfg, runtime: rt, volumesBase: t.TempDir(),
	}}
	d.reloadFn = func() error { return nil }
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apps/rag/start", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.False(t, rt.ManualStoppedApps()["rag"], "the attach must have cleared the detach flag")

	// An app that is merely stopped in the same namespace is still refused —
	// nothing would carry it beyond READY_TO_PULL.
	rt.InjectAppsForTest(&namespace.AppRuntime{
		Name:   "postgres",
		Status: namespace.AppStatusStopped,
		Def:    appdef.ApplicationDef{Name: "postgres"},
	})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/apps/postgres/start", http.NoBody))
	require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
	assert.Equal(t, api.ErrCodeNamespaceNotRunning, decodeErr(t, rec).Code)
}
