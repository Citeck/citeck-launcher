package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/citeck/citeck-launcher/internal/namespace"
	"github.com/citeck/citeck-launcher/internal/storage"
)

// recoveryFixture is a loaded-but-not-yet-started namespace whose migrate.Env
// is the shared in-memory fake, so recovery runs through its production entry
// point without Docker.
type recoveryFixture struct {
	d   *Daemon
	rt  *namespace.Runtime
	env *migratetest.FakeEnv
	act activeNamespace
}

func newRecoveryFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: "ns1"}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	base := t.TempDir()

	env := migratetest.New()
	env.DumpRoot = depsMigrationDir(base)
	env.Volumes["postgres2"] = map[string]string{"PG_VERSION": "17"}
	env.Volumes["postgres3"] = map[string]string{}
	env.VolSize["postgres2"] = 1 << 20
	env.Dirs[env.DumpRoot] = true
	env.Dirs[filepath.Join(env.DumpRoot, "postgres")] = true
	env.Containers[migrate.SrcContainer] = appdef.ApplicationDef{Name: migrate.SrcContainer, Image: "postgres:17.5"}
	env.Containers[migrate.DstContainer] = appdef.ApplicationDef{Name: migrate.DstContainer, Image: "postgres:18"}

	d := &Daemon{}
	d.depsEnvFn = func(activeNamespace) migrate.Env { return env }
	return &recoveryFixture{
		d: d, rt: rt, env: env,
		act: activeNamespace{runtime: rt, nsConfig: &namespace.Config{ID: "ns1"}, volumesBase: base},
	}
}

// openJournal installs an interrupted migration exactly as the persisted state
// would carry it into a fresh daemon.
func (f *recoveryFixture) openJournal(j *deps.MigrationJournal) {
	f.rt.RestoreDependencyState(map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}, j, nil)
}

func (f *recoveryFixture) dumpDir() string { return filepath.Join(f.env.DumpRoot, "postgres") }

func TestRecoveryRollsBackAJournalAndRequestsRestart(t *testing.T) {
	f := newRecoveryFixture(t)
	f.openJournal(&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		Step: "restore", CreatedVolume: "postgres3", DumpDir: f.dumpDir(), WasRunning: true})

	restart := f.d.recoverInterruptedMigration(context.Background(), f.act)

	assert.True(t, restart, "an interrupted migration stopped the namespace on the user's behalf")
	assert.Nil(t, f.rt.MigrationJournal(), "a clean rollback closes the migration")
	assert.Equal(t, "postgres:17.5", f.rt.DependencyPins()[deps.Postgres], "the pin never moved")
	assert.NotContains(t, f.env.Volumes, "postgres3", "the half-built target volume is removed")
	assert.Contains(t, f.env.Volumes, "postgres2", "the old data volume is never touched")
	assert.NotContains(t, f.env.Containers, migrate.SrcContainer)
	assert.NotContains(t, f.env.Containers, migrate.DstContainer)
	assert.NotContains(t, f.env.Dirs, f.dumpDir(), "the scratch dump is gone")
	require.NotNil(t, f.rt.LastDependencyMigration())
	assert.Contains(t, f.rt.LastDependencyMigration().Error, "interrupted")
	assert.Empty(t, f.env.Reloads(), "the caller restarts the namespace; recovery must not reload one that is not installed yet")
}

// A journal whose namespace was NOT running restores nothing to run.
func TestRecoveryDoesNotStartANamespaceThatWasStopped(t *testing.T) {
	f := newRecoveryFixture(t)
	f.openJournal(&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		CreatedVolume: "postgres3", DumpDir: f.dumpDir()})
	assert.False(t, f.d.recoverInterruptedMigration(context.Background(), f.act))
	assert.Nil(t, f.rt.MigrationJournal())
}

