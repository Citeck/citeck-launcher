package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
)

const (
	oldVol = deps.PostgresVolumeLegacy // postgres2
	newVol = deps.PostgresVolumeV18    // postgres3
	from17 = "postgres:17.5"
	to18   = "postgres:18"
)

// envWith17Data is a stopped namespace whose data volume holds a healthy
// PostgreSQL 17 cluster, with both temp containers scripted to answer like a
// real server.
func envWith17Data() *migratetest.FakeEnv {
	env := migratetest.New()
	env.Volumes[oldVol] = map[string]string{"PG_VERSION": "17\n"}
	env.VolSize[oldVol] = 2 << 30
	env.ExecFn = migratetest.PostgresExec(map[string]migratetest.PostgresInventory{"": migratetest.HealthyPostgres()})
	return env
}

// runPlan builds and runs the PostgreSQL plan against env. The store's
// journal hook is mirrored into the env's call log, which is what makes the
// write-ahead ordering (journal, THEN create) observable at all: the two are
// otherwise recorded in two unrelated sequences.
func runPlan(t *testing.T, env *migratetest.FakeEnv, opts PlanOptions) (*fakeStore, error) {
	t.Helper()
	plan, j, err := PostgresMigrator{}.Plan(context.Background(), env, from17, to18, opts)
	require.NoError(t, err)
	st := &fakeStore{onSet: func(rec deps.MigrationJournal) {
		if rec.CreatedVolume != "" {
			env.Record("journal:" + rec.CreatedVolume)
		}
	}}
	return st, Run(context.Background(), st, j, plan, nil)
}

func TestPreflightHappyPath(t *testing.T) {
	env := envWith17Data()
	env.Running = true
	res := PostgresMigrator{}.Preflight(context.Background(), env, from17, to18)
	assert.True(t, res.OK, res.Problems)
	assert.Empty(t, res.Problems)
	assert.Empty(t, res.Warnings)
	assert.True(t, res.WasRunning)
	assert.Equal(t, from17, res.From)
	assert.Equal(t, to18, res.To)
	assert.Equal(t, int64(2<<30), res.DataSizeBytes)
	assert.Equal(t, int64(2<<30)+SpaceMargin, res.RequiredHostBytes)
	assert.Equal(t, int64(2<<30)+SpaceMargin, res.RequiredVolumeBytes)
	assert.Equal(t, env.FreeHost, res.FreeHostBytes)
	assert.Equal(t, env.FreeVolume, res.FreeVolumeBytes)
	assert.Nil(t, res.ExistingTargetVolume)
}

