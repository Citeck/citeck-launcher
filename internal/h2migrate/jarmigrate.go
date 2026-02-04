package h2migrate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/h2migrate/embedded"
	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

const (
	// JarName is the filename for the temporary JAR extracted from embedded data.
	JarName = "h2-export.jar"
)

// MigrateStatus describes the current migration state.
type MigrateStatus struct {
	Needed           bool
	HasJava          bool   // true if Java found in PATH
	JavaPath         string // path to java binary (empty if not found)
	JREDownloadSizeMB int   // size of JRE download in MB (0 if Java found)
}

// CheckMigration checks if migration is needed and whether Java is available.
func CheckMigration(homeDir string) MigrateStatus {
	needed, _ := NeedsMigration(homeDir)
	if !needed {
		return MigrateStatus{}
	}
	javaPath := findJava()
	status := MigrateStatus{Needed: true, HasJava: javaPath != "", JavaPath: javaPath}
	if !status.HasJava {
		if p, ok := jrePlatforms[jrePlatformKey()]; ok {
			status.JREDownloadSizeMB = p.sizeMB
		}
	}
	return status
}

// DownloadJRE downloads a minimal Adoptium JRE and returns the java binary path.
// Caller should call CleanupJRE when done.
func DownloadJRE(homeDir string) (javaPath string, err error) {
	_, javaPath, err = downloadJRE(homeDir)
	return
}

// CleanupJRE removes the downloaded JRE directory.
func CleanupJRE(homeDir string) {
	_ = os.RemoveAll(filepath.Join(homeDir, "tmp-jre"))
}

// RunJarMigration runs the embedded H2 export JAR and imports the result.
// javaPath must be a valid path to a java binary (system or downloaded JRE).
func RunJarMigration(homeDir, javaPath string, store storage.Store) (*MigrateResult, error) {
	h2Path := filepath.Join(homeDir, "storage.db")
	jarPath := filepath.Join(homeDir, JarName)
	exportPath := filepath.Join(homeDir, "h2-export.json")

	// Step 1: Write embedded JAR to disk
	if err := os.WriteFile(jarPath, embedded.H2ExportJar, 0o644); err != nil { //nolint:gosec // JAR needs to be readable by JVM
		return nil, fmt.Errorf("write h2-export.jar: %w", err)
	}
	defer os.Remove(jarPath)

	// Step 3: Run JAR
	slog.Info("Running H2 export", "jar", jarPath, "db", h2Path)
	cmd := exec.Command(javaPath, "-jar", jarPath, h2Path, exportPath) //nolint:gosec // all arguments are controlled (local file paths)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run h2-export.jar: %w", err)
	}
	defer os.Remove(exportPath)

	// Step 5: Import JSON
	slog.Info("Importing exported data", "file", exportPath)
	return ImportExportJSON(homeDir, exportPath, store)
}

// jrePlatform holds download URL, SHA256, and size for a specific OS/arch.
type jrePlatform struct {
	url    string
	sha256 string
	sizeMB int // approximate download size in MB
}

// Adoptium Temurin JRE 17.0.18+8 — pinned URLs, SHA256 checksums, and sizes.
var jrePlatforms = map[string]jrePlatform{
	"linux/amd64": {
		url:    "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.18%2B8/OpenJDK17U-jre_x64_linux_hotspot_17.0.18_8.tar.gz",
		sha256: "8b418e38cca87945858ae911988401720095eb671357d47437b4adb49c28dcab",
		sizeMB: 44,
	},
	"linux/arm64": {
		url:    "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.18%2B8/OpenJDK17U-jre_aarch64_linux_hotspot_17.0.18_8.tar.gz",
		sha256: "88727c16610d118c0e739f62e6c99dc1cb5a7b4a93a27054fe2a3aa7150e7b5d",
		sizeMB: 42,
	},
	"darwin/amd64": {
		url:    "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.18%2B8/OpenJDK17U-jre_x64_mac_hotspot_17.0.18_8.tar.gz",
		sha256: "486ab329956941fae40012f42d9f4bcbd48d036e11249b924e259fe7a86ee3dc",
		sizeMB: 44,
	},
	"darwin/arm64": {
		url:    "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.18%2B8/OpenJDK17U-jre_aarch64_mac_hotspot_17.0.18_8.tar.gz",
		sha256: "6853987fa37340b157d7e8e895db0148efa13c3b2d6e6f3b289aac42e437d32e",
		sizeMB: 42,
	},
	"windows/amd64": {
		url:    "https://github.com/adoptium/temurin17-binaries/releases/download/jdk-17.0.18%2B8/OpenJDK17U-jre_x64_windows_hotspot_17.0.18_8.zip",
		sha256: "95c9ebe3ee16baab7239531757513d9a03799ca06483ef2f3b530e81e93e7b5b",
		sizeMB: 41,
	},
}

func jrePlatformKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

// downloadJRE downloads and extracts a minimal Adoptium JRE.
// Returns (jreDir, javaPath, error). Caller must os.RemoveAll(jreDir) when done.
func downloadJRE(homeDir string) (jreDir, javaPath string, _ error) {
	platform, ok := jrePlatforms[jrePlatformKey()]
	if !ok {
		return "", "", fmt.Errorf("no JRE available for %s", jrePlatformKey())
	}

	jreDir = filepath.Join(homeDir, "tmp-jre")
	_ = os.RemoveAll(jreDir)
	_ = os.MkdirAll(jreDir, 0o755) //nolint:gosec // G301: temp dir needs 0o755 for JRE binary execution

	isWindows := runtime.GOOS == "windows"
	archivePath := filepath.Join(jreDir, "jre.tar.gz")
	if isWindows {
		archivePath = filepath.Join(jreDir, "jre.zip")
	}

	slog.Info("Downloading JRE", "platform", jrePlatformKey(), "sizeMB", platform.sizeMB)
	if err := downloadFile(archivePath, platform.url); err != nil {
		return "", "", fmt.Errorf("download JRE: %w", err)
	}

	// Verify SHA256
	actualHash, err := fileSHA256(archivePath)
	if err != nil {
		_ = os.RemoveAll(jreDir)
		return "", "", fmt.Errorf("compute JRE hash: %w", err)
	}
	if actualHash != platform.sha256 {
		_ = os.RemoveAll(jreDir)
		return "", "", fmt.Errorf("JRE SHA256 mismatch: got %s, want %s", actualHash, platform.sha256)
	}
	slog.Info("JRE SHA256 verified")

	// Extract
	if isWindows {
		if err := extractZip(archivePath, jreDir); err != nil {
			return "", "", fmt.Errorf("extract JRE: %w", err)
		}
	} else {
		if err := extractTarGz(archivePath, jreDir); err != nil {
			return "", "", fmt.Errorf("extract JRE: %w", err)
		}
	}
	_ = os.Remove(archivePath)

	// Find java binary (java on Unix, java.exe on Windows)
	javaBin := "java"
	if isWindows {
		javaBin = "java.exe"
	}
	javaPath = ""
	_ = filepath.Walk(jreDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(path) == javaBin && strings.Contains(path, "bin") {
			javaPath = path
			return filepath.SkipAll
		}
		return nil
	})
	if javaPath == "" {
		return "", "", fmt.Errorf("java binary not found in extracted JRE")
	}

	_ = os.Chmod(javaPath, 0o755) //nolint:gosec // G302: java binary needs execute permission
	return jreDir, javaPath, nil
}

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)
	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name) //nolint:gosec // path traversal prevented by prefix check below
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanDest) {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0o755) //nolint:gosec // G301: extraction dirs need 0o755
			continue
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755) //nolint:gosec // G301: extraction dirs need 0o755
		rc, err := f.Open()
		if err != nil {
			continue
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, f.Mode())
		if err != nil {
			_ = rc.Close()
			continue
		}
		_, _ = io.Copy(out, rc) //nolint:gosec // G110: zip content is a known JRE archive from trusted source
		_ = out.Close()
		_ = rc.Close()
	}
	return nil
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath) //nolint:gosec // G304: archivePath is an internal temp path, not user input
	if err != nil {
		return fmt.Errorf("open archive %s: %w", archivePath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		target := filepath.Join(destDir, header.Name) //nolint:gosec // path traversal prevented by prefix check below
		// Prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, 0o755) //nolint:gosec // G301: extraction dirs need 0o755
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(target), 0o755) //nolint:gosec // G301: extraction dirs need 0o755
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode)) //nolint:gosec // mode is from trusted JRE tar archive
			if err != nil {
				continue
			}
			_, _ = io.Copy(out, tr) //nolint:gosec // G110: tar content is a known JRE archive from trusted source
			_ = out.Close()
		}
	}
	return nil
}

