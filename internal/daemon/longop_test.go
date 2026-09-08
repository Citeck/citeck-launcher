package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// gatedRoute is one row of the long-operation route gate. tolerantOfLifecycle
// marks the two routes that proceed ALONGSIDE ordinary lifecycle work (an
// update pass, another synchronous handler) and are refused only by the two
// data-owning holders, snapshot and migration.
type gatedRoute struct {
	handler      string
	method, path string
	body         string

	tolerantOfLifecycle bool
}

// gatedRoutes is THE list of routes that start, reshape or destroy the
// namespace. A handler missing from it is not a passing test, it is an ungated
// route: every long-operation test below is driven from this one table.
func gatedRoutes() []gatedRoute {
	return []gatedRoute{
		{handler: "handleStartNamespace", method: "POST", path: api.NamespaceStart, tolerantOfLifecycle: true},
		{handler: "handleStopNamespace", method: "POST", path: api.NamespaceStop, tolerantOfLifecycle: true},
		{handler: "handleReloadNamespace", method: "POST", path: api.NamespaceReload},
		{handler: "handleAppStart", method: "POST", path: api.AppStart("postgres")},
		{handler: "handleAppStop", method: "POST", path: api.AppStop("postgres")},
		{handler: "handleAppRestart", method: "POST", path: api.AppRestart("postgres")},
		{handler: "handlePutAppConfig", method: "PUT", path: "/api/v1/apps/postgres/config", body: "name: postgres\n"},
		{handler: "handleResetAppConfig", method: "POST", path: "/api/v1/apps/postgres/config/reset"},
		{handler: "handlePutAppFile", method: "PUT", path: "/api/v1/apps/postgres/files/postgres/pg_hba.conf", body: "x"},
		{handler: "handleResetAppFile", method: "POST", path: "/api/v1/apps/postgres/files/reset?path=postgres/pg_hba.conf"},
		{handler: "handlePutNamespaceEdit", method: "PUT", path: api.NamespaceEditPath("ns1"), body: "{}"},
		{handler: "handleDeleteNamespace", method: "DELETE", path: "/api/v1/namespaces/ns1"},
		{handler: "handleActivateNamespace", method: "POST", path: "/api/v1/namespaces/ns2/activate"},
		{handler: "handleDeactivateNamespace", method: "POST", path: "/api/v1/namespaces/deactivate"},
		{handler: "handleActivateWorkspace", method: "POST", path: "/api/v1/workspaces/ws2/activate"},
		{handler: "handleDeleteWorkspace", method: "DELETE", path: "/api/v1/workspaces/ws2"},
		{handler: "handleDeleteVolume", method: "DELETE", path: "/api/v1/volumes/postgres2"},
		{handler: "handleUpgradeNamespace", method: "POST", path: api.NamespaceUpgrade, body: `{"bundleRef":"citeck:community-2.0.0"}`},
		{handler: "handleWorkspaceUpdate", method: "POST", path: api.WorkspaceUpdate},
	}
}

