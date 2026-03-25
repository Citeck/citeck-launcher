package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHomeDirDefault(t *testing.T) {
	// Ensure clean state
	ResetDesktopMode()
	os.Unsetenv("CITECK_HOME")
	os.Unsetenv("CITECK_DESKTOP")

	got := HomeDir()
	if got != defaultServerHome {
		t.Errorf("HomeDir() = %q, want %q", got, defaultServerHome)
	}
}

func TestHomeDirEnvOverride(t *testing.T) {
	ResetDesktopMode()
	os.Setenv("CITECK_HOME", "/tmp/test-citeck")
	defer os.Unsetenv("CITECK_HOME")

	got := HomeDir()
	if got != "/tmp/test-citeck" {
		t.Errorf("HomeDir() = %q, want /tmp/test-citeck", got)
	}
}

func TestHomeDirDesktopFlag(t *testing.T) {
	os.Unsetenv("CITECK_HOME")
	os.Unsetenv("CITECK_DESKTOP")
	SetDesktopMode(true)
	defer ResetDesktopMode()

	got := HomeDir()
	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	var want string
	switch runtime.GOOS {
	case "darwin":
		want = filepath.Join(home, "Library", "Application Support", "Citeck", "launcher")
	case "windows":
		t.Skip("Windows path test requires LOCALAPPDATA")
	default:
		want = filepath.Join(home, ".citeck", "launcher")
	}

	if got != want {
		t.Errorf("HomeDir() desktop = %q, want %q", got, want)
	}
}

func TestHomeDirDesktopEnv(t *testing.T) {
	os.Unsetenv("CITECK_HOME")
	ResetDesktopMode() // no explicit mode — env var should take effect
	os.Setenv("CITECK_DESKTOP", "true")
	defer os.Unsetenv("CITECK_DESKTOP")

	if !IsDesktopMode() {
		t.Error("IsDesktopMode() should be true when CITECK_DESKTOP=true")
	}
}

func TestHomeDirEnvWinsOverDesktop(t *testing.T) {
	os.Setenv("CITECK_HOME", "/custom/path")
	defer os.Unsetenv("CITECK_HOME")
	SetDesktopMode(true)
	defer ResetDesktopMode()

	got := HomeDir()
	if got != "/custom/path" {
		t.Errorf("HomeDir() = %q, want /custom/path (CITECK_HOME should win)", got)
	}
}

func TestRunDirDesktop(t *testing.T) {
	os.Unsetenv("CITECK_HOME")
	os.Unsetenv("CITECK_RUN")
	SetDesktopMode(true)
	defer ResetDesktopMode()

	got := RunDir()
	// Desktop mode run dir should be under home
	if !filepath.IsAbs(got) {
		t.Errorf("RunDir() = %q, expected absolute path", got)
	}
	if !containsDir(got, "run") {
		t.Errorf("RunDir() = %q, expected to contain 'run'", got)
	}
}

func TestWorkspacePaths(t *testing.T) {
	os.Setenv("CITECK_HOME", "/test/home")
	defer os.Unsetenv("CITECK_HOME")
	SetDesktopMode(false)

	tests := []struct {
		name string
		fn   func() string
		want string
	}{
		{"WorkspacesDir", WorkspacesDir, "/test/home/ws"},
		{"WorkspaceDir", func() string { return WorkspaceDir("default") }, "/test/home/ws/default"},
		{"WorkspaceRepoDir", func() string { return WorkspaceRepoDir("default") }, "/test/home/ws/default/repo"},
		{"WorkspaceBundlesDir", func() string { return WorkspaceBundlesDir("default") }, "/test/home/ws/default/bundles"},
		{"NamespaceDir", func() string { return NamespaceDir("default", "prod") }, "/test/home/ws/default/ns/prod"},
		{"NamespaceRtfilesDir", func() string { return NamespaceRtfilesDir("default", "prod") }, "/test/home/ws/default/ns/prod/rtfiles"},
		{"WorkspaceNsConfigPath", func() string { return WorkspaceNamespaceConfigPath("default", "prod") }, "/test/home/ws/default/ns/prod/namespace.yml"},
		{"DaemonConfigPath", DaemonConfigPath, "/test/home/conf/daemon.yml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn()
			if got != tt.want {
				t.Errorf("%s() = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestResolvePathsServerMode(t *testing.T) {
	os.Setenv("CITECK_HOME", "/srv/citeck")
	defer os.Unsetenv("CITECK_HOME")
	SetDesktopMode(false)

	nsCfgPath := ResolveNamespaceConfigPath("daemon", "default")
	if nsCfgPath != "/srv/citeck/conf/namespace.yml" {
		t.Errorf("ResolveNamespaceConfigPath() = %q, want conf/namespace.yml", nsCfgPath)
	}

	volBase := ResolveVolumesBase("daemon", "default")
	if volBase != "/srv/citeck/data/runtime/default" {
		t.Errorf("ResolveVolumesBase() = %q, want data/runtime/default", volBase)
	}
}

func TestResolvePathsDesktopMode(t *testing.T) {
	os.Setenv("CITECK_HOME", "/home/user/.citeck/launcher")
	defer os.Unsetenv("CITECK_HOME")
	SetDesktopMode(true)
	defer ResetDesktopMode()

	nsCfgPath := ResolveNamespaceConfigPath("default", "prod")
	want := "/home/user/.citeck/launcher/ws/default/ns/prod/namespace.yml"
	if nsCfgPath != want {
		t.Errorf("ResolveNamespaceConfigPath() = %q, want %q", nsCfgPath, want)
	}

	volBase := ResolveVolumesBase("default", "prod")
	wantVol := "/home/user/.citeck/launcher/ws/default/ns/prod/rtfiles"
	if volBase != wantVol {
		t.Errorf("ResolveVolumesBase() = %q, want %q", volBase, wantVol)
	}
}

func containsDir(path, dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == dir {
			return true
		}
	}
	return false
}
