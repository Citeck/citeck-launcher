package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		// equal
		{"2.1.0", "2.1.0", true},
		{"v2.1.0", "2.1.0", true},
		{"2.1.0", "v2.1.0", true},
		// greater
		{"2.1.0", "2.0.0", true},
		{"2.1.1", "2.1.0", true},
		{"2.2.0", "2.1.9", true},
		{"3.0.0", "2.9.9", true},
		// less
		{"2.0.0", "2.1.0", false},
		{"2.0.9", "2.1.0", false},
		{"1.9.9", "2.0.0", false},
		// the actual case we use in the installer
		{"2.1.0", "2.1.0", true},  // v2.1.0 supports --leave-running
		{"2.0.0", "2.1.0", false}, // v2.0.0 needs SIGKILL fallback
		// edge cases
		{"2.1", "2.1.0", true},     // missing patch == 0
		{"2.1", "2.1.1", false},    // missing patch below
		{"2", "1.9.9", true},       // major only
		{"", "0.0.0", true},        // empty == 0.0.0
		{"", "0.0.1", false},       //
		{"abc", "0.0.0", true},     // non-numeric parses as 0
		{"abc", "0.0.1", false},    //
		{"2.10.0", "2.9.0", true},  // numeric (not lexicographic) compare
		{"2.9.0", "2.10.0", false}, //
	}

	for _, c := range cases {
		t.Run(c.a+"_vs_"+c.b, func(t *testing.T) {
			if got := versionAtLeast(c.a, c.b); got != c.want {
				t.Errorf("versionAtLeast(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestRunVersionFallback(t *testing.T) {
	// runVersionFallback parses "Citeck CLI X.Y.Z" lines. We can't easily
	// unit-test it against a real binary, but we can test the parsing logic
	// if we refactor — for now just verify the function doesn't panic on
	// a non-existent binary.
	if v := runVersionFallback("/nonexistent/citeck-xyz"); v != "" {
		t.Errorf("expected empty version for non-existent binary, got %q", v)
	}
}

func TestCleanupInstallerCacheOnSuccess(t *testing.T) {
	// Unset: no-op, no panic.
	t.Setenv(installerCacheEnv, "")
	cleanupInstallerCacheOnSuccess()

	// File exists: removed.
	dir := t.TempDir()
	cached := filepath.Join(dir, "citeck_2.1.0_linux_amd64")
	if err := os.WriteFile(cached, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write cached file: %v", err)
	}
	t.Setenv(installerCacheEnv, cached)
	cleanupInstallerCacheOnSuccess()
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Fatalf("expected cached file to be removed, err=%v", err)
	}

	// Already gone: no error surfaced (silent success).
	t.Setenv(installerCacheEnv, filepath.Join(dir, "nonexistent"))
	cleanupInstallerCacheOnSuccess() // must not panic or fail the test
}

// setInstallTarget redirects the package-level install target into a temp
// location for the duration of one test. Not parallel-safe (none of these
// tests call t.Parallel).
func setInstallTarget(t *testing.T, path string) {
	t.Helper()
	old := installTarget
	installTarget = path
	t.Cleanup(func() { installTarget = old })
}

func setSystemdUnitPath(t *testing.T, path string) {
	t.Helper()
	old := systemdUnitPath
	systemdUnitPath = path
	t.Cleanup(func() { systemdUnitPath = old })
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.bin")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o600))

	got, err := fileSHA256(path)
	require.NoError(t, err)
	// Known SHA-256 digest of the string "hello".
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", got)

	_, err = fileSHA256(filepath.Join(t.TempDir(), "missing"))
	require.Error(t, err)
}

func TestHashesMatch(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	c := filepath.Join(dir, "c")
	require.NoError(t, os.WriteFile(a, []byte("same-content"), 0o600))
	require.NoError(t, os.WriteFile(b, []byte("same-content"), 0o600))
	require.NoError(t, os.WriteFile(c, []byte("other-content"), 0o600))

	assert.True(t, hashesMatch(a, b))
	assert.False(t, hashesMatch(a, c))
	// Any unreadable side must collapse to false (caller falls through to upgrade).
	assert.False(t, hashesMatch(a, filepath.Join(dir, "missing")))
	assert.False(t, hashesMatch(filepath.Join(dir, "missing"), a))
}

func TestBuildSystemdUnit(t *testing.T) {
	unit := buildSystemdUnit("/usr/local/bin/citeck")

	// Load-bearing fields of the zero-downtime upgrade contract.
	assert.Contains(t, unit, "ExecStart=/usr/local/bin/citeck start --foreground")
	assert.Contains(t, unit, "ExecStop=/usr/local/bin/citeck stop --shutdown --leave-running")
	assert.Contains(t, unit, "KillMode=none")
	assert.Contains(t, unit, "Restart=on-failure")
	assert.Contains(t, unit, "TimeoutStopSec=30")
	assert.Contains(t, unit, "Requires=docker.service")
	assert.Contains(t, unit, "WantedBy=multi-user.target")

	// The exec path must be substituted, not hard-coded.
	other := buildSystemdUnit("/opt/custom/citeck")
	assert.Contains(t, other, "ExecStart=/opt/custom/citeck start --foreground")
	assert.NotContains(t, other, "/usr/local/bin/citeck")
}

func TestCopyBinaryAtomic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("binary-v2"), 0o600))

	require.NoError(t, copyBinaryAtomic(src, dst))

	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "binary-v2", string(got))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "installed binary must be executable")

	// Overwrite of an existing destination (the upgrade swap).
	require.NoError(t, os.WriteFile(src, []byte("binary-v3"), 0o600))
	require.NoError(t, copyBinaryAtomic(src, dst))
	got, err = os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "binary-v3", string(got))

	// Missing source surfaces an error and leaves dst intact.
	err = copyBinaryAtomic(filepath.Join(dir, "missing"), dst)
	require.Error(t, err)
	got, err = os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "binary-v3", string(got))
}

