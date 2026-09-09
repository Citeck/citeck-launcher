package migrate

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
)

const (
	zkFrom = "zookeeper:3.8.6"
	zkTo   = "zookeeper:3.9.5"
)

func zookeeperDescriptor(t *testing.T) deps.Descriptor {
	t.Helper()
	d, ok := deps.Lookup(deps.Zookeeper)
	require.True(t, ok, "zookeeper is not registered")
	return d
}

// zkEnv is a stopped namespace whose ZooKeeper volume holds ONLY a
// transaction log — the state a clean SIGTERM really leaves behind (measured:
// ZooKeeper snapshots after LOADING, not on shutdown), and the state the
// preflight must accept.
func zkEnv(t *testing.T, s *execScript) *guardEnv {
	t.Helper()
	src := deps.VolumeName(zookeeperDescriptor(t), 1)
	f := migratetest.New()
	f.Volumes[src] = map[string]string{"datalog/version-2/log.1": "txns"}
	f.VolSize[src] = 64 << 20
	f.States[deps.Zookeeper] = deps.DependencyState{Image: zkFrom}
	f.ExecFn = s.exec
	return newGuardEnv(t, f, src)
}

func runZkPlan(t *testing.T, env *guardEnv, from, to string) error {
	t.Helper()
	plan, j, err := (ZookeeperMigrator{}).Plan(context.Background(), env, from, to, PlanOptions{})
	require.NoError(t, err)
	return Run(context.Background(), j2store(), j, plan, nil)
}

// The one rule ZooKeeper has: data written before 3.5 may predate
// zookeeper.snapshot.trust.empty, so a newer server can take an empty snapshot
// for valid state — and what is in there is the per-webapp patch-result
// markers and eproc's permanent mongo-disabled marker.
func TestZkSupportsPairRefusesDataOlderThan35(t *testing.T) {
	v := func(major, minor, patch int) deps.Version {
		return deps.Version{Major: major, Minor: minor, Patch: patch}
	}
	t.Run("3.8 → 3.9", func(t *testing.T) {
		ok, problem := (ZookeeperMigrator{}).SupportsPair(v(3, 8, 6), v(3, 9, 5))
		assert.True(t, ok)
		assert.Empty(t, problem)
	})
	t.Run("3.5 is the floor and is allowed", func(t *testing.T) {
		ok, _ := (ZookeeperMigrator{}).SupportsPair(v(3, 5, 0), v(3, 9, 5))
		assert.True(t, ok)
	})
	t.Run("3.4 is refused and says why", func(t *testing.T) {
		ok, problem := (ZookeeperMigrator{}).SupportsPair(v(3, 4, 14), v(3, 9, 5))
		require.False(t, ok)
		assert.Contains(t, problem, "snapshot.trust.empty")
		assert.NotContains(t, problem, "update the launcher", "a newer launcher would refuse it too")
	})
	// The shared version checks word a downgrade, so this refuses it with no
	// reason of its own rather than overwriting the accurate message.
	t.Run("a downgrade is left to the shared checks", func(t *testing.T) {
		ok, problem := (ZookeeperMigrator{}).SupportsPair(v(3, 9, 5), v(3, 8, 6))
		require.False(t, ok)
		assert.Empty(t, problem)
	})
}

// F5, and the reason this test exists at all: a clean SIGTERM writes NO
// snapshot — ZooKeeper snapshots right after LOADING, so the durable state
// after a graceful stop is the txnlog alone. A preflight phrased as "confirm a
// snapshot exists" would refuse every ordinary stopped namespace while the
// data is perfectly intact.
func TestZkPreflightDoesNotRequireASnapshot(t *testing.T) {
	env := zkEnv(t, &execScript{})
	res := (ZookeeperMigrator{}).Preflight(context.Background(), env, zkFrom, zkTo)
	require.True(t, res.OK, res.Problems)
	assert.Empty(t, res.Problems)
	joined := strings.Join(append(res.Problems, res.Warnings...), "\n")
	assert.NotContains(t, joined, "snapshot",
		"nothing in the preflight may depend on a snapshot file existing")
	assert.True(t, res.Measured())
	assert.Zero(t, res.RequiredHostBytes, "a copy upgrade writes no host file")
}

