package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/storage"
)

func TestBuildWorkspaceRepoOpts_CustomRepoMapsAllFields(t *testing.T) {
	ws := storage.WorkspaceDto{
		ID:             "ws-priv",
		Name:           "Private",
		RepoURL:        "https://gitlab.example.com/citeck/ws.git",
		RepoBranch:     "release/2.1",
		RepoPullPeriod: "PT30M",
		AuthType:       "TOKEN",
	}
	sec := &fakeSecretReader{secrets: map[string]storage.Secret{
		workspaceRepoSecretKey(ws.ID): {Value: "glpat-xxx"},
	}}

	opts := buildWorkspaceRepoOpts(ws, sec)
	assert.Equal(t, "https://gitlab.example.com/citeck/ws.git", opts.URL)
	assert.Equal(t, "release/2.1", opts.Branch)
	assert.Equal(t, "glpat-xxx", opts.Token)
	// PT30M → 30 minutes; compare via Duration to avoid string drift.
	assert.Equal(t, int64(30*60), int64(opts.PullPeriod.Seconds()))
}

func TestBuildWorkspaceRepoOpts_NoAuthLeavesTokenEmpty(t *testing.T) {
	ws := storage.WorkspaceDto{
		ID:      "ws-pub",
		RepoURL: "https://github.com/Citeck/launcher-workspace.git",
	}
	sec := &fakeSecretReader{secrets: map[string]storage.Secret{
		workspaceRepoSecretKey(ws.ID): {Value: "should-not-be-used"},
	}}

	opts := buildWorkspaceRepoOpts(ws, sec)
	assert.Empty(t, opts.Token, "Token should only be resolved when AuthType==TOKEN")
}

// TestBuildWorkspaceRepoOpts_SecretIDWinsOverLegacy: an explicit shared-secret
// reference beats the legacy per-workspace "ws:{id}:repo" key even when both
// exist.
func TestBuildWorkspaceRepoOpts_SecretIDWinsOverLegacy(t *testing.T) {
	ws := storage.WorkspaceDto{
		ID:       "ws-1",
		RepoURL:  "https://gitlab.example.com/citeck/ws.git",
		AuthType: "TOKEN",
		SecretID: "shared-gitlab-token",
	}
	sec := &fakeSecretReader{secrets: map[string]storage.Secret{
		"shared-gitlab-token":         {Value: "glpat-shared"},
		workspaceRepoSecretKey(ws.ID): {Value: "glpat-legacy"},
	}}

	opts := buildWorkspaceRepoOpts(ws, sec)
	assert.Equal(t, "glpat-shared", opts.Token, "SecretID reference must win over the legacy ws:{id}:repo key")
}

// TestBuildWorkspaceRepoOpts_SecretIDResolvedRegardlessOfAuthType: an explicit
// secret link is authoritative — it resolves even when AuthType is stale/NONE,
// so a dialog inconsistency can't reproduce the silent-401 bug class.
func TestBuildWorkspaceRepoOpts_SecretIDResolvedRegardlessOfAuthType(t *testing.T) {
	ws := storage.WorkspaceDto{
		ID:       "ws-2",
		RepoURL:  "https://gitlab.example.com/citeck/ws.git",
		AuthType: "NONE",
		SecretID: "shared-gitlab-token",
	}
	sec := &fakeSecretReader{secrets: map[string]storage.Secret{
		"shared-gitlab-token": {Value: "glpat-shared"},
	}}

	opts := buildWorkspaceRepoOpts(ws, sec)
	assert.Equal(t, "glpat-shared", opts.Token)
}

// TestBuildWorkspaceRepoOpts_LegacySecretFallback: no SecretID + TOKEN auth →
// the legacy per-workspace key still resolves (back-compat for pre-secretId
// workspaces and Kotlin-migrated data).
func TestBuildWorkspaceRepoOpts_LegacySecretFallback(t *testing.T) {
	ws := storage.WorkspaceDto{
		ID:       "ws-3",
		RepoURL:  "https://gitlab.example.com/citeck/ws.git",
		AuthType: "TOKEN",
	}
	sec := &fakeSecretReader{secrets: map[string]storage.Secret{
		workspaceRepoSecretKey(ws.ID): {Value: "glpat-legacy"},
	}}

	opts := buildWorkspaceRepoOpts(ws, sec)
	assert.Equal(t, "glpat-legacy", opts.Token)
}

// TestBuildWorkspaceRepoOpts_MissingSecretLeavesTokenEmpty: a dangling
// SecretID (secret deleted) degrades to an unauthenticated clone — the
// resolver then records the sync failure instead of crashing.
func TestBuildWorkspaceRepoOpts_MissingSecretLeavesTokenEmpty(t *testing.T) {
	ws := storage.WorkspaceDto{
		ID:       "ws-4",
		RepoURL:  "https://gitlab.example.com/citeck/ws.git",
		AuthType: "TOKEN",
		SecretID: "deleted-secret",
	}
	opts := buildWorkspaceRepoOpts(ws, &fakeSecretReader{secrets: map[string]storage.Secret{}})
	assert.Empty(t, opts.Token)
}

