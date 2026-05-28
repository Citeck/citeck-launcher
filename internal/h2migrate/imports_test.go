package h2migrate

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// runImports drives the same import pipeline that Migrate uses but with a
// caller-supplied synthetic dump, so tests don't need a real MVStore file.
func runImports(homeDir string, maps map[string]map[string]string, store storage.Store) *MigrateResult {
	result := &MigrateResult{}
	importWorkspaces(maps, store, result)
	importNamespaces(homeDir, maps, store, result)
	importSecrets(maps, store, result)
	importRuntimeState(homeDir, maps, result)
	importGitRepos(maps, store, result)
	importState(maps, store)
	return result
}

func TestImports_SyntheticData(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	workspaceData := map[string]any{
		"name":       "Test Workspace",
		"repoUrl":    "https://github.com/test/repo.git",
		"repoBranch": "develop",
	}
	wsJSON, _ := json.Marshal(workspaceData)

	namespaceData := map[string]any{
		"name":      "My Namespace",
		"bundleRef": "community:2025.12",
		"authentication": map[string]any{
			"type": "KEYCLOAK",
		},
	}
	nsJSON, _ := json.Marshal(namespaceData)

	maps := map[string]map[string]string{
		"entities!workspace": {
			"ws-001": base64.StdEncoding.EncodeToString(wsJSON),
		},
		"entities/ws-001!namespace": {
			"ns-abc": base64.StdEncoding.EncodeToString(nsJSON),
		},
		"secrets!data": {
			"storage": "dGVzdC1ibG9i",
		},
		"launcher!state": {
			"selectedWorkspace": base64.StdEncoding.EncodeToString([]byte(`"ws-001"`)),
		},
		"workspace-state!ws-001": {
			"selectedNamespace": base64.StdEncoding.EncodeToString([]byte(`"ns-abc"`)),
		},
	}

	result := runImports(homeDir, maps, store)

	assert.Equal(t, 1, result.Workspaces)
	assert.Equal(t, 1, result.Namespaces)
	assert.Equal(t, 1, result.Secrets)
	assert.Equal(t, 0, result.Errors)

	ws, err := store.GetWorkspace("ws-001")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "Test Workspace", ws.Name)
	assert.Equal(t, "https://github.com/test/repo.git", ws.RepoURL)
	assert.Equal(t, "develop", ws.RepoBranch)

	nsConfigPath := filepath.Join(homeDir, "ws", "ws-001", "ns", "ns-abc", "namespace.yml")
	nsContent, err := os.ReadFile(nsConfigPath) //nolint:gosec // G304: test paths under t.TempDir()
	require.NoError(t, err)
	nsStr := string(nsContent)
	assert.Contains(t, nsStr, "ns-abc")
	assert.Contains(t, nsStr, "My Namespace")
	assert.Contains(t, nsStr, "community:2025.12")

	blob, err := store.GetSecretBlob()
	require.NoError(t, err)
	assert.Equal(t, "dGVzdC1ibG9i", blob)

	state, err := store.GetState()
	require.NoError(t, err)
	assert.Equal(t, "ws-001", state.WorkspaceID)
	assert.Equal(t, "ns-abc", state.NamespaceID())
}

func TestImportWorkspaces_DefaultsNameToID(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	wsData := map[string]any{"repoUrl": "https://example.com/repo.git"}
	wsJSON, _ := json.Marshal(wsData)

	maps := map[string]map[string]string{
		"entities!workspace": {
			"DEFAULT": base64.StdEncoding.EncodeToString(wsJSON),
		},
	}

	result := runImports(homeDir, maps, store)
	assert.Equal(t, 1, result.Workspaces)

	ws, err := store.GetWorkspace("DEFAULT")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "DEFAULT", ws.Name, "name should default to ID")
}

