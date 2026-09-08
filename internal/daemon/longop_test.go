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

// TestMutatingRoutesRefuseDuringALongOperation pins the route gate: every route
// that starts, reshapes or destroys the namespace must refuse while a long
// operation — a snapshot export/import or a dependency migration — holds
// longOpMu. A migration stops the namespace, reshapes its volumes and recreates
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

	d.longOpMu.Lock()
	t.Cleanup(d.longOpMu.Unlock)

	baseSeq := d.eventSeq.Load()

	routes := []struct {
		handler      string
		method, path string
		body         string
	}{
		{"handleStartNamespace", "POST", api.NamespaceStart, ""},
		{"handleStopNamespace", "POST", api.NamespaceStop, ""},
		{"handleReloadNamespace", "POST", api.NamespaceReload, ""},
		{"handleAppStart", "POST", api.AppStart("postgres"), ""},
		{"handleAppStop", "POST", api.AppStop("postgres"), ""},
		{"handleAppRestart", "POST", api.AppRestart("postgres"), ""},
		{"handlePutAppConfig", "PUT", "/api/v1/apps/postgres/config", "name: postgres\n"},
		{"handleResetAppConfig", "POST", "/api/v1/apps/postgres/config/reset", ""},
		{"handlePutAppFile", "PUT", "/api/v1/apps/postgres/files/postgres/pg_hba.conf", "x"},
		{"handleResetAppFile", "POST", "/api/v1/apps/postgres/files/reset?path=postgres/pg_hba.conf", ""},
		{"handlePutNamespaceEdit", "PUT", api.NamespaceEditPath("ns1"), "{}"},
		{"handleDeleteNamespace", "DELETE", "/api/v1/namespaces/ns1", ""},
		{"handleActivateNamespace", "POST", "/api/v1/namespaces/ns2/activate", ""},
		{"handleDeactivateNamespace", "POST", "/api/v1/namespaces/deactivate", ""},
		{"handleActivateWorkspace", "POST", "/api/v1/workspaces/ws2/activate", ""},
		{"handleDeleteWorkspace", "DELETE", "/api/v1/workspaces/ws2", ""},
		{"handleDeleteVolume", "DELETE", "/api/v1/volumes/postgres2", ""},
		{"handleUpgradeNamespace", "POST", api.NamespaceUpgrade, `{"bundleRef":"citeck:community-2.0.0"}`},
		{"handleWorkspaceUpdate", "POST", api.WorkspaceUpdate, ""},
	}
	for _, rtc := range routes {
		t.Run(rtc.handler, func(t *testing.T) {
			req := httptest.NewRequest(rtc.method, rtc.path, strings.NewReader(rtc.body))
			rec := httptest.NewRecorder()
			RecoveryMiddleware(mux).ServeHTTP(rec, req)
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)
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

	d.longOpMu.Lock()
	t.Cleanup(d.longOpMu.Unlock)

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

	release, ok := d.tryLongOp(httptest.NewRecorder())
	require.True(t, ok)

	rec := httptest.NewRecorder()
	blocked, ok2 := d.tryLongOp(rec)
	require.False(t, ok2, "the lock must not be handed out twice")
	assert.Nil(t, blocked, "a refused claim must not return a release func")
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeLongOpInProgress)

	release()

	release2, ok3 := d.tryLongOp(httptest.NewRecorder())
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

	d.longOpMu.Lock()
	t.Cleanup(d.longOpMu.Unlock)

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
