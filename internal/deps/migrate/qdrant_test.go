package migrate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"

	"github.com/citeck/citeck-launcher/internal/msg"
)

// The pair measured end to end on real containers (2026-09-15).
const (
	qdrantFrom = "qdrant/qdrant:v1.14.1"
	qdrantTo   = "qdrant/qdrant:v1.15.5"
)

func qdrantDescriptorOf(t *testing.T) deps.Descriptor {
	t.Helper()
	d, ok := deps.Lookup(deps.Qdrant)
	require.True(t, ok, "qdrant is not registered")
	return d
}

// qdrantEnv is a stopped namespace whose Qdrant volume holds what a real one
// holds: a collections tree and the raft state file.
func qdrantEnv(t *testing.T, s *execScript) *guardEnv {
	t.Helper()
	src := deps.VolumeName(qdrantDescriptorOf(t), 1)
	f := migratetest.New()
	f.Volumes[src] = map[string]string{
		"raft_state.json":                      `{"state":{}}`,
		"collections/docs/0/segments/x/db/LOG": "segment",
	}
	f.VolSize[src] = 739 << 20
	f.States[deps.Qdrant] = deps.DependencyState{Image: qdrantFrom}
	f.ExecFn = s.exec
	return newGuardEnv(t, f, src)
}

func runQdrantPlan(t *testing.T, env *guardEnv, from, to string) error {
	t.Helper()
	plan, j, err := (QdrantMigrator{ID: deps.Qdrant}).Plan(context.Background(), env, Path{from, to}, PlanOptions{})
	require.NoError(t, err)
	return Run(context.Background(), j2store(), j, plan, nil)
}

// Qdrant's one vendor rule: storage compatibility spans exactly ONE minor. A
// skipped minor is refused HERE, before anything is copied, and the refusal
// must not say "update the launcher" — a newer launcher would refuse it too,
// because the limit is the format's and not this binary's.
func TestQdrantSupportsPairFollowsTheOneMinorRule(t *testing.T) {
	v := func(major, minor, patch int) deps.Version {
		return deps.Version{Major: major, Minor: minor, Patch: patch}
	}
	t.Run("1.14 → 1.15", func(t *testing.T) {
		ok, problem := (QdrantMigrator{ID: deps.Qdrant}).SupportsPair(v(1, 14, 1), v(1, 15, 5))
		assert.True(t, ok)
		assert.True(t, problem.Empty())
	})
	t.Run("a patch move inside one minor", func(t *testing.T) {
		ok, _ := (QdrantMigrator{ID: deps.Qdrant}).SupportsPair(v(1, 14, 1), v(1, 14, 3))
		assert.True(t, ok)
	})
	t.Run("1.14 → 1.16 is refused and names the hop", func(t *testing.T) {
		ok, problem := (QdrantMigrator{ID: deps.Qdrant}).SupportsPair(v(1, 14, 1), v(1, 16, 0))
		require.False(t, ok)
		assert.Contains(t, oneEN(problem), "1.15", "the operator is told the one move they can make now")
		assert.NotContains(t, oneEN(problem), "update the launcher",
			"a newer launcher would refuse it too — the limit is the storage format's")
	})
	// The shared version checks word a downgrade, so this refuses it with no
	// reason of its own rather than overwriting the accurate message.
	t.Run("a downgrade is left to the shared checks", func(t *testing.T) {
		ok, problem := (QdrantMigrator{ID: deps.Qdrant}).SupportsPair(v(1, 15, 5), v(1, 14, 1))
		require.False(t, ok)
		assert.True(t, problem.Empty())
	})
}

func TestQdrantPreflightPassesOnAnOrdinaryStoppedNamespace(t *testing.T) {
	env := qdrantEnv(t, &execScript{})
	res := (QdrantMigrator{ID: deps.Qdrant}).Preflight(context.Background(), env, Path{qdrantFrom, qdrantTo})
	require.True(t, res.OK, res.Problems)
	assert.Empty(t, res.Problems)
	assert.True(t, res.Measured())
	assert.Zero(t, res.RequiredHostBytes, "a copy upgrade writes no host file")
}

