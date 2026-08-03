package h2migrate

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/citeck/citeck-launcher/internal/git"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
	"gopkg.in/yaml.v3"
)

// kotlinBackupSuffix is the filename suffix appended to the legacy storage.db
// when Migrate creates its pre-migration snapshot. The backup is a one-time
// parachute: if a future regression breaks the H2 reader, the user can hand
// this byte-identical copy to support (or roll back to Kotlin 1.x) instead of
// being stranded.
const kotlinBackupSuffix = ".kotlin-bak"

// backupKotlinStorage copies storage.db to storage.db.kotlin-bak when no
// previous backup exists. The backup must be:
//   - idempotent: subsequent migrations leave the first snapshot untouched so
//     it always reflects the original pre-migration state, not the most recent
//     attempt.
//   - atomic: write to a sibling .tmp and rename so a mid-copy crash never
//     leaves a truncated parachute that would shadow the real storage.db.
//   - non-destructive: storage.db is opened read-only via io.Copy; SHA256 of
//     the source is guaranteed unchanged.
//
// Errors are returned to the caller but treated as best-effort by Migrate:
// users running on a read-only filesystem or with the file already gone
// should still be allowed to attempt the migration.
func backupKotlinStorage(h2Path string) error {
	backupPath := h2Path + kotlinBackupSuffix

	if _, err := os.Stat(backupPath); err == nil {
		slog.Info("Kotlin storage backup already exists, skipping", "path", backupPath)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat backup %s: %w", backupPath, err)
	}

	src, err := os.Open(h2Path) //nolint:gosec // G304: h2Path is the launcher's own storage.db
	if err != nil {
		return fmt.Errorf("open storage.db: %w", err)
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat storage.db: %w", err)
	}

	tmpPath := backupPath + ".tmp"
	// Best-effort cleanup of a stale temp from a previous crashed run.
	_ = os.Remove(tmpPath)

	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: tmpPath is derived from the launcher's own storage.db backup target
	if err != nil {
		return fmt.Errorf("create backup tmp: %w", err)
	}

	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("copy storage.db: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync backup tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close backup tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod backup tmp: %w", err)
	}
	if err := os.Rename(tmpPath, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename backup: %w", err)
	}

	slog.Info("Backed up storage.db", "path", backupPath, "bytes", info.Size())
	return nil
}

// (backup uses a direct io.Copy rather than fsutil.AtomicWriteFile because
// AtomicWriteFile materializes the entire payload in RAM, which would be
// wasteful for a multi-MB MVStore.)

// MigrateResult holds the result of an H2 → SQLite migration.
//
// Degraded means the migration is LOSSY, in one of two distinct ways:
//
//   - FallbackReason set: the pure-Go MVStore reader could not be used at all
//     and the filesystem fallback ran, reconstructing only what the on-disk ws/
//     tree can prove.
//   - PartialReason set: the reader DID open the store and its layout/meta
//     index verified, but a lenient user-data map read had to drop entries or
//     whole sub-trees. Most data came through; some rows are simply gone.
//
// The two are mutually exclusive. See DegradedMigration for the durable record
// the UI reads.
type MigrateResult struct {
	Workspaces     int    `json:"workspaces"`
	Namespaces     int    `json:"namespaces"`
	Secrets        int    `json:"secrets"`
	GitRepos       int    `json:"gitRepos"`
	Errors         int    `json:"errors"`
	Degraded       bool   `json:"degraded,omitempty"`
	FallbackReason string `json:"fallbackReason,omitempty"`
	PartialReason  string `json:"partialReason,omitempty"`
}

// NeedsMigration reports whether the legacy Kotlin store exists without a
// SQLite replacement. Returns (true, nil) when storage.db exists and
// launcher.db does not.
func NeedsMigration(homeDir string) (bool, error) {
	h2Path := filepath.Join(homeDir, "storage.db")
	sqlitePath := filepath.Join(homeDir, "launcher.db")

	_, h2Err := os.Stat(h2Path)
	if h2Err != nil {
		if os.IsNotExist(h2Err) {
			return false, nil
		}
		return false, fmt.Errorf("check h2 database: %w", h2Err)
	}

	_, sqliteErr := os.Stat(sqlitePath)
	if sqliteErr == nil {
		return false, nil
	}
	if !os.IsNotExist(sqliteErr) {
		return false, fmt.Errorf("check sqlite database: %w", sqliteErr)
	}

	return true, nil
}