// h2ExportJSON is the top-level structure of h2-export.json.
type h2ExportJSON struct {
	Version int                          `json:"version"`
	Maps    map[string]map[string]string `json:"maps"` // mapName → key → base64(value)
}

// ImportExportJSON imports data from an H2 export JSON file into the store.
func ImportExportJSON(homeDir, exportPath string, store storage.Store) (*MigrateResult, error) {
	data, err := os.ReadFile(exportPath) //nolint:gosec // G304: exportPath is an internal temp path, not user input
	if err != nil {
		return nil, fmt.Errorf("read export file: %w", err)
	}

	var export h2ExportJSON
	if unmarshalErr := json.Unmarshal(data, &export); unmarshalErr != nil {
		return nil, fmt.Errorf("parse export file: %w", unmarshalErr)
	}

	if export.Version != 1 {
		return nil, fmt.Errorf("unsupported export version: %d", export.Version)
	}

	result := &MigrateResult{}

	// Import workspaces
	importWorkspaces(export.Maps, store, result)

	// Import namespace configs (create namespace.yml files)
	importNamespaces(homeDir, export.Maps, result)

	// Import secrets (encrypted blob)
	importSecrets(export.Maps, store, result)

	// Import launcher state
	importState(export.Maps, store)

	slog.Info("JAR migration complete",
		"workspaces", result.Workspaces,
		"namespaces", result.Namespaces,
		"secrets", result.Secrets,
	)
	return result, nil
}

func importWorkspaces(maps map[string]map[string]string, store storage.Store, result *MigrateResult) {
	for mapName, entries := range maps {
		if !strings.HasSuffix(mapName, "!workspace") {
			continue
		}
		for id, b64 := range entries {
			raw, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				continue
			}
			var ws struct {
				Name      string `json:"name"`
				RepoURL   string `json:"repoUrl"`
				RepoBranch string `json:"repoBranch"`
			}
			if err := json.Unmarshal(raw, &ws); err != nil {
				slog.Warn("Failed to parse workspace", "id", id, "err", err)
				continue
			}
			dto := storage.WorkspaceDto{
				ID:         id,
				Name:       ws.Name,
				RepoURL:    ws.RepoURL,
				RepoBranch: ws.RepoBranch,
			}
			if dto.Name == "" {
				dto.Name = id
			}
			if err := store.SaveWorkspace(dto); err != nil {
				slog.Warn("Failed to save workspace", "id", id, "err", err)
				result.Errors++
				continue
			}
			result.Workspaces++
			slog.Info("Migrated workspace", "id", id, "name", dto.Name)
		}
	}
}

