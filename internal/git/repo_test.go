package git

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloneOrPull_PublicRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	dir := filepath.Join(t.TempDir(), "repo")

	// Clone
	err := CloneOrPullWithAuth(context.Background(), RepoOpts{
		URL:     "https://github.com/Citeck/launcher-workspace.git",
		Branch:  "main",
		DestDir: dir,
	})
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}

	// Verify .git exists
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr != nil {
		t.Fatal(".git directory not found after clone")
	}

	// Verify workspace-v1.yml exists
	if _, statErr := os.Stat(filepath.Join(dir, "workspace-v1.yml")); statErr != nil {
		t.Fatal("workspace-v1.yml not found after clone")
	}

	// Pull (should succeed with NoErrAlreadyUpToDate)
	err = CloneOrPullWithAuth(context.Background(), RepoOpts{
		URL:     "https://github.com/Citeck/launcher-workspace.git",
		Branch:  "main",
		DestDir: dir,
	})
	if err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
}

func TestPullPeriod_Throttling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")

	// Reset sync times for clean test
	lastSyncMu.Lock()
	delete(lastSyncTimes, dir)
	lastSyncMu.Unlock()

	// First call with non-existent dir — should attempt clone (will fail since URL is fake,
	// but that's OK — we're testing throttling, not network)
	_ = CloneOrPullWithAuth(context.Background(), RepoOpts{
		URL:        "https://invalid.example.com/repo.git",
		Branch:     "main",
		DestDir:    dir,
		PullPeriod: time.Hour,
	})

	// Create a fake .git dir so next call thinks it's a repo
	os.MkdirAll(filepath.Join(dir, ".git"), 0o755)

	// Record a recent sync
	lastSyncMu.Lock()
	lastSyncTimes[dir] = time.Now()
	lastSyncMu.Unlock()

	// Second call should be throttled (skip pull)
	err := CloneOrPullWithAuth(context.Background(), RepoOpts{
		URL:        "https://invalid.example.com/repo.git",
		Branch:     "main",
		DestDir:    dir,
		PullPeriod: time.Hour,
	})
	if err != nil {
		t.Fatalf("Throttled pull should return nil, got: %v", err)
	}
}

// resetSkipState clears the host-skip map so tests don't pollute each other.
// Used by TestHostSkip_* — the global map otherwise persists across tests
// within the same package run.
func resetSkipState(t *testing.T) {
	t.Helper()
	skipMu.Lock()
	for k := range skipUntilByHost {
		delete(skipUntilByHost, k)
	}
	skipMu.Unlock()
}

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/repo.git":           "github.com",
		"https://gitlab.example.com:8080/org/r.git": "gitlab.example.com",
		"http://localhost:3000/repo.git":            "localhost",
		"git@github.com:org/repo.git":               "github.com",
		"ssh://git@gitlab.example.com:2222/r.git":   "gitlab.example.com",
		"HTTPS://Github.com/X.git":                  "github.com", // lowercased
		"":                                          "",
		"not a url":                                 "",
	}
	for in, want := range cases {
		got := HostFromURL(in)
		assert.Equal(t, want, got, "HostFromURL(%q)", in)
	}
}

func TestHostSkip_BasicLifecycle(t *testing.T) {
	resetSkipState(t)
	defer resetSkipState(t)

	const host = "gitlab.example.com"
	assert.False(t, IsSkipped(host), "host should not be skipped initially")

	// Set 1-hour skip → must be active.
	SkipPullForHost(host, time.Hour)
	assert.True(t, IsSkipped(host), "host should be skipped after SkipPullForHost")

	// Negative duration clears the skip.
	SkipPullForHost(host, -time.Second)
	assert.False(t, IsSkipped(host), "host should not be skipped after clear")
}

func TestHostSkip_Expires(t *testing.T) {
	resetSkipState(t)
	defer resetSkipState(t)

	const host = "expires.example.com"
	// Past time → IsSkipped must report false and evict lazily.
	skipMu.Lock()
	skipUntilByHost[host] = time.Now().Add(-time.Second)
	skipMu.Unlock()

	assert.False(t, IsSkipped(host), "expired entry should not skip")

	// Lazy eviction: entry must be removed.
	skipMu.Lock()
	_, present := skipUntilByHost[host]
	skipMu.Unlock()
	assert.False(t, present, "expired entry should be evicted on read")
}

// TestHostSkip_IntegrationWithCloneOrPull verifies that an active host-skip
// makes cloneOrPullInner return nil without attempting pull, but still allows
// fresh clone (the no-.git path is intentionally unconditional).
func TestHostSkip_IntegrationWithCloneOrPull(t *testing.T) {
	resetSkipState(t)
	defer resetSkipState(t)

	dir := filepath.Join(t.TempDir(), "repo")

	// Set up a fake existing repo (just a .git dir) — sufficient to make
	// cloneOrPullInner take the pull branch. We also set lastSync recent
	// enough that there'd be no pull anyway IF skip weren't set, but the
	// host-skip check sits BEFORE the throttle so we verify it fires first.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))

	const fakeURL = "https://skip-host.example.com/repo.git"
	SkipPullForHost("skip-host.example.com", time.Hour)

	// cloneOrPullInner must return nil immediately — no network call
	// attempted, no error returned. The (intentionally invalid) URL would
	// otherwise blow up doPull, so a success here is strong evidence that
	// the skip path short-circuited.
	err := cloneOrPullInner(context.Background(), RepoOpts{
		URL:     fakeURL,
		Branch:  "main",
		DestDir: dir,
	})
	assert.NoError(t, err, "host-skip should make pull a no-op")
}