// Migrate reads data from H2 MVStore (storage.db) using the pure-Go reader
// and folds it into SQLite + on-disk namespace.yml / state-<nsID>.json files
// via the unified import* helpers. The MVStore file is opened read-only and
// is never modified.
//
// If the MVStore reader cannot extract any maps (corrupt header, missing
// layout, parser bug), Migrate falls back to migrateFromFilesystem which
// reconstructs minimal state from the on-disk ws/ tree alone.
func Migrate(homeDir string, store storage.Store) (*MigrateResult, error) {
	h2Path := filepath.Join(homeDir, "storage.db")
	result := &MigrateResult{}

	slog.Info("Starting H2 → SQLite migration", "source", h2Path)

	// Defense in depth: snapshot the pre-migration MVStore before opening it
	// for read. Failures are logged but non-fatal so a read-only home dir or
	// vanished storage.db still lets the migration attempt proceed (and fail
	// loudly on OpenMVStore below).
	if err := backupKotlinStorage(h2Path); err != nil {
		slog.Warn("Failed to back up Kotlin storage.db", "err", err)
	}

	mvs, err := OpenMVStore(h2Path)
	if err != nil {
		slog.Warn("Falling back to filesystem migration: open MVStore failed", "err", err)
		return migrateFromFilesystem(homeDir, store, fmt.Sprintf("cannot open storage.db: %v", err))
	}
	defer mvs.Close()

	maps, dumpErr := mvs.DumpForImport()
	if reason := dumpFallbackReason(maps, dumpErr); reason != "" {
		slog.Warn("Falling back to filesystem migration", "reason", reason, "source", h2Path, "err", dumpErr)
		if dumpErr != nil {
			reason = fmt.Sprintf("%s: %v", reason, dumpErr)
		}
		return migrateFromFilesystem(homeDir, store, reason)
	}

	slog.Info("H2 maps loaded", "count", len(maps))

	importWorkspaces(maps, store, result)
	if err := importNamespaces(maps, store, result); err != nil {
		return nil, err
	}
	importSecrets(maps, store, result)
	if err := importRuntimeState(homeDir, maps, store, result); err != nil {
		return nil, err
	}
	importGitRepos(maps, store, result)
	importState(maps, store)

	// A lenient user-map read can drop entries or whole sub-trees WITHOUT
	// failing the dump — same silent-data-loss class the strict layout/meta
	// read closed one level up, just scoped to a single map. Surface it the
	// same way: mark the run degraded and persist the durable record the
	// migration-status endpoint serves to the UI. No-op when the read was clean.
	result.recordPartialRead(homeDir, store, mvs.partialReadSummary())

	slog.Info("Migration complete", "result", result)
	return result, nil
}

// dumpFallbackReason returns a non-empty reason when the pure-Go reader's
// dump cannot be trusted. Both an error AND an empty map are treated as
// failure: a real H2 MVStore with user data always carries at least one
// entity map, and quietly proceeding with zero data would let a broken
// parser drop every workspace and secret on disk.
func dumpFallbackReason(maps map[string]map[string]string, err error) string {
	switch {
	case err != nil:
		return "dump error"
	case len(maps) == 0:
		return "empty dump (parser likely failed)"
	}
	return ""
}

// migrateFromFilesystem reconstructs workspace records and stub namespace.yml
// files from the on-disk ws/ tree when storage.db is unreadable.
//
// This path is LOSSY by construction, and the loss used to be silent and total
// for the workspace repo: the stub carried only ID and Name, so RepoURL came
// out empty, the bundle resolver fell back to the default public GitHub
// workspace, the namespace's real bundle repo vanished and private-registry
// pulls failed — with no warning anywhere in the UI. Two things change that:
//
//  1. {homeDir}/ws/{wsID}/repo IS a git clone, so the remote URL and branch are
//     recoverable from disk (best-effort — a missing or broken clone must never
//     fail the migration).
//  2. whatever remains unrecoverable is written to a durable
//     DegradedMigration record that the daemon surfaces on the
//     migration-status endpoint.
func migrateFromFilesystem(homeDir string, store storage.Store, reason string) (*MigrateResult, error) {
	result := &MigrateResult{Degraded: true, FallbackReason: reason}
	slog.Warn("Using filesystem fallback migration — this migration is LOSSY", "reason", reason)

	recoveredRepos, missingRepos, err := migrateWorkspacesFromFilesystem(homeDir, store, result)
	if err != nil {
		return nil, err
	}
	result.recordDegradation(homeDir, store, recoveredRepos, missingRepos)
	return result, nil
}