// TestMutatingRoutesRefuseDuringALongOperation pins the route gate: every route
// that starts, reshapes or destroys the namespace must refuse while a long
// operation — a snapshot export/import or a dependency migration — holds
// the long-operation lock. A migration stops the namespace, reshapes its volumes and recreates
// its containers; a Start, a config edit or a namespace delete landing in the
// middle of that races the very state it is rewriting.
//
// The table must list EVERY gated handler: a handler missing from it is not a
// passing test, it is an ungated route.
func TestMutatingRoutesRefuseDuringALongOperation(t *testing.T) {
	volumesBase := t.TempDir()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	d := &Daemon{activeNs: &activeNamespace{
		runtime:     rt,
		nsConfig:    &namespace.Config{ID: "ns1"},
		workspaceID: "ws1",
		volumesBase: volumesBase,
	}}
	// Any reload at all is a mutation: the gate is supposed to answer before the
	// handler reaches one.
	reloaded := make(chan struct{}, 16)
	d.reloadExFn = func(_, _, _ bool) error {
		reloaded <- struct{}{}
		return nil
	}
	// A failure recorded by an earlier pass: handleStartNamespace clears it as
	// its first act, so its survival is cheap proof the handler did not run.
	d.recordUpdateFailure("ns1", "an earlier pass failed")

	mux := http.NewServeMux()
	d.registerRoutes(mux)

	require.True(t, d.longOp.TryLock(longOpMigration))
	t.Cleanup(d.longOp.Unlock)

	baseSeq := d.eventSeq.Load()

	routes := gatedRoutes()
	for _, rtc := range routes {
		t.Run(rtc.handler, func(t *testing.T) {
			req := httptest.NewRequest(rtc.method, rtc.path, strings.NewReader(rtc.body))
			rec := httptest.NewRecorder()
			RecoveryMiddleware(mux).ServeHTTP(rec, req)
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
			assert.Contains(t, rec.Body.String(), "a dependency migration is in progress",
				"the refusal must name the ACTUAL holder — naming a snapshot that nobody took "+
					"sends the operator hunting for an operation that does not exist")
		})
	}

	// Nothing may have been mutated on the way to those 409s.
	select {
	case <-reloaded:
		t.Fatal("a gated route reached a reload while the long-operation lock was held")
	default:
	}
	assert.False(t, d.updateInFlight.Load(), "a refused Update & Start must not raise the in-flight flag")
	assert.False(t, d.updatePending.Load(), "a refused Update & Start must not queue a pass")
	msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
	assert.Equal(t, "an earlier pass failed", msg,
		"a refused Update & Start must not clear the previous pass's failure")
	assert.Equal(t, baseSeq, d.eventSeq.Load(), "a refused route must broadcast nothing")
	entries, err := os.ReadDir(volumesBase)
	require.NoError(t, err)
	assert.Empty(t, entries, "a refused file edit must not write to the volumes tree")
	assert.NoFileExists(t, filepath.Join(volumesBase, "postgres", "pg_hba.conf"))
	require.True(t, d.reloadMu.TryLock(), "a refused route must not have left reloadMu held")
	d.reloadMu.Unlock()
}

// TestSnapshotRoutesKeepTheirOwnCodeOnTheSharedLock: snapshot export/import now
// share the mutex with everything else, but their answer is unchanged — the Web
// UI's snapshot dialog keys on SNAPSHOT_IN_PROGRESS. The rename must not become
// a wire-format change.
func TestSnapshotRoutesKeepTheirOwnCodeOnTheSharedLock(t *testing.T) {
	d := &Daemon{activeNs: &activeNamespace{}}
	mux := http.NewServeMux()
	d.registerRoutes(mux)

	require.True(t, d.longOp.TryLock(longOpMigration))
	t.Cleanup(d.longOp.Unlock)

	for _, path := range []string{api.SnapshotsExport, api.SnapshotsImport} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("POST", path, strings.NewReader(""))
			rec := httptest.NewRecorder()
			RecoveryMiddleware(mux).ServeHTTP(rec, req)
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), api.ErrCodeSnapshotInProgress)
			assert.NotContains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
		})
	}
}

// TestTryLongOpIsExclusiveAndReleases: the helper claims the lock for the
// duration of one synchronous handler. A second claim while it is held is
// refused with the 409 already written, and the release makes it claimable
// again — otherwise the first mutating request of the daemon's life would wedge
// every later one.
func TestTryLongOpIsExclusiveAndReleases(t *testing.T) {
	d := &Daemon{}

	release, ok := d.tryLongOp(httptest.NewRecorder(), tolerateNothing)
	require.True(t, ok)

	rec := httptest.NewRecorder()
	blocked, ok2 := d.tryLongOp(rec, tolerateNothing)
	require.False(t, ok2, "the lock must not be handed out twice")
	assert.Nil(t, blocked, "a refused claim must not return a release func")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)

	release()

	release2, ok3 := d.tryLongOp(httptest.NewRecorder(), tolerateNothing)
	require.True(t, ok3, "the lock must be claimable again after release")
	release2()
}

// slowResponseWriter delays the handler inside its response write, which is the
// last thing handleStartNamespace does before its deferred release runs. It
// makes the hand-off window in TestUpdateAndStartClickIsNotRefusedByItsOwnLock
// wide enough to be deterministic instead of a scheduling coin toss.
type slowResponseWriter struct {
	*httptest.ResponseRecorder
	delay time.Duration
}

