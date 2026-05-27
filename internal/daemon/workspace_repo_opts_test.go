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
