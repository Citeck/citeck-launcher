package daemon

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
)

// TestRegenOnAttachToggle_ReadsRuntimeGatingApps pins that regenOnAttachToggle
// is a pure lookup into the runtime's last-generation GatingApps set (see
// GenResp.GatingApps / NsGenContext.MarkGatingApp) — NOT a hardcoded app-name
// list in the daemon. That is the point of this refactor: a bundle/generator
// can add a new gating app (e.g. a future "rag" app gating "qdrant") and the
// daemon regenerates on its toggle automatically, with no daemon code change.
//
// Superseded assertion: this used to pin a package-level hardcoded set
// {onlyoffice, ai, stt-sidecar} via a name-only regenOnAttachToggle(name)
// free function (Kotlin parity: NamespaceGenerator's static
// dependsOnDetachedApps set consulted by NamespaceRuntime.detachedAppsChanged,
// v1.4.1 changelog). That hardcode is exactly what internal/namespace/generator.go's
// GatingApps field replaces, so pinning it here would just reintroduce the
// hardcode in the test. The real Kotlin-parity set is pinned on
// resp.GatingApps directly — from the generator side, where it is actually
// produced — by TestGatingApps_ReportedByGenerator in
// internal/namespace/generator_test.go, which asserts all three of
// GatingApps["ai"], GatingApps[appdef.AppOnlyoffice] and
// GatingApps[appdef.AppAlfresco]. (The pre-existing
// TestProxyTarget_AIRegistered/AlfrescoEnabled/AlfrescoDetached tests in that
// same file do NOT cover this — they assert proxy env vars and DependsOn,
// never resp.GatingApps, so they would not catch a regression that drops a
// MarkGatingApp call while leaving the env-var wiring intact.)
func TestRegenOnAttachToggle_ReadsRuntimeGatingApps(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, nil, t.TempDir())
	rt.SetGatingApps(map[string]bool{"rag": true, appdef.AppAi: true})

	assert.True(t, regenOnAttachToggle(rt, "rag"),
		"an app the generator marked gating must trigger regen, even one the daemon has never heard of")
	assert.True(t, regenOnAttachToggle(rt, appdef.AppAi))
	assert.False(t, regenOnAttachToggle(rt, appdef.AppGateway), "a non-gating app must NOT trigger regen")
	assert.False(t, regenOnAttachToggle(rt, ""), "empty name must NOT trigger regen")

	// The answer reflects the runtime's LATEST generation, not a fixed set: a
	// bundle change that drops "rag" from GatingApps (and adds onlyoffice)
	// changes the answer on the next SetGatingApps call, with no daemon change.
	rt.SetGatingApps(map[string]bool{appdef.AppOnlyoffice: true})
	assert.False(t, regenOnAttachToggle(rt, "rag"), "gating set must come from the latest generation, not accumulate")
	assert.True(t, regenOnAttachToggle(rt, appdef.AppOnlyoffice))
}

// TestRegenOnAttachToggle_NilGatingApps covers a runtime that never had
// SetGatingApps called (e.g. before the first successful generation) — must
// report false rather than panic on a nil map.
func TestRegenOnAttachToggle_NilGatingApps(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "test"}, nil, t.TempDir())
	assert.False(t, regenOnAttachToggle(rt, appdef.AppAi))
}

// syncBuffer is a race-free log sink: the regeneration WARN is written from the
// pass goroutine while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // test sink returns bytes.Buffer's (always nil) error verbatim
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func captureSyncSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func newAttachToggleTestDaemon(t *testing.T) (*Daemon, *http.ServeMux, chan struct{}) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	d := &Daemon{activeNs: &activeNamespace{
		runtime:  rt,
		nsConfig: &namespace.Config{ID: "ns1"},
	}}
	reloads := make(chan struct{}, 4)
	d.reloadFn = func() error {
		// Assert from INSIDE the reload: the pass must own the lock while it
		// regenerates, not merely have checked it a moment earlier.
		assert.Equal(t, longOpUpdatePass, d.longOp.Holder(),
			"the regeneration must HOLD the long-operation lock while it runs")
		reloads <- struct{}{}
		return nil
	}
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux, reloads
}

