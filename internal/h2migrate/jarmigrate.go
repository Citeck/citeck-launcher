package h2migrate

import (
	"archive/tar"
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
	Needed   bool
	JavaPath string // empty if Java not found
}

// CheckMigration checks if migration is needed and whether Java is available.
func CheckMigration(homeDir string) MigrateStatus {
	needed, _ := NeedsMigration(homeDir)
	if !needed {
		return MigrateStatus{}
	}
	javaPath := findJava()
	return MigrateStatus{Needed: true, JavaPath: javaPath}
}

// RunJarMigration downloads the H2 export JAR (and JRE if needed), runs it, and imports the result.
func RunJarMigration(homeDir string, javaPath string, store storage.Store) (*MigrateResult, error) {
	h2Path := filepath.Join(homeDir, "storage.db")
	jarPath := filepath.Join(homeDir, JarName)
	exportPath := filepath.Join(homeDir, "h2-export.json")

	// Step 1: Ensure Java is available
	jreDir := ""
	if javaPath == "" {
		slog.Info("Java not found, downloading minimal JRE")
		var err error
		jreDir, javaPath, err = downloadJRE(homeDir)
		if err != nil {
			return nil, fmt.Errorf("download JRE: %w", err)
		}
		defer os.RemoveAll(jreDir) // cleanup JRE after migration
		slog.Info("JRE downloaded", "java", javaPath)
	}

	// Step 2: Write embedded JAR to disk
	if err := os.WriteFile(jarPath, embedded.H2ExportJar, 0o644); err != nil {
		return nil, fmt.Errorf("write h2-export.jar: %w", err)
	}
	defer os.Remove(jarPath)

	// Step 3: Run JAR
	slog.Info("Running H2 export", "jar", jarPath, "db", h2Path)
	cmd := exec.Command(javaPath, "-jar", jarPath, h2Path, exportPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run h2-export.jar: %w", err)
	}
	defer os.Remove(exportPath)

	// Step 5: Import JSON
	slog.Info("Importing exported data", "file", exportPath)
	return ImportExportJSON(homeDir, exportPath, store)
}

// jreAdoptiumURL returns the Adoptium Temurin JRE download URL for the current platform.
// Uses JRE 17 (LTS) for broad compatibility — supports Java 8 bytecode.
func jreAdoptiumURL() string {
	os := runtime.GOOS
	arch := runtime.GOARCH

	// Map Go arch to Adoptium arch names
	adoptiumArch := "x64"
	if arch == "arm64" {
		adoptiumArch = "aarch64"
	}

	// Map Go OS to Adoptium OS names
	adoptiumOS := os
	if os == "darwin" {
		adoptiumOS = "mac"
	}

	return fmt.Sprintf(
		"https://api.adoptium.net/v3/binary/latest/17/ga/%s/%s/jre/hotspot/normal/eclipse",
		adoptiumOS, adoptiumArch,
	)
}

// downloadJRE downloads and extracts a minimal Adoptium JRE.
// Returns (jreDir, javaPath, error). Caller must os.RemoveAll(jreDir) when done.
func downloadJRE(homeDir string) (string, string, error) {
	jreDir := filepath.Join(homeDir, "tmp-jre")
	os.RemoveAll(jreDir) // clean previous attempt
	os.MkdirAll(jreDir, 0o755)

	url := jreAdoptiumURL()
	slog.Info("Downloading JRE", "url", url)

	archivePath := filepath.Join(jreDir, "jre.tar.gz")
	if runtime.GOOS == "windows" {
		archivePath = filepath.Join(jreDir, "jre.zip")
	}

	if err := downloadFile(archivePath, url); err != nil {
		return "", "", fmt.Errorf("download JRE: %w", err)
	}

	// Verify JRE SHA256 via Adoptium checksum endpoint
	checksumURL := url + ".sha256.txt"
	expectedHash, err := fetchText(checksumURL)
	if err != nil {
		slog.Warn("Could not fetch JRE checksum", "err", err)
	} else {
		expectedHash = strings.TrimSpace(strings.Fields(expectedHash)[0])
		actualHash, err := fileSHA256(archivePath)
		if err != nil {
			return "", "", fmt.Errorf("compute JRE hash: %w", err)
		}
		if actualHash != expectedHash {
			os.RemoveAll(jreDir)
			return "", "", fmt.Errorf("JRE SHA256 mismatch: got %s, want %s", actualHash, expectedHash)
		}
		slog.Info("JRE SHA256 verified", "hash", actualHash)
	}

	// Extract
	if runtime.GOOS == "windows" {
		return "", "", fmt.Errorf("Windows JRE extraction not implemented — install Java manually")
	}

	if err := extractTarGz(archivePath, jreDir); err != nil {
		return "", "", fmt.Errorf("extract JRE: %w", err)
	}
	os.Remove(archivePath)

	// Find java binary inside extracted dir (it's in a subdirectory like jdk-17.0.x-jre/)
	javaPath := ""
	filepath.Walk(jreDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Base(path) == "java" && strings.Contains(path, "bin") {
			javaPath = path
			return filepath.SkipAll
		}
		return nil
	})
	if javaPath == "" {
		return "", "", fmt.Errorf("java binary not found in extracted JRE")
	}

	os.Chmod(javaPath, 0o755)
	return jreDir, javaPath, nil
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, header.Name)
		// Prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0o755)
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				continue
			}
			io.Copy(out, tr)
			out.Close()
		}
	}
	return nil
}

// h2ExportJSON is the top-level structure of h2-export.json.
type h2ExportJSON struct {
	Version int                          `json:"version"`
	Maps    map[string]map[string]string `json:"maps"` // mapName → key → base64(value)
}

func ImportExportJSON(homeDir, exportPath string, store storage.Store) (*MigrateResult, error) {
	data, err := os.ReadFile(exportPath)
	if err != nil {
		return nil, fmt.Errorf("read export file: %w", err)
	}

	var export h2ExportJSON
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("parse export file: %w", err)
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
			if err := json.Unmarshal(raw, &ns); err != nil {
				slog.Warn("Failed to parse namespace", "ws", wsID, "ns", nsID, "err", err)
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
			os.MkdirAll(nsDir, 0o755)
			nsConfigPath := filepath.Join(nsDir, "namespace.yml")

			if err := os.WriteFile(nsConfigPath, yamlBytes, 0o644); err != nil {
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
	stateMap, ok := maps["launcher!state"]
	if !ok {
		return
	}
	for key, b64 := range stateMap {
		raw, _ := base64.StdEncoding.DecodeString(b64)
		if key == "selectedWorkspace" {
			var val string
			if json.Unmarshal(raw, &val) == nil && val != "" {
				store.SetState(storage.LauncherState{WorkspaceID: val})
				slog.Info("Migrated launcher state", "selectedWorkspace", val)
			}
		}
	}
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

func fetchText(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func downloadFile(dest, url string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(dest)
		return err
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
