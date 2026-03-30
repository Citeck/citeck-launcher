package h2migrate

import (
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
	"strings"
	"time"

	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

const (
	// JarURL is the download URL for the H2 export JAR.
	JarURL = "https://github.com/Citeck/citeck-launcher/releases/download/v2.0.0/h2-export.jar"
	// JarSHA256 is the expected SHA256 hash of the JAR file.
	JarSHA256 = "" // TODO: set after first release build
	// JarName is the filename for the downloaded JAR.
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

// RunJarMigration downloads the H2 export JAR, runs it, and imports the result.
func RunJarMigration(homeDir string, javaPath string, store storage.Store) (*MigrateResult, error) {
	h2Path := filepath.Join(homeDir, "storage.db")
	jarPath := filepath.Join(homeDir, JarName)
	exportPath := filepath.Join(homeDir, "h2-export.json")

	// Step 1: Download JAR
	slog.Info("Downloading H2 export tool", "url", JarURL)
	if err := downloadFile(jarPath, JarURL); err != nil {
		return nil, fmt.Errorf("download h2-export.jar: %w", err)
	}
	defer os.Remove(jarPath) // cleanup JAR after migration

	// Step 2: Verify SHA256
	if JarSHA256 != "" {
		hash, err := fileSHA256(jarPath)
		if err != nil {
			return nil, fmt.Errorf("compute jar hash: %w", err)
		}
		if hash != JarSHA256 {
			os.Remove(jarPath)
			return nil, fmt.Errorf("jar SHA256 mismatch: got %s, want %s", hash, JarSHA256)
		}
		slog.Info("JAR SHA256 verified", "hash", hash)
	} else {
		slog.Warn("JAR SHA256 not configured, skipping verification")
	}

	// Step 3: Run JAR
	slog.Info("Running H2 export", "jar", jarPath, "db", h2Path)
	cmd := exec.Command(javaPath, "-jar", jarPath, h2Path, exportPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run h2-export.jar: %w", err)
	}
	defer os.Remove(exportPath) // cleanup JSON after import

	// Step 4: Import JSON
	slog.Info("Importing exported data", "file", exportPath)
	return ImportExportJSON(homeDir, exportPath, store)
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
