package namespace

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// The retry made the WRITE honest; it did nothing for the operator's
// awareness. `citeck stop onlyoffice` stops the container and answers success,
// `citeck edit <app>` applies the patch to the live container and answers
// success — and on a store that keeps refusing (a full disk, a permission
// change, a damaged SQLite file) neither write ever lands. The retries go on
// in the background, the daemon log has one WARN, and the operator learns
// nothing until the next daemon start, where the app is un-detached again and
// the edit is gone.
//
// Returning the persist error from the mutator is not the answer: the
// container really did stop, so the action succeeded. The condition belongs on
// the namespace, where it also covers the case the per-call result cannot —
// a write refused by the LOOP TAIL, after the response was already sent.
func TestAStateWriteThatDidNotLandIsVisibleOnTheNamespace(t *testing.T) {
	const app = "gateway"
	baseDef := appdef.ApplicationDef{Name: app, Image: "gw:1"}
	r := NewRuntime(testConfig(), newMockDocker(), t.TempDir())
	r.SetGeneratedDefs([]appdef.ApplicationDef{baseDef})
	r.InjectAppsForTest(&AppRuntime{Name: app, Status: AppStatusRunning, Def: baseDef, ContainerID: "c1"})
	p := &armedFailPersister{}
	r.SetStatePersister(p)

	require.Empty(t, r.ToNamespaceDto().StateWriteError,
		"a namespace whose writes are landing must report nothing")

	// The detach really happens; only the record of it is refused.
	p.arm(1 << 30)
	require.NoError(t, r.StopApp(app), "the action itself succeeded — the container stopped")
	require.True(t, r.ManualStoppedApps()[app])

	dto := r.ToNamespaceDto()
	assert.Contains(t, dto.StateWriteError, errPersistFailed.Error(),
		"the namespace must name why its state is not reaching the store")

	// It self-heals: the next write that lands clears it, with no further
	// action from anyone.
	p.arm(0)
	r.mu.Lock()
	require.NoError(t, r.persistUnderLock("loop-tail"))
	r.mu.Unlock()
	assert.Empty(t, r.ToNamespaceDto().StateWriteError,
		"a store that came back must clear the condition by itself")
}

// The condition is derived from the SAME failure streak the tail retry uses —
// one source of truth, so what the UI reports and what the loop is retrying
// can never disagree.
func TestTheReportedStateWriteErrorFollowsTheRetryStreak(t *testing.T) {
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.SetStatePersister(failingPersister{})

	r.mu.Lock()
	require.Error(t, r.persistUnderLock("loop-tail"))
	streak := r.persistFailStreak
	r.mu.Unlock()
	require.Equal(t, 1, streak)
	require.NotEmpty(t, r.StateWriteError())

	r.SetStatePersister(&fakePersister{})
	r.mu.Lock()
	require.NoError(t, r.persistUnderLock("loop-tail"))
	streak = r.persistFailStreak
	r.mu.Unlock()
	require.Zero(t, streak)
	assert.Empty(t, r.StateWriteError())
}

// Detach is the other window, and it is the one the tail retry cannot cover:
// after doDetach there is no loop left to try again, so the final write is the
// last chance this runtime gets. Before giving up it therefore retries inline
// — a SQLITE_BUSY, a brief EAGAIN or an NFS hiccup clears in milliseconds, and
// losing the record of what the next daemon is about to adopt over one of
// those would be gratuitous.
//
// Driven against persistOnDetach directly rather than through the live loop:
// the loop's own tail retry is writing to the same store, so a count taken
// around ShutdownDetached would attribute its attempts to the detach.
func TestTheDetachWriteIsRetriedBeforeItIsGivenUpOn(t *testing.T) {
	p := &armedFailPersister{}
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.SetStatePersister(p)

	// One refusal. Counted in literals, not in detachPersistAttempts: an
	// expectation written in terms of the constant is satisfied by a constant
	// of 1, i.e. by no retry at all.
	p.arm(1)
	require.NoError(t, r.persistOnDetach(), "a single refusal must not lose the runtime's last write")

	calls, armed, failed := p.stats()
	assert.Zero(t, armed, "the armed failure must have been spent on a real attempt")
	assert.Equal(t, 1, failed)
	assert.Equal(t, 2, calls, "and the write must not be attempted again once it lands")
}

// A store that stays broken is not retried forever: the daemon is exiting and
// something is waiting on it (systemd, the upgrade's 30s stop budget, the
// desktop supervisor's 5s kill grace). Retrying past those turns "your state
// was not saved" into "your daemon was killed", which loses the report too.
func TestTheDetachRetryIsBounded(t *testing.T) {
	p := &armedFailPersister{}
	r := NewRuntime(&Config{ID: "nsX"}, nil, t.TempDir())
	r.SetStatePersister(p)

	p.arm(1 << 30)
	start := time.Now()
	err := r.persistOnDetach()
	elapsed := time.Since(start)

	require.ErrorIs(t, err, errPersistFailed)
	calls, _, _ := p.stats()
	assert.Equal(t, detachPersistAttempts, calls,
		"a broken store answers the same way every time — the retry must be bounded")
	assert.Less(t, elapsed, 2*time.Second,
		"and must not outlive the budgets waiting on this shutdown")
}

// The whole point of the retry is the verdict it produces: the shutdown path
// returned nothing useful before, so a detach that lost the state was
// invisible to everything but the daemon log — and the next thing the operator
// did was swap the binary on top of it.
func TestADetachThatCouldNotSaveTheStateTellsTheCaller(t *testing.T) {
	p := &armedFailPersister{}
	r := startedLoopRuntime(t, p)

	p.arm(1 << 30)
	require.ErrorIs(t, r.ShutdownDetached(), errPersistFailed,
		"a detach that could not save the state must say so to whoever asked for it")
}

// The verdict survives a second caller: shutdownAfter is one-shot
// (teardownOnce), so the daemon — which may reach the runtime through either
// entry point — must still be able to read what the detach write did.
func TestTheDetachVerdictIsReadableAfterTheTeardownHasRun(t *testing.T) {
	p := &armedFailPersister{}
	r := startedLoopRuntime(t, p)

	p.arm(1 << 30)
	require.Error(t, r.ShutdownDetached())
	require.ErrorIs(t, r.ShutdownDetached(), errPersistFailed,
		"a repeat call must report the same verdict, not a fresh nil")
	require.ErrorIs(t, r.DetachStateError(), errPersistFailed)
}

// A detach whose write landed reports nothing — the ordinary case must not cry
// wolf, or the upgrade would refuse to proceed on every healthy box.
func TestACleanDetachReportsNothing(t *testing.T) {
	p := &armedFailPersister{}
	r := startedLoopRuntime(t, p)

	require.NoError(t, r.ShutdownDetached())
	assert.NoError(t, r.DetachStateError())

	var st NsPersistedState
	require.NoError(t, json.Unmarshal([]byte(p.lastJSON()), &st))
	assert.Equal(t, NsStatusRunning, st.Status,
		"the state the next daemon adopts these containers with must be on disk")
}
