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
	"gopkg.in/yaml.v3"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

const (
	csProvider = "seaweedfs"
	// The three bundles of the test repo: one that can store content in S3,
	// one whose release stopped naming the provider, and a Community one.
	csBundleS3      = "2026.3"
	csBundleNoProv  = "2026.2"
	csBundleNoCont  = "2026.1"
	csWorkspaceYAML = `bundleRepos:
  - id: community
    name: Community
    path: community
contentStorage:
  s3:
    app: seaweedfs
    url: http://seaweedfs:8333
    bucket: ecos-content
    accessKey: citeck
    secretKey: ${secret:seaweedfs-s3}
secrets:
  - id: seaweedfs-s3
    value: seaweedfs-s3-secret
additionalApps:
  - name: seaweedfs
    environments:
      AWS_SECRET_ACCESS_KEY: ${secret:seaweedfs-s3}
namespaceTemplates:
  - id: default
    detachedApps: [seaweedfs, ai]
`
)

// writeContentStorageWorkspace lays out a workspace repo whose `community`
// bundle repo IS the workspace repo (the shape writeFlooredWorkspace uses),
// with the S3 provider declared the way the enterprise workspace declares it.
func writeContentStorageWorkspace(t *testing.T, wsID string) {
	t.Helper()
	repoDir := config.WorkspaceRepoDir(wsID)
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "community"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"), []byte(csWorkspaceYAML), 0o600))
	bundles := map[string]string{
		csBundleS3: "content:\n  image: enterprise/ecos-content:3.1.0\n" +
			"dependencies:\n  seaweedfs:\n    image: chrislusf/seaweedfs:4.48\n",
		csBundleNoProv: "content:\n  image: enterprise/ecos-content:3.1.0\n",
		csBundleNoCont: "EcosModelApp:\n  image: core/ecos-model:1.0\n",
	}
	for key, body := range bundles {
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "community", key+".yaml"), []byte(body), 0o600))
	}
}

// csConfigYAML is a minimal valid namespace config on one bundle with one
// content storage type ("" writes no block at all — every config in the field).
func csConfigYAML(t *testing.T, name, key string, typ namespace.ContentStorageType) []byte {
	t.Helper()
	cfg := namespace.DefaultNamespaceConfig()
	cfg.ID = "ns1"
	cfg.Name = name
	cfg.BundleRef = bundle.Ref{Repo: "community", Key: key}
	if typ != "" {
		cfg.ContentStorage = &namespace.ContentStorageProps{Type: typ}
	}
	raw, err := namespace.MarshalNamespaceConfig(&cfg)
	require.NoError(t, err)
	return raw
}

// parseCSWorkspace reads the test workspace config the way the resolver does.
func parseCSWorkspace(t *testing.T) *bundle.WorkspaceConfig {
	t.Helper()
	var ws bundle.WorkspaceConfig
	require.NoError(t, yaml.Unmarshal([]byte(csWorkspaceYAML), &ws))
	require.NotNil(t, ws.ContentStorageS3())
	return &ws
}

func newContentStorageDaemon(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	config.SetDesktopMode(true)
	t.Cleanup(config.ResetDesktopMode)
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	writeContentStorageWorkspace(t, "wsMain")
	return d, mux
}

func csRefusal(t *testing.T, err error) *errContentStorageRefused {
	t.Helper()
	var cs *errContentStorageRefused
	require.ErrorAs(t, err, &cs)
	return cs
}

// --- the write gate ---

// S3 is one-way, whatever the write path says: an explicit DB and a config
// that simply has no block any more are the same downgrade.
func TestContentStorageGate_RefusesTheWayBackToDB(t *testing.T) {
	d, _ := newContentStorageDaemon(t)
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1",
		csConfigYAML(t, "X", csBundleS3, namespace.ContentStorageS3)))

	for name, typ := range map[string]namespace.ContentStorageType{
		"explicit DB":       namespace.ContentStorageDB,
		"the block dropped": "",
	} {
		t.Run(name, func(t *testing.T) {
			err := d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", csBundleS3, typ))
			assert.Equal(t, api.ErrCodeContentStorageDowngrade, csRefusal(t, err).code)
		})
	}
	stored, err := d.loadNamespaceConfigFromStore("wsMain", "ns1")
	require.NoError(t, err)
	assert.Equal(t, namespace.ContentStorageS3, stored.ContentStorageType(), "nothing was written")
}

