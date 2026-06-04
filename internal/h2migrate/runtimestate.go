package h2migrate

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// runtimeStateMapPrefix is the Kotlin scope-name + scope-delimiter prefix used
// by Database.kt::getRepoKey for per-namespace runtime state. The key portion
// after the prefix is "<wsId>:<nsId>" (data repo) or
// "<wsId>:<nsId>/changedRuntimeFiles" (file overlay repo).
const runtimeStateMapPrefix = "namespace-runtime-state!"

// importRuntimeState walks every namespace-runtime-state map in the export and
// folds it into the Go runtime's state files / disk layout:
//
//   - manualStoppedApps    -> state-<nsID>.json {ManualStoppedApps}
//   - editedAndLockedApps  -> state-<nsID>.json {EditedLockedApps}
//   - editedApps           -> state-<nsID>.json {EditedApps}      (shape-translated)
//   - changedRuntimeFiles  -> volumesBase/<key>                   (one file per entry)
//     AND state-<nsID>.json {EditedFiles}
//   - bundleDef            -> state-<nsID>.json {CachedBundle}    (shape-translated)
//
// Status is intentionally skipped: it is recomputed by the state machine on
// first start. bundleDef IS migrated because the upstream bundles repo can
// reorganize (e.g. bundle moves develop/ → release/) and the Kotlin contract
// is to keep using the previously-resolved BundleDef rather than failing to
// resolve. The Go server already implements the same fallback rule at
// daemon/server.go (uses state.CachedBundle when bundlesService can't find
// the ref); we just need to seed it from the Kotlin store.
func importRuntimeState(homeDir string, maps map[string]map[string]string, store storage.Store, result *MigrateResult) error {
	// First pass: locate the file-overlay maps separately so we can fold them
	// into the same per-namespace state struct.
	dataMaps := make(map[string]map[string]string) // wsId:nsId -> entries
	fileOverlayMaps := make(map[string]map[string]string)

	for mapName, entries := range maps {
		rest, ok := strings.CutPrefix(mapName, runtimeStateMapPrefix)
		if !ok {
			continue
		}
		if repoKey, isOverlay := strings.CutSuffix(rest, "/changedRuntimeFiles"); isOverlay {
			fileOverlayMaps[repoKey] = entries
			continue
		}
		// Skip any other sub-scopes we don't recognize so legacy keys don't
		// silently overwrite the data-repo entry.
		if strings.Contains(rest, "/") {
			continue
		}
		dataMaps[rest] = entries
	}

	// Union of repo keys across both map kinds — a namespace may have edited
	// files but no detach/edit state, or vice versa.
	repoKeys := make(map[string]struct{}, len(dataMaps)+len(fileOverlayMaps))
	for k := range dataMaps {
		repoKeys[k] = struct{}{}
	}
	for k := range fileOverlayMaps {
		repoKeys[k] = struct{}{}
	}

	for repoKey := range repoKeys {
		wsID, nsID, ok := splitNsRepoKey(repoKey)
		if !ok {
			slog.Warn("Skipping runtime-state map with unexpected key", "repoKey", repoKey)
			continue
		}

		state := loadOrInitState(homeDir, wsID, nsID)

		if data := dataMaps[repoKey]; data != nil {
			applyDataRepoEntries(data, state, wsID, nsID)
		}

		volumesBase := resolveVolumesBaseForMigration(homeDir, wsID, nsID)
		if files := fileOverlayMaps[repoKey]; len(files) > 0 {
			applyChangedRuntimeFiles(files, volumesBase, state, wsID, nsID, result)
		}

		data, merr := json.Marshal(state)
		if merr != nil {
			slog.Error("CRITICAL: cannot marshal migrated state — aborting", "ws", wsID, "ns", nsID, "err", merr)
			return fmt.Errorf("marshal migrated state %s/%s: %w", wsID, nsID, merr)
		}
		var rt namespace.NsPersistedState
		if uerr := json.Unmarshal(data, &rt); uerr != nil {
			slog.Error("CRITICAL: migrated state JSON does not round-trip — aborting", "ws", wsID, "ns", nsID, "err", uerr)
			return fmt.Errorf("state round-trip %s/%s: %w", wsID, nsID, uerr)
		}
		if err := store.SaveNamespaceState(wsID, nsID, string(state.Status), string(data)); err != nil {
			return fmt.Errorf("save migrated state %s/%s: %w", wsID, nsID, err)
		}
		bundleVersion := ""
		if state.CachedBundle != nil {
			bundleVersion = state.CachedBundle.Key.Version
		}
		slog.Info("Migrated namespace runtime state",
			"ws", wsID, "ns", nsID,
			"manualStopped", len(state.ManualStoppedApps),
			"locked", len(state.EditedLockedApps),
			"editedApps", len(state.EditedApps),
			"editedFiles", len(state.EditedFiles),
			"cachedBundle", bundleVersion,
		)
	}
	return nil
}

// splitNsRepoKey parses "<wsId>:<nsId>" — wsId / nsId contain no ':' / '!' by
// validator contract, so a single split on the first ':' is unambiguous.
func splitNsRepoKey(key string) (wsID, nsID string, ok bool) {
	idx := strings.Index(key, ":")
	if idx <= 0 || idx == len(key)-1 {
		return "", "", false
	}
	return key[:idx], key[idx+1:], true
}

// resolveVolumesBaseForMigration mirrors config.ResolveVolumesBase for desktop
// mode without importing the config package (which would pull docker / SQLite
// init side effects into the migrator). Migration only runs on the desktop
// path, so the desktop layout is the only one we need.
func resolveVolumesBaseForMigration(homeDir, wsID, nsID string) string {
	return filepath.Join(homeDir, "ws", wsID, "ns", nsID, "rtfiles")
}