// migrateWorkspacesFromFilesystem walks ws/ and saves every workspace plus its
// stub namespaces, returning how many repo URLs were recovered and how many
// workspaces are left without one.
func migrateWorkspacesFromFilesystem(homeDir string, store storage.Store, result *MigrateResult) (recovered, missing int, _ error) {
	wsDir := filepath.Join(homeDir, "ws")
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read workspaces dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		wsID := entry.Name()
		repoDir := filepath.Join(wsDir, wsID, "repo")

		// RepoPullPeriod, AuthType and SecretID are genuinely unrecoverable
		// from the filesystem and are deliberately left zero. In particular we
		// do NOT guess AuthType from the URL scheme: an https:// remote is
		// equally likely to be public (NONE) or token-authenticated, and a
		// wrong TOKEN guess would send the daemon looking for a secret that
		// does not exist and fail the pull with a misleading auth error.
		ws := storage.WorkspaceDto{ID: wsID, Name: wsID}
		ws.RepoURL, ws.RepoBranch = recoverWorkspaceRepo(repoDir)
		switch {
		case ws.RepoURL != "":
			recovered++
			slog.Info("Recovered workspace repo from its on-disk clone",
				"ws", wsID, "repoUrl", ws.RepoURL, "branch", ws.RepoBranch)
		default:
			missing++
			slog.Warn("Workspace repo URL could not be recovered — the bundle resolver will fall back to the default workspace",
				"ws", wsID, "repoDir", repoDir)
		}

		if err := store.SaveWorkspace(ws); err != nil {
			slog.Warn("Failed to save workspace", "id", wsID, "err", err)
			result.Errors++
			continue
		}
		result.Workspaces++

		// A workspace with a clone but no ns/ directory used to be skipped
		// BEFORE SaveWorkspace and disappeared from the migrated store
		// entirely; it is now saved first and only its namespace walk is
		// skipped.
		nsDir := filepath.Join(wsDir, wsID, "ns")
		if _, err := os.Stat(nsDir); err != nil {
			continue
		}
		if err := migrateStubNamespaces(wsDir, wsID, nsDir, store, result); err != nil {
			return recovered, missing, err
		}
	}

	return recovered, missing, nil
}

func migrateStubNamespaces(wsDir, wsID, nsDir string, store storage.Store, result *MigrateResult) error {
	defaultBundleRef := ""
	wsCfgPath := filepath.Join(wsDir, wsID, "repo", "workspace-v1.yml")
	if data, err := os.ReadFile(wsCfgPath); err == nil { //nolint:gosec // G304: wsCfgPath is constructed from internal workspace dir
		defaultBundleRef = extractDefaultBundleRef(data)
	}

	nsEntries, err := os.ReadDir(nsDir)
	if err != nil {
		slog.Warn("Failed to list namespaces dir", "ws", wsID, "dir", nsDir, "err", err)
		return nil
	}
	for _, ns := range nsEntries {
		if !ns.IsDir() {
			continue
		}
		nsID := ns.Name()
		if _, exists, _ := store.LoadNamespaceConfig(wsID, nsID); exists {
			result.Namespaces++
			continue
		}
		stub, err := buildFallbackNamespaceYAML(nsID, defaultBundleRef)
		if err != nil {
			slog.Warn("Failed to build fallback namespace config", "ns", nsID, "err", err)
			result.Errors++
			continue
		}
		if _, verr := namespace.ValidateYAML(stub); verr != nil {
			slog.Error("CRITICAL: fallback namespace config is invalid — aborting migration",
				"ws", wsID, "ns", nsID, "err", verr)
			return fmt.Errorf("invalid fallback namespace %s/%s: %w", wsID, nsID, verr)
		}
		if err := store.SaveNamespaceConfig(wsID, nsID, nsID, string(stub)); err != nil {
			return fmt.Errorf("save fallback namespace %s/%s: %w", wsID, nsID, err)
		}
		result.Namespaces++
		slog.Info("Created stub namespace config", "ws", wsID, "ns", nsID, "bundleRef", defaultBundleRef)
	}
	return nil
}