// S3 may be CHOSEN only where it works, and the refusal names why.
func TestContentStorageGate_ChoosingS3NeedsABundleThatCanHaveIt(t *testing.T) {
	d, _ := newContentStorageDaemon(t)
	for key, reason := range map[string]string{
		csBundleNoCont: namespace.ContentStorageNoContentApp,
		csBundleNoProv: namespace.ContentStorageNoProviderImage,
		"2099.9":       api.ContentStorageBundleUnresolved,
	} {
		t.Run(key, func(t *testing.T) {
			err := d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", key, namespace.ContentStorageS3))
			cs := csRefusal(t, err)
			assert.Equal(t, api.ErrCodeContentStorageUnavailable, cs.code)
			assert.Equal(t, reason, cs.reason)
		})
	}
	assert.NoError(t, d.persistNamespaceConfig("wsMain", "ns1",
		csConfigYAML(t, "X", csBundleS3, namespace.ContentStorageS3)))
}

// A DB namespace is untouched by the gate on any bundle, including one that is
// not on disk — the edit path deliberately keeps accepting those.
func TestContentStorageGate_ADBNamespaceIsNeverAsked(t *testing.T) {
	d, _ := newContentStorageDaemon(t)
	for _, key := range []string{csBundleNoCont, "2099.9"} {
		assert.NoError(t, d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", key, "")))
	}
}

// Keeping S3 and the bundle is not a choice being made: a rename must pass even
// when the workspace has changed under the namespace — S3 cannot be undone, so
// refusing it would leave the namespace uneditable. Moving it to a bundle
// without the provider IS a choice, and is refused.
func TestContentStorageGate_KeepingS3AsksOnlyWhenTheBundleMoves(t *testing.T) {
	d, _ := newContentStorageDaemon(t)
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1",
		csConfigYAML(t, "X", csBundleS3, namespace.ContentStorageS3)))

	// The workspace stops declaring the provider.
	repoDir := config.WorkspaceRepoDir("wsMain")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: community\n    name: Community\n    path: community\n"), 0o600))
	assert.NoError(t, d.persistNamespaceConfig("wsMain", "ns1",
		csConfigYAML(t, "X renamed", csBundleS3, namespace.ContentStorageS3)),
		"a rename on the same bundle must pass")

	writeContentStorageWorkspace(t, "wsMain")
	err := d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", csBundleNoProv, namespace.ContentStorageS3))
	assert.Equal(t, namespace.ContentStorageNoProviderImage, csRefusal(t, err).reason)
}

// The stored side is read LENIENTLY: a stored S3 config that no longer passes
// validation must still be seen as S3, or a raw PUT fixing it could silently
// drop the one-way flag on the way.
func TestContentStorageGate_SeesS3InAnInvalidStoredConfig(t *testing.T) {
	d, _ := newContentStorageDaemon(t)
	broken := "id: ns1\nproxy:\n  port: 0\nbundleRef: community:2026.3\ncontentStorage:\n  type: S3\n"
	require.NoError(t, d.store.SaveNamespaceConfig("wsMain", "ns1", "X", broken))
	err := d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", csBundleS3, ""))
	assert.Equal(t, api.ErrCodeContentStorageDowngrade, csRefusal(t, err).code)
}

// The refusal is the daemon's sentence, rendered in the reader's language.
func TestContentStorageGate_RefusalFollowsTheRequestLocale(t *testing.T) {
	d, mux := newContentStorageDaemon(t)
	d.activeNs.nsConfig = &namespace.Config{ID: "ns1"}
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1",
		csConfigYAML(t, "X", csBundleS3, namespace.ContentStorageS3)))

	put := func(locale string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, api.Config, strings.NewReader(string(csConfigYAML(t, "X", csBundleS3, ""))))
		req.Header.Set(api.LocaleHeader, locale)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	en, ru := put("en"), put("ru")
	require.Equal(t, http.StatusConflict, en.Code, en.Body.String())
	assert.Contains(t, en.Body.String(), api.ErrCodeContentStorageDowngrade)
	assert.Contains(t, en.Body.String(), "cannot be switched back to the database")
	require.Equal(t, http.StatusConflict, ru.Code)
	assert.Contains(t, ru.Body.String(), "вернуть хранение в базу данных нельзя")
}

