package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/config"
)

// writeCreatableBundle puts a bundle where the CREATE-time resolve will find
// it, in whichever mode the test runs: resolveWorkspace's first priority is
// the offline-import tree under the workspace's bundles data dir, which is
// per-workspace on desktop and shared in server mode. Tests that only care
// about other parts of buildNamespaceConfigFromCreate still need this, because
// creating a namespace on a bundle nobody can read is now refused.
func writeCreatableBundle(t *testing.T, wsID, key string) {
	t.Helper()
	const repo = "community"
	root := filepath.Join(config.BundlesDataDir(wsID), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(root, repo), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: "+repo+"\n    name: "+repo+"\n    path: "+repo+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, repo, key+".yaml"),
		[]byte("EcosModelApp:\n  image: core/ecos-model:1.0\n"), 0o600))
}

// createNamespaceOn posts a create request naming one concrete bundle version.
func createNamespaceOn(t *testing.T, mux *http.ServeMux, wsID, key string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(api.NamespaceCreateDto{
		Name: "Probe", AuthType: "NONE", Host: "localhost", Port: 8099,
		BundleRepo: "community", BundleKey: key, WorkspaceID: wsID, UseDefaultPassword: true,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, api.Namespaces, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// A namespace being created cannot be running from a cached bundle — it has no
// cache and no way to acquire one — so a pinned version the synced repo does
// not have produces a namespace with zero applications: every Citeck service
// comes from the bundle while the infra apps are generated unconditionally.
// Measured on a real stand before this refusal: create succeeded, the daemon
// logged "Failed to resolve bundle and no cache available — daemon starts with
// 0 apps", and the namespace listed seven third-party containers.
func TestCreateNamespace_RefusesAPinnedBundleTheRepoDoesNotHave(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "wsMain", map[string]string{"2026.2": ""})

	rec := createNamespaceOn(t, mux, "wsMain", "2099.1")

	require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
	errDto := decodeErr(t, rec)
	assert.Equal(t, api.ErrCodeBundleNotSynced, errDto.Code)
	assert.Contains(t, errDto.Message, "2099.1", "the message must name the version that was asked for")

	rows, listErr := d.store.ListNamespaces("wsMain")
	require.NoError(t, listErr)
	assert.Empty(t, rows, "a refused create must leave nothing behind")
}

// The other half: a version the repo DOES have must still be created. Without
// this, "refuse everything" passes the test above.
func TestCreateNamespace_AcceptsAPinnedBundleTheRepoHas(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "wsMain", map[string]string{"2026.2": ""})

	rec := createNamespaceOn(t, mux, "wsMain", "2026.2")

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	rows, listErr := d.store.ListNamespaces("wsMain")
	require.NoError(t, listErr)
	assert.Len(t, rows, 1)
}

// The refusal is a CREATE rule and must not migrate to the write path: an
// existing namespace whose bundle left the repo is running from its cache, and
// refusing its config write would leave it unable to be renamed — or to be
// moved off that bundle, which is the way out.
func TestPersistNamespaceConfig_StillAcceptsAnUnresolvableBundle(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": ""})

	assert.NoError(t, d.persistNamespaceConfig("ws1", "ns1", nsConfigYAML(t, "X", "2099.1")))
}