// The source volume still has to BE there: the pin says the namespace has
// Qdrant data, and a plan that ran without it would copy nothing, boot an
// empty server and verify it against an equally empty picture.
func TestQdrantPreflightRefusesAMissingSourceVolume(t *testing.T) {
	env := qdrantEnv(t, &execScript{})
	delete(env.Volumes, deps.VolumeName(qdrantDescriptorOf(t), 1))
	res := (QdrantMigrator{ID: deps.Qdrant}).Preflight(context.Background(), env, Path{qdrantFrom, qdrantTo})
	require.False(t, res.OK)
	assert.Contains(t, joinEN(res.Problems), "qdrant2")
}

// Qdrant has no post-upgrade work: starting the new image on the copy IS the
// upgrade, because Qdrant migrates its own storage on boot. Its one
// pre-upgrade hook is a WAIT — a node is replaced only once its optimizers
// have settled (qdrant_optimizers_test.go).
func TestQdrantWaitsBeforeEachRungAndHasNoPostUpgradeWork(t *testing.T) {
	spec := qdrantCopySpec(deps.Qdrant)
	require.NotNil(t, spec.PreUpgrade, "a node is replaced only once its optimizers have settled")
	assert.Nil(t, spec.PostUpgrade)
	// And no node identity to pin. Unlike RabbitMQ — whose data path CONTAINS
	// its node name, so a temp container under an override name boots a fresh
	// empty node — a single-node Qdrant records no hostname anywhere in its
	// data (verified by reading raft_state.json out of a real volume:
	// peer_address_by_id is empty).
	assert.Empty(t, spec.TempEnv)
	assert.Empty(t, spec.HostAlias)
	// And nothing to pre-create: Qdrant makes collections/, aliases/ and
	// raft_state.json itself, and the generated def has no init container.
	assert.Empty(t, spec.EnsureDirs)
}

// Readiness is /readyz. The plan's first act inside a temp container must be
// the wait, not an inventory request against a server whose shards are still
// loading — which would read as a server with no points in it.
func TestQdrantReadinessWaitsForReadyz(t *testing.T) {
	s := realQdrantScript()
	env := qdrantEnv(t, s)
	require.NoError(t, runQdrantPlan(t, env, qdrantFrom, qdrantTo))
	first := s.calledIn(SrcContainer)
	require.NotEmpty(t, first)
	assert.Contains(t, first[0], "/readyz")
	assert.NotContains(t, first[0], "/healthz",
		"healthz answers before the shards are loaded")
}

// A server that never answers fails the step rather than letting the plan run
// its requests against something that is not there.
func TestQdrantReadinessGivesUpAndFailsTheStep(t *testing.T) {
	s := &execScript{fail: map[string]string{SrcContainer + "|/readyz": "connect: Connection refused"}}
	env := qdrantEnv(t, s)
	_, err := env.RunAppDef(context.Background(),
		mustQdrantDef(t, env), deps.TempContainerOpts{Name: SrcContainer})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the wait honors the context instead of polling for minutes
	err = qdrantCopySpec(deps.Qdrant).WaitReady(ctx, env, SrcContainer, func(float64, msg.Message) {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), SrcContainer)
}

// The whole plan, end to end on the pair that was measured: the copy lands in
// the NEXT generation and the namespace's own volume is never written to.
func TestQdrantPlanUpgradesTheCopyAndLeavesTheSourceAlone(t *testing.T) {
	env := qdrantEnv(t, realQdrantScript())
	require.NoError(t, runQdrantPlan(t, env, qdrantFrom, qdrantTo))
	assert.Contains(t, env.Volumes, deps.VolumeName(qdrantDescriptorOf(t), 2))
	assert.Contains(t, env.Volumes, deps.VolumeName(qdrantDescriptorOf(t), 1),
		"the source volume is only ever read")
}

func mustQdrantDef(t *testing.T, env *guardEnv) appdef.ApplicationDef {
	t.Helper()
	d, err := env.GenerateDefFor(deps.Qdrant, deps.DependencyState{Image: qdrantTo, VolumeGen: 2})
	require.NoError(t, err)
	return d
}