func TestImportNamespaces_SkipsVersionsAndRuntime(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	nsData := map[string]any{"name": "ns1", "bundleRef": "community:2025.12"}
	nsJSON, _ := json.Marshal(nsData)

	maps := map[string]map[string]string{
		"entities/ws1/versions!namespace": {
			"ns1": base64.StdEncoding.EncodeToString(nsJSON),
		},
		"entities/runtime!namespace": {
			"ns1": base64.StdEncoding.EncodeToString(nsJSON),
		},
		"entities/ws1!namespace": {
			"ns1": base64.StdEncoding.EncodeToString(nsJSON),
		},
	}

	result := runImports(homeDir, maps, store)
	assert.Equal(t, 1, result.Namespaces, "should only import the real namespace, not versions/runtime")
}

func TestParseWorkspaceJSON_AuthAndPullPeriod(t *testing.T) {
	raw := []byte(`{
		"name": "Private WS",
		"repoUrl": "https://git.example.com/private.git",
		"repoBranch": "main",
		"repoPullPeriod": "PT6H",
		"authType": "TOKEN"
	}`)

	dto, err := parseWorkspaceJSON("ws-1", raw)
	require.NoError(t, err)
	require.NotNil(t, dto)
	assert.Equal(t, "ws-1", dto.ID)
	assert.Equal(t, "Private WS", dto.Name)
	assert.Equal(t, "https://git.example.com/private.git", dto.RepoURL)
	assert.Equal(t, "main", dto.RepoBranch)
	assert.Equal(t, "PT6H", dto.RepoPullPeriod)
	assert.Equal(t, "TOKEN", dto.AuthType)
}

func TestParseWorkspaceJSON_PullPeriodAsSeconds(t *testing.T) {
	raw := []byte(`{"name":"X","repoPullPeriod":21600}`)
	dto, err := parseWorkspaceJSON("ws-2", raw)
	require.NoError(t, err)
	require.NotNil(t, dto)
	assert.Equal(t, "PT21600S", dto.RepoPullPeriod)
}

func TestParseWorkspaceJSON_OmittedFieldsDefaultEmpty(t *testing.T) {
	raw := []byte(`{"name":"Plain","repoUrl":"x","repoBranch":"y"}`)
	dto, err := parseWorkspaceJSON("ws-3", raw)
	require.NoError(t, err)
	require.NotNil(t, dto)
	assert.Empty(t, dto.RepoPullPeriod)
	assert.Empty(t, dto.AuthType)
}

func TestImports_RoundTripsAuthAndPullPeriod(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	wsData := map[string]any{
		"name":           "Tokened",
		"repoUrl":        "https://git.example.com/p.git",
		"repoBranch":     "main",
		"repoPullPeriod": "PT6H",
		"authType":       "TOKEN",
	}
	wsJSON, _ := json.Marshal(wsData)

	maps := map[string]map[string]string{
		"entities!workspace": {
			"ws-priv": base64.StdEncoding.EncodeToString(wsJSON),
		},
	}

	result := runImports(homeDir, maps, store)
	assert.Equal(t, 1, result.Workspaces)

	ws, err := store.GetWorkspace("ws-priv")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "PT6H", ws.RepoPullPeriod)
	assert.Equal(t, "TOKEN", ws.AuthType)
}

