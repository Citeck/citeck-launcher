package migrate

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
)

// rolledBackPostgres is the world a rollback is OFFERED in: a namespace that
// migrated 17.5 → 18.6, so the pin is generation 2 with generation 1 recorded
// as its target and the volume that migration copied FROM is still on disk.
func rolledBackPostgres(t *testing.T) (*migratetest.FakeEnv, deps.DependencyState) {
	t.Helper()
	env := migratetest.New()
	env.Running = true
	env.States[deps.Postgres] = deps.DependencyState{
		Image: "postgres:18.6", VolumeGen: 2,
		PrevImage: "postgres:17.5", PrevVolumeGen: 1,
	}
	env.Volumes["postgres2"] = map[string]string{"PG_VERSION": "17\n"}
	env.Volumes["postgres3"] = map[string]string{"18/docker/PG_VERSION": "18\n"}
	env.LocalImages["postgres:17.5"] = true
	prev, ok := env.States[deps.Postgres].Previous()
	require.True(t, ok)
	return env, prev
}

// A rollback CREATES NOTHING: the retained volume is already there and the one
// the namespace leaves is kept exactly as it is. So there is nothing to
// measure, and SpaceChecked must stay false — both renderers skip an unmeasured
// result, and rendering the zeros verbatim reads as a namespace with no data on
// a full disk.
func TestRollbackPreflightMeasuresNothing(t *testing.T) {
	env, prev := rolledBackPostgres(t)
	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)

	require.True(t, res.OK, "problems: %v", res.Problems)
	assert.False(t, res.Measured())
	assert.False(t, res.SpaceChecked)
	assert.Zero(t, res.DataSizeBytes)
	assert.Zero(t, res.RequiredHostBytes)
	assert.Zero(t, res.RequiredVolumeBytes)
	assert.Zero(t, res.RequiredTotalBytes)
	assert.Zero(t, res.FreeHostBytes)
	assert.Zero(t, res.FreeVolumeBytes)
	assert.False(t, res.SharedFilesystem)
	assert.Nil(t, res.ExistingTargetVolume)

	// From/To are the move the rollback would make, and WasRunning is what
	// decides whether the namespace comes back up afterwards.
	assert.Equal(t, "postgres:18.6", res.From)
	assert.Equal(t, "postgres:17.5", res.To)
	assert.True(t, res.WasRunning)

	// The wire contract every PreflightResult owes: arrays, never null.
	assert.NotNil(t, res.Problems)
	assert.NotNil(t, res.Warnings)
}

// The ordinary state of a namespace that has never migrated. The row simply
// carries no rollback offer, so reaching the preflight at all is a race or a
// hand-typed command — and it must say so rather than probe a volume name it
// derived from nothing.
func TestRollbackPreflightRefusesWithNoTarget(t *testing.T) {
	env := migratetest.New()
	env.States[deps.Postgres] = deps.DependencyState{Image: "postgres:17.5"}

	res := RollbackPreflight(context.Background(), env, deps.Postgres, deps.DependencyState{})
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "no recorded previous version")
	assert.Empty(t, res.Warnings, "nothing to warn about when there is nothing to do")
}

// The launcher actively tells the operator they may delete the retained volume
// (`deps.oldVolume`), so this is a state it creates itself. The message names
// the volume: without it the refusal is mysterious, and the operator cannot
// tell "I deleted it" from "the launcher lost it".
func TestRollbackPreflightNamesTheMissingRetainedVolume(t *testing.T) {
	env, prev := rolledBackPostgres(t)
	delete(env.Volumes, "postgres2")

	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "postgres2")
	assert.Contains(t, renderEN(res.Problems)[0], "postgres:17.5")
	// The consequence warnings describe a rollback that is going to happen.
	assert.Empty(t, res.Warnings)
}

func TestRollbackPreflightReportsAVolumeItCannotCheck(t *testing.T) {
	env, prev := rolledBackPostgres(t)
	env.FailOn = map[string]error{"volexists:postgres2": errors.New("docker is away")}

	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "postgres2")
	assert.Contains(t, renderEN(res.Problems)[0], "docker is away")
}