func (s slowResponseWriter) Write(b []byte) (int, error) {
	time.Sleep(s.delay)
	return s.ResponseRecorder.Write(b) //nolint:wrapcheck // test double must return the recorder's error verbatim
}

// TestUpdateAndStartClickIsNotRefusedByItsOwnLock: the handler gate and the
// pass-level gate are the SAME mutex, so a handler that kept holding it across
// the hand-off would make every Update & Start refuse itself — 200 on the wire,
// "a snapshot or dependency migration is in progress" in the DTO, and no
// reload — with no snapshot and no migration anywhere. The handler must let go
// before it spawns the pass.
func TestUpdateAndStartClickIsNotRefusedByItsOwnLock(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "ns1", namespace.NsStatusStopped)
	d.activeNs.appDefs = []appdef.ApplicationDef{{Name: "postgres"}}
	got := captureReloadEx(d)

	mux := http.NewServeMux()
	d.registerRoutes(mux)

	req := httptest.NewRequest("POST", api.NamespaceStart, http.NoBody)
	rec := slowResponseWriter{ResponseRecorder: httptest.NewRecorder(), delay: 100 * time.Millisecond}
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	select {
	case args := <-got:
		assert.True(t, args.refreshImages)
	case <-time.After(5 * time.Second):
		msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
		t.Fatalf("the click never reached its reload; recorded failure: %q", msg)
	}
	msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
	assert.Empty(t, msg, "an uncontended click must not report a refusal")
}

// TestUpdateAndStartPassRefusesWhileALongOperationHoldsTheLock is the second
// layer for Update & Start. The handler-level gate covers the click that
// arrives while the migration runs; this covers the click that was already
// queued on reloadMu when the migration took the lock. The pass must not run
// its reload against a namespace a migration is rewriting — and, because HTTP
// answered 200 long ago, it must report the refusal rather than vanish.
func TestUpdateAndStartPassRefusesWhileALongOperationHoldsTheLock(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "ns1", namespace.NsStatusStopped)
	got := captureReloadEx(d)

	require.True(t, d.longOp.TryLock(longOpMigration))
	t.Cleanup(d.longOp.Unlock)

	d.setUpdateInFlight(true, "ns1")
	d.updateAndStartAsync(false, "ns1")

	require.Eventually(t, func() bool {
		msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
		return msg != ""
	}, 5*time.Second, 5*time.Millisecond, "the refused pass must report why it did not run")

	msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
	assert.Contains(t, msg, "migration")

	select {
	case args := <-got:
		t.Fatalf("the pass reloaded while a long operation held the lock: %+v", args)
	case <-time.After(200 * time.Millisecond):
	}

	// The pass is over: the in-flight flag is lowered (a spinner that never
	// stops is the same "my click went nowhere" this reports around) and both
	// locks are free again.
	require.Eventually(t, func() bool { return !d.updateInFlight.Load() }, 5*time.Second, 5*time.Millisecond,
		"the refused pass must lower the in-flight flag")
	require.Eventually(t, func() bool {
		if d.reloadMu.TryLock() {
			d.reloadMu.Unlock()
			return true
		}
		return false
	}, 5*time.Second, 5*time.Millisecond, "the refused pass must release reloadMu")
}

// newGateTestDaemon builds the daemon the route-gate tests drive over HTTP: a
// real (stopped) runtime, a namespace, and both reload seams captured so no
// test can reach real git/Docker I/O on a success path.
func newGateTestDaemon(t *testing.T) (*Daemon, *http.ServeMux) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	d := &Daemon{activeNs: &activeNamespace{
		runtime:     rt,
		nsConfig:    &namespace.Config{ID: "ns1"},
		workspaceID: "ws1",
		volumesBase: t.TempDir(),
		appDefs:     []appdef.ApplicationDef{{Name: "postgres"}},
	}}
	d.reloadFn = func() error { return nil }
	d.reloadExFn = func(_, _, _ bool) error { return nil }
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	return d, mux
}