func importNamespaces(homeDir string, maps map[string]map[string]string, result *MigrateResult) {
	for mapName, entries := range maps {
		// Match entities/{wsId}!namespace (not versions, not runtime)
		if !strings.HasSuffix(mapName, "!namespace") {
			continue
		}
		if strings.Contains(mapName, "/versions") || strings.Contains(mapName, "runtime") {
			continue
		}

		// Extract workspace ID from "entities/{wsId}!namespace"
		wsID := strings.TrimPrefix(mapName, "entities/")
		wsID = strings.TrimSuffix(wsID, "!namespace")

		for nsID, b64 := range entries {
			raw, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				continue
			}

			var ns struct {
				Name           string         `json:"name"`
				BundleRef      string         `json:"bundleRef"`
				Authentication map[string]any `json:"authentication"`
				Snapshot       string         `json:"snapshot"`
				Template       string         `json:"template"`
			}
			if unmarshalErr := json.Unmarshal(raw, &ns); unmarshalErr != nil {
				slog.Warn("Failed to parse namespace", "ws", wsID, "ns", nsID, "err", unmarshalErr)
				continue
			}

			// Build namespace.yml
			nsCfg := map[string]any{
				"id":   nsID,
				"name": ns.Name,
			}
			if ns.BundleRef != "" {
				nsCfg["bundleRef"] = ns.BundleRef
			}
			if ns.Authentication != nil {
				nsCfg["authentication"] = ns.Authentication
			}
			if ns.Snapshot != "" {
				nsCfg["snapshot"] = ns.Snapshot
			}
			if ns.Template != "" {
				nsCfg["template"] = ns.Template
			}

			yamlBytes, err := yaml.Marshal(nsCfg)
			if err != nil {
				continue
			}

			nsDir := filepath.Join(homeDir, "ws", wsID, "ns", nsID)
			_ = os.MkdirAll(nsDir, 0o755) //nolint:gosec // G301: namespace dirs need 0o755
			nsConfigPath := filepath.Join(nsDir, "namespace.yml")

			if err := os.WriteFile(nsConfigPath, yamlBytes, 0o644); err != nil { //nolint:gosec // config file needs to be readable
				slog.Warn("Failed to write namespace config", "path", nsConfigPath, "err", err)
				result.Errors++
				continue
			}
			result.Namespaces++
			slog.Info("Migrated namespace", "ws", wsID, "ns", nsID, "name", ns.Name, "bundle", ns.BundleRef)
		}
	}
}

func importSecrets(maps map[string]map[string]string, store storage.Store, result *MigrateResult) {
	secretsMap, ok := maps["secrets!data"]
	if !ok {
		return
	}
	storageB64, ok := secretsMap["storage"]
	if !ok {
		return
	}

	// Store the encrypted blob as-is — Go launcher will decrypt when master password is provided
	if err := store.PutSecretBlob(storageB64); err != nil {
		slog.Warn("Failed to import secrets blob", "err", err)
		result.Errors++
		return
	}
	result.Secrets = 1
	slog.Info("Migrated encrypted secrets blob")
}

func importState(maps map[string]map[string]string, store storage.Store) {
	// Read selectedWorkspace from launcher!state
	var wsID string
	if stateMap, ok := maps["launcher!state"]; ok {
		if b64, ok := stateMap["selectedWorkspace"]; ok {
			raw, _ := base64.StdEncoding.DecodeString(b64)
			_ = json.Unmarshal(raw, &wsID)
		}
	}
	if wsID == "" {
		return
	}

	// Read selectedNamespace from workspace-state!{wsId}
	var nsID string
	wsStateKey := "workspace-state!" + wsID
	if wsState, ok := maps[wsStateKey]; ok {
		if b64, ok := wsState["selectedNamespace"]; ok {
			raw, _ := base64.StdEncoding.DecodeString(b64)
			_ = json.Unmarshal(raw, &nsID)
		}
	}

	_ = store.SetState(storage.LauncherState{WorkspaceID: wsID, NamespaceID: nsID})
	slog.Info("Migrated launcher state", "workspace", wsID, "namespace", nsID)
}

// findJava searches for a Java executable in PATH and common locations.
func findJava() string {
	if p, err := exec.LookPath("java"); err == nil {
		return p
	}
	// Check common JDK locations
	candidates := []string{
		"/usr/bin/java",
		"/usr/local/bin/java",
	}
	// Check JAVA_HOME
	if jh := os.Getenv("JAVA_HOME"); jh != "" {
		candidates = append([]string{filepath.Join(jh, "bin", "java")}, candidates...)
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func downloadFile(dest, url string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url) //nolint:gosec // URL is an internal constant, not user input
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest) //nolint:gosec // G304: dest is an internal temp path
	if err != nil {
		return fmt.Errorf("create file %s: %w", dest, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("write file %s: %w", dest, err)
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is an internal temp path
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