// rolledBack means ATTEMPTED, not restored: a rollback that FAILED keeps the
// journal open (its leftovers are still out there) and must not hand the
// namespace back to the user running — a surviving temp container would then
// hold the old data volume beside the namespace's own postgres.
func TestRecoveryKeepsTheJournalAndStaysStoppedWhenTheRollbackFails(t *testing.T) {
	f := newRecoveryFixture(t)
	f.env.FailOn["rmvol:postgres3"] = errors.New("volume is in use")
	f.openJournal(&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		CreatedVolume: "postgres3", DumpDir: f.dumpDir(), WasRunning: true})

	assert.False(t, f.d.recoverInterruptedMigration(context.Background(), f.act),
		"a failed rollback must not auto-start the namespace")
	require.NotNil(t, f.rt.MigrationJournal(), "the journal is the only record of what is still out there")
	require.NotNil(t, f.rt.LastDependencyMigration())
	assert.Contains(t, f.rt.LastDependencyMigration().Error, "volume is in use")
}

// A journal naming a dependency THIS launcher cannot roll back must keep the
// journal and say which one — silently clearing it would strand the leftovers
// with no record at all.
//
// The id is one no release has ever shipped, on purpose: a journal is written
// by whichever launcher STARTED the migration, so the launcher that finds it
// may be an older build than the one that opened it. Naming a dependency this
// release does migrate would make the test pass for the wrong reason the day
// somebody wires a rollback for it.
func TestRecoveryOfAnUnknownDependencyKeepsTheJournal(t *testing.T) {
	f := newRecoveryFixture(t)
	f.openJournal(&deps.MigrationJournal{ID: deps.ID("redis"), From: "redis:7", To: "redis:8"})

	assert.False(t, f.d.recoverInterruptedMigration(context.Background(), f.act))
	require.NotNil(t, f.rt.MigrationJournal())
	require.NotNil(t, f.rt.LastDependencyMigration())
	assert.Contains(t, f.rt.LastDependencyMigration().Error, "redis",
		"the verdict must name the dependency nobody can undo")
}

func TestRecoveryWithoutAJournalOnlyRemovesTheStaleDir(t *testing.T) {
	f := newRecoveryFixture(t)
	assert.False(t, f.d.recoverInterruptedMigration(context.Background(), f.act))
	assert.NotContains(t, f.env.Dirs, f.env.DumpRoot, "a crash after the commit leaves a dump nobody will read")
	assert.Contains(t, f.env.Volumes, "postgres2", "nothing else is touched")
	assert.Contains(t, f.env.Containers, migrate.SrcContainer,
		"with no journal there is nothing to roll back — containers are the runtime's business")
}

// The stale-dir sweep must NOT run while a journal is open: that directory
// holds the dump the pending rollback still describes.
func TestRecoveryKeepsTheScratchDirWhileAJournalIsOpen(t *testing.T) {
	f := newRecoveryFixture(t)
	f.env.FailOn["rmdir:"+f.dumpDir()] = errors.New("busy")
	f.openJournal(&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		DumpDir: f.dumpDir()})

	assert.False(t, f.d.recoverInterruptedMigration(context.Background(), f.act))
	assert.Contains(t, f.env.Dirs, f.dumpDir())
	assert.Contains(t, f.env.Dirs, f.env.DumpRoot, "the scratch parent is not swept while a rollback is pending")
	assert.NotNil(t, f.rt.MigrationJournal())
}

func TestRecoveryIsANoOpWithoutARuntime(t *testing.T) {
	d := &Daemon{}
	assert.False(t, d.recoverInterruptedMigration(context.Background(), activeNamespace{}))
}

// observingEnv decorates the SHARED fake (never replaces it) with one hook: it
// reports what the world looked like at the moment the rollback removed the
// half-built target volume. That instant is what the two production entry
// points are about — recovery must reach it before the runtime is started and
// before the namespace is installed.
type observingEnv struct {
	*migratetest.FakeEnv
	onRollback func()
}

func (e observingEnv) RemoveVolume(ctx context.Context, v string) error {
	if e.onRollback != nil {
		e.onRollback()
	}
	return e.FakeEnv.RemoveVolume(ctx, v) //nolint:wrapcheck // decorator must return the fake's error verbatim
}

// bootFixture is a namespace as the boot path has it: loaded, not started.
func bootFixture(t *testing.T, journal *deps.MigrationJournal, shouldStart bool) (*Daemon, *loadedNamespace, *recoveryFixture) {
	t.Helper()
	f := newRecoveryFixture(t)
	if journal != nil {
		f.openJournal(journal)
	}
	loaded := &loadedNamespace{
		NsConfig:    &namespace.Config{ID: "ns1"},
		Runtime:     f.rt,
		AppDefs:     []appdef.ApplicationDef{{Name: "postgres"}},
		VolumesBase: f.act.volumesBase,
		ShouldStart: shouldStart,
	}
	return f.d, loaded, f
}