// TestImportNamespacesPreservesAllFields verifies that the full ten-field
// Kotlin NamespaceConfig shape (including webapps customizations) is forwarded
// into the migrated namespace.yml without loss.
func TestImportNamespacesPreservesAllFields(t *testing.T) {
	homeDir := t.TempDir()

	nsData := map[string]any{
		"name":      "Full NS",
		"snapshot":  "snap-v1",
		"template":  "default",
		"bundleRef": "community:2025.12",
		"authentication": map[string]any{
			"type":  "BASIC",
			"users": []string{"admin", "fet"},
		},
		"pgAdmin": map[string]any{
			"enabled": true,
			"image":   "dpage/pgadmin4:7.5",
		},
		"mongodb": map[string]any{
			"image": "mongo:6.0",
		},
		"citeckProxy": map[string]any{
			"image": "caddy:2.7",
		},
		"webapps": map[string]any{
			"eapps": map[string]any{
				"enabled":     true,
				"image":       "citeck/ecos-apps:1.2.3",
				"debugPort":   5005,
				"heapSize":    "512m",
				"memoryLimit": "1g",
				"serverPort":  8080,
				"javaOpts":    "-XX:+UseG1GC",
				"environments": map[string]any{
					"FOO": "bar",
				},
				"springProfiles": "prod,extra",
				"cloudConfig": map[string]any{
					"spring": map[string]any{
						"datasource": map[string]any{
							"url": "jdbc:postgresql://postgres/eapps",
						},
					},
				},
				"dataSources": map[string]any{
					"main": map[string]any{
						"url": "jdbc:postgresql://postgres/eapps",
						"xa":  true,
					},
				},
			},
		},
	}
	nsJSON, _ := json.Marshal(nsData)

	maps := map[string]map[string]string{
		"entities/ws1!namespace": {
			"ns-full": base64.StdEncoding.EncodeToString(nsJSON),
		},
	}

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	result := runImports(homeDir, maps, store)
	assert.Equal(t, 1, result.Namespaces)

	nsConfigPath := filepath.Join(homeDir, "ws", "ws1", "ns", "ns-full", "namespace.yml")
	body, readErr := os.ReadFile(nsConfigPath) //nolint:gosec // G304: test paths under t.TempDir()
	require.NoError(t, readErr)
	yamlStr := string(body)

	for _, want := range []string{
		"id: ns-full",
		"Full NS",
		"snap-v1",
		"template: default",
		"community:2025.12",
		"BASIC",
		"admin",
		"fet",
		"dpage/pgadmin4",
		"mongo:6.0",
		"proxy:",
		"caddy:2.7",
		"eapps",
		"5005",
		"512m",
		"1g",
		"-XX:+UseG1GC",
		"jdbc:postgresql://postgres/eapps",
		"springProfiles: prod,extra",
	} {
		assert.Contains(t, yamlStr, want, "missing field: %q", want)
	}
	assert.NotContains(t, yamlStr, "citeckProxy")
}

