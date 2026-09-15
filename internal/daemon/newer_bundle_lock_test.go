package daemon

import (
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// TestNewerBundleFillDoesNotDeadlockUnderConfigMu guards against a deadlock
// that shipped in the "newer bundle" indicator: doReloadEx (server.go) and
// handleBundleRepoPull (routes_ns.go) both fill activeNamespace.newerBundle
// from INSIDE a d.configMu.Lock() write-lock section. The fill expression
// resolves the bundle repo's on-disk directory, and there are two ways to do
// that:
//
//   - resolveBundleRepoDir(workspaceID, repo) — a package-level function that
//     takes the workspace id the caller already has in hand. Safe under the
//     lock.
//   - d.resolveBundleDir(repo) — a *Daemon convenience method that re-derives
//     the workspace id via d.activeWorkspaceID() -> d.active() ->
//     d.configMu.RLock(). sync.RWMutex is NOT reentrant, so calling this
//     from inside d.configMu.Lock() blocks forever on the goroutine already
//     holding the write lock — bricking the daemon on the first reload.
//
// This test takes the write lock exactly as doReloadEx/handleBundleRepoPull
// do, then runs the (fixed) fill expression in a goroutine with a short
// deadline, failing if it has not returned in time. To confirm this test
// actually catches the bug it exists for, swap the
// resolveBundleRepoDir(a.workspaceID, repoEntry) call below for
// d.resolveBundleDir(repoEntry) and re-run: it must hang until the deadline
// and fail, not pass. (Reverting after that check is expected — do not leave
// the buggy call in.)
func TestNewerBundleFillDoesNotDeadlockUnderConfigMu(t *testing.T) {
	d := &Daemon{version: "2.13.0"}
	d.configMu.Lock()
	a := d.activeLocked()
	a.workspaceID = "ws-1"

	repoEntry := bundle.BundlesRepo{ID: "release"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Same shape as the fill sites in server.go/routes_ns.go: the
		// workspace id is the one already in hand (act.workspaceID from the
		// pre-lock snapshot), NOT re-derived through d.active().
		_ = bundle.FindNewerBundle(resolveBundleRepoDir(a.workspaceID, repoEntry), "1.0.0", d.version)
	}()

	select {
	case <-done:
		// completed without deadlocking — release the lock like the real
		// callers do.
	case <-time.After(3 * time.Second):
		t.Fatal("newer-bundle fill did not return within 3s while configMu was held — " +
			"it is almost certainly calling something that re-acquires configMu " +
			"(e.g. d.resolveBundleDir/d.activeWorkspaceID/d.active), which deadlocks " +
			"under the write lock this test (and doReloadEx/handleBundleRepoPull) hold")
	}
	d.configMu.Unlock()
}