// The boot path's contract, in one order: roll back, THEN start — and start at
// all only because the rollback said the namespace had been running.
func TestBootRollsBackBeforeItStartsAndInheritsTheRestart(t *testing.T) {
	d, loaded, f := bootFixture(t, &deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5",
		To: "postgres:18", CreatedVolume: "postgres3", WasRunning: true}, false)

	var starts int
	var startsAtRollback = -1
	var journalOpenAtStart bool
	d.runtimeStartFn = func(rt *namespace.Runtime, apps []appdef.ApplicationDef) {
		starts++
		journalOpenAtStart = rt.MigrationJournal() != nil
		assert.Equal(t, []appdef.ApplicationDef{{Name: "postgres"}}, apps)
	}
	base := f.env
	d.depsEnvFn = func(activeNamespace) migrate.Env {
		return observingEnv{FakeEnv: base, onRollback: func() { startsAtRollback = starts }}
	}

	d.recoverThenStartLoadedNamespace(context.Background(), loaded, "ws1")

	assert.Equal(t, 0, startsAtRollback, "the rollback must run BEFORE the namespace is started")
	assert.Equal(t, 1, starts, "a namespace the migration had stopped is handed back running")
	assert.False(t, journalOpenAtStart, "the runtime starts only once the journal is closed")
	assert.True(t, loaded.ShouldStart)
}

// A rollback that FAILED leaves leftovers (a temp container still holding the
// old data volume), so the boot path must not start the namespace at all —
// whatever the journal's WasRunning says.
func TestBootDoesNotStartWhenTheRollbackFailed(t *testing.T) {
	d, loaded, f := bootFixture(t, &deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5",
		To: "postgres:18", CreatedVolume: "postgres3", WasRunning: true}, false)
	f.env.FailOn["rmvol:postgres3"] = errors.New("volume is in use")
	var starts int
	d.runtimeStartFn = func(*namespace.Runtime, []appdef.ApplicationDef) { starts++ }

	d.recoverThenStartLoadedNamespace(context.Background(), loaded, "ws1")

	assert.Zero(t, starts)
	assert.False(t, loaded.ShouldStart)
	assert.NotNil(t, f.rt.MigrationJournal())
}

// The ordinary boot — no journal — is unchanged: the persisted-status hint
// alone decides, and the stale scratch dir is swept before the start.
func TestBootWithoutAJournalStartsOnThePersistedHint(t *testing.T) {
	d, loaded, f := bootFixture(t, nil, true)
	var starts int
	var dirAtStart bool
	d.runtimeStartFn = func(*namespace.Runtime, []appdef.ApplicationDef) {
		starts++
		dirAtStart = f.env.Dirs[f.env.DumpRoot]
	}

	d.recoverThenStartLoadedNamespace(context.Background(), loaded, "ws1")

	assert.Equal(t, 1, starts)
	assert.False(t, dirAtStart, "the stale scratch dir is swept before the runtime starts")

	// …and a namespace the user had stopped stays stopped.
	d2, loaded2, _ := bootFixture(t, nil, false)
	var starts2 int
	d2.runtimeStartFn = func(*namespace.Runtime, []appdef.ApplicationDef) { starts2++ }
	d2.recoverThenStartLoadedNamespace(context.Background(), loaded2, "ws1")
	assert.Zero(t, starts2)
}

