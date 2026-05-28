package h2migrate

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

// internalMapNames is the set of MVStore housekeeping map names that carry
// no user data — skipped by DumpForImport so consumers only see entity maps.
var internalMapNames = map[string]struct{}{
	"_":                {},
	"layout":           {},
	"meta":             {},
	"undoLog":          {},
	"openTransactions": {},
}

// DumpForImport reads every relevant map from the MVStore and re-base64s
// each value so downstream import* helpers can decode JSON OR a base64
// encrypted blob from a uniform shape — the same shape the now-removed
// JAR exporter produced.
func (s *MVStore) DumpForImport() (map[string]map[string]string, error) {
	mapNames, err := s.ListMapNames()
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(mapNames))
	for _, name := range mapNames {
		if _, internal := internalMapNames[name]; internal {
			continue
		}
		if strings.HasPrefix(name, "undoLog") || strings.HasPrefix(name, "openTransactions") {
			continue
		}
		entries, err := s.ReadMap(name)
		if err != nil {
			slog.Warn("Failed to read map for dump", "name", name, "err", err)
			continue
		}
		if len(entries) == 0 {
			continue
		}
		converted := make(map[string]string, len(entries))
		for k, v := range entries {
			converted[k] = base64.StdEncoding.EncodeToString(v)
		}
		out[name] = converted
	}
	return out, nil
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
			dto, err := parseWorkspaceJSON(id, raw)
			if err != nil {
				slog.Warn("Failed to parse workspace", "id", id, "err", err)
				continue
			}
			if err := store.SaveWorkspace(*dto); err != nil {
				slog.Warn("Failed to save workspace", "id", id, "err", err)
				result.Errors++
				continue
			}
			result.Workspaces++
			slog.Info("Migrated workspace", "id", id, "name", dto.Name)
		}
	}
}

func importNamespaces(homeDir string, maps map[string]map[string]string, _ storage.Store, result *MigrateResult) {
	for mapName, entries := range maps {
		// Match entities/{wsId}!namespace (not versions, not runtime)
		if !strings.HasSuffix(mapName, "!namespace") {
			continue
		}
		if strings.Contains(mapName, "/versions") || strings.Contains(mapName, "runtime") {
			continue
		}

		wsID := strings.TrimPrefix(mapName, "entities/")
		wsID = strings.TrimSuffix(wsID, "!namespace")

		for nsID, b64 := range entries {
			raw, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				continue
			}

			nsCfg, err := buildNamespaceYAMLMap(nsID, raw)
			if err != nil {
				slog.Warn("Failed to parse namespace", "ws", wsID, "ns", nsID, "err", err)
				continue
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
			slog.Info("Migrated namespace", "ws", wsID, "ns", nsID, "name", nsCfg["name"], "bundle", nsCfg["bundleRef"])
		}
	}
}

// buildNamespaceYAMLMap turns a Kotlin Jackson-serialized NamespaceConfig blob
// into a YAML-ready map matching Go's namespace.Config field naming. The Go
// YAML field for ProxyProps is `proxy`, but Kotlin's Jackson property is
// `citeckProxy` — we rewrite the key so the migrated namespace.yml round-trips
// through ParseNamespaceConfig.
//
// All ten Kotlin top-level fields (id, name, snapshot, template,
// authentication, bundleRef, pgAdmin, mongodb, citeckProxy, webapps) are
// preserved; webapps inner shape (cloudConfig / environments / debugPort /
// heapSize / memoryLimit / serverPort / javaOpts / dataSources /
// springProfiles) passes through as a nested map without loss.
func buildNamespaceYAMLMap(nsID string, raw []byte) (map[string]any, error) {
	var src map[string]any
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, err //nolint:wrapcheck // caller logs with context
	}
	out := make(map[string]any, len(src)+1)
	out["id"] = nsID
	if v, ok := src["name"]; ok {
		out["name"] = v
	} else {
		out["name"] = nsID
	}
	for _, k := range []string{"snapshot", "template", "bundleRef"} {
		if v, ok := src[k]; ok && !isEmptyAny(v) {
			out[k] = v
		}
	}
	if v, ok := src["authentication"]; ok && v != nil {
		out["authentication"] = v
	}
	if v, ok := src["pgAdmin"]; ok && !isEmptyAny(v) {
		out["pgAdmin"] = v
	}
	if v, ok := src["mongodb"]; ok && !isEmptyAny(v) {
		out["mongodb"] = v
	}
	// Kotlin Jackson key `citeckProxy` -> Go YAML key `proxy`.
	if v, ok := src["citeckProxy"]; ok && !isEmptyAny(v) {
		out["proxy"] = v
	}
	if v, ok := src["webapps"]; ok && !isEmptyAny(v) {
		out["webapps"] = v
	}
	return out, nil
}