func TestRunRollback_NoBackup(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "citeck")
	require.NoError(t, os.WriteFile(target, []byte("current"), 0o755)) //nolint:gosec // G306: test binary stand-in
	setInstallTarget(t, target)

	err := runRollback()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nothing to rollback")

	// The installed binary must be untouched.
	got, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "current", string(got))
}

func TestInstalledAtTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "citeck")
	require.NoError(t, os.WriteFile(target, []byte("bin"), 0o755)) //nolint:gosec // G306: test binary stand-in
	setInstallTarget(t, target)

	// Same file (via a different path spelling) → true.
	assert.True(t, installedAtTarget(filepath.Join(dir, ".", "citeck")))

	// Different file → false.
	other := filepath.Join(dir, "other")
	require.NoError(t, os.WriteFile(other, []byte("bin"), 0o755)) //nolint:gosec // G306: test binary stand-in
	assert.False(t, installedAtTarget(other))

	// Missing self path → false.
	assert.False(t, installedAtTarget(filepath.Join(dir, "missing")))

	// Missing target → false.
	setInstallTarget(t, filepath.Join(dir, "gone"))
	assert.False(t, installedAtTarget(target))
}

func TestMigrateSystemdUnitIfStale(t *testing.T) {
	t.Run("no unit file is a no-op", func(t *testing.T) {
		setSystemdUnitPath(t, filepath.Join(t.TempDir(), "citeck.service"))
		migrateSystemdUnitIfStale() // must not panic or create the file
		assert.NoFileExists(t, systemdUnitPath)
	})

	t.Run("up-to-date unit is left untouched", func(t *testing.T) {
		dir := t.TempDir()
		unitPath := filepath.Join(dir, "citeck.service")
		setSystemdUnitPath(t, unitPath)
		setInstallTarget(t, filepath.Join(dir, "citeck"))

		fresh := buildSystemdUnit(installTarget)
		require.NoError(t, os.WriteFile(unitPath, []byte(fresh), 0o644)) //nolint:gosec // G306: systemd unit convention

		migrateSystemdUnitIfStale()

		got, err := os.ReadFile(unitPath)
		require.NoError(t, err)
		assert.Equal(t, fresh, string(got))
	})

	t.Run("stale unit is not rewritten without root", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("running as root — non-root warn path not reachable")
		}
		dir := t.TempDir()
		unitPath := filepath.Join(dir, "citeck.service")
		setSystemdUnitPath(t, unitPath)
		setInstallTarget(t, filepath.Join(dir, "citeck"))

		stale := "[Service]\nExecStart=/old/binary start\n"
		require.NoError(t, os.WriteFile(unitPath, []byte(stale), 0o644)) //nolint:gosec // G306: systemd unit convention

		migrateSystemdUnitIfStale()

		// Non-root must only warn — the unit file stays as-is.
		got, err := os.ReadFile(unitPath)
		require.NoError(t, err)
		assert.Equal(t, stale, string(got))
	})
}

func TestReadBinaryVersion_FakeBinaries(t *testing.T) {
	dir := t.TempDir()

	// Modern binary: supports `version --short`.
	short := filepath.Join(dir, "citeck-short")
	require.NoError(t, os.WriteFile(short, []byte("#!/bin/sh\necho 'v2.3.4'\n"), 0o755)) //nolint:gosec // G306: test script must be executable
	assert.Equal(t, "2.3.4", readBinaryVersion(short))

	// v2.0.0-style binary: no --short flag, prints a "Citeck CLI X.Y.Z" banner.
	legacy := filepath.Join(dir, "citeck-legacy")
	script := "#!/bin/sh\nif [ \"$2\" = \"--short\" ]; then exit 1; fi\necho 'Citeck CLI 2.0.0'\n"
	require.NoError(t, os.WriteFile(legacy, []byte(script), 0o755)) //nolint:gosec // G306: test script must be executable
	assert.Equal(t, "2.0.0", readBinaryVersion(legacy))

	// Broken binary: no version output at all.
	broken := filepath.Join(dir, "citeck-broken")
	require.NoError(t, os.WriteFile(broken, []byte("#!/bin/sh\nexit 1\n"), 0o755)) //nolint:gosec // G306: test script must be executable
	assert.Empty(t, readBinaryVersion(broken))
}
