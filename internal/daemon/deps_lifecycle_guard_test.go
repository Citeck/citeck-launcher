package daemon

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// postgresVolume is the plain name of one generation of the postgres data
// volume, asked of the registry rather than spelled out: the names are a
// function of the generation counter now, and a literal here would keep
// passing while the launcher emitted something else entirely.
func postgresVolume(gen int) string {
	d, ok := deps.Lookup(deps.Postgres)
	if !ok {
		panic("postgres is not registered")
	}
	return deps.VolumeName(d, gen)
}

// openJournal is the record an interrupted migration leaves behind when its
// ROLLBACK failed: the engine keeps it precisely because the leftovers it
// describes — depsmig-src with the namespace's own postgres2 mounted
// read-write, the half-built target volume — are still on the host.
func openJournal() *deps.MigrationJournal {
	return &deps.MigrationJournal{
		ID: deps.Postgres, From: "postgres:17", To: "postgres:18",
		Step: "restore", CreatedVolume: postgresVolume(2), WasRunning: true,
	}
}

// A pending rollback must refuse a manual Start. Nothing else does: the
// long-op lock is released the moment the failed migration's goroutine
// returns, so without this the namespace's own postgres starts a SECOND
// postmaster on the PGDATA depsmig-src still has open — the postmaster.pid
// interlock does not hold across PID/IPC namespaces.
func TestStartIsRefusedWhileARollbackIsPending(t *testing.T) {
	d, mux := newGateTestDaemon(t)
	require.NoError(t, d.active().runtime.SetMigrationJournal(openJournal()))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.NamespaceStart, http.NoBody))
	require.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyMigrationInProgress)
	assert.Contains(t, rec.Body.String(), "rollback pending")
}

// The per-app play button and `citeck start postgres` are the same door: they
// start the namespace's own postgres, on the PGDATA depsmig-src may still hold
// read-write. Stop is deliberately NOT guarded — it is the escape hatch, and
// stopping a container over that volume is what the operator wants.
func TestPerAppStartAndRestartAreRefusedWhileARollbackIsPending(t *testing.T) {
	for _, path := range []string{api.AppStart("postgres"), api.AppRestart("postgres")} {
		t.Run(path, func(t *testing.T) {
			d, mux := newGateTestDaemon(t)
			require.NoError(t, d.active().runtime.SetMigrationJournal(openJournal()))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, http.NoBody))
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), api.ErrCodeDependencyMigrationInProgress)
			assert.Contains(t, rec.Body.String(), "rollback pending")
		})
	}
	// Stop stays open: it is how the operator gets the container off the
	// volume in the first place.
	d, mux := newGateTestDaemon(t)
	require.NoError(t, d.active().runtime.SetMigrationJournal(openJournal()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.AppStop("postgres"), http.NoBody))
	assert.NotEqual(t, http.StatusConflict, rec.Code, rec.Body.String())
}

// With a clear journal the guard lets the request through to the next door.
// This runtime has never been started, so that door is the per-app lifecycle
// gate, which refuses a namespace that is not running — a different refusal
// from a later check. The assertion is therefore on the CODE, not on the
// status: what this test is about is that the JOURNAL guard stood aside.
// (It used to assert the 404 from the app lookup, which was the same proof
// before that gate existed.)
func TestPerAppStartIsAcceptedWithNoOpenJournal(t *testing.T) {
	d, mux := newGateTestDaemon(t)
	require.Nil(t, d.active().runtime.MigrationJournal())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.AppStart("postgres"), http.NoBody))
	assert.NotContains(t, rec.Body.String(), api.ErrCodeDependencyMigrationInProgress,
		"a clear journal must not produce the migration refusal")
	assert.NotContains(t, rec.Body.String(), "rollback pending")
	assert.Contains(t, rec.Body.String(), api.ErrCodeNamespaceNotRunning)
}

// The same namespace with a CLEAR journal must still start — the guard is
// about leftovers, not about the feature being present.
func TestStartIsAcceptedWithNoOpenJournal(t *testing.T) {
	d, mux := newGateTestDaemon(t)
	require.Nil(t, d.active().runtime.MigrationJournal())

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.NamespaceStart, http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// The queued Update & Start pass re-checks, because it may have been waiting on
// reloadMu since before the rollback failed — and HTTP answered 200 long ago,
// so the refusal has to be REPORTED, not returned.
func TestUpdateAndStartPassRefusesWhileARollbackIsPending(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "ns1", namespace.NsStatusStopped)
	got := captureReloadEx(d)
	require.NoError(t, d.active().runtime.SetMigrationJournal(openJournal()))

	d.setUpdateInFlight(true, "ns1")
	d.updateAndStartAsync(false, "ns1")

	require.Eventually(t, func() bool {
		msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
		return msg != ""
	}, 5*time.Second, 5*time.Millisecond, "the refused pass must report why it did not run")
	msg, _ := d.updateFailureFor(&namespace.Config{ID: "ns1"})
	assert.Contains(t, msg, "rollback pending")

	select {
	case args := <-got:
		t.Fatalf("the pass reloaded over a pending rollback: %+v", args)
	case <-time.After(200 * time.Millisecond):
	}
}