// TestBuildWorkspaceRepoOpts_BasicSecretUsesValue: a BASIC-style secret
// (Username set) still works for git token auth — only Value (the
// password/token half) is sent; git's BasicAuth username is fixed
// ("x-token-auth"), so the stored Username is ignored by design.
func TestBuildWorkspaceRepoOpts_BasicSecretUsesValue(t *testing.T) {
	ws := storage.WorkspaceDto{
		ID:       "ws-5",
		RepoURL:  "https://gitlab.example.com/citeck/ws.git",
		SecretID: "basic-cred",
	}
	sec := &fakeSecretReader{secrets: map[string]storage.Secret{
		"basic-cred": {SecretMeta: storage.SecretMeta{Username: "deploy-user"}, Value: "s3cr3t:with:colons"},
	}}

	opts := buildWorkspaceRepoOpts(ws, sec)
	assert.Equal(t, "s3cr3t:with:colons", opts.Token)
}

func TestLookupWorkspaceRepoOpts_MissingWorkspaceReturnsZero(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	opts := lookupWorkspaceRepoOpts(store, nil, "nonexistent")
	assert.Equal(t, bundle.WorkspaceRepoOpts{}, opts)
}

func TestLookupWorkspaceRepoOpts_NoWorkspaceIDReturnsZero(t *testing.T) {
	opts := lookupWorkspaceRepoOpts(nil, nil, "")
	assert.Equal(t, bundle.WorkspaceRepoOpts{}, opts)
}

func TestLookupWorkspaceRepoOpts_RoundTripsStoredWorkspace(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	require.NoError(t, store.SaveWorkspace(storage.WorkspaceDto{
		ID:             "team-a",
		Name:           "Team A",
		RepoURL:        "https://gitlab.example.com/team-a/ws.git",
		RepoBranch:     "main",
		RepoPullPeriod: "PT2H",
	}))

	opts := lookupWorkspaceRepoOpts(store, nil, "team-a")
	assert.Equal(t, "https://gitlab.example.com/team-a/ws.git", opts.URL)
	assert.Equal(t, "main", opts.Branch)
	assert.Equal(t, int64(2*3600), int64(opts.PullPeriod.Seconds()))
}

// TestMigrateWorkspaceSecretLinks_BackfillsLegacyLink: a pre-secret-reference
// workspace (AuthType=TOKEN, no SecretID) whose legacy "ws:<id>:repo" secret
// exists gets the link persisted; runs idempotently; workspaces without a
// legacy secret or with an explicit link are left untouched.
func TestMigrateWorkspaceSecretLinks_BackfillsLegacyLink(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()

	legacy := storage.WorkspaceDto{ID: "ws-old", Name: "Old", RepoURL: "https://gl.example/ws.git", AuthType: "TOKEN"}
	linked := storage.WorkspaceDto{ID: "ws-new", Name: "New", RepoURL: "https://gl.example/ws2.git", AuthType: "TOKEN", SecretID: "shared-token"}
	noSecret := storage.WorkspaceDto{ID: "ws-none", Name: "None", RepoURL: "https://gl.example/ws3.git", AuthType: "TOKEN"}
	public := storage.WorkspaceDto{ID: "ws-pub", Name: "Pub", RepoURL: "https://gh.example/ws4.git", AuthType: "NONE"}
	for _, ws := range []storage.WorkspaceDto{legacy, linked, noSecret, public} {
		require.NoError(t, store.SaveWorkspace(ws))
	}
	sec := &fakeSecretReader{secrets: map[string]storage.Secret{
		workspaceRepoSecretKey("ws-old"): {SecretMeta: storage.SecretMeta{ID: workspaceRepoSecretKey("ws-old"), Type: storage.SecretGitToken}, Value: "glpat-old"},
		"shared-token":                   {SecretMeta: storage.SecretMeta{ID: "shared-token", Type: storage.SecretGitToken}, Value: "glpat-shared"},
	}}

	migrateWorkspaceSecretLinks(store, sec)

	got, err := store.GetWorkspace("ws-old")
	require.NoError(t, err)
	assert.Equal(t, workspaceRepoSecretKey("ws-old"), got.SecretID, "legacy workspace must be linked to its ws:<id>:repo secret")

	got, err = store.GetWorkspace("ws-new")
	require.NoError(t, err)
	assert.Equal(t, "shared-token", got.SecretID, "explicit link must be untouched")

	got, err = store.GetWorkspace("ws-none")
	require.NoError(t, err)
	assert.Empty(t, got.SecretID, "no legacy secret -> nothing to link")

	got, err = store.GetWorkspace("ws-pub")
	require.NoError(t, err)
	assert.Empty(t, got.SecretID, "AuthType NONE is skipped")

	// Idempotent: a second run changes nothing.
	migrateWorkspaceSecretLinks(store, sec)
	got, err = store.GetWorkspace("ws-old")
	require.NoError(t, err)
	assert.Equal(t, workspaceRepoSecretKey("ws-old"), got.SecretID)
}

// TestMigrateWorkspaceSecretLinks_LockedStoreDefers: GetSecret failing (locked
// SecretService) must leave everything untouched — the back-fill re-runs on
// unlock via rebuildAuthCaches.
func TestMigrateWorkspaceSecretLinks_LockedStoreDefers(t *testing.T) {
	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.SaveWorkspace(storage.WorkspaceDto{ID: "ws-old", Name: "Old", RepoURL: "https://gl.example/ws.git", AuthType: "TOKEN"}))

	migrateWorkspaceSecretLinks(store, &fakeSecretReader{secrets: map[string]storage.Secret{}})

	got, err := store.GetWorkspace("ws-old")
	require.NoError(t, err)
	assert.Empty(t, got.SecretID)
}
