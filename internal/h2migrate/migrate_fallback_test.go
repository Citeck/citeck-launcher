package h2migrate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// TestDumpFallbackReason locks in the defense-in-depth contract: any reader
// result other than "non-empty dump with nil error" must trigger the
// filesystem fallback. The empty-dump-with-nil-error case matters because
// a broken parser would otherwise silently drop every workspace and secret.
func TestDumpFallbackReason(t *testing.T) {
	cases := []struct {
		name    string
		maps    map[string]map[string]string
		err     error
		expFall bool
	}{
		{"error_triggers_fallback", nil, errors.New("boom"), true},
		{"nil_map_triggers_fallback", nil, nil, true},
		{"empty_map_triggers_fallback", map[string]map[string]string{}, nil, true},
		{"valid_dump_proceeds", map[string]map[string]string{"entities/global!workspace": {"a": ""}}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := dumpFallbackReason(tc.maps, tc.err)
			if tc.expFall {
				assert.NotEmpty(t, reason, "expected fallback to fire")
			} else {
				assert.Empty(t, reason, "expected fast path")
			}
		})
	}
}

// TestBuildFallbackNamespaceYAML covers the B3 default: filesystem-fallback
// migration must seed authentication.{type=BASIC, users=[admin, fet]} so the
// resulting namespace.yml mirrors Kotlin's NamespaceConfig.DEFAULT.
func TestBuildFallbackNamespaceYAML(t *testing.T) {
	body, err := buildFallbackNamespaceYAML("nsA", "community:2025.12")
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(body, &parsed))
	assert.Equal(t, "nsA", parsed["id"])
	assert.Equal(t, "community:2025.12", parsed["bundleRef"])
	auth, ok := parsed["authentication"].(map[string]any)
	require.True(t, ok, "authentication block must be present")
	assert.Equal(t, "BASIC", auth["type"])
	users, ok := auth["users"].([]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []any{"admin", "fet"}, users)
}

// TestFilesystemFallbackPopulatesAuthDefaults runs the full filesystem
// fallback (no storage.db readable, only a directory tree present) and
// asserts that each generated namespace.yml carries the default auth block.
func TestFilesystemFallbackPopulatesAuthDefaults(t *testing.T) {
	homeDir := t.TempDir()
	nsDir := filepath.Join(homeDir, "ws", "default", "ns", "demo")
	require.NoError(t, os.MkdirAll(nsDir, 0o755))

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	res, err := migrateFromFilesystem(homeDir, store)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.Namespaces, 1)

	body, err := os.ReadFile(filepath.Join(nsDir, "namespace.yml")) //nolint:gosec // G304: test path under t.TempDir()
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(body, &parsed))
	auth, ok := parsed["authentication"].(map[string]any)
	require.True(t, ok, "authentication block must be present in fallback yaml")
	assert.Equal(t, "BASIC", auth["type"])
}