// The source volume still has to BE there: the pin says the namespace has
// ZooKeeper data, and a plan that ran without it would copy nothing, boot an
// empty node and verify it against an equally empty picture.
func TestZkPreflightRefusesAMissingSourceVolume(t *testing.T) {
	env := zkEnv(t, &execScript{})
	delete(env.Volumes, deps.VolumeName(zookeeperDescriptor(t), 1))
	res := (ZookeeperMigrator{}).Preflight(context.Background(), env, zkFrom, zkTo)
	require.False(t, res.OK)
	assert.Contains(t, strings.Join(res.Problems, "\n"), "zookeeper2")
}

// A temp container runs the container and nothing around it — no init
// container — and ZooKeeper's generated def has one whose only job is to make
// ZOO_DATA_DIR and ZOO_DATA_LOG_DIR. They are created on the COPY, before
// anything starts on it, and their names must match the generator's env vars
// (generator_infra.go: /citeck/zookeeper/{data,datalog} under the volume root).
func TestZkPlanMakesItsDataDirectoriesOnTheCopy(t *testing.T) {
	env := zkEnv(t, realZkScript())
	require.NoError(t, runZkPlan(t, env, zkFrom, zkTo))
	assert.Equal(t, []string{"data", "datalog"},
		env.VolumeDirs(deps.VolumeName(zookeeperDescriptor(t), 2)))
}

// ZooKeeper has no pre/post-upgrade work: 3.8 and 3.9 share one on-disk
// format, so the upgrade IS starting the new image on the data. Inventing a
// hook here would be a step that claims to do something and does not.
func TestZkHasNoUpgradeHooks(t *testing.T) {
	spec := zkCopySpec()
	assert.Nil(t, spec.PreUpgrade)
	assert.Nil(t, spec.PostUpgrade)
	// And no node identity to pin: unlike RabbitMQ, ZooKeeper's data path is
	// fixed by ZOO_DATA_DIR and carries no hostname, so a temp container's
	// override name cannot make it read the wrong directory.
	assert.Empty(t, spec.TempEnv)
	assert.Empty(t, spec.HostAlias)
}

// Readiness is the AdminServer, which is what the generated app's own probes
// use. The client port opens seconds before the embedded Jetty does, so a
// check on 2181 would report ready while /commands/monitor still refuses.
func TestZkReadinessWaitsForTheAdminServer(t *testing.T) {
	s := realZkScript()
	env := zkEnv(t, s)
	require.NoError(t, runZkPlan(t, env, zkFrom, zkTo))
	first := s.calledIn(SrcContainer)
	require.NotEmpty(t, first)
	assert.Contains(t, first[0], "8080/commands/ruok")
}

// An admin server that never answers fails the step rather than letting the
// plan run its commands against a server that is not there.
func TestZkReadinessGivesUpAndFailsTheStep(t *testing.T) {
	s := &execScript{fail: map[string]string{SrcContainer + "|ruok": "curl: (7) Failed to connect"}}
	env := zkEnv(t, s)
	_, err := env.RunAppDef(context.Background(),
		mustZkDef(t, env), deps.TempContainerOpts{Name: SrcContainer})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the wait honors the context instead of polling for minutes
	err = zkCopySpec().WaitReady(ctx, env, SrcContainer, func(float64, string) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), SrcContainer)
}

func mustZkDef(t *testing.T, env *guardEnv) appdef.ApplicationDef {
	t.Helper()
	d, err := env.GenerateDefFor(deps.Zookeeper, deps.DependencyState{Image: zkTo, VolumeGen: 2})
	require.NoError(t, err)
	return d
}
