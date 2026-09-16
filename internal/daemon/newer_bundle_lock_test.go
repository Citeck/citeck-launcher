package daemon

import (
	"go/ast"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// The "newer bundle" indicator is filled from INSIDE a d.configMu.Lock()
// write-lock section, in two places: doReloadEx (server.go) and
// handleBundleRepoPull (routes_ns.go). The fill needs the bundle repo's
// on-disk directory, and there are two ways to ask for it:
//
//   - resolveBundleRepoDir(workspaceID, repo) — a package-level function taking
//     the workspace id the caller already holds. Touches no mutex. Correct.
//   - d.resolveBundleDir(repo) — a *Daemon convenience method that re-derives
//     the workspace id via d.activeWorkspaceID() -> d.active() ->
//     d.configMu.RLock(). sync.RWMutex is NOT reentrant, so calling it from
//     inside d.configMu.Lock() blocks forever on the goroutine already holding
//     the write lock — bricking the daemon on the first reload after start.
//
// The rule, therefore: NOTHING called between d.configMu.Lock() and the
// matching Unlock() in these two functions may re-acquire configMu.
//
// It is guarded from both sides, because neither side covers both call sites:
//
//   - TestBundleRepoPullDoesNotDeadlockOnTheNewerBundleFill drives the REAL
//     handleBundleRepoPull through its route and fails on a deadline. That is
//     the honest proof the rule holds at runtime, and it is only reachable for
//     that one site.
//   - TestNewerBundleFillNeverReacquiresConfigMu asserts on the production
//     SOURCE of both sites. doReloadEx cannot be driven from a unit test (git,
//     bundle resolve, real Docker client — the same reason
//     TestPinsAreWiredIntoEveryGenerateCallSite in deps_seed_test.go checks it
//     structurally), so for that half a source assertion is all there is.
//
// The test this file replaced re-typed the fill expression in its own body
// instead of calling production code, so reintroducing the bug at BOTH sites
// left the whole daemon suite green.

// forbiddenUnderConfigMu are the *Daemon methods that re-acquire configMu.
// d.active() takes configMu.RLock directly; the other two reach it through
// d.active().
var forbiddenUnderConfigMu = []string{"resolveBundleDir", "activeWorkspaceID", "active"}

// newerBundleFillSites are the two functions that fill
// activeNamespace.newerBundle under the configMu write lock.
var newerBundleFillSites = []struct{ file, fn string }{
	{"server.go", "doReloadEx"},
	{"routes_ns.go", "handleBundleRepoPull"},
}

// TestNewerBundleFillNeverReacquiresConfigMu reads the production source of
// both fill sites, isolates the d.configMu.Lock() ... Unlock() region that
// contains the FindNewerBundle call, and fails if anything in that region
// calls a *Daemon method that takes configMu again.
func TestNewerBundleFillNeverReacquiresConfigMu(t *testing.T) {
	for _, site := range newerBundleFillSites {
		t.Run(site.fn, func(t *testing.T) {
			fn := parseFuncDecl(t, site.file, site.fn)
			recv := receiverName(t, fn)
			lock, unlock := configMuRegion(t, fn, recv)

			fillPos := callPos(fn, "FindNewerBundle")
			require.NotEqual(t, token.NoPos, fillPos,
				"%s no longer calls bundle.FindNewerBundle — this guard is out of date", site.fn)
			require.True(t, fillPos > lock && fillPos < unlock,
				"%s: the FindNewerBundle call is expected INSIDE the configMu write-lock region; "+
					"if it moved out, this guard needs rewriting rather than deleting", site.fn)

			for _, method := range forbiddenUnderConfigMu {
				pos := callPosIn(fn, recv, method, lock, unlock)
				assert.Equal(t, token.NoPos, pos,
					"%s calls %s.%s() inside its d.configMu.Lock() section. "+
						"sync.RWMutex is not reentrant, so that method's configMu.RLock deadlocks the "+
						"goroutine already holding the write lock and bricks the daemon on the first "+
						"reload. Use the package-level resolveBundleRepoDir(<workspace id already in "+
						"hand>, repo) instead, which touches no mutex.",
					site.fn, recv, method)
			}
		})
	}
}

// TestBundleRepoPullDoesNotDeadlockOnTheNewerBundleFill drives the real
// POST /api/v1/bundles/{repoId}/pull handler, whose newer-bundle fill runs
// under the configMu write lock, and fails if the response has not come back
// within newerBundleFillDeadline.
//
// A deadlock here is a HANG, not an error, so the request is run in a
// goroutine and the deadline is enforced from the test goroutine — the suite
// must report a failure rather than sit until the package timeout.
//
// The bundle repo is backed by a local fixture directory rather than a clone:
// the repo's URL points at a path that does not exist, so the handler's git
// sync fails fast (syncBundleRepo only WARNs on a git error and still returns
// the directory), while ResolveBundleRepoDir's "local workspace repo" branch
// wins and hands FindNewerBundle the fixture. The assertion on a.newerBundle
// is what proves the fill actually ran — without it, deleting the fill
// entirely would pass.
func TestBundleRepoPullDoesNotDeadlockOnTheNewerBundleFill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CITECK_HOME", home)

	// Server mode: BundlesDataDir and the newer-bundle fill's own dataDir are
	// both config.DataDir(), so one fixture serves both.
	wsRepoDir := filepath.Join(home, "data", "bundles", "workspace")
	require.NoError(t, os.MkdirAll(wsRepoDir, 0o755))
	for _, version := range []string{"2026.1.0", "2026.2.0"} {
		require.NoError(t, os.WriteFile(filepath.Join(wsRepoDir, version+".yaml"),
			[]byte("applications:\n  emodel:\n    image: citeck/emodel:1.0.0\n"), 0o644))
	}

	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	svc, err := storage.NewSecretService(store)
	require.NoError(t, err)
	require.NoError(t, svc.SetMasterPassword(storage.DefaultMasterPassword, true))

	d := &Daemon{
		store:         store,
		secretService: svc,
		version:       "2.13.0",
		activeNs: &activeNamespace{
			workspaceID: "wsMain",
			workspaceConfig: &bundle.WorkspaceConfig{BundleRepos: []bundle.BundlesRepo{{
				ID: "release",
				// A path that does not exist: the git sync fails immediately and
				// locally, and the fixture above is never touched by it (a clone
				// would land in data/bundles/release, not bundles/workspace).
				URL: filepath.Join(home, "no-such-git-repo"),
			}}},
			bundleDef: &bundle.Def{Key: bundle.Key{Version: "2026.1.0"}},
			nsConfig: &namespace.Config{
				ID:        "ns1",
				BundleRef: bundle.Ref{Repo: "release", Key: "2026.1.0"},
			},
		},
	}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/bundles/release/pull", http.NoBody))
		done <- rec
	}()

	select {
	case rec := <-done:
		require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	case <-time.After(newerBundleFillDeadline):
		t.Fatalf("POST /api/v1/bundles/{repoId}/pull did not return within %s. "+
			"Its newer-bundle fill runs under d.configMu.Lock(), so it is almost certainly "+
			"calling something that re-acquires configMu (d.resolveBundleDir / "+
			"d.activeWorkspaceID / d.active) — sync.RWMutex is not reentrant and that "+
			"deadlocks the goroutine already holding the write lock.", newerBundleFillDeadline)
	}

	require.True(t, d.configMu.TryLock(), "the handler must release configMu")
	d.configMu.Unlock()

	newer := d.active().newerBundle
	require.NotNil(t, newer, "the fill must have run: 2026.2.0 is newer than the active 2026.1.0")
	assert.Equal(t, "2026.2.0", newer.Version)
}

