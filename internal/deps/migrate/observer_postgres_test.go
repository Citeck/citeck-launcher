package migrate

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The observer's database is a SECOND PostgreSQL cluster in the same namespace.
// It takes the same plan — that is the point of keying the migrator by id
// instead of by the "postgres" constant — and every volume, dump directory and
// journal entry it touches must be ITS own. A plan that named the stand's own
// database here would dump and restore the wrong data.
func newObserverPostgresFakeEnv(t *testing.T, image string, gen int) *migratetest.FakeEnv {
	t.Helper()
	d, ok := deps.Lookup(deps.ObserverPostgres)
	require.True(t, ok, "the observer's database must be registered")
	v, ok := d.ParseVersion(image)
	require.True(t, ok, "unparsable seed image %q", image)

	env := migratetest.New()
	// Both clusters exist, on different versions and different generations:
	// anything that reads the wrong one shows up as a wrong volume below.
	env.States[deps.ObserverPostgres] = deps.DependencyState{Image: image, VolumeGen: gen}
	env.States[deps.Postgres] = deps.DependencyState{Image: "postgres:17.5", VolumeGen: 1}

	obsVol := deps.VolumeName(d, gen)
	env.Volumes[obsVol] = map[string]string{
		deps.PostgresLayoutFor(v.Major).PGVersionRel: strconv.Itoa(v.Major) + "\n",
	}
	env.VolSize[obsVol] = 2 << 30
	env.Volumes["postgres2"] = map[string]string{"PG_VERSION": "17\n"}
	env.VolSize["postgres2"] = 2 << 30
	env.ExecFn = migratetest.PostgresExec(map[string]migratetest.PostgresInventory{"": migratetest.HealthyPostgres()})
	return env
}

func TestObserverPostgresMigratesInItsOwnVolumes(t *testing.T) {
	env := newObserverPostgresFakeEnv(t, "postgres:17.5", 1)

	plan, j, err := PostgresMigrator{ID: deps.ObserverPostgres}.Plan(context.Background(), env,
		Path{"postgres:17.5", "postgres:18.6"}, PlanOptions{})
	require.NoError(t, err)

	assert.Equal(t, deps.ObserverPostgres, j.ID, "the journal must name the cluster being moved")
	assert.Equal(t, "obs_postgres2", j.SourceVolume,
		"the source is the observer's own generation, never the stand database's postgres2")
	assert.Equal(t, 2, j.ToVolumeGen)
	assert.Contains(t, j.DumpDir, string(deps.ObserverPostgres),
		"two clusters migrating must not share one dump directory")
	assert.Equal(t, PostgresStepIDs(), stepIDs(plan), "the same plan, step for step")

	require.NoError(t, runPlanAgainstFake(t, j, plan))
	live := env.LiveVolumes()
	assert.Contains(t, live, "obs_postgres3", "the new cluster lands in the observer's next generation")
	assert.NotContains(t, live, "postgres3",
		"nothing about the observer's migration may touch the stand's own database")
	assert.NotContains(t, env.RemovedVolumes(), "postgres2",
		"nor may it remove the stand database's data")
}

// The registry must answer for it too — a descriptor that claims Migratable()
// with no plan wired is a "the launcher offered an upgrade it cannot perform"
// bug that only surfaces after the operator presses the button.
func TestObserverPostgresHasAMigratorAndARollbackOfItsOwn(t *testing.T) {
	m, ok := MigratorFor(deps.ObserverPostgres)
	require.True(t, ok)
	assert.Equal(t, PostgresMigrator{ID: deps.ObserverPostgres}, m,
		"it must be the postgres plan keyed to the observer's cluster, not to the stand's")
	_, ok = RollbackFor(deps.ObserverPostgres)
	assert.True(t, ok)
}

// A migrator built without an id would migrate the wrong cluster's data, so it
// refuses instead of defaulting to the stand's own database.
func TestAPostgresMigratorWithNoIDRefusesToPlan(t *testing.T) {
	env := newObserverPostgresFakeEnv(t, "postgres:17.5", 1)
	_, _, err := PostgresMigrator{}.Plan(context.Background(), env,
		Path{"postgres:17.5", "postgres:18.6"}, PlanOptions{})
	require.Error(t, err)
}