func TestImportRuntimeStatePreservesDetachAndEdits(t *testing.T) {
	homeDir := t.TempDir()

	editedDef := map[string]any{
		"name":         "eapps",
		"image":        "citeck/ecos-apps:edited",
		"environments": map[string]any{"OVERRIDE": "1"},
		"dependsOn":    []string{"postgres"},
		"kind":         "CITECK_CORE",
		"shmSize":      "64m",
		"initActions":  []map[string]any{{"type": "exec-shell", "command": "echo edit"}},
	}
	editedAppsBlob, _ := json.Marshal(map[string]any{"eapps": editedDef})

	manualStopped, _ := json.Marshal([]string{"alfresco", "onlyoffice"})
	locked, _ := json.Marshal([]string{"eapps"})

	bundleDefBlob, _ := json.Marshal(map[string]any{
		"key": "release/2025.1.0",
		"applications": map[string]any{
			"eapps": map[string]any{"image": "citeck/ecos-apps:2025.1.0"},
		},
		"citeckApps": []map[string]any{
			{"image": "citeck/ecos-apps:2025.1.0"},
		},
		"content": map[string]any{"name": "release/2025.1.0"},
	})

	maps := map[string]map[string]string{
		"namespace-runtime-state!ws1:nsA": {
			"manualStoppedApps":   base64.StdEncoding.EncodeToString(manualStopped),
			"editedAndLockedApps": base64.StdEncoding.EncodeToString(locked),
			"editedApps":          base64.StdEncoding.EncodeToString(editedAppsBlob),
			"bundleDef":           base64.StdEncoding.EncodeToString(bundleDefBlob),
		},
		"namespace-runtime-state!ws1:nsA/changedRuntimeFiles": {
			"proxy/Caddyfile":  base64.StdEncoding.EncodeToString([]byte("# edited\n")),
			"keycloak/init.sh": base64.StdEncoding.EncodeToString([]byte("#!/bin/sh\nexec /init\n")),
		},
	}

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	runImports(homeDir, maps, store)

	volumesBase := filepath.Join(homeDir, "ws", "ws1", "ns", "nsA", "rtfiles")
	statePath := filepath.Join(volumesBase, "state-nsA.json")
	stateBytes, err := os.ReadFile(statePath) //nolint:gosec // G304: test paths under t.TempDir()
	require.NoError(t, err, "state-nsA.json should be written")

	var state struct {
		ManualStoppedApps []string                  `json:"manualStoppedApps"`
		EditedLockedApps  []string                  `json:"editedLockedApps"`
		EditedApps        map[string]map[string]any `json:"editedApps"`
		EditedFiles       []string                  `json:"editedFiles"`
		CachedBundle      map[string]any            `json:"cachedBundle"`
	}
	require.NoError(t, json.Unmarshal(stateBytes, &state))

	assert.ElementsMatch(t, []string{"alfresco", "onlyoffice"}, state.ManualStoppedApps)
	assert.ElementsMatch(t, []string{"eapps"}, state.EditedLockedApps)
	require.Contains(t, state.EditedApps, "eapps")
	assert.Equal(t, "citeck/ecos-apps:edited", state.EditedApps["eapps"]["image"])
	deps, ok := state.EditedApps["eapps"]["dependsOn"].(map[string]any)
	require.True(t, ok, "dependsOn must be an object")
	assert.True(t, deps["postgres"].(bool))

	require.NotNil(t, state.CachedBundle)
	keyMap, ok := state.CachedBundle["key"].(map[string]any)
	require.True(t, ok, "cachedBundle.key must be an object on Go side")
	assert.Equal(t, "release/2025.1.0", keyMap["version"])
	apps, ok := state.CachedBundle["applications"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, apps, "eapps")
	assert.Equal(t, "citeck/ecos-apps:2025.1.0", apps["eapps"].(map[string]any)["image"])

	// Typed-field assertion on bundle.Def directly. The map[string]any
	// inspection above only proves the round-trip JSON shape; if Def.Key's
	// JSON tag ever drifted from "version" the map assertion would catch
	// it, but the value-level invariant is clearer when read from the
	// typed struct.
	var typedState struct {
		CachedBundle *bundle.Def `json:"cachedBundle"`
	}
	require.NoError(t, json.Unmarshal(stateBytes, &typedState))
	require.NotNil(t, typedState.CachedBundle)
	assert.Equal(t, "release/2025.1.0", typedState.CachedBundle.Key.Version)
	assert.Equal(t, "citeck/ecos-apps:2025.1.0", typedState.CachedBundle.Applications["eapps"].Image)

	assert.ElementsMatch(t, []string{"proxy/Caddyfile", "keycloak/init.sh"}, state.EditedFiles)

	caddy, err := os.ReadFile(filepath.Join(volumesBase, "proxy", "Caddyfile")) //nolint:gosec // G304: test paths under t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "# edited\n", string(caddy))
	init, err := os.ReadFile(filepath.Join(volumesBase, "keycloak", "init.sh")) //nolint:gosec // G304: test paths under t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\nexec /init\n", string(init))
}

func TestImportRuntimeStateRejectsPathTraversal(t *testing.T) {
	homeDir := t.TempDir()

	maps := map[string]map[string]string{
		"namespace-runtime-state!ws1:nsA/changedRuntimeFiles": {
			"../../etc/evil": base64.StdEncoding.EncodeToString([]byte("pwned")),
			"good/file":      base64.StdEncoding.EncodeToString([]byte("ok")),
		},
	}

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	runImports(homeDir, maps, store)

	volumesBase := filepath.Join(homeDir, "ws", "ws1", "ns", "nsA", "rtfiles")
	good, err := os.ReadFile(filepath.Join(volumesBase, "good", "file")) //nolint:gosec // G304: test paths under t.TempDir()
	require.NoError(t, err)
	assert.Equal(t, "ok", string(good))

	_, err = os.Stat(filepath.Join(homeDir, "etc", "evil"))
	assert.True(t, os.IsNotExist(err), "traversal path must not be created")
}

