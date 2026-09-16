package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBundleDir lays out a bundles directory: version key → file body.
func writeBundleDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for key, body := range files {
		path := filepath.Join(dir, key+".yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	return dir
}

const appOnly = "EcosModelApp:\n  image: core/ecos-model:1.0\n"

func floored(v string) string {
	return "minLauncherVersion: \"" + v + "\"\n" + appOnly
}

// LATEST means "the newest one you can run". Refusing instead would leave an
// old launcher unable to create ANY namespace from a repo that has moved on,
// Quick Start included — the bricking this feature avoids at load time,
// transplanted to create time.
func TestLatestRunnableBundle_SkipsWhatThisLauncherCannotRun(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.1": appOnly,
		"2026.2": appOnly,
		"2026.3": floored("2.13.0"),
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.2", got)
}

func TestLatestRunnableBundle_TakesTheNewestWhenItFits(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2": appOnly,
		"2026.3": floored("2.12.0"),
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.3", got)
}

// The walk does not stop at the first floor it cannot clear: a repo can publish
// a newer bundle with a LOWER floor than the one below it.
func TestLatestRunnableBundle_KeepsWalkingPastABlockedRung(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.1": appOnly,
		"2026.2": floored("2.12.0"),
		"2026.3": floored("2.13.0"),
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.2", got)
}

// An unknown launcher version enforces nothing, so LATEST is the plain latest.
func TestLatestRunnableBundle_NoLauncherVersionTakesTheNewest(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2": appOnly,
		"2026.3": floored("2.13.0"),
	})
	got, err := LatestRunnableBundle(dir, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.3", got)
}

// Nothing runnable is NOT an error. This walk runs on the load and reload paths
// too, and an error there would stop the operator from opening the namespace —
// the one thing the design forbids. It answers the newest version, and the
// config WRITE gate is what refuses it, once, with an actionable message.
func TestLatestRunnableBundle_NothingRunnableTakesTheNewestAnyway(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2": floored("2.13.0"),
		"2026.3": floored("2.14.0"),
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err, "a floor must never fail a resolve — only a write")
	assert.Equal(t, "2026.3", got)
}

// The four cases below are ported from the deleted findLatestBundle's test
// suite (resolver_test.go): findLatestBundle lost its only caller when
// LatestRunnableBundle replaced it in Resolver.Resolve, and a second
// implementation of "what LATEST means" living on only through its own tests
// is a defect, not coverage — it can drift from the rule that actually ships.

// Ported from TestFindLatestBundle_NumericVersions: LATEST must compare
// versions numerically, not lexicographically ("2025.10" > "2025.9").
func TestLatestRunnableBundle_NumericVersions(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2025.9":  appOnly,
		"2025.10": appOnly,
		"2024.12": appOnly,
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "2025.10", got, "not \"2025.9\" — numeric comparison")
}

// Ported from TestFindLatestBundle_ErrNoBundles_MissingDir: callers (e.g.
// `citeck update`) classify a missing bundles layout via errors.Is(err,
// ErrNoBundles) to distinguish it from a genuine pull error.
func TestLatestRunnableBundle_ErrNoBundles_MissingDir(t *testing.T) {
	_, err := LatestRunnableBundle(filepath.Join(t.TempDir(), "does-not-exist"), "2.12.2", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoBundles)
}

// Ported from TestFindLatestBundle_ErrNoBundles_EmptyDir: an existing
// directory with no version YAMLs also surfaces ErrNoBundles, and a
// non-version file (README.md) must not count as a bundle.
func TestLatestRunnableBundle_ErrNoBundles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644))
	_, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoBundles)
}

// Ported from TestFindLatestBundle_NestedLayout: LATEST must walk a nested
// (Helm-chart-style, scoped) layout and pick the highest-priority key —
// LatestRunnableBundle had no scoped-key test of its own before this.
func TestLatestRunnableBundle_NestedLayout(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"community/2025.10": appOnly,
		"community/2025.5":  appOnly,
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "community/2025.10", got)
}

// New coverage findLatestBundle could never have had: the floor-skip walk
// must apply inside a scope exactly as it does at top level.
func TestLatestRunnableBundle_NestedLayoutSkipsAFlooredScopedVersion(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"community/2025.10": floored("2.13.0"),
		"community/2025.5":  appOnly,
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "community/2025.5", got)
}