// The defect a real stand found: every psql, pg_dumpall, pg_isready and
// vacuumdb the plan runs used to name the role "postgres". The observer's
// cluster is created with POSTGRES_USER=observer, which means it has NO
// "postgres" role and no "postgres" database — so `pg_isready -U postgres`
// never answered, the source container sat there until the five-minute
// readiness deadline, and the migration rolled back every time.
func TestPostgresCredentialsComeFromTheClustersOwnDefinition(t *testing.T) {
	envOf := func(pairs ...string) appdef.ApplicationDef {
		def := appdef.ApplicationDef{}
		for i := 0; i+1 < len(pairs); i += 2 {
			def.Environments.Set(pairs[i], pairs[i+1])
		}
		return def
	}

	assert.Equal(t, pgCreds{User: "postgres", DB: "postgres"}, pgCredsFromDef(envOf()),
		"a def that names neither takes the image's own defaults")
	assert.Equal(t, pgCreds{User: "observer", DB: "observer"},
		pgCredsFromDef(envOf("POSTGRES_USER", "observer")),
		"the image defaults POSTGRES_DB to the user's name, and so must we")
	assert.Equal(t, pgCreds{User: "observer", DB: "obsdata"},
		pgCredsFromDef(envOf("POSTGRES_USER", "observer", "POSTGRES_DB", "obsdata")))
	assert.Equal(t, pgCreds{User: "postgres", DB: "postgres"},
		pgCredsFromDef(envOf("POSTGRES_USER", "")),
		"an empty value is not a name")
}

// And the commands actually carry it: the dump, the restore and the readiness
// probe must all address the cluster's own superuser.
func TestTheDumpAndRestoreAddressTheClustersOwnSuperuser(t *testing.T) {
	c := pgCreds{User: "observer", DB: "observer"}

	dump := DumpScript(c, "/dump/d.sql.gz")
	require.Len(t, dump, 3)
	assert.Contains(t, dump[2], "pg_dumpall -h 127.0.0.1 -U observer",
		"pg_dumpall as a role that does not exist fails the whole migration")

	restore := RestoreScript(c, "/dump/d.sql.gz")
	require.Len(t, restore, 3)
	assert.Contains(t, restore[2], "-U observer")
	assert.Contains(t, restore[2], "-d observer",
		"there is no 'postgres' database in this cluster to connect to")

	assert.Equal(t, []string{"psql", "-h", "127.0.0.1", "-U", "observer", "-d", "observer",
		"-q", "-At", "-c", "select 1"}, psqlArgs(c, c.DB, "select 1"))
}

// The diagnostics contract: whatever a failed step leaves behind is collected
// BEFORE the rollback deletes it, and reaches the caller.
//
// This is the missing half of the observer's first real failure: the launcher
// reported a five-minute timeout and the container that knew the reason was
// already gone, so "it would not update" had no answer in it.
func TestAFailedStepCollectsTheTempContainerLogsBeforeTheRollback(t *testing.T) {
	env := newObserverPostgresFakeEnv(t, "postgres:17.5", 1)
	env.Logs[SrcContainer] = `FATAL:  role "postgres" does not exist`
	// The source container starts and then never answers — exactly the shape of
	// the real failure (a readiness probe against a role that does not exist).
	env.FailOn["running:"+SrcContainer] = errors.New("container is gone")

	plan, j, err := PostgresMigrator{ID: deps.ObserverPostgres}.Plan(context.Background(), env,
		Path{"postgres:17.5", "postgres:18.6"}, PlanOptions{})
	require.NoError(t, err)

	runErr := Run(context.Background(), &fakeStore{}, j, plan, nil)
	require.Error(t, runErr)

	var sf *StepFailure
	require.ErrorAs(t, runErr, &sf, "a step failure must carry its evidence to the caller")
	require.NotEmpty(t, sf.Diagnostics, "the logs were collected while the container still existed")
	assert.Contains(t, sf.Diagnostics[0].Name, SrcContainer)
	assert.Contains(t, sf.Diagnostics[0].Text, `role "postgres" does not exist`)
	assert.Equal(t, runErr.Error(), sf.Error(),
		"wrapping must not change the message every existing caller already prints")
}
