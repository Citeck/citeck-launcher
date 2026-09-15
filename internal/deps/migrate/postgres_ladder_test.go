package migrate

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPostgresFakeEnv is a stopped namespace pinned to image at generation gen,
// with a healthy cluster of that image's major on the volume that generation
// names. It generalizes envWith17Data (generation 1 / postgres:17.5 only) so
// a ladder test can seed a namespace at any rung — every case in this file
// happens to start at generation 1 on 17.5, but the parameters are real: a
// caller seeding a namespace mid-ladder is a one-line change, not a signature
// change.
//
//nolint:unparam // see above
func newPostgresFakeEnv(t *testing.T, image string, gen int) *migratetest.FakeEnv {
	t.Helper()
	d := postgresDescriptor()
	v, ok := d.ParseVersion(image)
	require.True(t, ok, "unparsable seed image %q", image)
	vol := deps.VolumeName(d, gen)
	env := migratetest.New()
	env.States[deps.Postgres] = deps.DependencyState{Image: image, VolumeGen: gen}
	env.Volumes[vol] = map[string]string{
		deps.PostgresLayoutFor(v.Major).PGVersionRel: strconv.Itoa(v.Major) + "\n",
	}
	env.VolSize[vol] = 2 << 30
	env.ExecFn = migratetest.PostgresExec(map[string]migratetest.PostgresInventory{"": migratetest.HealthyPostgres()})
	return env
}

// runPlanAgainstFake runs plan (built against a FakeEnv) with a throwaway
// journal store: these tests only care about what the fake observed, not
// about the commit/rollback bookkeeping already covered by the other postgres
// tests.
func runPlanAgainstFake(t *testing.T, j deps.MigrationJournal, plan *Plan) error {
	t.Helper()
	return Run(context.Background(), &fakeStore{}, j, plan, nil)
}

// assertNeverCoexist walks trace in order, keeping a live count for kindA and
// for kindB (a Created event increments, a removal decrements), and fails the
// moment either count exceeds one — naming the step and the item that did it.
// Passing the same kind for both arguments (as every caller here does) simply
// asks "does more than one of this kind ever exist at once".
func assertNeverCoexist(t *testing.T, trace []migratetest.TraceEvent, kindA, kindB string) {
	t.Helper()
	live := map[string]int{}
	for i, ev := range trace {
		if ev.Kind != kindA && ev.Kind != kindB {
			continue
		}
		if ev.Created {
			live[ev.Kind]++
		} else {
			live[ev.Kind]--
		}
		if live[ev.Kind] > 1 {
			t.Fatalf("more than one live %q at trace step %d (%s, created=%v): %+v",
				ev.Kind, i, ev.Name, ev.Created, trace)
		}
	}
}

// The single-hop plan is the 10 steps it has always been.
func TestPostgresSingleHopPlanIsUnchanged(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	plan, j, err := PostgresMigrator{}.Plan(context.Background(), env,
		Path{"postgres:17.5", "postgres:18.6"}, PlanOptions{})
	require.NoError(t, err)
	assert.Equal(t, PostgresStepIDs(), stepIDs(plan))
	assert.Empty(t, j.ScratchVolume, "a single hop needs no intermediate cluster")
}

// Three rungs: one dump/restore cycle per rung, ONE scratch volume reused for
// every intermediate, and only the final cluster in the next generation.
//
// CreatedVolume and ScratchVolume are write-ahead fields — like the
// single-hop plan's CreatedVolume, they are journalled by the STEP that is
// about to create the volume, not by Plan() itself (Plan()'s own returned
// journal only carries what is known before anything runs: the identity, the
// generation and the source — see ToVolumeGen/SourceVolume below). So this
// runs the plan against a throwaway store and reads them off the LAST
// journal write, the way TestSameLayoutMajorsMigrateIntoTheNextGeneration
// already does for the single-hop plan's CreatedVolume.
func TestPostgresThreeRungPlanReusesOneScratchVolume(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	path := Path{"postgres:17.5", "postgres:18.6", "postgres:19.2", "postgres:20.1"}
	plan, j, err := PostgresMigrator{}.Plan(context.Background(), env, path, PlanOptions{})
	require.NoError(t, err)

	ids := stepIDs(plan)
	assert.Equal(t, 3, countID(ids, "restore"), "one restore per rung")
	assert.Equal(t, 3, countID(ids, "dump"), "the bottom dump plus one per intermediate")
	assert.Equal(t, 1, countID(ids, "verify"), "compared once, at the top")
	assert.Equal(t, 1, countID(ids, "stop-namespace"))

	assert.Equal(t, 2, j.ToVolumeGen, "the generation grows by exactly one")
	assert.Equal(t, "postgres2", j.SourceVolume, "the source is never written to")
	assert.Equal(t, "postgres:17.5", j.From)
	assert.Equal(t, "postgres:20.1", j.To)

	st := &fakeStore{}
	require.NoError(t, Run(context.Background(), st, j, plan, nil))
	require.NotEmpty(t, st.journals)
	last := st.journals[len(st.journals)-1]
	assert.Equal(t, "postgres3", last.CreatedVolume, "the FINAL cluster lands in generation 2")
	assert.Equal(t, "postgres3-hop", last.ScratchVolume,
		"one reused intermediate, journalled so the rollback removes it")
}