// fakeSyncStore is an in-memory SyncStateStore used to verify the
// write-through + fault-in behavior of recordSync / loadPersistedSyncLocked.
type fakeSyncStore struct {
	mu       sync.Mutex
	entries  map[string]SyncStateEntry
	getCalls int
	setCalls int
}

func newFakeSyncStore() *fakeSyncStore {
	return &fakeSyncStore{entries: make(map[string]SyncStateEntry)}
}

func (f *fakeSyncStore) GetGitRepoState(path string) (*SyncStateEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	e, ok := f.entries[path]
	if !ok {
		return nil, nil
	}
	return &e, nil
}

func (f *fakeSyncStore) SetGitRepoState(e SyncStateEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	f.entries[e.Path] = e
	return nil
}

// resetGitState wipes the in-memory caches between tests so a previous test's
// recordSync doesn't poison the next test's "fresh process" simulation.
func resetGitState(t *testing.T) {
	t.Helper()
	lastSyncMu.Lock()
	lastSyncTimes = make(map[string]time.Time)
	lastSyncMu.Unlock()
	SetSyncStateStore(nil, "")
}

func TestSyncStateStore_PersistsOnRecordSync(t *testing.T) {
	resetGitState(t)
	defer resetGitState(t)

	home := t.TempDir()
	destDir := filepath.Join(home, "ws", "team-a", "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(destDir, ".git"), 0o755))

	store := newFakeSyncStore()
	SetSyncStateStore(store, home)

	opts := RepoOpts{URL: "https://example.com/r.git", Branch: "main", DestDir: destDir}
	recordSync(opts)

	require.Len(t, store.entries, 1)
	got, ok := store.entries["ws/team-a/repo"]
	require.True(t, ok, "entry must use forward-slash relative path")
	assert.NotZero(t, got.LastSyncMs)
	// No real git repo here, hash falls back to "" — confirms readHeadHash is best-effort.
	assert.Empty(t, got.LastCommitHash)
}

func TestSyncStateStore_FaultsInOnFreshProcess(t *testing.T) {
	resetGitState(t)
	defer resetGitState(t)

	home := t.TempDir()
	destDir := filepath.Join(home, "ws", "team-b", "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(destDir, ".git"), 0o755))

	store := newFakeSyncStore()
	prevSync := time.Now().Add(-10 * time.Minute)
	store.entries["ws/team-b/repo"] = SyncStateEntry{
		Path:       "ws/team-b/repo",
		LastSyncMs: prevSync.UnixMilli(),
	}
	SetSyncStateStore(store, home)

	// PullPeriod = 1h; the persisted sync was 10m ago → cloneOrPullInner must
	// short-circuit without hitting the network.
	err := cloneOrPullInner(context.Background(), RepoOpts{
		URL:        "https://invalid.example.com/r.git",
		Branch:     "main",
		DestDir:    destDir,
		PullPeriod: time.Hour,
	})
	require.NoError(t, err, "throttle must read persisted sync time")
	assert.Equal(t, 1, store.getCalls, "fault-in must read the store exactly once")
}

func TestSyncStateStore_NoStoreFallsBackToInMemory(t *testing.T) {
	resetGitState(t)
	defer resetGitState(t)

	dir := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))

	// No store wired — recordSync should not panic.
	recordSync(RepoOpts{URL: "https://example.com/r.git", Branch: "main", DestDir: dir})

	lastSyncMu.Lock()
	_, ok := lastSyncTimes[dir]
	lastSyncMu.Unlock()
	assert.True(t, ok, "in-memory cache still populated without a store")
}

func TestRelativeRepoPath_FallsBackWhenOutsideRoot(t *testing.T) {
	resetGitState(t)
	defer resetGitState(t)

	SetSyncStateStore(newFakeSyncStore(), "/non/existent/root")
	// Path with no common ancestor → filepath.Rel returns a ".." path; we keep
	// it as-is because Kotlin parity only cares about repos under the home.
	got := relativeRepoPath("/somewhere/else/repo")
	assert.NotEmpty(t, got)
}

func TestCloneOrPull_BackwardsCompat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network test in short mode")
	}

	dir := filepath.Join(t.TempDir(), "repo")

	// Use the old-style API
	err := CloneOrPull("https://github.com/Citeck/launcher-workspace.git", "main", dir)
	if err != nil {
		t.Fatalf("CloneOrPull failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(".git directory not found")
	}
}