func TestImportState_MultiWorkspaceCoversAllMaps(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	maps := map[string]map[string]string{
		"launcher!state": {
			"selectedWorkspace": base64.StdEncoding.EncodeToString([]byte(`"ws-active"`)),
		},
		"workspace-state!ws-active": {
			"selectedNamespace": base64.StdEncoding.EncodeToString([]byte(`"ns-active"`)),
		},
		"workspace-state!ws-other-1": {
			"selectedNamespace": base64.StdEncoding.EncodeToString([]byte(`"ns-other-1"`)),
		},
		"workspace-state!ws-other-2": {
			"selectedNamespace": base64.StdEncoding.EncodeToString([]byte(`"ns-other-2"`)),
		},
	}

	runImports(homeDir, maps, store)

	state, err := store.GetState()
	require.NoError(t, err)
	assert.Equal(t, "ws-active", state.WorkspaceID)
	assert.Equal(t, "ns-active", state.SelectedNs["ws-active"])
	assert.Equal(t, "ns-other-1", state.SelectedNs["ws-other-1"])
	assert.Equal(t, "ns-other-2", state.SelectedNs["ws-other-2"])
}

func TestImportState_OnlyOtherWorkspaceSelectionsNoActive(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	maps := map[string]map[string]string{
		"workspace-state!ws-1": {
			"selectedNamespace": base64.StdEncoding.EncodeToString([]byte(`"ns-1"`)),
		},
	}

	runImports(homeDir, maps, store)

	state, err := store.GetState()
	require.NoError(t, err)
	assert.Empty(t, state.WorkspaceID)
	assert.Equal(t, "ns-1", state.SelectedNs["ws-1"])
}

func TestImportGitRepos_WritesRows(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	entry1, _ := json.Marshal(map[string]any{
		"repoProps":        map[string]any{"url": "https://example.com/r1.git", "branch": "main"},
		"lastSyncTimeMs":   int64(1700000000000),
		"hashOfLastCommit": "abc123",
	})
	entry2, _ := json.Marshal(map[string]any{
		"repoProps":        map[string]any{"url": "https://example.com/r2.git", "branch": "develop"},
		"lastSyncTimeMs":   int64(1700100000000),
		"hashOfLastCommit": "def456",
	})

	maps := map[string]map[string]string{
		"git-repo!instances": {
			"ws/team-a/repo":                base64.StdEncoding.EncodeToString(entry1),
			"ws/team-a/bundles/community-1": base64.StdEncoding.EncodeToString(entry2),
		},
	}

	result := runImports(homeDir, maps, store)
	assert.Equal(t, 2, result.GitRepos)

	st1, err := store.GetGitRepoState("ws/team-a/repo")
	require.NoError(t, err)
	require.NotNil(t, st1)
	assert.Equal(t, int64(1700000000000), st1.LastSyncMs)
	assert.Equal(t, "abc123", st1.LastCommitHash)

	st2, err := store.GetGitRepoState("ws/team-a/bundles/community-1")
	require.NoError(t, err)
	require.NotNil(t, st2)
	assert.Equal(t, int64(1700100000000), st2.LastSyncMs)
	assert.Equal(t, "def456", st2.LastCommitHash)

	all, err := store.ListGitRepoStates()
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestImportGitRepos_SkipsZeroTimestamp(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	zeroEntry, _ := json.Marshal(map[string]any{
		"lastSyncTimeMs":   int64(0),
		"hashOfLastCommit": "",
	})
	maps := map[string]map[string]string{
		"git-repo!instances": {
			"ws/x/repo": base64.StdEncoding.EncodeToString(zeroEntry),
		},
	}

	result := runImports(homeDir, maps, store)
	assert.Equal(t, 0, result.GitRepos)
}

func TestNeedsMigration_OnlyH2(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "storage.db"), []byte("h2"), 0o644))

	needed, err := NeedsMigration(homeDir)
	require.NoError(t, err)
	assert.True(t, needed)
}

func TestNeedsMigration_NeitherFile(t *testing.T) {
	homeDir := t.TempDir()

	needed, err := NeedsMigration(homeDir)
	require.NoError(t, err)
	assert.False(t, needed)
}

func TestNeedsMigration_BothFiles(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "storage.db"), []byte("h2"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "launcher.db"), []byte("sqlite"), 0o644))

	needed, err := NeedsMigration(homeDir)
	require.NoError(t, err)
	assert.False(t, needed)
}