// TestAttachToggleRegenHoldsTheLongOperationLock: the regeneration re-runs
// Generate — it seeds dependency pins and rewrites the runtime files a
// migration is itself rewriting — so it is not a benign reload that may race
// one. It must own the long-operation lock for its whole doReload, and the
// spawning handler must have let go of that lock first or the toggle would skip
// its own regeneration.
func TestAttachToggleRegenHoldsTheLongOperationLock(t *testing.T) {
	d, _, reloads := newAttachToggleTestDaemon(t)

	d.regenAfterAttachToggleAsync(appdef.AppAi, "detach")

	select {
	case <-reloads:
	case <-time.After(5 * time.Second):
		t.Fatal("the attach-toggle regeneration never ran")
	}
	require.Eventually(t, func() bool {
		if d.longOp.TryLock(longOpRequest) {
			d.longOp.Unlock()
			return true
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "the regeneration must release the lock when done")
}

// TestAttachToggleRegenSkipsWhenALongOperationHoldsTheLock covers the window
// the handler cannot close: it releases the lock before the hand-off, so a
// migration can take it in between. Skipping is safe — the detach is already
// persisted in ManualStoppedApps and the next reload regenerates from it — but
// it must be LOUD, because until that reload the proxy keeps an upstream the
// operator just removed.
func TestAttachToggleRegenSkipsWhenALongOperationHoldsTheLock(t *testing.T) {
	logs := captureSyncSlog(t)
	d, _, reloads := newAttachToggleTestDaemon(t)

	require.True(t, d.longOp.TryLock(longOpMigration))
	t.Cleanup(d.longOp.Unlock)

	d.regenAfterAttachToggleAsync(appdef.AppAi, "detach")

	require.Eventually(t, func() bool {
		return strings.Contains(logs.String(), "Attach-toggle regeneration skipped")
	}, 5*time.Second, 5*time.Millisecond, "the skip must be reported: %s", logs.String())
	out := logs.String()
	assert.Contains(t, out, "level=WARN", "a silently dropped regeneration is how a stale proxy goes unnoticed")
	assert.Contains(t, out, "a dependency migration is in progress", "the skip must name the holder")
	assert.Contains(t, out, "regenerated on the next reload or start",
		"the WARN must say when the wiring comes back — otherwise a skip reads as permanent breakage, "+
			"and under the memory-relief recipe (a burst of toggles) it is the ordinary case")

	select {
	case <-reloads:
		t.Fatal("the regeneration ran while a migration held the lock")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestAttachToggleHandlerIsRefusedDuringAMigration: the handler itself is
// gated, so the common case never reaches the window above.
func TestAttachToggleHandlerIsRefusedDuringAMigration(t *testing.T) {
	d, mux, reloads := newAttachToggleTestDaemon(t)
	require.True(t, d.longOp.TryLock(longOpMigration))
	t.Cleanup(d.longOp.Unlock)

	req := httptest.NewRequest("POST", api.AppStop(appdef.AppAi), http.NoBody)
	rec := httptest.NewRecorder()
	RecoveryMiddleware(mux).ServeHTTP(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)

	select {
	case <-reloads:
		t.Fatal("a refused toggle must not regenerate")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestAttachToggleHandlerDoesNotSkipItsOwnRegeneration: the handler and the
// regeneration pass share one lock, so a handler that kept holding it across
// the hand-off would make every attach/detach of a cross-wiring app skip its
// own regeneration — 200 on the wire, a WARN in the log, and a proxy left
// pointing at an upstream the operator just removed.
//
// The response write is the last thing the handler does before its deferred
// release, so parking it there (gatedResponseWriter, longop_test.go) holds the
// handler inside the window under test for as long as the test needs, instead
// of betting on a sleep being longer than the scheduler's whim.
func TestAttachToggleHandlerDoesNotSkipItsOwnRegeneration(t *testing.T) {
	d, mux, reloads := newAttachToggleTestDaemon(t)
	d.activeNs.runtime.InjectAppsForTest(&namespace.AppRuntime{
		Name:   appdef.AppAi,
		Status: namespace.AppStatusStopped,
		Def:    appdef.ApplicationDef{Name: appdef.AppAi},
	})

	release := make(chan struct{})
	rec := gatedResponseWriter{ResponseRecorder: httptest.NewRecorder(), release: release}
	served := make(chan struct{})
	go func() {
		defer close(served)
		mux.ServeHTTP(rec, httptest.NewRequest("POST", api.AppStart(appdef.AppAi), http.NoBody))
	}()

	// The regeneration must happen while the handler is still parked: that is
	// exactly the stretch in which a lock it failed to drop would still be
	// held, so reaching the reload here is proof it dropped it.
	select {
	case <-reloads:
	case <-time.After(5 * time.Second):
		t.Error("the toggle skipped its own regeneration — the handler was still holding the lock")
	}

	close(release)
	<-served
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
