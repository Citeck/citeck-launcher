package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// TestNamespaceHeaderShowsTheEditedBundle pins the reported bug: switching a
// namespace's bundle updated the namespaces dialog (which reads the store) but
// left the dashboard header on the previous bundle.
//
// ToNamespaceDto reports `r.config.BundleRef` — the runtime's OWN copy of the
// namespace config, which is refreshed only by the async cmdRegenerate, i.e.
// only while the runtime loop is alive. handlePutNamespaceEdit persists the
// YAML and calls doReload, which updates the daemon's activeNs.nsConfig
// synchronously and then enqueues that same config for the runtime. On a
// STOPPED namespace nothing drains the queue, so the runtime's copy — and the
// header — kept the old value until the namespace was re-activated.
//
// The same defect was already found and fixed for the namespace NAME right
// below in handleGetNamespace; BundleRef is the other field ToNamespaceDto
// derives from that stale copy, and it was left behind.
func TestNamespaceHeaderShowsTheEditedBundle(t *testing.T) {
	for _, status := range []namespace.NsRuntimeStatus{
		namespace.NsStatusStopped,
		namespace.NsStatusRunning,
	} {
		t.Run(string(status), func(t *testing.T) {
			mux, d := newAppsTestDaemonRabbit(t, status)

			// What the runtime was constructed with — the pre-edit bundle.
			d.activeNs.runtime.SetConfigForTest(&namespace.Config{
				ID:        "test",
				Name:      "Citeck #2",
				BundleRef: bundle.Ref{Repo: "enterprise", Key: "2026.2"},
			})
			// What the edit persisted and doReload published on the daemon.
			d.activeNs.nsConfig = &namespace.Config{
				ID:        "test",
				Name:      "Citeck #2",
				BundleRef: bundle.Ref{Repo: "enterprise-rc", Key: "2026.3-RC2"},
			}

			req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

			var dto api.NamespaceDto
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
			require.Equal(t, "enterprise-rc:2026.3-RC2", dto.BundleRef,
				"the header must show the bundle the namespace is configured with now")
		})
	}
}

// A symbolic LATEST key still resolves to the concrete version the bundle
// resolver settled on, exactly as the namespaces dialog renders it.
func TestNamespaceHeaderResolvesLatestToTheConcreteVersion(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusRunning)
	d.activeNs.nsConfig = &namespace.Config{
		ID:        "test",
		BundleRef: bundle.Ref{Repo: "enterprise", Key: "LATEST"},
	}
	d.activeNs.bundleDef = &bundle.Def{Key: bundle.Key{Version: "2026.3"}}

	req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.Equal(t, "enterprise:2026.3", dto.BundleRef)
}
