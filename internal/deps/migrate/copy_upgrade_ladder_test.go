package migrate

import (
	"context"
	"fmt"
	"testing"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// qdrantLadderPath is a route whose every adjacent hop stays inside Qdrant's
// own one-minor-at-a-time vendor rule (deps.qdrantDescriptor.UpgradeSupport):
// 14→15, 15→16 and 16→17 each span exactly one minor. A ladder that skipped a
// minor anywhere — e.g. straight from 16 to 19 — would be refused by
// QdrantMigrator.SupportsPair on that one hop, which is a real rule, not an
// artifact of this test's fixtures.
var qdrantLadderPath = Path{
	qdrantFrom, qdrantTo, "qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.17.1",
}

func stepIDs(p *Plan) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.ID)
	}
	return out
}

// okPreflight is a preflight that passed, which is all BuildCopyUpgrade reads
// out of it besides the leftover-volume warning.
func okPreflight(from, to string) PreflightResult {
	res := NewPreflightResult(from, to)
	res.OK = true
	return res
}

func countID(ids []string, want string) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}

// A single-hop plan must be what it has always been, step for step: this is
// the release-safety property of the whole feature.
func TestASingleHopPlanIsUnchanged(t *testing.T) {
	env := qdrantEnv(t, &execScript{})
	plan, _, err := BuildCopyUpgrade(env, qdrantCopySpec(deps.Qdrant),
		Path{qdrantFrom, qdrantTo}, PlanOptions{},
		okPreflight(qdrantFrom, qdrantTo))
	require.NoError(t, err)
	assert.Equal(t, CopyStepIDs(), stepIDs(plan))
}

// Three rungs: one copy, one stop-namespace, one create-volume — and the
// start/post/pre trio once per rung, with verify and the final stop-new only
// at the top.
func TestAThreeRungPlanClimbsOneCopy(t *testing.T) {
	env := qdrantEnv(t, &execScript{})
	path := qdrantLadderPath
	plan, j, err := BuildCopyUpgrade(env, qdrantCopySpec(deps.Qdrant), path, PlanOptions{},
		okPreflight(path.From(), path.To()))
	require.NoError(t, err)

	ids := stepIDs(plan)
	assert.Equal(t, 1, countID(ids, "copy-volume"), "one copy, whatever the ladder's length")
	assert.Equal(t, 1, countID(ids, "create-volume"), "one generation, whatever the ladder's length")
	assert.Equal(t, 1, countID(ids, "stop-namespace"))
	assert.Equal(t, 3, countID(ids, "start-new"), "one start per rung")
	assert.Equal(t, 3, countID(ids, "post-upgrade"))
	assert.Equal(t, 3, countID(ids, "pre-upgrade"), "before every rung, whatever the ladder's length")
	assert.Equal(t, 1, countID(ids, "verify"), "the inventory is compared once, at the top")
	assert.Equal(t, path.To(), j.To)
	assert.Equal(t, path.From(), j.From)
	assert.Equal(t, 2, j.ToVolumeGen, "the generation grows by exactly one")
}

// ladderInventorySpec is a minimal CopySpec used only to observe HOW the plan
// drives a dependency's hooks across a ladder — never a real dependency's wire
// format. WaitReady checks only that the container is running (no exec
// probes: this is not a test of any real broker's boot sequence), and
// Inventory answers from a fixed per-container map. That is enough to catch
// two mutations a real dependency's spec would hide behind its own retries
// and JSON parsing: pulling only the top image, and re-capturing the "before"
// inventory on every rung instead of just the bottom one.
func ladderInventorySpec(id deps.ID, inv map[string]countInventory) CopySpec {
	return CopySpec{
		ID: id,
		WaitReady: func(ctx context.Context, env Env, container string, _ StepProgress) error {
			running, err := env.ContainerRunning(ctx, container)
			if err != nil {
				return fmt.Errorf("check %s: %w", container, err)
			}
			if !running {
				return fmt.Errorf("%s is not running", container)
			}
			return nil
		},
		Inventory: func(_ context.Context, _ Env, container string) (Inventory, error) {
			return inv[container], nil
		},
	}
}

