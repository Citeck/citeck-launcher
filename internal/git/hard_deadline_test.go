package git

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCloneOrPullHardDeadline pins the wall-clock backstop: even if the
// underlying clone/pull ignores context cancellation and blocks, the call must
// return promptly with a deadline error so the caller (and any lock it holds,
// e.g. the daemon's reloadMu) is never wedged.
func TestCloneOrPullHardDeadline(t *testing.T) {
	// Shorten the grace and stub the runner to block far longer than the
	// deadline while ignoring ctx (mimics a hung go-git pull).
	origGrace := pullHardDeadlineGrace
	origRunner := cloneOrPullRunner
	pullHardDeadlineGrace = 40 * time.Millisecond
	cloneOrPullRunner = func(_ context.Context, _ RepoOpts) error {
		time.Sleep(5 * time.Second)
		return nil
	}
	t.Cleanup(func() {
		pullHardDeadlineGrace = origGrace
		cloneOrPullRunner = origRunner
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := CloneOrPullWithAuth(ctx, RepoOpts{URL: "https://example.invalid/x.git", Branch: "main", DestDir: t.TempDir()})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a hard-deadline error from a hung pull")
	}
	if !strings.Contains(err.Error(), "hard deadline") {
		t.Fatalf("expected hard-deadline error, got: %v", err)
	}
	// Must return well before the stubbed 5s block (deadline ≈ 10ms+40ms).
	if elapsed > 2*time.Second {
		t.Fatalf("CloneOrPullWithAuth blocked %v — backstop did not fire", elapsed)
	}
}

// TestCloneOrPullHardDeadlinePassesThrough confirms the happy path is
// unaffected: a fast runner returns its own result, not a deadline error.
func TestCloneOrPullHardDeadlinePassesThrough(t *testing.T) {
	origRunner := cloneOrPullRunner
	cloneOrPullRunner = func(_ context.Context, _ RepoOpts) error { return nil }
	t.Cleanup(func() { cloneOrPullRunner = origRunner })

	if err := CloneOrPullWithAuth(context.Background(), RepoOpts{DestDir: t.TempDir()}); err != nil {
		t.Fatalf("fast runner should pass through nil, got: %v", err)
	}
}
