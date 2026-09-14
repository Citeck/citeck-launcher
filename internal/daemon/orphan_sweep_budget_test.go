package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/docker"
)

// fakeSweeper records the deadline each phase was handed and can hang the
// deciding phase the way an unreachable Docker does.
type fakeSweeper struct {
	decideDeadline time.Time
	purgeDeadline  time.Time
	decideHangs    bool
	targets        []docker.OrphanTarget
	purgeCalled    bool
}

func (f *fakeSweeper) FindOrphans(ctx context.Context, _ map[string]bool) []docker.OrphanTarget {
	f.decideDeadline, _ = ctx.Deadline()
	if f.decideHangs {
		<-ctx.Done()
		return nil // a listing that failed decides nothing — fail-safe
	}
	return f.targets
}

func (f *fakeSweeper) RemoveOrphanContainers(ctx context.Context, targets []docker.OrphanTarget) []string {
	f.purgeCalled = true
	f.purgeDeadline, _ = ctx.Deadline()
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.NS)
	}
	return out
}

// The two phases are not the same question. DECIDING is three cheap
// enumerations whose failure is already fail-safe (nothing is purged), so it
// must give up quickly — on the Windows host that motivated this, those three
// calls hung on a dead Docker Desktop pipe and ate the sweep's whole 90 s
// budget, all of it before the namespace could start. REMOVING containers and
// multi-gigabyte named volumes legitimately takes longer.
func TestTheDecidingPhaseGetsAMuchShorterBudgetThanTheRemovals(t *testing.T) {
	f := &fakeSweeper{targets: []docker.OrphanTarget{{NS: "gone", WS: "ws1"}}}

	started := time.Now()
	purged := runOrphanSweep(context.Background(), f, map[string]bool{})

	assert.Equal(t, []string{"gone"}, purged)
	require.False(t, f.decideDeadline.IsZero(), "the deciding phase must be bounded")
	require.True(t, f.purgeCalled)
	require.False(t, f.purgeDeadline.IsZero(), "the removals must be bounded too")

	decide := f.decideDeadline.Sub(started)
	purge := f.purgeDeadline.Sub(started)
	assert.InDelta(t, orphanSweepDecideTimeout, decide, float64(time.Second))
	assert.InDelta(t, orphanSweepPurgeTimeout, purge, float64(time.Second))
	assert.Less(t, decide, purge, "one shared deadline is what made boot wait on a dead Docker")
}

// With Docker unreachable the sweep must cost the deciding budget and stop
// there: there is nothing to remove, and the namespace (and the UI behind it)
// is waiting.
func TestAnUnreachableDockerCostsOnlyTheDecidingBudget(t *testing.T) {
	restore := orphanSweepDecideTimeout
	orphanSweepDecideTimeout = 100 * time.Millisecond
	t.Cleanup(func() { orphanSweepDecideTimeout = restore })

	f := &fakeSweeper{decideHangs: true}
	started := time.Now()
	purged := runOrphanSweep(context.Background(), f, map[string]bool{})
	elapsed := time.Since(started)

	assert.Empty(t, purged)
	assert.False(t, f.purgeCalled, "a deciding phase that answered nothing must remove nothing")
	assert.Less(t, elapsed, 5*time.Second, "the sweep waited on the removal budget instead (%s)", elapsed)
}