// loadOrInitState reads any preexisting state-<nsID>.json (test fixture, or a
// previous partial migration run) and returns a non-nil state to fill in.
func loadOrInitState(homeDir, wsID, nsID string) *namespace.NsPersistedState {
	volumesBase := resolveVolumesBaseForMigration(homeDir, wsID, nsID)
	if existing := namespace.LoadNsState(volumesBase, nsID); existing != nil {
		return existing
	}
	return &namespace.NsPersistedState{}
}

func applyDataRepoEntries(entries map[string]string, state *namespace.NsPersistedState, wsID, nsID string) {
	for key, b64 := range entries {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			slog.Warn("Skipping malformed runtime-state entry", "ws", wsID, "ns", nsID, "key", key, "err", err)
			continue
		}
		switch key {
		case "manualStoppedApps":
			state.ManualStoppedApps = decodeStringArray(raw, state.ManualStoppedApps)
		case "editedAndLockedApps":
			state.EditedLockedApps = decodeStringArray(raw, state.EditedLockedApps)
		case "editedApps":
			state.EditedApps = decodeEditedAppsMap(raw, state.EditedApps, wsID, nsID)
		case "bundleDef":
			state.CachedBundle = decodeCachedBundle(raw, state.CachedBundle, wsID, nsID)
			// status intentionally ignored — see importRuntimeState doc.
		}
	}
}

func decodeStringArray(raw []byte, existing []string) []string {
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return existing
	}
	// Preserve any prior entries from a previous partial migration run, but
	// dedupe — set semantics on the Kotlin side.
	seen := make(map[string]struct{}, len(existing)+len(arr))
	merged := make([]string, 0, len(existing)+len(arr))
	for _, v := range existing {
		if _, dup := seen[v]; dup || v == "" {
			continue
		}
		seen[v] = struct{}{}
		merged = append(merged, v)
	}
	for _, v := range arr {
		if _, dup := seen[v]; dup || v == "" {
			continue
		}
		seen[v] = struct{}{}
		merged = append(merged, v)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// decodeCachedBundle translates the Kotlin BundleDef wire shape into Go's
// bundle.Def. An existing non-empty cache from a prior partial migration run
// is preserved when the incoming blob is malformed or empty; this matches the
// Kotlin behavior where a parse failure falls back to BundleDef.EMPTY (so
// having any prior value is strictly better than losing it).
func decodeCachedBundle(raw []byte, existing *bundle.Def, wsID, nsID string) *bundle.Def {
	def, err := decodeKotlinBundleDef(raw)
	if err != nil {
		slog.Warn("Skipping bundleDef blob", "ws", wsID, "ns", nsID, "err", err)
		return existing
	}
	if def.IsEmpty() {
		return existing
	}
	return &def
}

func decodeEditedAppsMap(raw []byte, existing map[string]appdef.ApplicationDef, wsID, nsID string) map[string]appdef.ApplicationDef {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		slog.Warn("Skipping editedApps blob", "ws", wsID, "ns", nsID, "err", err)
		return existing
	}
	if len(rawMap) == 0 {
		return existing
	}
	out := existing
	if out == nil {
		out = make(map[string]appdef.ApplicationDef, len(rawMap))
	}
	for appName, blob := range rawMap {
		def, err := decodeKotlinApplicationDef(blob)
		if err != nil {
			slog.Warn("Skipping edited app", "ws", wsID, "ns", nsID, "app", appName, "err", err)
			continue
		}
		if def.Name == "" {
			def.Name = appName
		}
		out[appName] = def
	}
	return out
}

func applyChangedRuntimeFiles(
	entries map[string]string,
	volumesBase string,
	state *namespace.NsPersistedState,
	wsID, nsID string,
	result *MigrateResult,
) {
	// The Kotlin contract: key is a Unix-style local path relative to the
	// namespace rtfiles dir; value is the raw file bytes. Go runtime stores
	// edited files at exactly volumesBase/<key>, so we write them straight
	// through without any path rewriting.
	seen := make(map[string]struct{}, len(state.EditedFiles))
	for _, p := range state.EditedFiles {
		seen[p] = struct{}{}
	}
	for key, b64 := range entries {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			slog.Warn("Skipping malformed runtime file", "ws", wsID, "ns", nsID, "key", key, "err", err)
			result.Errors++
			continue
		}
		if !safeRelativePath(key) {
			slog.Warn("Skipping runtime file with unsafe path", "ws", wsID, "ns", nsID, "key", key)
			result.Errors++
			continue
		}
		dest := filepath.Join(volumesBase, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { //nolint:gosec // G301: per-namespace volume dirs match runtime layout
			slog.Warn("Failed to create runtime file dir", "ws", wsID, "ns", nsID, "key", key, "err", err)
			result.Errors++
			continue
		}
		if err := os.WriteFile(dest, raw, 0o644); err != nil { //nolint:gosec // G306: runtime files match the runtime's own permission scheme
			slog.Warn("Failed to write runtime file", "ws", wsID, "ns", nsID, "key", key, "err", err)
			result.Errors++
			continue
		}
		if _, dup := seen[key]; !dup {
			seen[key] = struct{}{}
			state.EditedFiles = append(state.EditedFiles, key)
		}
	}
}

// safeRelativePath rejects keys that would escape the rtfiles dir. The Kotlin
// side guarantees this already (relativeTo + startsWith filesDir checks), but
// we re-validate at the trust boundary.
func safeRelativePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	if strings.Contains(p, "\\") {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return false
	}
	return true
}
