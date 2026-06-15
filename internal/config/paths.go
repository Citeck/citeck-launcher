package config

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Default paths and image references.
const (
	defaultServerHome = "/opt/citeck"
	defaultRun        = "/run/citeck"
	socketFile        = "daemon.sock"
	DefaultUtilsImage = "registry.citeck.ru/community/launcher-utils:1.0"
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

// LogDir returns the log directory. Named "logs" (plural) to match the legacy
// Kotlin 1.x launcher, so a desktop upgrade keeps a single log dir instead of
// leaving the 1.x "logs/" orphaned beside a 2.x "log/".
func LogDir() string {
	return filepath.Join(HomeDir(), "logs")
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

// WrapperSocketPath is the desktop wrapper's control socket (daemon → wrapper
// native-verb calls). Lives next to the daemon socket under RunDir.
func WrapperSocketPath() string {
	return filepath.Join(RunDir(), "wrapper.sock")
}

// DaemonPidPath records the supervised daemon child PID so a restarted wrapper
// can reap an orphaned daemon (macOS/Windows have no Pdeathsig).
func DaemonPidPath() string {
	return filepath.Join(RunDir(), "daemon.pid")
}

// UpdatesDir holds downloaded daemon payloads for desktop auto-update.
// Created here so the layout is stable.
func UpdatesDir() string {
	return filepath.Join(HomeDir(), "updates")
}

// DaemonLogPath returns the path to the daemon log file.
func DaemonLogPath() string {
	return filepath.Join(LogDir(), "daemon.log")
}

// DaemonConfigPath returns the path to daemon.yml.
func DaemonConfigPath() string {
	return filepath.Join(ConfDir(), "daemon.yml")
}

// APITokenPath returns the path of the persisted API auth token file
// (created 0600 by the daemon when api_auth is enabled without an explicit
// token in daemon.yml). File permissions are the access-control mechanism:
// only users who can read it can drive the token-gated TCP API.
func APITokenPath() string {
	return filepath.Join(ConfDir(), "api-token")
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

// BundlesDataDir returns the base directory the bundle resolver works under
// (git clones, bundle caches, offline imports). Server mode uses the shared
// DataDir; desktop mode scopes it per workspace ({home}/ws/{wsID}, Kotlin
// layout parity). Single source of truth — the daemon previously copy-pasted
// this branch at every resolver construction site.
func BundlesDataDir(wsID string) string {
	if IsDesktopMode() {
		return WorkspaceDir(wsID)
	}
	return DataDir()
}

// WorkspaceRepoDir returns the workspace repo dir: {home}/ws/{wsID}/repo/
func WorkspaceRepoDir(wsID string) string {
	return filepath.Join(WorkspaceDir(wsID), "repo")
}

// WorkspaceBundlesDir returns the bundle cache dir: {home}/ws/{wsID}/bundles/
func WorkspaceBundlesDir(wsID string) string {
	return filepath.Join(WorkspaceDir(wsID), "bundles")
}

// WorkspaceSnapshotsDir returns the per-workspace snapshot cache dir matching
// Kotlin's `<AppDir>/ws/<workspaceId>/snapshots/` layout.
//
// Snapshots referenced from workspace-v1.yml are immutable artifacts identified
// by ID + SHA-256; caching them at workspace scope (instead of per-namespace)
// lets multiple namespaces in the same workspace share a single download.
// Per-namespace snapshot files (user-created exports) continue to live under
// ResolveVolumesBase(wsID, nsID)/snapshots/.
func WorkspaceSnapshotsDir(wsID string) string {
	return filepath.Join(WorkspaceDir(wsID), "snapshots")
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

// DetectOutboundIP returns the best IP for external access.
// Online: queries public service for external IP (handles NAT). Offline: returns empty.
func DetectOutboundIP(offline bool) string {
	if offline {
		return ""
	}
	return detectExternalIP()
}

// DetectDisplayIP returns a non-loopback IP suitable for display (e.g., "Web UI available at").
// Uses local interface scanning — does NOT call external services.
func DetectDisplayIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "localhost"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return "localhost"
}

// detectExternalIP queries a public service for the machine's external IP.
func detectExternalIP() string {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil || resp.StatusCode != http.StatusOK {
		return ""
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}
