package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