// The switch/activate path: recovery must finish BEFORE the pointer swap, so
// the namespace is never reachable — and therefore never startable — while a
// temp container still holds its data volume.
func TestInstallLoadedNamespaceRecoversBeforeTheSwap(t *testing.T) {
	f := newRecoveryFixture(t)
	f.openJournal(&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		CreatedVolume: "postgres3", WasRunning: true})

	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	d := f.d
	d.store = store
	d.activeNs = &activeNamespace{workspaceID: "ws1", nsConfig: &namespace.Config{ID: "ns-old"}}

	activeAtRollback := "<not called>"
	base := f.env
	d.depsEnvFn = func(activeNamespace) migrate.Env {
		return observingEnv{FakeEnv: base, onRollback: func() {
			activeAtRollback = namespaceIDOf(d.active())
		}}
	}

	loaded := &loadedNamespace{
		NsConfig:    &namespace.Config{ID: "ns1"},
		Runtime:     f.rt,
		AppDefs:     []appdef.ApplicationDef{{Name: "postgres"}},
		VolumesBase: f.act.volumesBase,
		ShouldStart: true, // ignored here: an activate never auto-starts
	}
	require.NoError(t, d.installLoadedNamespace(loaded, "ws1", "ns1"))

	assert.Equal(t, "ns-old", activeAtRollback,
		"the rollback must finish before the new namespace becomes reachable")
	assert.Equal(t, "ns1", namespaceIDOf(d.active()), "and the swap still happens")
	assert.Nil(t, f.rt.MigrationJournal(), "the interrupted migration was closed on the way in")
	assert.NotContains(t, f.env.Volumes, "postgres3")
}

// reloadingEnv mirrors the ONE thing about the production Env that makes the
// recovery path's lock discipline load-bearing: depsEnv.ReloadAndStart takes
// d.reloadMu (see its doc — the migration holds longOp for its whole run, and
// reloadMu is the only other lock it may take). The shared fake takes nothing,
// so without this the deadlock below could only be argued about, not observed.
type reloadingEnv struct {
	*migratetest.FakeEnv
	d *Daemon
}

func (e reloadingEnv) ReloadAndStart(ctx context.Context, start bool) error {
	e.d.reloadMu.Lock()
	defer e.d.reloadMu.Unlock()
	return e.FakeEnv.ReloadAndStart(ctx, start) //nolint:wrapcheck // decorator must return the fake's error verbatim
}

// installLoadedNamespace runs crash recovery while its CALLERS hold reloadMu
// (its own doc says they must), and the rollback it runs would take that same
// lock if it ever restarted the namespace. What keeps that from being a
// deadlock is one line in recoverInterruptedMigration: the journal it hands to
// RollbackPostgres carries WasRunning=false, because restarting is the
// caller's decision at this point — the namespace is not installed yet.
//
// Until now that was a comment. Here it is the test: the journal says the
// namespace WAS running, the active namespace has the same id as the one being
// installed (so the Env's other guard — "refuse a reload aimed at a namespace
// that is not active" — does not apply and cannot be what saves us), and
// reloadMu is held throughout. A rollback that inherited WasRunning would hang
// on it forever.
func TestInstallLoadedNamespaceRollbackCannotDeadlockOnReloadMu(t *testing.T) {
	f := newRecoveryFixture(t)
	f.openJournal(&deps.MigrationJournal{ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18",
		CreatedVolume: "postgres3", WasRunning: true})

	store, err := storage.NewSQLiteStore(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	d := f.d
	d.store = store
	// The SAME namespace is already active: this is the arrangement in which
	// the Env's active-namespace refusal is not what prevents the reload.
	d.activeNs = &activeNamespace{workspaceID: "ws1", nsConfig: &namespace.Config{ID: "ns1"}}
	base := f.env
	d.depsEnvFn = func(activeNamespace) migrate.Env { return reloadingEnv{FakeEnv: base, d: d} }

	loaded := &loadedNamespace{
		NsConfig:    &namespace.Config{ID: "ns1"},
		Runtime:     f.rt,
		AppDefs:     []appdef.ApplicationDef{{Name: "postgres"}},
		VolumesBase: f.act.volumesBase,
	}

	// Exactly what handleActivateNamespace does around this call.
	d.reloadMu.Lock()
	done := make(chan error, 1)
	go func() { done <- d.installLoadedNamespace(loaded, "ws1", "ns1") }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("installLoadedNamespace deadlocked: its crash recovery tried to reload under reloadMu")
	}
	d.reloadMu.Unlock()

	assert.Empty(t, f.env.Reloads(),
		"recovery must not restart a namespace that is not installed yet — the caller decides that")
	assert.Nil(t, f.rt.MigrationJournal(), "the rollback still ran to completion")
	assert.NotContains(t, f.env.Volumes, "postgres3")
}