// PG_VERSION is what the data itself says it is; the target says what the
// launcher THINKS it left there. A disagreement means somebody deleted and
// recreated that volume, and starting the old server on it is not a rollback.
func TestRollbackPreflightRefusesDataThatDisagreesWithTheTarget(t *testing.T) {
	env, prev := rolledBackPostgres(t)
	env.Volumes["postgres2"] = map[string]string{"PG_VERSION": "16\n"}

	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "postgres2")
	assert.Contains(t, renderEN(res.Problems)[0], `"16"`)
	assert.Contains(t, renderEN(res.Problems)[0], "postgres:17.5")

	// A marker that is not there at all is the same verdict: the volume exists
	// but holds no cluster the target could describe.
	env, prev = rolledBackPostgres(t)
	env.Volumes["postgres2"] = map[string]string{}
	res = RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "PG_VERSION")
}

// RabbitMQ and ZooKeeper write no version marker into their data. The check is
// SKIPPED entirely rather than inventing a path — a problem derived from a file
// that was never supposed to exist would refuse every rollback of those two.
func TestRollbackPreflightSkipsTheVersionCheckWhereThereIsNoMarker(t *testing.T) {
	for _, c := range []struct {
		id        deps.ID
		cur, prev string
		vol       string
	}{
		{deps.RabbitMQ, "rabbitmq:4.2.9-management", "rabbitmq:4.1.8-management", "rabbitmq2"},
		{deps.Zookeeper, "zookeeper:3.9.5", "zookeeper:3.8.6", "zookeeper2"},
	} {
		t.Run(string(c.id), func(t *testing.T) {
			env := migratetest.New()
			env.States[c.id] = deps.DependencyState{
				Image: c.cur, VolumeGen: 2, PrevImage: c.prev, PrevVolumeGen: 1,
			}
			// Deliberately full of junk: nothing in it may be read as a version.
			env.Volumes[c.vol] = map[string]string{"PG_VERSION": "42"}
			env.Volumes[deps.VolumeName(mustLookup(t, c.id), 2)] = map[string]string{}
			env.LocalImages[c.prev] = true

			prev, ok := env.States[c.id].Previous()
			require.True(t, ok)
			res := RollbackPreflight(context.Background(), env, c.id, prev)
			assert.True(t, res.OK, "problems: %v", res.Problems)
		})
	}
}

// A previous image whose tag cannot be read is still a rollback target — the
// pin says the namespace really ran it — but nothing can be compared against
// the data, so the check is skipped rather than turned into a refusal.
func TestRollbackPreflightSkipsTheVersionCheckForAnUnreadableTag(t *testing.T) {
	env, _ := rolledBackPostgres(t)
	env.States[deps.Postgres] = deps.DependencyState{
		Image: "postgres:18.6", VolumeGen: 2, PrevImage: "postgres:custom", PrevVolumeGen: 1,
	}
	env.LocalImages["postgres:custom"] = true
	prev, ok := env.States[deps.Postgres].Previous()
	require.True(t, ok)

	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.True(t, res.OK, "problems: %v", res.Problems)
}

// R1.4's three sentences, which are the whole reason the rollback needs a
// confirmation at all: the data is as of the migration, everything since then
// lives on a volume that is KEPT and never read again, and there is no
// roll-forward.
func TestRollbackPreflightWarnsAboutTheFrozenVolumeAndTheOneWayTrip(t *testing.T) {
	env, prev := rolledBackPostgres(t)
	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	require.True(t, res.OK, "problems: %v", res.Problems)

	all := joinEN(res.Warnings)
	assert.Contains(t, all, "postgres3", "the frozen volume must be named")
	assert.Contains(t, all, "unreachable", "the newer data becomes unreachable")
	assert.Contains(t, all, "keeps it")
	assert.Contains(t, all, "postgres:17.5")
	assert.Contains(t, all, "postgres:18.6")
	assert.Contains(t, all, "no roll-forward")
	// The retained volume is what it will run on, and the operator has to be
	// able to recognize it on disk.
	assert.Contains(t, all, "postgres2")
}

// Ruling 6 (OPEN QUESTION 6, candidate B): check the previous image locally and
// WARN, never refuse. Without it the failure lands after the namespace is
// already stopped; with it the operator gets a heads-up that costs nothing to
// ignore, because the pin names an image that really ran.
func TestRollbackPreflightWarnsWhenThePreviousImageIsNotLocal(t *testing.T) {
	env, prev := rolledBackPostgres(t)
	delete(env.LocalImages, "postgres:17.5")

	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.True(t, res.OK, "a missing image must never refuse: %v", res.Problems)
	assert.Empty(t, res.Problems)

	all := joinEN(res.Warnings)
	assert.Contains(t, all, "postgres:17.5")
	assert.Contains(t, all, "not present locally")

	// And it is silent when the image IS there.
	env, prev = rolledBackPostgres(t)
	res = RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.NotContains(t, joinEN(res.Warnings), "not present locally")
}