// pull-image pulls EVERY rung, not just the last: an unreachable registry
// then costs a stopped namespace and nothing else, whatever rung it is on —
// and on a ladder that has to hold for the rung four steps up, not just the
// first.
func TestAThreeRungPlanPullsEveryRung(t *testing.T) {
	env := qdrantEnv(t, &execScript{})
	path := qdrantLadderPath
	plan, j, err := BuildCopyUpgrade(env, ladderInventorySpec(deps.Qdrant, nil), path, PlanOptions{},
		okPreflight(path.From(), path.To()))
	require.NoError(t, err)
	require.NoError(t, Run(context.Background(), j2store(), j, plan, nil))
	assert.ElementsMatch(t, path.Rungs(), env.Pulled(),
		"every rung is pulled before anything irreversible happens")
}

// pre-upgrade prepares the NEXT node, so on a ladder it runs before every rung
// — on the old image first, then on each intermediate — and never after the
// last one, where there is no next node to prepare.
func TestPreUpgradeRunsBeforeEveryRungButNotAfterTheLast(t *testing.T) {
	env := rabbitEnv(t)
	path := Path{rabbitFrom, "rabbitmq:4.2.9-management", "rabbitmq:4.3.5-management"}
	toV := deps.Version{Major: 4, Minor: 3, Patch: 5}
	plan, _, err := BuildCopyUpgrade(env, rabbitCopySpec(toV), path, PlanOptions{},
		okPreflight(path.From(), path.To()))
	require.NoError(t, err)

	ids := stepIDs(plan)
	assert.Equal(t, 2, countID(ids, "pre-upgrade"),
		"before 4.2 and before 4.3, never after the top rung")
	// And the order: the last three steps are the top rung's.
	assert.Equal(t, []string{"post-upgrade", "verify", "stop-new"}, ids[len(ids)-3:])
}

// The "before" inventory is captured ONCE, from the bottom of the ladder, and
// nothing overwrites it as the copy climbs: a re-capture at an intermediate
// rung would read the SAME container the top rung's own "after" reading comes
// from, so a real difference between the bottom and the top would silently
// stop being reported. Rigged so a genuine loss (kept 7 → 6) is visible only
// if the "before" picture really came from the bottom.
func TestBeforeInventoryIsCapturedOnceAtTheBottom(t *testing.T) {
	env := rabbitEnv(t)
	path := Path{rabbitFrom, "rabbitmq:4.2.9-management", "rabbitmq:4.3.5-management"}
	inv := map[string]countInventory{
		SrcContainer: {kept: 7},
		DstContainer: {kept: 6},
	}
	plan, j, err := BuildCopyUpgrade(env, ladderInventorySpec(deps.RabbitMQ, inv), path, PlanOptions{},
		okPreflight(path.From(), path.To()))
	require.NoError(t, err)

	err = Run(context.Background(), j2store(), j, plan, nil)
	require.ErrorContains(t, err, "kept 7 → 6",
		"the \"before\" inventory must be the BOTTOM's (SrcContainer): a re-capture at the "+
			"intermediate rung would read DstContainer, the same key the top's \"after\" reads, "+
			"and this loss would go unreported")
}

// A ladder is walked on ONE copy, so the space a copy upgrade demands does not
// depend on how many rungs there are. This is the half of the disk-space rule
// that is a NON-change, and it is worth a test precisely because the other
// half (postgres, one extra dump) is a change: without this, "make the ladder
// ask for more room" would be applied to both plans.
func TestALadderDoesNotRaiseTheCopyPlansSpaceRequirement(t *testing.T) {
	env := qdrantEnv(t, &execScript{})
	one := QdrantMigrator{ID: deps.Qdrant}.Preflight(context.Background(), env, Path{qdrantFrom, qdrantTo})
	require.True(t, one.OK, one.Problems)
	many := QdrantMigrator{ID: deps.Qdrant}.Preflight(context.Background(), env, qdrantLadderPath)
	require.True(t, many.OK, many.Problems)
	assert.Equal(t, one.RequiredVolumeBytes, many.RequiredVolumeBytes)
	assert.Equal(t, one.RequiredHostBytes, many.RequiredHostBytes)
	assert.Equal(t, one.RequiredTotalBytes, many.RequiredTotalBytes)
}