func doGatedRequest(t *testing.T, mux *http.ServeMux, rtc gatedRoute) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(rtc.method, rtc.path, strings.NewReader(rtc.body))
	rec := httptest.NewRecorder()
	RecoveryMiddleware(mux).ServeHTTP(rec, req)
	return rec
}

// TestGatedRoutesRefuseLifecycleWorkExceptStartAndStop is the fix for the
// regression the first cut shipped: holding the lock for the Update & Start
// pass made EVERY gated route 409 during the git-pull/generate window of an
// ORDINARY start — with a message naming a snapshot and a migration that were
// not happening — and the same applies to a synchronous reload, which holds the
// lock for the whole of doReload (minutes on an enterprise namespace). Start
// and Stop must survive both windows (see the tests below for what they must
// actually DO); everything else is genuinely unsafe beside a pass that is
// regenerating and recreating containers.
func TestGatedRoutesRefuseLifecycleWorkExceptStartAndStop(t *testing.T) {
	for _, holder := range []longOpKind{longOpUpdatePass, longOpRequest} {
		t.Run(string(holder), func(t *testing.T) {
			d, mux := newGateTestDaemon(t)
			require.True(t, d.longOp.TryLock(holder))
			t.Cleanup(d.longOp.Unlock)

			tolerated := 0
			for _, rtc := range gatedRoutes() {
				t.Run(rtc.handler, func(t *testing.T) {
					rec := doGatedRequest(t, mux, rtc)
					if rtc.tolerantOfLifecycle {
						assert.NotEqual(t, http.StatusConflict, rec.Code,
							"%s must proceed alongside %s", rtc.handler, holder)
						assert.NotContains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
						return
					}
					require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
					assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
					assert.Contains(t, rec.Body.String(), holder.busyMessage(),
						"the refusal must name the real holder, not an imaginary snapshot")
				})
				if rtc.tolerantOfLifecycle {
					tolerated++
				}
			}
			assert.Equal(t, 2, tolerated, "exactly Start and Stop tolerate lifecycle work")
		})
	}
}

// TestStartAndStopAreStillRefusedByASnapshot: the tolerance is for the update
// pass ONLY. A snapshot is restoring volumes under the namespace, so starting
// it — or racing its stop — is exactly what the lock exists to prevent.
func TestStartAndStopAreStillRefusedByASnapshot(t *testing.T) {
	for _, holder := range []longOpKind{longOpSnapshot, longOpMigration} {
		t.Run(string(holder), func(t *testing.T) {
			d, mux := newGateTestDaemon(t)
			require.True(t, d.longOp.TryLock(holder))
			t.Cleanup(d.longOp.Unlock)

			for _, rtc := range gatedRoutes() {
				if !rtc.tolerantOfLifecycle {
					continue
				}
				rec := doGatedRequest(t, mux, rtc)
				require.Equal(t, http.StatusConflict, rec.Code, "%s: %s", rtc.handler, rec.Body.String())
				assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
				assert.Contains(t, rec.Body.String(), holder.busyMessage())
			}
			assert.False(t, d.updatePending.Load(), "a refused Start must not queue a pass")
		})
	}
}

// TestSecondUpdateClickFoldsInsteadOfBeing409ed pins the documented
// click-folding contract at the HTTP layer, which is where it was silently
// narrowed: extra clicks fold into the single-slot queue and the Force flag is
// OR-ed. Driving updateAndStartAsync directly — as every other queue test does
// — cannot see a handler that refuses the click before the queue is reached.
// Both tolerated holders are exercised: an in-flight update pass, and a
// synchronous handler (a reload) holding the lock.
func TestSecondUpdateClickFoldsInsteadOfBeing409ed(t *testing.T) {
	for _, holder := range []longOpKind{longOpUpdatePass, longOpRequest} {
		t.Run(string(holder), func(t *testing.T) {
			d, mux := newGateTestDaemon(t)
			got := captureReloadEx(d)

			// Something is mid-flight: it owns reloadMu (so a new pass would
			// queue behind it) and the long-operation lock.
			d.reloadMu.Lock()
			require.True(t, d.longOp.TryLock(holder))

			rec := doGatedRequest(t, mux, gatedRoute{method: "POST", path: api.NamespaceStart})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Eventually(t, func() bool { return d.updatePending.Load() }, time.Second, 5*time.Millisecond,
				"the click must occupy the queue slot, not be refused")

			rec2 := doGatedRequest(t, mux, gatedRoute{method: "POST", path: api.NamespaceStart + "?force=true"})
			require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
			assert.True(t, d.updatePending.Load(), "the second click folds into the queued pass")
			assert.True(t, d.updatePendingForce.Load(), "the folded Force click's intent must be OR-ed in")

			// Let the mid-flight work finish; the queued pass must then run.
			d.longOp.Unlock()
			d.reloadMu.Unlock()
			select {
			case args := <-got:
				assert.True(t, args.refreshImages)
				assert.True(t, args.force, "the folded Force click must be honored")
			case <-time.After(5 * time.Second):
				msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
				t.Fatalf("the folded click never ran; recorded failure: %q", msg)
			}
		})
	}
}

