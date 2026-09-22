package migrate

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
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
	plan, j, err := PostgresMigrator{ID: deps.Postgres}.Plan(context.Background(), env,
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
	plan, j, err := PostgresMigrator{ID: deps.Postgres}.Plan(context.Background(), env, path, PlanOptions{})
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
	one := PostgresMigrator{ID: deps.Postgres}.Preflight(context.Background(), env, Path{"postgres:17.5", "postgres:18.6"})
	many := PostgresMigrator{ID: deps.Postgres}.Preflight(context.Background(), env,
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
	plan, j, err := PostgresMigrator{ID: deps.Postgres}.Plan(context.Background(), env, path, PlanOptions{})
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
	plan, j, err := PostgresMigrator{ID: deps.Postgres}.Plan(context.Background(), env, path, PlanOptions{})
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

// genRecordingEnv wraps a FakeEnv and records, per image, the def
// GenerateDefForVolume actually returned — the same def RunAppDef then
// starts a container from. Only GenerateDefForVolume is overridden; every
// other call is the embedded FakeEnv's, unchanged.
//
// It exists to close a coverage hole: every other test in this file observes
// only the WALK'S END STATE (which volumes survive, what the journal says).
// None of them asks what volume a specific rung's container was actually
// generated to mount while the walk was running — which is exactly the
// question a plan that named the wrong volume for one rung, but still ended
// up in a consistent final state, would dodge.
type genRecordingEnv struct {
	*migratetest.FakeEnv
	defsByImage map[string]appdef.ApplicationDef
}

func newGenRecordingEnv(f *migratetest.FakeEnv) *genRecordingEnv {
	return &genRecordingEnv{FakeEnv: f, defsByImage: map[string]appdef.ApplicationDef{}}
}

func (g *genRecordingEnv) GenerateDefForVolume(
	id deps.ID, st deps.DependencyState, volume string,
) (appdef.ApplicationDef, error) {
	def, err := g.FakeEnv.GenerateDefForVolume(id, st, volume)
	if err == nil {
		g.defsByImage[st.Image] = def
	}
	return def, err //nolint:wrapcheck // a decorator must return the fake's error verbatim
}

// defMountsVolume mirrors the daemon's own mountsVolume: a volume entry is
// "<source>:<container path>[:opts]" and only the source half is compared.
func defMountsVolume(def appdef.ApplicationDef, name string) bool {
	for _, v := range def.Volumes {
		if src, _, ok := strings.Cut(v, ":"); ok && src == name {
			return true
		}
	}
	return false
}

// Rung 1's container must be generated for the SCRATCH volume, never the
// source — the gap C1 found: startTempOnVolume passes VolumeGen unset, so on
// a never-migrated namespace GenerateDefForVolume's own "ordinary" mount for
// generation 1 IS the source volume, and if the plan ever named the source
// as VOLUME there, the retarget guard would see mountVolume == ordinary and
// skip itself as a no-op — the def would "correctly" mount the source and
// every end-state assertion in this file would still pass. Asserting rung 1's
// def directly is the only way to catch that.
func TestPostgresLadderRung1MountsScratchVolumeNotSource(t *testing.T) {
	base := newPostgresFakeEnv(t, "postgres:17.5", 1)
	env := newGenRecordingEnv(base)
	path := Path{"postgres:17.5", "postgres:18.6", "postgres:19.2", "postgres:20.1"}
	plan, j, err := PostgresMigrator{ID: deps.Postgres}.Plan(context.Background(), env, path, PlanOptions{})
	require.NoError(t, err)
	// ScratchVolume/CreatedVolume are write-ahead fields, journalled by the
	// STEP that is about to create them — see TestPostgresThreeRungPlanReusesOneScratchVolume
	// — so Plan()'s own returned journal has neither yet. ToVolumeGen and
	// SourceVolume ARE set at Plan() time, so the expected scratch volume is
	// computed the same way pgRun itself does, independent of the run.
	d, ok := deps.Lookup(deps.Postgres)
	require.True(t, ok)
	expectedScratch := deps.ScratchVolumeName(d, j.ToVolumeGen)
	require.NotEmpty(t, expectedScratch, "a 3-rung ladder must have a scratch volume to assert against")

	require.NoError(t, runPlanAgainstFake(t, j, plan))

	def, ok := env.defsByImage["postgres:18.6"]
	require.True(t, ok, "rung 1 (postgres:18.6) never asked GenerateDefForVolume for a def")
	assert.True(t, defMountsVolume(def, expectedScratch),
		"rung 1's container must mount the scratch volume %q, got %v", expectedScratch, def.Volumes)
	assert.False(t, defMountsVolume(def, j.SourceVolume),
		"rung 1's container must NOT mount the source volume %q, got %v", j.SourceVolume, def.Volumes)
}

// A same-major rung in the middle of an otherwise major-crossing ladder must
// not be refused: SupportsPair's only rule (to.Major > from.Major) is a
// statement about the PLAN's own capability, not a vendor per-hop
// restriction, so Preflight asks it of the route's ENDPOINTS (17.5 → 18.6,
// a real forward major move) rather than of every hop the way CopyPreflight
// does for RabbitMQ/ZooKeeper. Applying it per hop would refuse the hop
// 17.5 → 17.9 (same major, to.Major > from.Major is false) even though the
// bundle author sanctioned that exact rung and the logical dump plan handles
// a same-major republish with no extra risk at all — see the comment on
// Preflight and spec §7 ("a rung is never skipped").
func TestPostgresLadderToleratesASameMajorIntermediateRung(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	route := Path{"postgres:17.5", "postgres:17.9", "postgres:18.6"}

	res := PostgresMigrator{ID: deps.Postgres}.Preflight(context.Background(), env, route)

	assert.True(t, res.OK, "a same-major rung inside a forward ladder must not be refused: %v", res.Problems)
	assert.Empty(t, res.Problems)
}