// Problems and Warnings cross the API as arrays: the web dialog maps over both
// without a nil guard, so a clean preflight marshalling `"problems":null` is
// the confirm screen crashing in the ORDINARY case.
func TestPreflightMarshalsEmptyListsNotNull(t *testing.T) {
	env := envWith17Data()
	res := PostgresMigrator{}.Preflight(context.Background(), env, from17, to18)
	require.True(t, res.OK, res.Problems)
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"problems":[]`)
	assert.Contains(t, string(raw), `"warnings":[]`)
	assert.NotContains(t, string(raw), "null")
}

// The existing target volume is reported ONCE, through the structured field
// both consumers render; the English prose duplicate it used to append showed
// up next to its own localized restatement on the confirm screen.
func TestExistingTargetVolumeIsReportedOnlyAsAStructuredField(t *testing.T) {
	env := envWith17Data()
	env.Volumes[newVol] = map[string]string{"18/docker/PG_VERSION": "18\n"}
	env.VolSize[newVol] = 1 << 30
	res := PostgresMigrator{}.Preflight(context.Background(), env, from17, to18)
	require.NotNil(t, res.ExistingTargetVolume)
	assert.Equal(t, newVol, res.ExistingTargetVolume.Name)
	assert.Equal(t, "18", res.ExistingTargetVolume.Version)
	assert.Empty(t, res.Warnings, "no prose restatement of the structured field")
	assert.True(t, res.OK, "an existing target volume is a confirmation, not a problem")
}

// A pair whose two majors share one volume because this launcher has no layout
// for the newer one is NOT the in-place case: telling the operator about
// volumes sends them after a disk problem they do not have.
func TestPreflightRefusesAnUnsupportedPairAsALauncherUpdate(t *testing.T) {
	env := envWith17Data()
	env.Volumes[newVol] = map[string]string{"18/docker/PG_VERSION": "18\n"}
	res := PostgresMigrator{}.Preflight(context.Background(), env, to18, "postgres:19")
	assert.False(t, res.OK)
	joined := strings.Join(res.Problems, "\n")
	assert.Contains(t, joined, "update the launcher")
	assert.NotContains(t, joined, "separate volume")
}

// Supports is the migrator's own precondition — different volumes, forwards —
// and it is what the daemon asks before it offers an upgrade at all.
func TestPostgresMigratorSupports(t *testing.T) {
	v := func(major int) deps.Version { return deps.Version{Major: major} }
	m := PostgresMigrator{}
	assert.True(t, m.Supports(v(17), v(18)), "the pair the launcher was built for")
	assert.True(t, m.Supports(v(16), v(18)))
	assert.False(t, m.Supports(v(18), v(19)), "no layout for 19 yet")
	assert.False(t, m.Supports(v(16), v(17)), "one volume: in place, not a migration")
	assert.False(t, m.Supports(v(18), v(17)), "backwards")
	assert.False(t, m.Supports(v(18), v(18)))
}

func TestPreflightProblems(t *testing.T) {
	ctx := context.Background()
	t.Run("unreadable version", func(t *testing.T) {
		res := PostgresMigrator{}.Preflight(ctx, envWith17Data(), "postgres:latest", to18)
		assert.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "current image")
	})
	t.Run("not breaking", func(t *testing.T) {
		res := PostgresMigrator{}.Preflight(ctx, envWith17Data(), from17, "postgres:17.11")
		assert.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "not a major upgrade")
	})
	t.Run("downgrade", func(t *testing.T) {
		env := envWith17Data()
		env.Volumes[oldVol]["PG_VERSION"] = "18\n"
		res := PostgresMigrator{}.Preflight(ctx, env, to18, from17)
		assert.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "downgrade")
	})
	// Two majors that share one data volume (anything below 18) would have the
	// plan build the new cluster in the volume holding the old data — and then
	// offer to DELETE it as a leftover. The migrator only ever moves data into
	// a separate volume, so it refuses instead.
	t.Run("target major shares the old volume", func(t *testing.T) {
		env := envWith17Data()
		env.Volumes[oldVol]["PG_VERSION"] = "16\n"
		res := PostgresMigrator{}.Preflight(ctx, env, "postgres:16", "postgres:17")
		assert.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "separate volume")
		assert.Nil(t, res.ExistingTargetVolume, "the source volume is not offered for deletion")
		assert.Empty(t, res.Warnings)
	})
	t.Run("data major mismatch", func(t *testing.T) {
		env := envWith17Data()
		env.Volumes[oldVol]["PG_VERSION"] = "16\n"
		res := PostgresMigrator{}.Preflight(ctx, env, from17, to18)
		assert.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "PG_VERSION")
	})
	t.Run("unreadable PG_VERSION", func(t *testing.T) {
		env := envWith17Data()
		delete(env.Volumes[oldVol], "PG_VERSION")
		res := PostgresMigrator{}.Preflight(ctx, env, from17, to18)
		assert.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "PG_VERSION")
	})
	t.Run("no space on host", func(t *testing.T) {
		env := envWith17Data()
		env.FreeHost = 1 << 30
		res := PostgresMigrator{}.Preflight(ctx, env, from17, to18)
		assert.False(t, res.OK)
		joined := strings.Join(res.Problems, "\n")
		assert.Contains(t, joined, "host")
		assert.Contains(t, joined, "2.5 GiB", "the message names the requirement in binary units")
	})
	t.Run("no space on volume filesystem", func(t *testing.T) {
		env := envWith17Data()
		env.FreeVolume = 1 << 30
		res := PostgresMigrator{}.Preflight(ctx, env, from17, to18)
		assert.False(t, res.OK)
		assert.Contains(t, strings.Join(res.Problems, "\n"), "volume")
	})
	t.Run("existing target volume is a warning with size and version", func(t *testing.T) {
		env := envWith17Data()
		env.Volumes[newVol] = map[string]string{"18/docker/PG_VERSION": "18\n"}
		env.VolSize[newVol] = 7 << 20
		res := PostgresMigrator{}.Preflight(ctx, env, from17, to18)
		assert.True(t, res.OK, "an existing volume is confirmable, not fatal")
		require.NotNil(t, res.ExistingTargetVolume)
		assert.Equal(t, ExistingVolume{Name: newVol, SizeBytes: 7 << 20, Version: "18"}, *res.ExistingTargetVolume)
		assert.Empty(t, res.Warnings, "the structured field IS the warning; see TestExistingTargetVolumeIsReportedOnlyAsAStructuredField")
	})
	t.Run("existing but empty target volume reports no version", func(t *testing.T) {
		env := envWith17Data()
		env.Volumes[newVol] = map[string]string{}
		res := PostgresMigrator{}.Preflight(ctx, env, from17, to18)
		require.NotNil(t, res.ExistingTargetVolume)
		assert.Equal(t, "empty", res.ExistingTargetVolume.Version)
	})
}

func TestPostgresPlanHappyPath(t *testing.T) {
	env := envWith17Data()
	env.Running = true
	st, err := runPlan(t, env, PlanOptions{})
	require.NoError(t, err)

	joined := strings.Join(env.Log(), "\n")
	idx := func(s string) int {
		i := strings.Index(joined, s)
		require.GreaterOrEqual(t, i, 0, "%s never happened in:\n%s", s, joined)
		return i
	}
	assert.Less(t, idx("stopns"), idx("pull:"+to18))
	// The scratch directory is created (mode 1777) BEFORE the container that
	// writes the dump into it: Docker would otherwise create the bind source
	// itself, root-owned, and pg_dumpall -f would die with Permission denied
	// after the namespace has already been stopped.
	assert.Less(t, idx("mkdir:/host/deps-migration/postgres"), idx("run:"+SrcContainer))
	assert.Less(t, idx("pull:"+to18), idx("run:"+SrcContainer+":"+from17))
	assert.Less(t, idx("run:"+SrcContainer), idx("rm:"+SrcContainer))
	assert.Less(t, idx("rm:"+SrcContainer), idx("createvol:"+newVol))
	assert.Less(t, idx("createvol:"+newVol), idx("run:"+DstContainer+":"+to18))
	assert.Less(t, idx("run:"+DstContainer), idx("rm:"+DstContainer))
	assert.Less(t, idx("rm:"+DstContainer), idx("rmdir:/host/deps-migration/postgres"))
	assert.Less(t, idx("rmdir:/host/deps-migration/postgres"), idx("reload:true"))

	assert.Contains(t, joined, "run:"+SrcContainer+":"+from17+":/host/deps-migration/postgres:/citeck/depsmig",
		"the temp container runs the namespace's real def with the scratch dir bound in")
	assert.Equal(t, 2, env.PortsStripped(), "both temp containers got the generated def, ports and all")

	assert.Equal(t, to18, st.pin)
	require.Len(t, st.commits, 1)
	assert.Equal(t, oldVol, st.commits[0].OldVolume)
	assert.Contains(t, env.Volumes, oldVol, "the old volume is never touched")
	assert.Contains(t, env.Volumes, newVol)
	assert.Empty(t, env.Containers, "no temp container left behind")
	assert.Equal(t, []bool{true}, env.Reloads(), "namespace was running → started again")
	assert.Empty(t, env.Dirs, "the scratch directory is gone")

	// Every step is journaled, in order. "stop-source" appears twice because
	// create-volume persists its write-ahead claim while stop-source is still
	// the last COMPLETED step — the duplicate IS the write-ahead entry.
	assert.Equal(t, []string{
		"stop-namespace", "pull-image", "start-source", "dump", "stop-source",
		"stop-source", "create-volume", "start-target", "restore", "verify", "stop-target",
	}, st.steps()[1:])
}

// PostgresStepIDs is what the CLI's locale keys, the integration test and the
// web dialog's step list are all built from, so it has to BE the plan — not a
// copy that drifts the first time a step is renamed.
func TestPostgresStepIDsAreThePlansOwnSteps(t *testing.T) {
	env := envWith17Data()
	plan, _, err := PostgresMigrator{}.Plan(context.Background(), env, from17, to18, PlanOptions{})
	require.NoError(t, err)
	ids := make([]string, 0, len(plan.Steps))
	for _, st := range plan.Steps {
		ids = append(ids, st.ID)
	}
	assert.Equal(t, PostgresStepIDs(), ids)
}

func TestPostgresPlanStoppedNamespaceIsNotStarted(t *testing.T) {
	env := envWith17Data()
	_, err := runPlan(t, env, PlanOptions{})
	require.NoError(t, err)
	assert.Equal(t, []bool{false}, env.Reloads())
	assert.NotContains(t, strings.Join(env.Log(), "\n"), "stopns")
}

func TestFinalizeRemovesTheScratchDirectoryAndItsEmptyParent(t *testing.T) {
	env := envWith17Data()
	env.Dirs["/host/deps-migration"] = true
	_, err := runPlan(t, env, PlanOptions{})
	require.NoError(t, err)
	assert.Empty(t, env.Dirs)

	// A parent another dependency is still using is kept.
	env = envWith17Data()
	env.Dirs["/host/deps-migration"] = true
	env.Dirs["/host/deps-migration/rabbitmq"] = true
	_, err = runPlan(t, env, PlanOptions{})
	require.NoError(t, err)
	assert.Contains(t, env.Dirs, "/host/deps-migration")
	assert.Contains(t, env.Dirs, "/host/deps-migration/rabbitmq")
}

func TestVolumeIsJournaledBeforeItIsCreated(t *testing.T) {
	// The write-ahead order itself: what the store persisted names the volume
	// before the env is asked to create it.
	env := envWith17Data()
	_, err := runPlan(t, env, PlanOptions{})
	require.NoError(t, err)
	joined := strings.Join(env.Log(), "\n")
	require.Contains(t, joined, "journal:"+newVol)
	assert.Less(t, strings.Index(joined, "journal:"+newVol), strings.Index(joined, "createvol:"+newVol))

	// And what that buys: a crash (here, a failing create) between the two
	// leaves a journal that already claims the volume, so the rollback removes
	// it whether or not it came into existence.
	env = envWith17Data()
	env.FailOn["createvol:"+newVol] = errors.New("disk full")
	st, err := runPlan(t, env, PlanOptions{})
	require.ErrorContains(t, err, "disk full")
	var claimed *deps.MigrationJournal
	for i, rec := range st.journals {
		if rec.CreatedVolume != "" {
			claimed = &st.journals[i]
			break
		}
	}
	require.NotNil(t, claimed, "the volume was never journaled")
	assert.Equal(t, newVol, claimed.CreatedVolume)
	assert.Equal(t, "stop-source", claimed.Step,
		"persisted from inside create-volume, so the last COMPLETED step is still stop-source")
	assert.NotContains(t, env.Volumes, newVol)
	assert.Empty(t, env.Containers)
	require.Len(t, st.failures, 1)
}

// The restore's command line must BE the exported prefix, not merely resemble
// it: the integration test picks the restore's stderr out of every command the
// migration ran by matching RestoreCommandPrefix, and a step that quietly
// stopped using it would leave that lookup matching nothing — which reads as
// "the restore printed nothing", not as a failure.
func TestRestoreRunsTheExportedCommandPrefix(t *testing.T) {
	env := envWith17Data()
	base := env.ExecFn
	var restoreCmd string
	env.ExecFn = func(c, cmd string) (string, string, int, error) {
		if strings.HasPrefix(cmd, "psql") && strings.Contains(cmd, " -f ") {
			restoreCmd = cmd
		}
		return base(c, cmd)
	}
	_, err := runPlan(t, env, PlanOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, restoreCmd, "the plan ran no restore")
	assert.True(t, strings.HasPrefix(restoreCmd, strings.Join(RestoreCommandPrefix(), " ")),
		"restore ran %q, which does not start with the exported prefix %q",
		restoreCmd, strings.Join(RestoreCommandPrefix(), " "))
	assert.True(t, strings.HasSuffix(restoreCmd, " -f /citeck/depsmig/dump.sql"),
		"the prefix carries everything but the dump: %q", restoreCmd)
}

func TestRestoreErrorRollsBackEverything(t *testing.T) {
	env := envWith17Data()
	base := env.ExecFn
	env.ExecFn = func(c, cmd string) (string, string, int, error) {
		if strings.HasPrefix(cmd, "psql") && strings.Contains(cmd, " -f ") {
			return "", `psql:/citeck/depsmig/dump.sql:9: ERROR:  syntax error at or near "BOGUS"`, 0, nil
		}
		return base(c, cmd)
	}
	st, err := runPlan(t, env, PlanOptions{})
	require.ErrorContains(t, err, "syntax error")
	assert.Empty(t, st.pin)
	assert.NotContains(t, env.Volumes, newVol, "the new volume is removed")
	assert.Contains(t, env.Volumes, oldVol)
	assert.Empty(t, env.Containers)
	assert.NotContains(t, env.Dirs, "/host/deps-migration/postgres", "the dump dir is removed")
	require.Len(t, st.failures, 1)
}

// psql can also fail without printing an ERROR line at all (it could not
// connect, the file was unreadable). The exit code alone must fail the step,
// or an empty cluster would pass verify only by accident.
func TestRestoreFailsOnANonZeroExitWithoutAnErrorLine(t *testing.T) {
	env := envWith17Data()
	base := env.ExecFn
	env.ExecFn = func(c, cmd string) (string, string, int, error) {
		if strings.HasPrefix(cmd, "psql") && strings.Contains(cmd, " -f ") {
			return "", "psql: could not open file", 1, nil
		}
		return base(c, cmd)
	}
	_, err := runPlan(t, env, PlanOptions{})
	require.ErrorContains(t, err, "exit 1")
	require.ErrorContains(t, err, "could not open file")
}

func TestVerifyMismatchFails(t *testing.T) {
	env := envWith17Data()
	short := migratetest.HealthyPostgres()
	short.Tables = map[string]int{"citeck_emodel": 41, "citeck_keycloak": 91}
	env.ExecFn = migratetest.PostgresExec(map[string]migratetest.PostgresInventory{
		"": migratetest.HealthyPostgres(), DstContainer: short,
	})
	st, err := runPlan(t, env, PlanOptions{})
	require.ErrorContains(t, err, "table count")
	assert.Empty(t, st.pin, "a verify failure never moves the pin")
	assert.NotContains(t, env.Volumes, newVol)
}

func TestVerifyFailsWhenARoleDidNotSurvive(t *testing.T) {
	env := envWith17Data()
	short := migratetest.HealthyPostgres()
	short.Roles = []string{"postgres"}
	env.ExecFn = migratetest.PostgresExec(map[string]migratetest.PostgresInventory{
		"": migratetest.HealthyPostgres(), DstContainer: short,
	})
	_, err := runPlan(t, env, PlanOptions{})
	require.ErrorContains(t, err, "roles differ")
}

func TestExistingTargetVolumeRequiresConfirmation(t *testing.T) {
	env := envWith17Data()
	env.Volumes[newVol] = map[string]string{"18/docker/PG_VERSION": "18\n"}
	_, _, err := PostgresMigrator{}.Plan(context.Background(), env, from17, to18, PlanOptions{})
	require.ErrorContains(t, err, "already exists")

	st, err := runPlan(t, env, PlanOptions{ReplaceExistingVolume: true})
	require.NoError(t, err)
	assert.Equal(t, to18, st.pin)
	joined := strings.Join(env.Log(), "\n")
	assert.Less(t, strings.Index(joined, "rmvol:"+newVol), strings.Index(joined, "createvol:"+newVol))
}

// The preflight refusal is not enough: the volume can appear between the
// preflight and the step (another launcher, a hand-run docker command). The
// step re-checks, refuses, and — because it journaled nothing — the rollback
// leaves that volume's data alone.
func TestCreateVolumeRefusesAVolumeThatAppearedAfterThePreflight(t *testing.T) {
	env := envWith17Data()
	plan, j, err := PostgresMigrator{}.Plan(context.Background(), env, from17, to18, PlanOptions{})
	require.NoError(t, err)
	env.Volumes[newVol] = map[string]string{"18/docker/PG_VERSION": "18\n"}

	st := &fakeStore{}
	err = Run(context.Background(), st, j, plan, nil)
	require.ErrorContains(t, err, "already exists")
	assert.Contains(t, env.Volumes, newVol, "somebody else's data is not deleted by the rollback")
	assert.NotContains(t, strings.Join(env.Log(), "\n"), "rmvol:"+newVol)
	assert.Empty(t, st.pin)
}

func TestRollbackIsIdempotentAndRestartsIfItWasRunning(t *testing.T) {
	ctx := context.Background()
	env := envWith17Data()
	j := &deps.MigrationJournal{ID: deps.Postgres, From: from17, To: to18,
		Step: "restore", CreatedVolume: newVol, DumpDir: env.DumpDir(deps.Postgres), WasRunning: true}
	env.Volumes[newVol] = map[string]string{}
	env.Dirs[j.DumpDir] = true
	env.Containers[DstContainer] = env.Defs[to18]

	require.NoError(t, RollbackPostgres(ctx, env, j))
	require.NoError(t, RollbackPostgres(ctx, env, j), "a second rollback finds nothing and succeeds")
	assert.NotContains(t, env.Volumes, newVol)
	assert.Empty(t, env.Containers)
	assert.NotContains(t, env.Dirs, j.DumpDir)
	assert.Equal(t, []bool{true, true}, env.Reloads())
	assert.Contains(t, env.Volumes, oldVol, "the old data is never touched")
}

func TestRollbackDoesNotRemoveAVolumeItDidNotJournal(t *testing.T) {
	env := envWith17Data()
	env.Volumes[newVol] = map[string]string{"18/docker/PG_VERSION": "18\n"}
	j := &deps.MigrationJournal{ID: deps.Postgres, From: from17, To: to18, Step: "pull-image"}
	require.NoError(t, RollbackPostgres(context.Background(), env, j))
	assert.Contains(t, env.Volumes, newVol, "a volume the journal never claimed is somebody else's data")
}

// A rollback that cannot finish its job reports it — the engine keeps the
// journal open on that verdict, so the failure must not be swallowed.
func TestRollbackReportsEveryFailureItHit(t *testing.T) {
	env := envWith17Data()
	env.FailOn["rmvol:"+newVol] = errors.New("volume in use")
	env.FailOn["rmdir:"+env.DumpDir(deps.Postgres)] = errors.New("scratch busy")
	j := &deps.MigrationJournal{ID: deps.Postgres, From: from17, To: to18,
		CreatedVolume: newVol, DumpDir: env.DumpDir(deps.Postgres), WasRunning: true}
	err := RollbackPostgres(context.Background(), env, j)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "volume in use")
	assert.Contains(t, err.Error(), "scratch busy")
}

// A restart that fails is reported like every other half of the rollback —
// the cleanup itself succeeded, so attempting it was right.
func TestRollbackReportsAFailedRestart(t *testing.T) {
	env := envWith17Data()
	env.FailOn["reload:"] = errors.New("docker is down")
	j := &deps.MigrationJournal{ID: deps.Postgres, From: from17, To: to18,
		CreatedVolume: newVol, DumpDir: env.DumpDir(deps.Postgres), WasRunning: true}
	require.ErrorContains(t, RollbackPostgres(context.Background(), env, j), "docker is down")
}

// depsmig-src mounts the namespace's OWN data volume read-write. A rollback
// that could not remove it must NOT hand the namespace back running: the
// postmaster.pid interlock does not hold across PID/IPC namespaces, so the
// namespace's postgres would start a second server on the user's only copy of
// the data. The journal stays open and the next launcher start retries.
func TestRollbackLeavesTheNamespaceStoppedWhenATempContainerSurvived(t *testing.T) {
	env := envWith17Data()
	env.Containers[SrcContainer] = env.Defs[from17]
	env.FailOn["rm:"+SrcContainer] = errors.New("container is locked")
	j := &deps.MigrationJournal{ID: deps.Postgres, From: from17, To: to18,
		CreatedVolume: newVol, DumpDir: env.DumpDir(deps.Postgres), WasRunning: true}
	err := RollbackPostgres(context.Background(), env, j)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container is locked")
	assert.Contains(t, err.Error(), "namespace left stopped")
	assert.Empty(t, env.Reloads(), "no second postmaster on the old volume")
}

func TestPlanRefusesWhenPreflightFails(t *testing.T) {
	env := envWith17Data()
	env.FreeHost = 1 << 20
	_, _, err := PostgresMigrator{}.Plan(context.Background(), env, from17, to18, PlanOptions{})
	require.ErrorContains(t, err, "preflight failed")
}

// A step whose Docker call fails outright (not a non-zero exit) still rolls
// back: the temp container and the scratch dir must not outlive the attempt.
func TestAStartFailureRollsBackTheScratchDirectory(t *testing.T) {
	env := envWith17Data()
	env.FailOn["run:"+SrcContainer] = errors.New("no such image")
	_, err := runPlan(t, env, PlanOptions{})
	require.ErrorContains(t, err, "no such image")
	assert.Empty(t, env.Dirs)
	assert.Empty(t, env.Containers)
	assert.NotContains(t, env.Volumes, newVol)
}

func noProgress(float64, string) {}

// The official image runs a TEMPORARY server on the Unix socket during
// first-time init, so pg_isready alone goes true and then false again. The
// wait therefore also has to get an answer out of the real TCP server.
func TestReadinessWaitsForTheTCPServerNotJustPgIsready(t *testing.T) {
	ctx := context.Background()
	env := migratetest.New()
	env.Containers[SrcContainer] = appdef.ApplicationDef{Name: "postgres"}
	var tcpUp atomic.Bool
	env.ExecFn = func(_, cmd string) (string, string, int, error) {
		switch {
		case strings.HasPrefix(cmd, "pg_isready"):
			return "accepting connections", "", 0, nil
		case strings.Contains(cmd, "select 1"):
			if !tcpUp.Load() {
				return "", "psql: could not connect to server", 2, nil
			}
			return "1", "", 0, nil
		}
		return "", "", 0, nil
	}
	err := waitReady(ctx, env, SrcContainer, 20*time.Millisecond, time.Millisecond, noProgress)
	require.ErrorContains(t, err, "did not become ready")

	tcpUp.Store(true)
	require.NoError(t, waitReady(ctx, env, SrcContainer, time.Second, time.Millisecond, noProgress))
}

func TestReadinessFailsAtOnceWhenTheContainerIsGone(t *testing.T) {
	env := migratetest.New()
	err := waitReady(context.Background(), env, SrcContainer, time.Minute, time.Minute, noProgress)
	require.ErrorContains(t, err, "is not running")
}

func TestReadinessStopsWhenTheContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	env := migratetest.New()
	env.Containers[SrcContainer] = appdef.ApplicationDef{Name: "postgres"}
	env.ExecFn = func(string, string) (string, string, int, error) { return "", "down", 2, nil }
	cancel()
	require.ErrorIs(t, waitReady(ctx, env, SrcContainer, time.Hour, time.Millisecond, noProgress), context.Canceled)
}

func TestDumpProgressFollowsTheGrowingFile(t *testing.T) {
	env := migratetest.New()
	path := "/host/deps-migration/postgres/dump.sql"
	env.Files[path] = 512 << 20
	reports := make(chan string, 32)
	report := func(pct float64, msg string) {
		select {
		case reports <- fmt.Sprintf("%.0f|%s", pct, msg):
		default:
		}
	}

	stop := watchFileGrowth(context.Background(), env, path, 1<<30, time.Millisecond, report)
	assert.Equal(t, "50|dumped 512.0 MiB", <-reports)
	stop()

	// A dump bigger than the data it came from must not report 137%.
	env.Files[path] = 4 << 30
	stop = watchFileGrowth(context.Background(), env, path, 1<<30, time.Millisecond, report)
	assert.Equal(t, "99|dumped 4.0 GiB", <-reports)
	stop()

	// stop() waits for the reporter to return, so a step that finished can
	// never have its progress callback invoked afterwards.
	for len(reports) > 0 {
		<-reports
	}
	assert.Empty(t, reports)
}
