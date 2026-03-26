package h2migrate

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// MigrateResult holds the result of an H2 → SQLite migration.
type MigrateResult struct {
	Workspaces int `json:"workspaces"`
	Namespaces int `json:"namespaces"`
	Secrets    int `json:"secrets"`
	Errors     int `json:"errors"`
}

// NeedsMigration checks if migration is needed:
// storage.db exists but launcher.db does not.
// Returns (true, nil) if migration needed, (false, nil) if not, (false, err) on access problems.
func NeedsMigration(homeDir string) (bool, error) {
	h2Path := filepath.Join(homeDir, "storage.db")
	sqlitePath := filepath.Join(homeDir, "launcher.db")

	_, h2Err := os.Stat(h2Path)
	if h2Err != nil {
		if os.IsNotExist(h2Err) {
			return false, nil // no H2 database
		}
		return false, fmt.Errorf("check h2 database: %w", h2Err)
	}

	_, sqliteErr := os.Stat(sqlitePath)
	if sqliteErr == nil {
		return false, nil // SQLite already exists
	}
	if !os.IsNotExist(sqliteErr) {
		return false, fmt.Errorf("check sqlite database: %w", sqliteErr)
	}

	return true, nil // H2 exists, SQLite doesn't
}

// Migrate reads data from H2 MVStore (storage.db) and writes it to a SQLite store.
// It also creates namespace.yml files in the workspace directory structure.
func Migrate(homeDir string, store storage.Store) (*MigrateResult, error) {
	h2Path := filepath.Join(homeDir, "storage.db")
	result := &MigrateResult{}

	slog.Info("Starting H2 → SQLite migration", "source", h2Path)

	mvs, err := OpenMVStore(h2Path)
	if err != nil {
		return result, fmt.Errorf("open h2 database: %w", err)
	}
	defer mvs.Close()

	// List all maps to understand what data exists
	mapNames, err := mvs.ListMapNames()
	if err != nil {
		slog.Warn("Failed to list map names, trying filesystem fallback", "err", err)
		return migrateFromFilesystem(homeDir, store)
	}

	slog.Info("H2 maps found", "count", len(mapNames), "names", mapNames)

	// Extract workspaces
	if err := migrateWorkspaces(mvs, mapNames, store, result); err != nil {
		slog.Error("Workspace migration failed", "err", err)
		result.Errors++
	}

	// Extract secrets/auth
	if err := migrateSecrets(mvs, mapNames, store, result); err != nil {
		slog.Error("Secret migration failed", "err", err)
		result.Errors++
	}

	// Migrate namespace configs from workspace directories (they're already YAML files)
	if err := migrateNamespaceConfigs(homeDir, result); err != nil {
		slog.Error("Namespace config migration failed", "err", err)
		result.Errors++
	}

	slog.Info("Migration complete", "result", result)
	return result, nil
}

// migrateWorkspaces extracts workspace entities from H2 maps.
func migrateWorkspaces(mvs *MVStore, mapNames []string, store storage.Store, result *MigrateResult) error {
	// Workspace entities are stored in maps named like "entities!workspace"
	for _, name := range mapNames {
		if !strings.Contains(name, "workspace") {
			continue
		}

		entries, err := mvs.ReadMap(name)
		if err != nil {
			slog.Warn("Failed to read map", "name", name, "err", err)
			continue
		}

		for id, data := range entries {
			ws, err := parseWorkspaceJSON(id, data)
			if err != nil {
				slog.Warn("Failed to parse workspace", "id", id, "err", err)
				continue
			}
			if err := store.SaveWorkspace(*ws); err != nil {
				slog.Warn("Failed to save workspace", "id", ws.ID, "err", err)
				continue
			}
			result.Workspaces++
			slog.Info("Migrated workspace", "id", ws.ID, "name", ws.Name)
		}
	}
	return nil
}

// migrateSecrets extracts auth/secret data from H2 maps.
func migrateSecrets(mvs *MVStore, mapNames []string, store storage.Store, result *MigrateResult) error {
	// Auth data is stored in maps named like "auth" or containing "secret"
	for _, name := range mapNames {
		if !strings.Contains(name, "auth") && !strings.Contains(name, "secret") {
			continue
		}

		entries, err := mvs.ReadMap(name)
		if err != nil {
			slog.Warn("Failed to read secret map", "name", name, "err", err)
			continue
		}

		for id, data := range entries {
			secret, err := parseSecretJSON(id, data)
			if err != nil {
				slog.Warn("Failed to parse secret", "id", id, "err", err)
				continue
			}
			if err := store.SaveSecret(*secret); err != nil {
				slog.Warn("Failed to save secret", "id", secret.ID, "err", err)
				continue
			}
			result.Secrets++
			slog.Info("Migrated secret", "id", secret.ID, "type", secret.Type)
		}
	}
	return nil
}