func isEmptyAny(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
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

	// Store the encrypted blob as-is — Go launcher will decrypt when master password is provided.
	if err := store.PutSecretBlob(storageB64); err != nil {
		slog.Warn("Failed to import secrets blob", "err", err)
		result.Errors++
		return
	}
	result.Secrets = 1
	slog.Info("Migrated encrypted secrets blob")
}

// importGitRepos migrates Kotlin's `git-repo!instances` map into
// storage.GitRepoState rows. Kotlin keyed the map by relative repo path
// (e.g. `ws/{wsId}/repo`, `ws/{wsId}/bundles/{bundleId}`); Go uses the same
// key shape (relativeRepoPath in internal/git) so the migration is a direct
// copy of (lastSyncTimeMs, hashOfLastCommit). repoProps is discarded — Go
// derives URL/branch from workspace config on every sync, so persisting them
// would invite drift.
func importGitRepos(maps map[string]map[string]string, store storage.Store, result *MigrateResult) {
	entries, ok := maps["git-repo!instances"]
	if !ok {
		return
	}
	for path, b64 := range entries {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		var dto struct {
			LastSyncTimeMs   int64  `json:"lastSyncTimeMs"`
			HashOfLastCommit string `json:"hashOfLastCommit"`
		}
		if err := json.Unmarshal(raw, &dto); err != nil {
			slog.Warn("Failed to parse git repo instance", "path", path, "err", err)
			continue
		}
		if dto.LastSyncTimeMs <= 0 {
			continue
		}
		state := storage.GitRepoState{
			Path:           path,
			LastSyncMs:     dto.LastSyncTimeMs,
			LastCommitHash: dto.HashOfLastCommit,
		}
		if err := store.SetGitRepoState(state); err != nil {
			slog.Warn("Failed to save git repo state", "path", path, "err", err)
			result.Errors++
			continue
		}
		result.GitRepos++
	}
	if result.GitRepos > 0 {
		slog.Info("Migrated git repo sync state", "count", result.GitRepos)
	}
}

func importState(maps map[string]map[string]string, store storage.Store) {
	var wsID string
	if stateMap, ok := maps["launcher!state"]; ok {
		if b64, ok := stateMap["selectedWorkspace"]; ok {
			raw, _ := base64.StdEncoding.DecodeString(b64)
			_ = json.Unmarshal(raw, &wsID)
		}
	}

	// Walk EVERY workspace-state!{wsID} map — Kotlin tracked the selected
	// namespace per workspace, so a user with three workspaces and three
	// different selections would otherwise lose two of them on migration.
	selectedNs := make(map[string]string)
	for mapName, entries := range maps {
		const prefix = "workspace-state!"
		if !strings.HasPrefix(mapName, prefix) {
			continue
		}
		thisWS := strings.TrimPrefix(mapName, prefix)
		if thisWS == "" {
			continue
		}
		b64, ok := entries["selectedNamespace"]
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		var nsID string
		if err := json.Unmarshal(raw, &nsID); err != nil || nsID == "" {
			continue
		}
		selectedNs[thisWS] = nsID
	}

	if wsID == "" && len(selectedNs) == 0 {
		return
	}

	state := storage.LauncherState{WorkspaceID: wsID}
	if len(selectedNs) > 0 {
		state.SelectedNs = selectedNs
	}
	_ = store.SetState(state)
	slog.Info("Migrated launcher state", "workspace", wsID, "namespaces", len(selectedNs))
}
