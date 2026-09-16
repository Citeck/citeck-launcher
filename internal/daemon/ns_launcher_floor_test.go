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
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// writeFlooredWorkspace lays out a workspace repo whose `community` bundle repo
// IS the workspace repo (no url → shouldUseLocalBundles), one file per entry:
// version key → the minLauncherVersion it declares ("" for none).
func writeFlooredWorkspace(t *testing.T, wsID string, floors map[string]string) {
	t.Helper()
	repoDir := config.WorkspaceRepoDir(wsID)
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "community"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: community\n    name: Community\n    path: community\n"), 0o600))
	for key, floor := range floors {
		body := "EcosModelApp:\n  image: core/ecos-model:1.0\n"
		if floor != "" {
			body = "minLauncherVersion: \"" + floor + "\"\n" + body
		}
		require.NoError(t, os.WriteFile(
			filepath.Join(repoDir, "community", key+".yaml"), []byte(body), 0o600))
	}
}

// nsConfigYAML marshals a minimal but VALID namespace config naming one
// bundle. namespace.ValidateYAML enforces a real proxy port and at least one
// BASIC-auth user, so this starts from namespace.DefaultNamespaceConfig()
// (port 80, admin/fet) rather than a bare struct literal — a bare literal
// round-trips a zero proxy port through MarshalNamespaceConfig and
// ValidateNamespaceConfig then refuses it before the launcher-floor gate ever
// runs. The point of this helper is the bundle ref, not the rest of the
// schema.
func nsConfigYAML(t *testing.T, name, key string) []byte {
	t.Helper()
	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "ns1"
	cfg.Name = name
	cfg.BundleRef = bundle.Ref{Repo: "community", Key: key}
	raw, err := namespace.MarshalNamespaceConfig(&cfg)
	require.NoError(t, err)
	return raw
}

// The write path is the gate. It asks what the config being WRITTEN names, not
// whether the bundle ref changed — see the two tests below for why that
// distinction is the whole design.
func TestPersistNamespaceConfig_RefusesABundleThisLauncherCannotRun(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	d.version = "2.12.2"
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": "", "2026.3": "2.13.0"})

	err := d.persistNamespaceConfig("ws1", "ns1", nsConfigYAML(t, "X", "2026.3"))

	var floor *errLauncherTooOld
	require.ErrorAs(t, err, &floor)
	assert.Equal(t, "2.13.0", floor.needs)
	assert.Equal(t, "2.12.2", floor.current)

	rows, listErr := d.store.ListNamespaces("ws1")
	require.NoError(t, listErr)
	assert.Empty(t, rows, "a refused write must leave nothing behind")
}