// A target that does not go back a generation is not a rollback: the volume it
// would "return to" is the one the namespace is already on, so the frozen-volume
// warning would name the live data. It can only arrive from a corrupted state
// file or a commit that recorded the wrong side, and both deserve a refusal
// rather than a confident, wrong sentence.
func TestRollbackPreflightRefusesATargetThatIsNotOlderThanTheCurrentGeneration(t *testing.T) {
	env, _ := rolledBackPostgres(t)
	env.States[deps.Postgres] = deps.DependencyState{
		Image: "postgres:18.6", VolumeGen: 2, PrevImage: "postgres:17.5", PrevVolumeGen: 2,
	}
	prev, ok := env.States[deps.Postgres].Previous()
	require.True(t, ok)

	res := RollbackPreflight(context.Background(), env, deps.Postgres, prev)
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "generation")
}

func TestRollbackPreflightRefusesAnUnregisteredDependency(t *testing.T) {
	env := migratetest.New()
	res := RollbackPreflight(context.Background(), env, "redis",
		deps.DependencyState{Image: "redis:7", VolumeGen: 1})
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "not a registered dependency")
}

// Keycloak's state lives in the PostgreSQL database and it has no volume of its
// own, so there is no generation to switch. It is not migratable either, so the
// case is unreachable in practice — which is exactly why it must not fall
// through to VolumeExists(ctx, "").
func TestRollbackPreflightRefusesADependencyWithNoDataVolume(t *testing.T) {
	env := migratetest.New()
	env.States[deps.Keycloak] = deps.DependencyState{Image: "keycloak/keycloak:26.7", VolumeGen: 2,
		PrevImage: "keycloak/keycloak:26.4", PrevVolumeGen: 1}
	prev, ok := env.States[deps.Keycloak].Previous()
	require.True(t, ok)

	res := RollbackPreflight(context.Background(), env, deps.Keycloak, prev)
	assert.False(t, res.OK)
	require.Len(t, res.Problems, 1)
	assert.Contains(t, renderEN(res.Problems)[0], "no data volume")
}

// The rollback reuses the migration's progress channel, so its steps need ids
// and every id needs a locale key. Two of the three are new; "stop-namespace"
// is deliberately the SAME id both migration plans already use, because it is
// literally the same step and a second spelling would be a second translation
// of one sentence.
func TestRollbackStepIDs(t *testing.T) {
	assert.Equal(t, []string{"stop-namespace", "switch-generation", "start-namespace"}, RollbackStepIDs())
	assert.Contains(t, PostgresStepIDs(), "stop-namespace")
	assert.Contains(t, CopyStepIDs(), "stop-namespace")
}

// The two backwards sentences are built HERE and nowhere else: internal/deps is
// pure and must not carry operator prose, internal/namespace may not import
// this package, and the daemon renders what it is given.
func TestBundleOlderNotices(t *testing.T) {
	plain := oneEN(BundleOlderNotice("postgres:18.6", "postgres:17.5"))
	assert.Contains(t, plain, "postgres:17.5")
	assert.Contains(t, plain, "postgres:18.6")
	assert.Contains(t, plain, "older")
	// It must NOT send the operator to an action that will refuse them.
	assert.NotContains(t, plain, "citeck deps upgrade")
	assert.NotContains(t, plain, "citeck deps rollback")

	withRollback := oneEN(BundleOlderRollbackNotice("postgres", "postgres:18.6", "postgres:17.5"))
	assert.Contains(t, withRollback, "postgres:17.5")
	assert.Contains(t, withRollback, "postgres:18.6")
	assert.Contains(t, withRollback, "citeck deps rollback postgres")
	assert.NotContains(t, withRollback, "citeck deps upgrade")

	// The dependency ID is what is interpolated into the command, so it must be
	// spelled the way the CLI accepts it — the same rule VendorPathProblem
	// follows.
	assert.Contains(t, oneEN(BundleOlderRollbackNotice("rabbitmq",
		"rabbitmq:4.2.9-management", "rabbitmq:4.1.8-management")), "citeck deps rollback rabbitmq")
}

func mustLookup(t *testing.T, id deps.ID) deps.Descriptor {
	t.Helper()
	d, ok := deps.Lookup(id)
	require.True(t, ok)
	return d
}
