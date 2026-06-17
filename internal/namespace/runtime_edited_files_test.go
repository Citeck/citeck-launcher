package namespace

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntime_RestoreEditedState_RoundTrip(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	edits := map[string]FileEdit{
		"postgres/postgresql.conf": {Format: "textual", Payload: json.RawMessage(`"@@ -1 +1 @@"`)},
		"proxy/lua/auth.lua":       {Format: "textual", Payload: json.RawMessage(`"@@ -1 +1 @@"`)},
	}
	patches := map[string]json.RawMessage{"eapps": json.RawMessage(`{"image":"x:2"}`)}
	r.RestoreEditedState(patches, edits)

	require.True(t, r.IsFileEdited("postgres/postgresql.conf"))
	require.True(t, r.IsFileEdited("proxy/lua/auth.lua"))
	require.False(t, r.IsFileEdited("missing/file.txt"))
	require.NotNil(t, r.AppPatch("eapps"))

	snap := r.FileEditsSnapshot()
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	require.Equal(t, []string{"postgres/postgresql.conf", "proxy/lua/auth.lua"}, keys)
}

func TestRuntime_FileEditsSnapshot_EmptyWhenNoEdits(t *testing.T) {
	md := newMockDocker()
	r := NewRuntime(testConfig(), md, t.TempDir())
	defer r.Shutdown()

	require.Nil(t, r.FileEditsSnapshot(), "expected nil snapshot when no edits")
}
