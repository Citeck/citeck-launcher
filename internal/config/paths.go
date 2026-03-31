package config

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Default paths and image references.
const (
	defaultServerHome  = "/opt/citeck"
	defaultRun         = "/run/citeck"
	socketFile         = "daemon.sock"
	DefaultUtilsImage  = "registry.citeck.ru/community/launcher-utils:1.0"
)

// UtilsImage returns the launcher-utils image, overridable via CITECK_UTILS_IMAGE env var.
func UtilsImage() string {
	if img := os.Getenv("CITECK_UTILS_IMAGE"); img != "" {
		return img
	}
	return DefaultUtilsImage
}

var (
	desktopMode    bool
	desktopModeSet bool // true after explicit SetDesktopMode call
	modeMu         sync.RWMutex
)

// SetDesktopMode explicitly sets the desktop mode flag. Once set, env var is ignored.
// Must be called before any path functions.
func SetDesktopMode(enabled bool) {
	modeMu.Lock()
	desktopMode = enabled
	desktopModeSet = true
	modeMu.Unlock()
}

// ResetDesktopMode clears the explicit desktop mode flag (for testing only).
func ResetDesktopMode() {
	modeMu.Lock()
	desktopMode = false
	desktopModeSet = false
	modeMu.Unlock()
}

// IsDesktopMode returns true if running in desktop mode.
// Priority: explicit SetDesktopMode > CITECK_DESKTOP env var.
func IsDesktopMode() bool {
	modeMu.RLock()
	defer modeMu.RUnlock()
	if desktopModeSet {
		return desktopMode
	}
	return os.Getenv("CITECK_DESKTOP") == "true"
}

// HomeDir returns the root directory for Citeck Launcher.
//
// Resolution order:
//  1. CITECK_HOME env var (always wins)
//  2. Desktop mode → platform-specific user data dir (~/.citeck/launcher on Linux)
//  3. Server mode → /opt/citeck
func HomeDir() string {
	if v := os.Getenv("CITECK_HOME"); v != "" {
		return v
	}
	if IsDesktopMode() {
		return desktopHomeDir()
	}
	return defaultServerHome
}

// desktopHomeDir returns platform-specific desktop home matching Kotlin AppDir.
func desktopHomeDir() string {
	switch runtime.GOOS {
	case "windows":
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			return filepath.Join(appData, "Citeck", "launcher")
		}
	case "darwin":
		if home := os.Getenv("HOME"); home != "" {
			return filepath.Join(home, "Library", "Application Support", "Citeck", "launcher")
		}
	default: // linux, freebsd, etc.
		if home := os.Getenv("HOME"); home != "" {
			return filepath.Join(home, ".citeck", "launcher")
		}
	}
	return defaultServerHome
}

// RunDir returns the runtime directory for PID files and sockets.
func RunDir() string {
	if v := os.Getenv("CITECK_RUN"); v != "" {
		return v
	}
	if IsDesktopMode() {
		return filepath.Join(HomeDir(), "run")
	}
	return defaultRun
}

// ConfDir returns the configuration directory.
func ConfDir() string {
	return filepath.Join(HomeDir(), "conf")
}

// DataDir returns the data directory for bundles and repos.
func DataDir() string {
	return filepath.Join(HomeDir(), "data")
}

// LogDir returns the log directory.
func LogDir() string {
	return filepath.Join(HomeDir(), "log")
}

// WebUICADir returns the directory for trusted client certificates (mTLS).
func WebUICADir() string {
	return filepath.Join(ConfDir(), "webui-ca")
}

// WebUITLSDir returns the directory for Web UI server cert+key (mTLS/HTTPS).
func WebUITLSDir() string {
	return filepath.Join(ConfDir(), "webui-tls")
}

// NamespaceConfigPath returns the default namespace config path (server mode).
// In desktop mode, use WorkspaceNamespaceConfigPath instead.
func NamespaceConfigPath() string {
	return filepath.Join(ConfDir(), "namespace.yml")
}

// SocketPath returns the Unix domain socket path for daemon communication.
func SocketPath() string {
	return filepath.Join(RunDir(), socketFile)
}

// DaemonLogPath returns the path to the daemon log file.
func DaemonLogPath() string {
	return filepath.Join(LogDir(), "daemon.log")
}

// DaemonConfigPath returns the path to daemon.yml.
func DaemonConfigPath() string {
	return filepath.Join(ConfDir(), "daemon.yml")
}

// --- Desktop mode workspace paths (matching Kotlin structure) ---

// WorkspacesDir returns the root dir for all workspaces: {home}/ws/
func WorkspacesDir() string {
	return filepath.Join(HomeDir(), "ws")
}

// WorkspaceDir returns the dir for a specific workspace: {home}/ws/{wsID}/
func WorkspaceDir(wsID string) string {
	return filepath.Join(WorkspacesDir(), wsID)
}

// WorkspaceRepoDir returns the workspace repo dir: {home}/ws/{wsID}/repo/
func WorkspaceRepoDir(wsID string) string {
	return filepath.Join(WorkspaceDir(wsID), "repo")
}

// WorkspaceBundlesDir returns the bundle cache dir: {home}/ws/{wsID}/bundles/
func WorkspaceBundlesDir(wsID string) string {
	return filepath.Join(WorkspaceDir(wsID), "bundles")
}

// NamespaceDir returns the namespace dir: {home}/ws/{wsID}/ns/{nsID}/
func NamespaceDir(wsID, nsID string) string {
	return filepath.Join(WorkspaceDir(wsID), "ns", nsID)
}

// NamespaceRtfilesDir returns the runtime files dir: {home}/ws/{wsID}/ns/{nsID}/rtfiles/
func NamespaceRtfilesDir(wsID, nsID string) string {
	return filepath.Join(NamespaceDir(wsID, nsID), "rtfiles")
}

// WorkspaceNamespaceConfigPath returns the namespace config path in desktop mode:
// {home}/ws/{wsID}/ns/{nsID}/namespace.yml
func WorkspaceNamespaceConfigPath(wsID, nsID string) string {
	return filepath.Join(NamespaceDir(wsID, nsID), "namespace.yml")
}