// recoverWorkspaceRepo reads the git remote and branch back out of the 1.x
// workspace clone at {homeDir}/ws/{wsID}/repo.
//
// Best-effort by contract: every failure mode (no directory, not a git repo,
// no origin remote, detached HEAD) returns empty strings rather than an error,
// because a workspace with an unknown repo URL is still far better than a
// migration that refuses to run. HEAD is read as a SYMBOLIC reference so an
// unborn branch (a clone with no fetched commits) still yields its name.
//
// The URL half delegates to git.OriginURL — the shared origin-remote reader —
// so this stays one of ONE implementation rather than a third near-duplicate.
// Its error is deliberately dropped (only logged): the best-effort contract
// above says an unreadable remote yields "", never a failed migration.
func recoverWorkspaceRepo(repoDir string) (repoURL, branch string) {
	if originURL, oerr := git.OriginURL(repoDir); oerr == nil {
		repoURL = strings.TrimSpace(originURL)
	} else {
		slog.Debug("Workspace repo origin not readable", "dir", repoDir, "err", oerr)
	}

	repo, err := gogit.PlainOpen(repoDir)
	if err != nil {
		slog.Debug("Workspace repo clone not readable", "dir", repoDir, "err", err)
		return repoURL, ""
	}

	if ref, rerr := repo.Reference(plumbing.HEAD, false); rerr == nil && ref.Type() == plumbing.SymbolicReference {
		branch = ref.Target().Short()
	} else if head, herr := repo.Head(); herr == nil && head.Name().IsBranch() {
		branch = head.Name().Short()
	}

	return repoURL, branch
}

// buildFallbackNamespaceYAML produces the default Kotlin-parity namespace
// config used by the filesystem-fallback migration path. Default
// authentication mirrors AuthenticationProps.DEFAULT = BASIC + {admin, fet}.
func buildFallbackNamespaceYAML(nsID, defaultBundleRef string) ([]byte, error) {
	cfg := map[string]any{
		"id":   nsID,
		"name": nsID,
		"authentication": map[string]any{
			"type":  "BASIC",
			"users": []string{"admin", "fet"},
		},
	}
	if defaultBundleRef != "" {
		cfg["bundleRef"] = defaultBundleRef
	}
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal fallback namespace yaml: %w", err)
	}
	return out, nil
}

func extractDefaultBundleRef(data []byte) string {
	var cfg struct {
		NamespaceTemplates []struct {
			Config struct {
				BundleRef string `yaml:"bundleRef"`
			} `yaml:"config"`
		} `yaml:"namespaceTemplates"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	for _, t := range cfg.NamespaceTemplates {
		if t.Config.BundleRef != "" {
			return t.Config.BundleRef
		}
	}
	return ""
}

// parseWorkspaceJSON parses a workspace entity from Jackson JSON bytes.
//
// Kotlin's DurationSerializer emits Duration.toString() (e.g. "PT6H"), but the
// matching deserializer also accepts integer seconds, so hand-edited legacy
// blobs may carry repoPullPeriod as a number — converted below to "PTnS".
func parseWorkspaceJSON(id string, data []byte) (*storage.WorkspaceDto, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal workspace %s: %w", id, err)
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
	if authType, ok := raw["authType"].(string); ok {
		ws.AuthType = strings.TrimSpace(authType)
	}
	if period := decodeJacksonDuration(raw["repoPullPeriod"]); period != "" {
		ws.RepoPullPeriod = period
	}

	if ws.Name == "" {
		ws.Name = id
	}

	return ws, nil
}

// decodeJacksonDuration converts a Jackson-serialized Duration value (ISO-8601
// string from DurationSerializer, or integer seconds from the deserializer's
// VALUE_NUMBER_INT branch) to the launcher's wire format.
func decodeJacksonDuration(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == 0 {
			return ""
		}
		return fmt.Sprintf("PT%dS", int64(x))
	case int64:
		if x == 0 {
			return ""
		}
		return fmt.Sprintf("PT%dS", x)
	}
	return ""
}
