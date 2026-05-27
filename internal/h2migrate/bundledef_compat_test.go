package h2migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBundleDefCompat_KotlinFixture loads a hand-crafted Kotlin-shaped
// BundleDef JSON and asserts every field round-trips into Go's bundle.Def.
// The fixture covers the only known shape mismatch (key as string vs object)
// plus the byte-for-byte fields (applications, citeckApps, content).
func TestBundleDefCompat_KotlinFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "bundledef_v1.json"))
	require.NoError(t, err)

	got, err := decodeKotlinBundleDef(data)
	require.NoError(t, err)

	// key string -> Key{Version}
	assert.Equal(t, "release/2025.1.0", got.Key.Version)

	require.Contains(t, got.Applications, "eapps")
	assert.Equal(t, "citeck/ecos-apps:2025.1.0", got.Applications["eapps"].Image)
	require.Contains(t, got.Applications, "emodel")
	assert.Equal(t, "citeck/ecos-model:2025.1.0", got.Applications["emodel"].Image)

	require.Len(t, got.CiteckApps, 2)
	assert.Equal(t, "citeck/ecos-apps:2025.1.0", got.CiteckApps[0].Image)
	assert.Equal(t, "citeck/ecos-model:2025.1.0", got.CiteckApps[1].Image)

	require.NotNil(t, got.Content)
	assert.Equal(t, "release/2025.1.0", got.Content["name"])

	assert.False(t, got.IsEmpty())
}

// TestBundleDefCompat_Empty exercises empty and null inputs — the Kotlin
// runtime treats absence as "no cache", and the migrator must do the same.
func TestBundleDefCompat_Empty(t *testing.T) {
	cases := map[string][]byte{
		"nil":            nil,
		"empty":          []byte(""),
		"json-empty-obj": []byte("{}"),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := decodeKotlinBundleDef(in)
			require.NoError(t, err)
			assert.True(t, got.IsEmpty())
		})
	}
}

// TestBundleDefCompat_RoundTripIntoGoJSON asserts that after translation the
// Go-shaped JSON is exactly what NsPersistedState.CachedBundle would produce —
// no field renames, no nested-key drift.
func TestBundleDefCompat_RoundTripIntoGoJSON(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "bundledef_v1.json"))
	require.NoError(t, err)

	got, err := decodeKotlinBundleDef(data)
	require.NoError(t, err)

	encoded, err := json.Marshal(got)
	require.NoError(t, err)

	var asMap map[string]any
	require.NoError(t, json.Unmarshal(encoded, &asMap))

	// Go contract: key is an object with version field, NOT a bare string.
	keyMap, ok := asMap["key"].(map[string]any)
	require.True(t, ok, "Go BundleDef.key must serialize as object")
	assert.Equal(t, "release/2025.1.0", keyMap["version"])
}