// migrateNamespaceConfigs ensures namespace YAML configs exist in the workspace dir structure.
// The Kotlin app stores them via the entity system, but the actual YAML generation happens
// when the namespace is created. If namespace.yml files already exist in ws/{id}/ns/{id}/,
// they don't need migration.
func migrateNamespaceConfigs(homeDir string, result *MigrateResult) error {
	wsDir := filepath.Join(homeDir, "ws")
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, wsEntry := range entries {
		if !wsEntry.IsDir() {
			continue
		}

		nsDir := filepath.Join(wsDir, wsEntry.Name(), "ns")
		nsEntries, err := os.ReadDir(nsDir)
		if err != nil {
			continue
		}

		for _, nsEntry := range nsEntries {
			if !nsEntry.IsDir() {
				continue
			}

			configPath := filepath.Join(nsDir, nsEntry.Name(), "namespace.yml")
			if _, err := os.Stat(configPath); err == nil {
				result.Namespaces++
				slog.Info("Found namespace config", "ws", wsEntry.Name(), "ns", nsEntry.Name())
			}
		}
	}
	return nil
}

// migrateFromFilesystem is a fallback that creates workspace records from the directory structure
// when the H2 file can't be parsed.
func migrateFromFilesystem(homeDir string, store storage.Store) (*MigrateResult, error) {
	result := &MigrateResult{}
	slog.Info("Using filesystem fallback migration")

	wsDir := filepath.Join(homeDir, "ws")
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wsID := entry.Name()

		// Check if workspace has a repo dir (valid workspace)
		repoDir := filepath.Join(wsDir, wsID, "repo")
		if _, err := os.Stat(repoDir); err != nil {
			continue
		}

		ws := storage.WorkspaceDto{
			ID:   wsID,
			Name: wsID,
		}
		if err := store.SaveWorkspace(ws); err != nil {
			slog.Warn("Failed to save workspace", "id", wsID, "err", err)
			result.Errors++
			continue
		}
		result.Workspaces++

		// Count namespaces
		nsDir := filepath.Join(wsDir, wsID, "ns")
		nsEntries, err := os.ReadDir(nsDir)
		if err != nil {
			continue
		}
		for _, ns := range nsEntries {
			if ns.IsDir() {
				result.Namespaces++
			}
		}
	}

	return result, nil
}

// parseWorkspaceJSON parses a workspace entity from Jackson JSON bytes.
func parseWorkspaceJSON(id string, data []byte) (*storage.WorkspaceDto, error) {
	// Kotlin stores workspace data as Jackson JSON
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	ws := &storage.WorkspaceDto{ID: id}

	if name, ok := raw["name"].(string); ok {
		ws.Name = name
	}
	if repoURL, ok := raw["repoUrl"].(string); ok {
		ws.RepoURL = repoURL
	}
	if repoBranch, ok := raw["repoBranch"].(string); ok {
		ws.RepoBranch = repoBranch
	}

	if ws.Name == "" {
		ws.Name = id
	}

	return ws, nil
}

// parseSecretJSON parses a secret/auth entry from Jackson JSON bytes.
func parseSecretJSON(id string, data []byte) (*storage.Secret, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	secret := &storage.Secret{
		SecretMeta: storage.SecretMeta{
			ID:   id,
			Name: id,
		},
	}

	if name, ok := raw["name"].(string); ok && name != "" {
		secret.Name = name
	}
	if typ, ok := raw["type"].(string); ok {
		secret.Type = storage.SecretType(typ)
	}
	if value, ok := raw["value"].(string); ok {
		secret.Value = value
	}
	if token, ok := raw["token"].(string); ok && secret.Value == "" {
		secret.Value = token
		secret.Type = storage.SecretGitToken
	}
	if password, ok := raw["password"].(string); ok && secret.Value == "" {
		secret.Value = password
		secret.Type = storage.SecretBasicAuth
	}

	return secret, nil
}
