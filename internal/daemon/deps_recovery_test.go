package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/citeck/citeck-launcher/internal/namespace"
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

// A journal naming a dependency THIS launcher cannot roll back (a downgrade,
// or a newer launcher's dependency) must keep the journal and say which one —
// silently clearing it would strand the leftovers with no record at all.
func TestRecoveryOfAnUnknownDependencyKeepsTheJournal(t *testing.T) {
	f := newRecoveryFixture(t)
	f.openJournal(&deps.MigrationJournal{ID: deps.RabbitMQ, From: "rabbitmq:4.1.2", To: "rabbitmq:4.2.9"})

	assert.False(t, f.d.recoverInterruptedMigration(context.Background(), f.act))
	require.NotNil(t, f.rt.MigrationJournal())
	require.NotNil(t, f.rt.LastDependencyMigration())
	assert.Contains(t, f.rt.LastDependencyMigration().Error, "rabbitmq",
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