// --- create / edit ---

func TestCreateNamespace_ContentStorage(t *testing.T) {
	create := func(mux *http.ServeMux, key, storage string) *httptest.ResponseRecorder {
		body := `{"name":"X","authType":"BASIC","users":["admin"],"bundleRepo":"community",` +
			`"bundleKey":"` + key + `","workspaceId":"wsMain","contentStorage":"` + storage + `"}`
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.Namespaces, strings.NewReader(body)))
		return rec
	}

	t.Run("S3 on a bundle that can have it", func(t *testing.T) {
		d, mux := newContentStorageDaemon(t)
		rec := create(mux, csBundleS3, "s3")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		rows, err := d.store.ListNamespaces("wsMain")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		cfg, err := d.loadNamespaceConfigFromStore("wsMain", rows[0].ID)
		require.NoError(t, err)
		assert.Equal(t, namespace.ContentStorageS3, cfg.ContentStorageType())
	})

	t.Run("S3 on a Community bundle is refused and nothing persisted", func(t *testing.T) {
		d, mux := newContentStorageDaemon(t)
		rec := create(mux, csBundleNoCont, "S3")
		require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), api.ErrCodeContentStorageUnavailable)
		rows, err := d.store.ListNamespaces("wsMain")
		require.NoError(t, err)
		assert.Empty(t, rows)
	})

	t.Run("DB writes no block", func(t *testing.T) {
		d, mux := newContentStorageDaemon(t)
		rec := create(mux, csBundleS3, "DB")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		rows, err := d.store.ListNamespaces("wsMain")
		require.NoError(t, err)
		require.Len(t, rows, 1)
		raw, _, err := d.store.LoadNamespaceConfig("wsMain", rows[0].ID)
		require.NoError(t, err)
		assert.NotContains(t, raw, "contentStorage")
	})

	t.Run("an unknown type is a 400", func(t *testing.T) {
		_, mux := newContentStorageDaemon(t)
		rec := create(mux, csBundleS3, "minio")
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})
}

func putEdit(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, api.NamespaceEditPath("ns1"), strings.NewReader(body)))
	return rec
}

