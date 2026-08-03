package h2migrate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gogitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/citeck/citeck-launcher/internal/storage"
)

// TestDumpFallbackReason locks in the defense-in-depth contract: any reader
// result other than "non-empty dump with nil error" must trigger the
// filesystem fallback. The empty-dump-with-nil-error case matters because
// a broken parser would otherwise silently drop every workspace and secret.
func TestDumpFallbackReason(t *testing.T) {
	cases := []struct {
		name    string
		maps    map[string]map[string]string
		err     error
		expFall bool
	}{
		{"error_triggers_fallback", nil, errors.New("boom"), true},
		{"nil_map_triggers_fallback", nil, nil, true},
		{"empty_map_triggers_fallback", map[string]map[string]string{}, nil, true},
		{"valid_dump_proceeds", map[string]map[string]string{"entities/global!workspace": {"a": ""}}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := dumpFallbackReason(tc.maps, tc.err)
			if tc.expFall {
				assert.NotEmpty(t, reason, "expected fallback to fire")
			} else {
				assert.Empty(t, reason, "expected fast path")
			}
		})
	}
}

// TestBuildFallbackNamespaceYAML covers the B3 default: filesystem-fallback
// migration must seed authentication.{type=BASIC, users=[admin, fet]} so the
// resulting namespace.yml mirrors Kotlin's NamespaceConfig.DEFAULT.
func TestBuildFallbackNamespaceYAML(t *testing.T) {
	body, err := buildFallbackNamespaceYAML("nsA", "community:2025.12")
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(body, &parsed))
	assert.Equal(t, "nsA", parsed["id"])
	assert.Equal(t, "community:2025.12", parsed["bundleRef"])
	auth, ok := parsed["authentication"].(map[string]any)
	require.True(t, ok, "authentication block must be present")
	assert.Equal(t, "BASIC", auth["type"])
	users, ok := auth["users"].([]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []any{"admin", "fet"}, users)
}

// TestFilesystemFallbackPopulatesAuthDefaults runs the full filesystem
// fallback (no storage.db readable, only a directory tree present) and
// asserts that each generated namespace.yml carries the default auth block.
func TestFilesystemFallbackPopulatesAuthDefaults(t *testing.T) {
	homeDir := t.TempDir()
	nsDir := filepath.Join(homeDir, "ws", "default", "ns", "demo")
	require.NoError(t, os.MkdirAll(nsDir, 0o755))

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	res, err := migrateFromFilesystem(homeDir, store, "test")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, res.Namespaces, 1)

	body, ok, err := store.LoadNamespaceConfig("default", "demo")
	require.NoError(t, err)
	require.True(t, ok)
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(body), &parsed))
	auth, ok := parsed["authentication"].(map[string]any)
	require.True(t, ok, "authentication block must be present in fallback yaml")
	assert.Equal(t, "BASIC", auth["type"])
}

// seedWorkspaceRepoClone materializes the 1.x on-disk shape of a workspace
// repo: {homeDir}/ws/{wsID}/repo is a real git clone with an `origin` remote
// and a checked-out branch. No commit is needed — HEAD is a symbolic ref from
// the moment the repo is initialized, which is also true of a real clone.
func seedWorkspaceRepoClone(t *testing.T, homeDir, wsID, url, branch string) {
	t.Helper()
	repoDir := filepath.Join(homeDir, "ws", wsID, "repo")
	require.NoError(t, os.MkdirAll(repoDir, 0o755))

	repo, err := gogit.PlainInit(repoDir, false)
	require.NoError(t, err)
	_, err = repo.CreateRemote(&gogitconfig.RemoteConfig{Name: "origin", URLs: []string{url}})
	require.NoError(t, err)
	require.NoError(t, repo.Storer.SetReference(
		plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(branch))))
}

// TestFilesystemFallbackRecoversRepoURLAndBranch is the regression test for
// the user-visible disaster: the fallback used to save a workspace stub with
// an EMPTY repoUrl, which made the bundle resolver silently fall back to the
// public default workspace and lose the enterprise bundle repo. The 1.x
// workspace directory is a git clone and still knows the answer.
func TestFilesystemFallbackRecoversRepoURLAndBranch(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, "ws", "enterprise", "ns", "demo"), 0o755))
	seedWorkspaceRepoClone(t, homeDir, "enterprise",
		"https://gitlab.example.com/citeck/enterprise-workspace.git", "develop")

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	_, err = migrateFromFilesystem(homeDir, store, "dump error")
	require.NoError(t, err)

	ws, err := store.GetWorkspace("enterprise")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Equal(t, "https://gitlab.example.com/citeck/enterprise-workspace.git", ws.RepoURL)
	assert.Equal(t, "develop", ws.RepoBranch)
}

// TestFilesystemFallbackWithoutCloneStillSaves: a missing or broken clone must
// never fail the migration — the workspace is still saved, just with empty
// repo fields (and counted as degraded).
func TestFilesystemFallbackWithoutCloneStillSaves(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, "ws", "plain", "ns", "demo"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, "ws", "plain", "repo"), 0o755))

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	_, err = migrateFromFilesystem(homeDir, store, "dump error")
	require.NoError(t, err)

	ws, err := store.GetWorkspace("plain")
	require.NoError(t, err)
	require.NotNil(t, ws)
	assert.Empty(t, ws.RepoURL)
}

