package cli

import (
	"os"
	"path/filepath"
	"testing"
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
		{"2.1.0", "2.1.0", true}, // v2.1.0 supports --leave-running
		{"2.0.0", "2.1.0", false}, // v2.0.0 needs SIGKILL fallback
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