// TestStopIsAcceptedDuringLifecycleWork: Stop is the escape hatch from a pass
// stuck in a slow git pull, and from a reload grinding through a 24-app
// namespace. Refusing it leaves the operator with a namespace they cannot stop
// and a 409 blaming a snapshot nobody took. Accepting it is also what every
// release before this feature did — Runtime.Stop only enqueues a command.
func TestStopIsAcceptedDuringLifecycleWork(t *testing.T) {
	for _, holder := range []longOpKind{longOpUpdatePass, longOpRequest} {
		t.Run(string(holder), func(t *testing.T) {
			d, mux := newGateTestDaemon(t)
			require.True(t, d.longOp.TryLock(holder))
			t.Cleanup(d.longOp.Unlock)

			rec := doGatedRequest(t, mux, gatedRoute{method: "POST", path: api.NamespaceStop})
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "stop requested")
		})
	}
}

// TestGatedRoutesReleaseTheLockOnTheSuccessPath: a gate whose release is never
// reached wedges every mutating route in the daemon, permanently and silently —
// the failure mode is "the launcher stopped responding to buttons", with no
// error anywhere. Replaying the whole table against a FREE lock and finding it
// free afterwards is the only cheap proof that all 19 defer their release.
func TestGatedRoutesReleaseTheLockOnTheSuccessPath(t *testing.T) {
	d, mux := newGateTestDaemon(t)

	for _, rtc := range gatedRoutes() {
		t.Run(rtc.handler, func(t *testing.T) {
			// Whatever the handler answers (404, 400, 500 — the stub daemon has
			// no store and no Docker), it must not keep the lock. A panic
			// recovered by the middleware must not keep it either.
			doGatedRequest(t, mux, rtc)
			require.Eventually(t, func() bool {
				if d.longOp.TryLock(longOpRequest) {
					d.longOp.Unlock()
					return true
				}
				return false
			}, 5*time.Second, 5*time.Millisecond,
				"%s left the long-operation lock held (holder=%q)", rtc.handler, d.longOp.Holder())
		})
	}
}

// TestLongOpToleranceAllows pins the policy sets themselves: which holder a
// Start or a Stop may run beside is the whole of this feature's behavior, and
// it is a property of these two values rather than of any one route.
func TestLongOpToleranceAllows(t *testing.T) {
	assert.False(t, tolerateNothing.allows(longOpUpdatePass))
	assert.False(t, tolerateNothing.allows(longOpRequest))
	assert.False(t, tolerateNothing.allows(longOpSnapshot))
	assert.False(t, tolerateNothing.allows(longOpMigration))

	assert.True(t, tolerateLifecycleWork.allows(longOpUpdatePass))
	assert.True(t, tolerateLifecycleWork.allows(longOpRequest))
	assert.False(t, tolerateLifecycleWork.allows(longOpSnapshot),
		"a snapshot is restoring the volumes under the namespace")
	assert.False(t, tolerateLifecycleWork.allows(longOpMigration),
		"a migration is rewriting the volumes and recreating the containers")
	// A holder that let go between the failed TryLock and the read is refused
	// like any non-member: the free-lock case belongs to the plain TryLock.
	assert.False(t, tolerateLifecycleWork.allows(longOpNone))
}