func TestNamespaceEdit_ContentStorageIsReportedAndOneWay(t *testing.T) {
	d, mux := newContentStorageDaemon(t)
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", csBundleS3, "")))

	get := func() api.NamespaceEditDto {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.NamespaceEditPath("ns1"), http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var dto api.NamespaceEditDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		return dto
	}
	assert.Equal(t, "DB", get().ContentStorage, "absent is reported as the effective DB")

	rec := putEdit(mux, `{"name":"X"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "DB", get().ContentStorage, "an empty value leaves it unchanged")

	rec = putEdit(mux, `{"contentStorage":"S3"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "S3", get().ContentStorage)

	rec = putEdit(mux, `{"contentStorage":"DB"}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), api.ErrCodeContentStorageDowngrade)
	assert.Equal(t, "S3", get().ContentStorage)
}

// DB → S3 on a namespace that is not the active one: its provider, held back
// by the template, is re-attached in its persisted state — what its next load
// reads. Only the provider: every other detach stays the operator's.
func TestNamespaceEdit_SwitchingToS3ReattachesTheProviderOnce(t *testing.T) {
	d, mux := newContentStorageDaemon(t)
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", csBundleS3, "")))
	state, err := json.Marshal(namespace.NsPersistedState{
		Status:            namespace.NsStatusStopped,
		ManualStoppedApps: []string{csProvider, "ai"},
	})
	require.NoError(t, err)
	require.NoError(t, d.store.SaveNamespaceState("wsMain", "ns1", string(namespace.NsStatusStopped), string(state)))

	rec := putEdit(mux, `{"contentStorage":"S3"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	st := loadNsStateFromStore(d.store, "wsMain", "ns1")
	require.NotNil(t, st)
	assert.Equal(t, []string{"ai"}, st.ManualStoppedApps)

	// Once: an operator who stops the provider again is honored — a later
	// edit that keeps S3 does not undo it.
	state, err = json.Marshal(namespace.NsPersistedState{
		Status: namespace.NsStatusStopped, ManualStoppedApps: []string{csProvider},
	})
	require.NoError(t, err)
	require.NoError(t, d.store.SaveNamespaceState("wsMain", "ns1", string(namespace.NsStatusStopped), string(state)))
	rec = putEdit(mux, `{"name":"Y","contentStorage":"S3"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{csProvider}, loadNsStateFromStore(d.store, "wsMain", "ns1").ManualStoppedApps)
}

// The active namespace is re-attached through its runtime.
func TestReattachRequiredApps_ActiveNamespaceGoesThroughTheRuntime(t *testing.T) {
	d, _ := newContentStorageDaemon(t)
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetManualStoppedApps(map[string]bool{csProvider: true, "ai": true})
	ws := parseCSWorkspace(t)
	d.activeNs.runtime = rt
	d.activeNs.nsConfig = &namespace.Config{ID: "ns1"}
	d.activeNs.workspaceConfig = ws

	cfg := &namespace.Config{ID: "ns1", ContentStorage: &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}}
	d.reattachRequiredApps("wsMain", "ns1", cfg)
	assert.Equal(t, map[string]bool{"ai": true}, rt.ManualStoppedApps())
}

// The raw config PUT switching DB → S3 re-attaches and reloads, like the
// dialog; every other raw save keeps its save-only shape.
func TestPutConfig_SwitchingToS3ReattachesAndReloads(t *testing.T) {
	d, mux := newContentStorageDaemon(t)
	d.activeNs.nsConfig = &namespace.Config{ID: "ns1"}
	reloads := 0
	d.reloadFn = func() error { reloads++; return nil }
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1", csConfigYAML(t, "X", csBundleS3, "")))
	state, err := json.Marshal(namespace.NsPersistedState{
		Status: namespace.NsStatusStopped, ManualStoppedApps: []string{csProvider},
	})
	require.NoError(t, err)
	require.NoError(t, d.store.SaveNamespaceState("wsMain", "ns1", string(namespace.NsStatusStopped), string(state)))

	put := func(body []byte) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, api.Config, strings.NewReader(string(body))))
		return rec
	}
	rec := put(csConfigYAML(t, "renamed", csBundleS3, ""))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Zero(t, reloads, "an ordinary raw save does not reload")

	rec = put(csConfigYAML(t, "renamed", csBundleS3, namespace.ContentStorageS3))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, reloads)
	assert.Empty(t, loadNsStateFromStore(d.store, "wsMain", "ns1").ManualStoppedApps)
}

// --- detach rules on load and reload ---

// A NEW S3 namespace starts its provider with the namespace, whatever the
// template holds back; a DB namespace keeps the template's answer, and the
// operator's own set is never filtered.
func TestDetachedAppsOnLoad_SubtractsRequiredApps(t *testing.T) {
	ws := parseCSWorkspace(t)
	s3 := &namespace.Config{Template: "default", ContentStorage: &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}}
	db := &namespace.Config{Template: "default"}

	assert.Equal(t, map[string]bool{"ai": true}, detachedAppsOnLoad(nil, ws, s3))
	assert.Equal(t, map[string]bool{"ai": true, csProvider: true}, detachedAppsOnLoad(nil, ws, db))

	persisted := &namespace.NsPersistedState{ManualStoppedApps: []string{csProvider}}
	assert.Equal(t, map[string]bool{csProvider: true}, detachedAppsOnLoad(persisted, ws, s3),
		"an operator's stop of the provider is honored")
}

// The new-app decision (load AND reload path) reads the template the same way:
// a provider that arrives with a bundle on an S3 namespace is not held back.
func TestDecideNewAppDetach_SubtractsRequiredApps(t *testing.T) {
	ws := parseCSWorkspace(t)
	generated := []appdef.ApplicationDef{{Name: "emodel"}, {Name: csProvider}}
	absent := func(string) namespace.AppPresence { return namespace.AppPresenceAbsent }
	known := []string{"emodel"}

	s3 := &namespace.Config{Template: "default", ContentStorage: &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}}
	res := decideNewAppDetach(known, generated, s3, ws, nil, absent)
	assert.NotContains(t, res.Detach, csProvider)
	assert.Contains(t, res.Known, csProvider)

	db := &namespace.Config{Template: "default"}
	res = decideNewAppDetach(known, generated, db, ws, nil, absent)
	assert.Contains(t, res.Detach, csProvider)
}

// --- options, retry, DTO ---

func TestContentStorageOptions(t *testing.T) {
	_, mux := newContentStorageDaemon(t)
	ask := func(key string) api.ContentStorageOptionsDto {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
			api.ContentStorageOptions+"?bundleRepo=community&bundleKey="+key, http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var dto api.ContentStorageOptionsDto
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
		return dto
	}
	assert.Equal(t, api.ContentStorageOptionDto{Available: true, ProviderApp: csProvider}, ask(csBundleS3).S3)
	assert.Equal(t, namespace.ContentStorageNoContentApp, ask(csBundleNoCont).S3.Reason)
	assert.Equal(t, namespace.ContentStorageNoProviderImage, ask(csBundleNoProv).S3.Reason)
	unresolved := ask("2099.9").S3
	assert.False(t, unresolved.Available)
	assert.Equal(t, api.ContentStorageBundleUnresolved, unresolved.Reason)
}

func TestContentStorageProvisionRoute(t *testing.T) {
	d, mux := newContentStorageDaemon(t)
	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.NamespaceContentStorageProvision, http.NoBody))
		return rec
	}
	assert.Equal(t, http.StatusNotFound, post().Code, "no namespace, nothing to provision")

	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	d.activeNs.runtime = rt
	d.activeNs.nsConfig = &namespace.Config{ID: "ns1"}
	rec := post()
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeContentStorageNotS3)

	d.activeNs.nsConfig = &namespace.Config{ID: "ns1", ContentStorage: &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}}
	assert.Equal(t, http.StatusAccepted, post().Code)
}

func TestNamespaceDto_ContentStorage(t *testing.T) {
	d, _ := newContentStorageDaemon(t)
	ws := parseCSWorkspace(t)
	tr := englishForLogs

	act := activeNamespace{nsConfig: &namespace.Config{ID: "ns1"}, workspaceConfig: ws}
	assert.Nil(t, d.contentStorageDto(tr, act), "a DB namespace reports nothing")

	act.nsConfig = &namespace.Config{ID: "ns1", ContentStorage: &namespace.ContentStorageProps{Type: namespace.ContentStorageS3}}
	act.contentStorageProblem = &namespace.ContentStorageProblem{
		Reason: namespace.ContentStorageProviderNotGenerated, ProviderApp: csProvider,
	}
	got := d.contentStorageDto(tr, act)
	require.NotNil(t, got)
	assert.Equal(t, api.ContentStorageDto{
		Type: "S3", ProviderApp: csProvider, State: api.ContentStorageStatePending,
		Problem: namespace.ContentStorageProviderNotGenerated,
	}, *got)
}

// Status is namespace-pinned: another namespace's verdict never shows, and a
// Reset forgets it.
func TestContentStorageProvisioner_StatusIsNamespacePinned(t *testing.T) {
	p := (&Daemon{}).contentStorageProv()
	p.mu.Lock()
	p.nsID, p.status = "ns1", ContentStorageStatus{State: api.ContentStorageStateConnected}
	p.mu.Unlock()
	assert.Equal(t, api.ContentStorageStateConnected, p.Status("ns1").State)
	assert.Equal(t, api.ContentStorageStatePending, p.Status("ns2").State)
	p.Reset()
	assert.Equal(t, api.ContentStorageStatePending, p.Status("ns1").State)
}
