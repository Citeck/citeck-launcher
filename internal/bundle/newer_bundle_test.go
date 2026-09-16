package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindNewerBundle_NothingNewer(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.1": appOnly, "2026.2": appOnly})
	assert.Nil(t, FindNewerBundle(dir, "2026.2", "2.12.2"))
}

func TestFindNewerBundle_NewerAndRunnable(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.2": appOnly, "2026.3": appOnly})
	got := FindNewerBundle(dir, "2026.2", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "2026.3", got.Version)
	assert.Empty(t, got.RequiresLauncher, "we can run it — nothing to ask the operator for")
}

func TestFindNewerBundle_NewerButNeedsANewerLauncher(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.2": appOnly, "2026.3": floored("2.13.0")})
	got := FindNewerBundle(dir, "2026.2", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "2026.3", got.Version)
	assert.Equal(t, "2.13.0", got.RequiresLauncher)
}

// A runnable version between the current one and a blocked one is the one to
// name: the operator can act on it today, and telling them to update the
// launcher instead would be advice they do not need.
func TestFindNewerBundle_PrefersTheRunnableOneBelowABlockedOne(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2": appOnly,
		"2026.3": appOnly,
		"2026.4": floored("2.13.0"),
	})
	got := FindNewerBundle(dir, "2026.2", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "2026.3", got.Version)
	assert.Empty(t, got.RequiresLauncher)
}

// compareBundleVersions ranks EVERY unscoped version above EVERY scoped one, so
// without a scope filter a namespace pinned inside archive/ would be told that
// all of mainline is newer.
func TestFindNewerBundle_ScopeIsNotCrossed(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2":         appOnly,
		"archive/2025.5": appOnly,
		"archive/2025.6": appOnly,
	})
	assert.Nil(t, FindNewerBundle(dir, "2026.2", "2.12.2"),
		"an unscoped namespace is not behind because archive/ has files")

	got := FindNewerBundle(dir, "archive/2025.5", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "archive/2025.6", got.Version)
}

// A namespace whose bundle is unresolved has no current version to compare.
func TestFindNewerBundle_NoCurrentVersion(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.2": appOnly})
	assert.Nil(t, FindNewerBundle(dir, "", "2.12.2"))
}