// rollbackBlocker reports only the arm the operator has to act on. A migration
// running RIGHT NOW holds the long-op lock, which refuses these paths already
// and with a message that names it — reporting it here too would send the user
// looking for leftovers that are not leftovers.
func TestRollbackBlockerIgnoresARunningMigration(t *testing.T) {
	d, _ := newGateTestDaemon(t)
	act := d.active()
	require.NoError(t, act.runtime.SetMigrationJournal(openJournal()))
	require.NotEmpty(t, d.rollbackBlocker(englishForLogs, act))

	d.setDepsMigration("ns1", &api.DependencyMigrationDto{ID: "postgres", Step: "dump"})
	t.Cleanup(func() { d.setDepsMigration("ns1", nil) })
	assert.Empty(t, d.rollbackBlocker(englishForLogs, act))
	assert.Contains(t, d.journalBlocker(englishForLogs, act), "already running")
}

// The vault unlock starts the namespace WITHOUT going through
// handleStartNamespace, so it consults the long-op lock itself: a migration
// started on the deferred (STOPPED) namespace owns its data volumes, and this
// start would land right on top of them.
func TestDeferredSecretsStartIsRefusedByAMigration(t *testing.T) {
	testDeferredSecretsStartIsRefusedBy(t, longOpMigration)
}

// A ROLLBACK refuses it for a sharper reason than a migration does: it rewrites
// nothing, but between its stop and its pin write a start would bring the OLD
// container up, and syncDependencyPinsUnderLock would re-pin that image forward
// while the generation stayed behind. This path does not go through the route,
// so it has to ask the holder itself.
func TestDeferredSecretsStartIsRefusedByADependencyRollback(t *testing.T) {
	testDeferredSecretsStartIsRefusedBy(t, longOpDepsRollback)
}

func testDeferredSecretsStartIsRefusedBy(t *testing.T, holder longOpKind) {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	started := make(chan struct{}, 1)
	d := &Daemon{activeNs: &activeNamespace{
		runtime:            rt,
		nsConfig:           &namespace.Config{ID: "ns1"},
		appDefs:            []appdef.ApplicationDef{{Name: "postgres"}},
		deferredForSecrets: true,
	}}
	d.runtimeStartFn = func(*namespace.Runtime, []appdef.ApplicationDef) { started <- struct{}{} }

	require.True(t, d.longOp.TryLock(holder))
	d.startNamespaceDeferredForSecrets("secrets unlocked")
	select {
	case <-started:
		t.Fatalf("the namespace was started over a running %s", holder)
	case <-time.After(100 * time.Millisecond):
	}
	// The deferral survives the refusal, so the namespace is not silently
	// stripped of the only flag that says it is waiting to be started.
	assert.True(t, d.active().deferredForSecrets)

	// Once the migration is over, the same call starts it.
	d.longOp.Unlock()
	d.startNamespaceDeferredForSecrets("secrets unlocked")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the deferred namespace was never started after the migration finished")
	}
	assert.False(t, d.active().deferredForSecrets)
}

// A FREE lock is the ordinary case and must not be read as a refusing holder:
// longOpNone is a member of no tolerance set, so the naive
// `!tolerateLifecycleWork.allows(holder)` test would never start anything.
func TestDeferredSecretsStartRunsWithNoLongOperation(t *testing.T) {
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	started := make(chan struct{}, 1)
	d := &Daemon{activeNs: &activeNamespace{
		runtime:            rt,
		nsConfig:           &namespace.Config{ID: "ns1"},
		appDefs:            []appdef.ApplicationDef{{Name: "postgres"}},
		deferredForSecrets: true,
	}}
	d.runtimeStartFn = func(*namespace.Runtime, []appdef.ApplicationDef) { started <- struct{}{} }
	d.startNamespaceDeferredForSecrets("secrets unlocked")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("nothing held the lock and the deferred namespace was still not started")
	}
}

// An update pass or another synchronous handler is ordinary lifecycle work,
// which this start has always been allowed to overlap — the five lifecycle
// routes tolerate exactly those two holders.
func TestDeferredSecretsStartToleratesLifecycleWork(t *testing.T) {
	for _, holder := range []longOpKind{longOpUpdatePass, longOpRequest} {
		t.Run(string(holder), func(t *testing.T) {
			rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
			t.Cleanup(rt.Shutdown)
			started := make(chan struct{}, 1)
			d := &Daemon{activeNs: &activeNamespace{
				runtime:            rt,
				nsConfig:           &namespace.Config{ID: "ns1"},
				appDefs:            []appdef.ApplicationDef{{Name: "postgres"}},
				deferredForSecrets: true,
			}}
			d.runtimeStartFn = func(*namespace.Runtime, []appdef.ApplicationDef) { started <- struct{}{} }
			require.True(t, d.longOp.TryLock(holder))
			t.Cleanup(d.longOp.Unlock)
			d.startNamespaceDeferredForSecrets("secrets unlocked")
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatalf("%s must not refuse the deferred start", holder)
			}
		})
	}
}

// The post-pull regeneration is the other unlocked path into the namespace's
// runtime files. reloadMu does not protect it: a migration recreates containers
// without holding that lock for its whole run.
func TestReloadAfterImagePullIsRefusedByAMigration(t *testing.T) {
	reloaded := make(chan struct{}, 1)
	d := &Daemon{}
	d.reloadFn = func() error {
		reloaded <- struct{}{}
		return nil
	}
	require.True(t, d.longOp.TryLock(longOpMigration))
	d.reloadAfterImagePull("postgres:18")
	select {
	case <-reloaded:
		t.Fatal("the namespace was regenerated during a dependency migration")
	case <-time.After(100 * time.Millisecond):
	}

	// With the lock free the pull's whole point — recreating the app on the new
	// digest — still happens.
	d.longOp.Unlock()
	d.reloadAfterImagePull("postgres:18")
	select {
	case <-reloaded:
	case <-time.After(2 * time.Second):
		t.Fatal("nothing held the lock and the post-pull reload never ran")
	}
}
