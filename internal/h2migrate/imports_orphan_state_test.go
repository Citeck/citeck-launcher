package h2migrate

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nsEntity(t *testing.T, nsID, name string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": nsID, "name": name})
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(raw)
}

// TestOrphanedRuntimeStateDoesNotCreateAPhantomNamespace pins the "migration
// added junk" report.
//
// Deleting a namespace in 1.x removes only the runtime-state DATA repo —
// NamespacesService's delete listener calls deleteRepo(scope, "<ws>:<ns>") and
// never touches the "<ws>:<ns>/changedRuntimeFiles" inner repo — so every
// namespace a user ever deleted leaves its file-overlay map behind forever.
// importRuntimeState took the UNION of both map kinds, and SaveNamespaceState
// INSERTs a row, so each of those orphans came back as a namespace with no
// name, no bundle and no config: the list showed it as a bare id and opening it
// answered `namespace "..." not found in workspace "..."`. One real store had
// 11 of them.
func TestOrphanedRuntimeStateDoesNotCreateAPhantomNamespace(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	edited := base64.StdEncoding.EncodeToString([]byte("# edited\n"))
	maps := map[string]map[string]string{
		"entities/ws1!namespace": {
			"nsLive": nsEntity(t, "nsLive", "Live namespace"),
		},
		"namespace-runtime-state!ws1:nsLive/changedRuntimeFiles": {
			"proxy/Caddyfile": edited,
		},
		// Deleted in 1.x: the data repo is gone, the overlay map is not.
		"namespace-runtime-state!ws1:nsGone/changedRuntimeFiles": {
			"proxy/Caddyfile": edited,
		},
	}

	runImports(t, homeDir, maps, store)

	rows, err := store.ListNamespaces("ws1")
	require.NoError(t, err)
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"nsLive"}, ids,
		"a runtime-state map with no namespace config is an orphan, not a namespace")

	_, ok, err := store.LoadNamespaceState("ws1", "nsGone")
	require.NoError(t, err)
	assert.False(t, ok)
}

// TestRuntimeStateBindsToTheConfigsWorkspaceSpelling covers the legacy default
// workspace, where the two halves disagree on case: Kotlin's WORKSPACE_ALIASES
// makes the entity scope "entities/DEFAULT!namespace" while the runtime-state
// repo key stays "default:<ns>". Matching them exactly would file the state
// under a second, config-less "default" row — a phantom AND a real namespace
// robbed of its detach/bundle state.
func TestRuntimeStateBindsToTheConfigsWorkspaceSpelling(t *testing.T) {
	homeDir := t.TempDir()
	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	manualStopped, err := json.Marshal([]string{"onlyoffice"})
	require.NoError(t, err)

	maps := map[string]map[string]string{
		"entities/DEFAULT!namespace": {
			"nsA": nsEntity(t, "nsA", "Legacy default"),
		},
		"namespace-runtime-state!default:nsA": {
			"manualStoppedApps": base64.StdEncoding.EncodeToString(manualStopped),
		},
	}

	runImports(t, homeDir, maps, store)

	assert.Empty(t, mustListNamespaceIDs(t, store, "default"),
		"the lowercase alias must not become a second workspace of phantom rows")

	stateJSON, ok, err := store.LoadNamespaceState("DEFAULT", "nsA")
	require.NoError(t, err)
	require.True(t, ok, "state must land on the same row as the config")

	var state struct {
		ManualStoppedApps []string `json:"manualStoppedApps"`
	}
	require.NoError(t, json.Unmarshal([]byte(stateJSON), &state))
	assert.Equal(t, []string{"onlyoffice"}, state.ManualStoppedApps)
}

func mustListNamespaceIDs(t *testing.T, store storage.Store, wsID string) []string {
	t.Helper()
	rows, err := store.ListNamespaces(wsID)
	require.NoError(t, err)
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}