// THE WAY BACK. A namespace already bound to a too-new bundle — written by a
// newer launcher on the same machine, or by hand — must still be editable ONTO
// something this launcher can run. If the gate asked "did the ref change" this
// would be refused too, and the namespace would be a dead end.
func TestPersistNamespaceConfig_AllowsMovingOffATooNewBundle(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": "", "2026.3": "2.13.0"})

	// Written while no floor was enforced (an unknown launcher version enforces
	// nothing) — this stands in for "some newer launcher wrote it".
	d.version = ""
	require.NoError(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X", "2026.3")))

	d.version = "2.12.2"
	assert.NoError(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X", "2026.2")),
		"moving OFF a too-new bundle is the way out and must be allowed")

	assert.Error(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X renamed", "2026.3")),
		"an edit that KEEPS the too-new bundle is still refused")
}

// A bundle that is not on disk yields no refusal: there is nothing to read, and
// the create path refuses an unsynced LATEST with its own code.
func TestPersistNamespaceConfig_UnresolvableBundleIsNotARefusal(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	d.version = "2.12.2"
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": ""})

	assert.NoError(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X", "2099.1")))
}

// RULING: a symbolic LATEST is a policy, not a choice of bundle, so the write
// gate must never refuse it — not even in the degenerate case where NOTHING in
// the repo clears the floor and bundle.LatestRunnableBundle deliberately hands
// back the newest version anyway. Without this, a legacy namespace stored with
// LATEST could not even be RENAMED until someone pinned a concrete version.
func TestPersistNamespaceConfig_LatestIsNeverRefusedEvenWhenNothingClearsTheFloor(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	d.version = "2.12.2"
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": "2.13.0", "2026.3": "2.14.0"})

	assert.NoError(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X", "LATEST")),
		"a symbolic LATEST must never be refused by the floor gate")
}

// The gate must resolve through the operator's manual workspace-config delta,
// not just the git baseline — every OTHER resolver construction in the daemon
// chains .WithWorkspaceOverlay, and launcherFloorRefusal used to be the one
// that did not. Here the git baseline names no "community" bundleRepos entry
// at all; only the operator's stored delta adds it. Without the overlay,
// findBundleRepo fails, Resolve errors, and launcherFloorRefusal's
// `if err != nil { return nil }` reads that as "no refusal" — silently
// allowing the exact write the gate exists to stop. With the overlay chained,
// the delta is re-applied, the repo resolves, and the floor fires.
func TestPersistNamespaceConfig_ResolvesThroughTheOperatorWorkspaceDelta(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	d.version = "2.12.2"

	// Bundle files live under repo/community/ regardless of whether any
	// bundleRepos entry names that path yet.
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.3": "2.13.0"})
	// Overwrite the baseline written by writeFlooredWorkspace with one that
	// does NOT declare the "community" bundleRepos entry — that entry exists
	// only in the operator's delta, computed below.
	repoDir := config.WorkspaceRepoDir("ws1")
	baseline := "quickStartVariants: []\n"
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"), []byte(baseline), 0o600))

	edited := "quickStartVariants: []\n" +
		"bundleRepos:\n  - id: community\n    name: Community\n    path: community\n"
	edit, err := namespace.MakeFileEdit(workspaceConfigFile, []byte(baseline), []byte(edited))
	require.NoError(t, err)
	deltaJSON, err := json.Marshal(edit)
	require.NoError(t, err)
	require.NoError(t, d.store.SetStateValue(wsConfigDeltaKey("ws1"), string(deltaJSON)))

	err = d.persistNamespaceConfig("ws1", "ns1", nsConfigYAML(t, "X", "2026.3"))

	var floor *errLauncherTooOld
	require.ErrorAs(t, err, &floor,
		"the delta-only bundleRepos entry must still be seen by the gate")
	assert.Equal(t, "2.13.0", floor.needs)
}

// The HTTP shape of the refusal, and the promise that nothing is persisted.
// Modeled on TestCreateNamespace_LatestUnsyncedRepoRefused.
func TestCreateNamespace_PinnedTooNewBundleRefusedAndNothingPersisted(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	d.version = "2.12.2"
	writeFlooredWorkspace(t, "ws-target", map[string]string{"2026.2": "", "2026.3": "2.13.0"})

	body := `{"name":"X","authType":"BASIC","users":["admin"],` +
		`"bundleRepo":"community","bundleKey":"2026.3","workspaceId":"ws-target"}`
	req := httptest.NewRequest("POST", api.Namespaces, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), api.ErrCodeLauncherTooOld)
	assert.Contains(t, rec.Body.String(), "2.13.0",
		"the refusal must name the version to update to, or it is not actionable")

	rows, err := d.store.ListNamespaces("ws-target")
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused create must leave no namespace behind")
}

// Opening the edit dialog is a READ. Gating it would take away the only control
// that can move the namespace off the bundle it is stuck on.
func TestGetNamespaceEdit_StillOpensOnATooNewBundle(t *testing.T) {
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "wsMain", map[string]string{"2026.3": "2.13.0"})
	d.version = ""
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1",
		nsConfigYAML(t, "X", "2026.3")))
	d.version = "2.12.2"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/ns1/edit", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), api.ErrCodeLauncherTooOld)
}