// newerBundleFillDeadline is generous next to the work the handler actually
// does (a failing local git open plus two directory reads) and short enough
// that a deadlock is reported as a failure instead of sitting until the
// package timeout.
const newerBundleFillDeadline = 15 * time.Second

// receiverName returns the name fn gives its receiver ("d"), which is what the
// forbidden calls are spelled against.
func receiverName(t *testing.T, fn *ast.FuncDecl) string {
	t.Helper()
	require.NotNil(t, fn.Recv, "%s has no receiver — this guard is out of date", fn.Name.Name)
	require.Len(t, fn.Recv.List, 1)
	require.Len(t, fn.Recv.List[0].Names, 1, "%s's receiver is unnamed", fn.Name.Name)
	return fn.Recv.List[0].Names[0].Name
}

// configMuRegion returns the positions of the <recv>.configMu.Lock() and
// <recv>.configMu.Unlock() calls that bracket the newer-bundle fill. Exactly
// one pair must exist: a second Lock/Unlock pair in the same function would
// make "inside the lock" ambiguous, and guessing there is how a guard starts
// approving the thing it was written to forbid.
func configMuRegion(t *testing.T, fn *ast.FuncDecl, recv string) (lock, unlock token.Pos) {
	t.Helper()
	locks := configMuCalls(fn, recv, "Lock")
	unlocks := configMuCalls(fn, recv, "Unlock")
	require.Len(t, locks, 1, "%s must hold exactly one %s.configMu.Lock() section", fn.Name.Name, recv)
	require.Len(t, unlocks, 1, "%s must hold exactly one %s.configMu.Unlock() call", fn.Name.Name, recv)
	require.Greater(t, unlocks[0], locks[0], "%s unlocks configMu before it locks it", fn.Name.Name)
	return locks[0], unlocks[0]
}

// configMuCalls returns the positions of every <recv>.configMu.<name>() call
// in fn.
func configMuCalls(fn *ast.FuncDecl, recv, name string) []token.Pos {
	var out []token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != name {
			return true
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok || inner.Sel.Name != "configMu" {
			return true
		}
		if ident, ok := inner.X.(*ast.Ident); ok && ident.Name == recv {
			out = append(out, call.Pos())
		}
		return true
	})
	return out
}

// callPos returns the position of the first call to the function or method
// `name` anywhere in fn, or token.NoPos.
func callPos(fn *ast.FuncDecl, name string) token.Pos {
	pos := token.NoPos
	ast.Inspect(fn, func(n ast.Node) bool {
		if pos != token.NoPos {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && callNamed(call, name) {
			pos = call.Pos()
		}
		return true
	})
	return pos
}

// callPosIn returns the position of the first <recv>.<method>(...) call in fn
// that lies strictly between from and to, or token.NoPos.
func callPosIn(fn *ast.FuncDecl, recv, method string, from, to token.Pos) token.Pos {
	pos := token.NoPos
	ast.Inspect(fn, func(n ast.Node) bool {
		if pos != token.NoPos {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || call.Pos() <= from || call.Pos() >= to {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != method {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == recv {
			pos = call.Pos()
		}
		return true
	})
	return pos
}