// The rollback must remove BOTH volumes the walk created. A journal written
// by an older launcher has no ScratchVolume and must still roll back (see
// TestRollbackRemovesOnlyWhatTheJournalNamesForAnOlderLauncher below).
func TestPostgresRollbackRemovesTheScratchVolumeToo(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	j := &deps.MigrationJournal{
		ID: deps.Postgres, From: "postgres:17.5", To: "postgres:20.1",
		CreatedVolume: "postgres3", ScratchVolume: "postgres3-hop",
		SourceVolume: "postgres2", Step: "restore",
	}
	require.NoError(t, RollbackPostgres(context.Background(), env, j))
	assert.ElementsMatch(t, []string{"postgres3", "postgres3-hop"}, env.RemovedVolumes())
}

// A journal an OLDER launcher wrote has no ScratchVolume field at all — it
// reads as "", the same value a single-hop migration's own journal carries —
// and the rollback must still remove exactly the one volume it named.
func TestRollbackPostgresStillWorksForAJournalWithNoScratchField(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	j := &deps.MigrationJournal{
		ID: deps.Postgres, From: "postgres:17.5", To: "postgres:18.6",
		CreatedVolume: "postgres3", SourceVolume: "postgres2", Step: "restore",
	}
	require.NoError(t, RollbackPostgres(context.Background(), env, j))
	assert.Equal(t, []string{"postgres3"}, env.RemovedVolumes())
}

// The requirement must NOT grow with the ladder. It stays at
// `source + one dump + one cluster` — today's single-hop peak — because each
// dump is removed as soon as the restore that consumed it succeeded and each
// intermediate cluster as soon as the next dump has been taken from it.
// Raising the requirement instead would refuse a migration that fits.
func TestPostgresLadderAsksForNoMoreDiskThanOneHop(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	one := PostgresMigrator{}.Preflight(context.Background(), env, Path{"postgres:17.5", "postgres:18.6"})
	many := PostgresMigrator{}.Preflight(context.Background(), env,
		Path{"postgres:17.5", "postgres:18.6", "postgres:19.2", "postgres:20.1"})

	require.True(t, one.OK, one.Problems)
	require.True(t, many.OK, many.Problems)
	assert.Equal(t, one.RequiredHostBytes, many.RequiredHostBytes)
	assert.Equal(t, one.RequiredVolumeBytes, many.RequiredVolumeBytes)
	assert.Equal(t, one.RequiredTotalBytes, many.RequiredTotalBytes)
}

// …and the order that makes it true. A dump must be gone BEFORE the next one
// is taken, and an intermediate cluster BEFORE the next is created — put the
// deletions at the end of the migration instead and the peak becomes two
// dumps plus a cluster, i.e. the preflight under-requires and the walk dies
// of ENOSPC on an already-stopped namespace.
func TestPostgresLadderDeletesEachDumpBeforeTakingTheNext(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	path := Path{"postgres:17.5", "postgres:18.6", "postgres:19.2", "postgres:20.1"}
	plan, j, err := PostgresMigrator{}.Plan(context.Background(), env, path, PlanOptions{})
	require.NoError(t, err)

	require.NoError(t, runPlanAgainstFake(t, j, plan))

	// The fake records every dump written, every dump removed, every volume
	// created and every volume removed, in order, on one trace.
	assertNeverCoexist(t, env.Trace(), "dump", "dump")
	assertNeverCoexist(t, env.Trace(), "cluster", "cluster")
	// And nothing is left behind by the successful walk except the final one.
	assert.Equal(t, []string{"postgres3"}, env.LiveVolumes())
	assert.Empty(t, env.LiveDumps())
}

// Every rung's dump is compressed, not just the first.
func TestEveryRungsDumpIsCompressed(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	path := Path{"postgres:17.5", "postgres:18.6", "postgres:19.2"}
	plan, j, err := PostgresMigrator{}.Plan(context.Background(), env, path, PlanOptions{})
	require.NoError(t, err)
	require.NoError(t, runPlanAgainstFake(t, j, plan))

	for _, cmd := range env.ExecutedCommands() {
		if len(cmd) == 3 && strings.Contains(cmd[2], "pg_dumpall") {
			assert.Contains(t, cmd[2], "gzip -1")
		}
	}
	for _, name := range env.DumpsWritten() {
		assert.True(t, strings.HasSuffix(name, ".sql.gz"), "dump %q is not compressed", name)
	}
}