// TestFilesystemFallbackSavesWorkspaceWithoutNsDir: a workspace that has a
// cloned repo but no ns/ directory used to be `continue`d BEFORE SaveWorkspace
// and vanished from the migrated store entirely.
func TestFilesystemFallbackSavesWorkspaceWithoutNsDir(t *testing.T) {
	homeDir := t.TempDir()
	seedWorkspaceRepoClone(t, homeDir, "nsless", "https://git.example.com/x.git", "main")

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	res, err := migrateFromFilesystem(homeDir, store, "dump error")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Workspaces)

	ws, err := store.GetWorkspace("nsless")
	require.NoError(t, err)
	require.NotNil(t, ws, "a workspace without ns/ must still be migrated")
	assert.Equal(t, "https://git.example.com/x.git", ws.RepoURL)
}

// TestFilesystemFallbackRecordsDegradedMigration: the fallback used to be
// invisible — MigrateResult was logged once and discarded, hasPendingSecrets
// stayed false (no secrets blob was ever written) and the UI showed nothing.
// A degraded run must leave a durable, queryable record.
func TestFilesystemFallbackRecordsDegradedMigration(t *testing.T) {
	homeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, "ws", "enterprise", "ns", "demo"), 0o755))
	seedWorkspaceRepoClone(t, homeDir, "enterprise", "https://git.example.com/e.git", "develop")

	store, err := storage.NewSQLiteStore(homeDir)
	require.NoError(t, err)
	defer store.Close()

	res, err := migrateFromFilesystem(homeDir, store, "dump error: layout map missing meta.id entry")
	require.NoError(t, err)
	assert.True(t, res.Degraded)

	rec, err := LoadDegradedMigration(store)
	require.NoError(t, err)
	require.NotNil(t, rec, "a degraded migration must be persisted")
	assert.Contains(t, rec.Reason, "dump error")
	assert.Equal(t, 1, rec.Workspaces)
	assert.Equal(t, 0, rec.Secrets)
	assert.Equal(t, 1, rec.RecoveredRepoURLs)
	assert.Contains(t, rec.LostFields, "secrets")
	assert.Contains(t, rec.LostFields, "workspace.secretId")
	assert.Contains(t, rec.LostFields, "workspace.repoPullPeriod")
}

// TestSuccessfulMigrationRecordsNoDegradation keeps the happy path clean: the
// UI banner must not fire for a normal H2 migration.
//
// This test used to be vacuous — it only asserted that a brand-new, never-
// touched store carried no DegradedMigration record, which is true of any
// fresh KV store and would have passed even with the invariant violated. It
// now runs a REAL migration over an intact synthetic MVStore (the same builder
// the partial-read tests use) and asserts the record is still absent
// afterwards. Absence is what makes the migration-status endpoint omit
// `migrationDegraded` / `degradedMigration` entirely, which is the presence-
// not-`=== false` contract the web UI tests against.
func TestSuccessfulMigrationRecordsNoDegradation(t *testing.T) {
	homeDir, store := newStoreForSynth(t, buildWorkspaceStore(t, false))

	res, err := Migrate(homeDir, store)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Workspaces, "the happy path must actually migrate something")
	assert.False(t, res.Degraded, "a clean H2 migration must never be marked degraded")
	assert.Empty(t, res.FallbackReason)
	assert.Empty(t, res.PartialReason)

	rec, err := LoadDegradedMigration(store)
	require.NoError(t, err)
	assert.Nil(t, rec, "a clean migration must leave no degraded-migration record")
}

// TestRecoverWorkspaceRepoIsBestEffort pins the contract that survived the
// dedupe onto git.OriginURL: the shared reader returns an ERROR for every
// failure mode, but recoverWorkspaceRepo must keep swallowing it and returning
// empty strings — a workspace with an unknown repo URL is still far better
// than a migration that refuses to run.
func TestRecoverWorkspaceRepoIsBestEffort(t *testing.T) {
	t.Run("real clone yields url and branch", func(t *testing.T) {
		homeDir := t.TempDir()
		seedWorkspaceRepoClone(t, homeDir, "ent", "https://git.example.com/ws.git", "develop")
		url, branch := recoverWorkspaceRepo(filepath.Join(homeDir, "ws", "ent", "repo"))
		assert.Equal(t, "https://git.example.com/ws.git", url)
		assert.Equal(t, "develop", branch)
	})

	t.Run("missing directory", func(t *testing.T) {
		url, branch := recoverWorkspaceRepo(filepath.Join(t.TempDir(), "nope"))
		assert.Empty(t, url)
		assert.Empty(t, branch)
	})

	t.Run("directory that is not a git repo", func(t *testing.T) {
		url, branch := recoverWorkspaceRepo(t.TempDir())
		assert.Empty(t, url)
		assert.Empty(t, branch)
	})

	t.Run("git repo without an origin remote still yields the branch", func(t *testing.T) {
		repoDir := t.TempDir()
		repo, err := gogit.PlainInit(repoDir, false)
		require.NoError(t, err)
		require.NoError(t, repo.Storer.SetReference(
			plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))))

		url, branch := recoverWorkspaceRepo(repoDir)
		assert.Empty(t, url, "no origin remote must not be an error")
		assert.Equal(t, "main", branch, "the branch half must be unaffected by a missing remote")
	})
}
