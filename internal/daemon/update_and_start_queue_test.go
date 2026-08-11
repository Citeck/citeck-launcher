package daemon

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// TestUpdateAndStartAsync_QueuesInsteadOfDroppingWhileReloadInFlight pins the
// contract that "Update And Start" is never silently swallowed.
//
// It used to TryLock reloadMu and, on failure, log "coalesced into in-progress
// reload" and return — on the theory that the running reload satisfied the
// request. It does not: every other reload path passes refreshImages=false, so
// the :snapshot digest refresh that IS this action never ran, no container was
// recreated, and HTTP had already answered 200 "Namespace start requested".
// Now the pass waits for the lock instead, and extra clicks fold into that one
// queued pass (which begins after they arrive, so it satisfies them too).
func TestUpdateAndStartAsync_QueuesInsteadOfDroppingWhileReloadInFlight(t *testing.T) {
	// A REAL runtime plus the reload seam: with a nil runtime the pass bails
	// before invokeReloadEx, and then "the slot was freed" is indistinguishable
	// from "the pass was swallowed" — which is the very thing this test claims
	// to rule out.
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopped)
	got := captureReloadEx(d)

	// Simulate a reload already in flight (e.g. a config-edit save).
	d.reloadMu.Lock()

	d.updateAndStartAsync(true, "test") // Force Update And Start
	require.Eventually(t, func() bool { return d.updatePending.Load() }, time.Second, 5*time.Millisecond,
		"first click must occupy the queue slot and wait for reloadMu, not drop")
	assert.True(t, d.updatePendingForce.Load(),
		"force flag must survive until the queued pass consumes it")

	// A second click while the first is still queued folds into it rather than
	// stacking another full update pass.
	d.updateAndStartAsync(false, "test")
	assert.True(t, d.updatePending.Load(), "queue slot stays occupied; no second waiter")

	// Releasing the in-flight reload lets the queued pass run.
	d.reloadMu.Unlock()

	// Freeing the slot is not enough — the pass must actually reach the reload,
	// carrying the refresh that IS this action and the folded force intent.
	select {
	case args := <-got:
		assert.True(t, args.refreshImages, "the queued pass must reach the reload with refreshImages=true")
		assert.True(t, args.force, "the Force click's intent must survive the queue")
	case <-time.After(5 * time.Second):
		t.Fatal("queued pass never reached the reload — slot freed but nothing ran")
	}

	require.Eventually(t, func() bool {
		return !d.updatePending.Load() && !d.updatePendingForce.Load()
	}, 5*time.Second, 5*time.Millisecond,
		"the queued update must actually execute once reloadMu frees, consuming the slot and the force flag")

	// And it must have released reloadMu when done.
	require.Eventually(t, func() bool {
		if d.reloadMu.TryLock() {
			d.reloadMu.Unlock()
			return true
		}
		return false
	}, time.Second, 5*time.Millisecond, "queued update must release reloadMu")
}

// TestSetUpdateInFlight_BroadcastsOnlyOnTransition: the flag is what tells the
// UI "your click landed", so every real change must reach subscribers — but a
// repeat call with the same value must not, or a burst of clicks would spam SSE
// and force a full refetch per event.
func TestSetUpdateInFlight_BroadcastsOnlyOnTransition(t *testing.T) {
	d := &Daemon{}
	base := d.eventSeq.Load()

	d.setUpdateInFlight(true, "test")
	assert.True(t, d.updateInFlight.Load())
	require.Equal(t, base+1, d.eventSeq.Load(), "raising the flag must broadcast")

	d.setUpdateInFlight(true, "test")
	require.Equal(t, base+1, d.eventSeq.Load(), "no transition must not broadcast")

	d.setUpdateInFlight(false, "")
	assert.False(t, d.updateInFlight.Load())
	require.Equal(t, base+2, d.eventSeq.Load(), "lowering the flag must broadcast")
}

// TestUpdateInFlight_StaysUpForTheWholeWait is the anti-"click went nowhere"
// contract. Between the accepted request and the runtime handoff, nothing
// touches namespace or app status — the reloadMu wait, the git pull, the bundle
// resolve and the file generation all leave them reading STOPPED/RUNNING. The
// flag must therefore be up the entire time and drop only once the pass is done.
func TestUpdateInFlight_StaysUpForTheWholeWait(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopped)
	got := captureReloadEx(d)

	// Something else already holds reloadMu (e.g. a config-edit reload).
	d.reloadMu.Lock()

	// What handleStartNamespace does synchronously, before responding 200.
	d.setUpdateInFlight(true, "test")
	d.updateAndStartAsync(false, "test")

	require.Eventually(t, func() bool { return d.updatePending.Load() }, time.Second, 5*time.Millisecond)
	assert.True(t, d.updateInFlight.Load(),
		"flag must stay up while the pass is still waiting for reloadMu — this is exactly the window "+
			"where no status changes and the click would otherwise look ignored")

	d.reloadMu.Unlock()

	// The flag may only drop because the pass RAN, not because it was swallowed.
	select {
	case args := <-got:
		assert.True(t, args.refreshImages)
	case <-time.After(5 * time.Second):
		t.Fatal("pass never reached the reload")
	}
	require.Eventually(t, func() bool { return !d.updateInFlight.Load() }, 5*time.Second, 5*time.Millisecond,
		"flag must drop once the pass completes and the runtime owns the work")
}

// TestUpdateAndStartAsync_ForceIsNotDegradedByAFoldedPlainClick guards the
// OR-ing of the force flag: a plain click folding into a queued Force pass must
// not downgrade it to a throttled git pull.
func TestUpdateAndStartAsync_ForceIsNotDegradedByAFoldedPlainClick(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopped)
	got := captureReloadEx(d)
	d.reloadMu.Lock()

	d.updateAndStartAsync(false, "test") // plain Update And Start queues first
	require.Eventually(t, func() bool { return d.updatePending.Load() }, time.Second, 5*time.Millisecond)
	assert.False(t, d.updatePendingForce.Load())

	d.updateAndStartAsync(true, "test") // Force click folds in — force must be recorded
	assert.True(t, d.updatePendingForce.Load(),
		"a Force click folding into a queued plain pass must upgrade it, not be lost")

	d.reloadMu.Unlock()

	// And it must reach the reload AS a force, not merely leave the atomic set.
	select {
	case args := <-got:
		assert.True(t, args.force, "the folded Force click must upgrade the pass's git pull")
	case <-time.After(5 * time.Second):
		t.Fatal("pass never reached the reload")
	}
	require.Eventually(t, func() bool { return !d.updatePending.Load() }, 5*time.Second, 5*time.Millisecond)
}

// TestUpdateAndStart_FoldedClickRetargetsThePass: a folding click must hand over
// its NAMESPACE as well as its force flag. The pass pins a namespace so it can
// never act on one the user did not click — but if a fold dropped the new
// target, the pass would wake, find the active namespace no longer matches the
// FIRST caller's id, and skip: nothing at all would run for the namespace that
// was actually clicked, after HTTP already answered 200. That is precisely the
// silent loss this whole change exists to remove.
func TestUpdateAndStart_FoldedClickRetargetsThePass(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "ns2", namespace.NsStatusStopped)
	got := captureReloadEx(d)

	d.reloadMu.Lock()
	// Queued back when ns1 was the active namespace.
	d.updateAndStartAsync(false, "ns1")
	require.Eventually(t, func() bool { return d.updatePending.Load() }, time.Second, 5*time.Millisecond)

	// The active namespace has since switched to ns2 and the user clicked there;
	// this click folds into the already-queued pass.
	d.updateAndStartAsync(false, "ns2")
	d.reloadMu.Unlock()

	select {
	case args := <-got:
		assert.True(t, args.refreshImages)
	case <-time.After(5 * time.Second):
		t.Fatal("the folded click's namespace was dropped: the pass skipped and the click did nothing")
	}
}

// TestShouldStartNotRegenerate_StatusMapping pins the Start-vs-Regenerate decision for
// every namespace status. Both directions fail SILENTLY when wrong —
// Runtime.Start early-returns on its `running` CAS, and a cmdRegenerate posted to
// a dead loop is never drained — so a mis-mapped status throws the whole pass
// (git pull + resolve + generate) away after HTTP already answered 200.
//
// STARTING is the case this exists for: the queue deliberately lets a click land
// during an in-flight pass, and a real bundle sits in STARTING for minutes, so it
// is the status a second click is most likely to sample. It used to fall into the
// Start branch and be discarded.
func TestUpdateStartActionFor_StatusMapping(t *testing.T) {
	for _, tc := range []struct {
		st   namespace.NsRuntimeStatus
		want updateStartAction
		why  string
	}{
		{namespace.NsStatusStopped, updateStartStart, "loop is down — Start"},
		{namespace.NsStatusStarting, updateStartRegenerate,
			"loop is ALIVE — Start would hit the CAS guard and be dropped"},
		{namespace.NsStatusRunning, updateStartRegenerate, "loop is alive — Regenerate"},
		{namespace.NsStatusStalled, updateStartRegenerate, "loop is alive — Regenerate"},
		{namespace.NsStatusStopping, updateStartSkip,
			"mid-teardown: awaitStoppedLoopExit only waits once shutdownComplete is CLOSED, which " +
				"happens after the status is already STOPPED — so through the whole STOPPING window " +
				"Start's CAS fails and the command is dropped. Neither verb can land; bail loudly."},
	} {
		t.Run(string(tc.st), func(t *testing.T) {
			assert.Equal(t, tc.want, updateStartActionFor(tc.st), tc.why)
		})
	}
}

// TestUpdateAndStart_ResamplesStatusAfterTheLock pins WHERE the status is read.
// The pass can sit on reloadMu for the length of someone else's reload, so the
// decision must come from the status at wake-up, not at click time. Sampling on
// the request goroutine (as the code did before) hands Runtime.Start a stale
// answer and the command is silently dropped.
//
// Here the namespace is STOPPED when the click lands and STARTING when the pass
// finally runs: a click-time sample would say Start, the correct post-lock
// sample says Regenerate.
func TestUpdateAndStart_ResamplesStatusAfterTheLock(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopped)
	got := captureReloadEx(d)

	d.reloadMu.Lock()
	d.updateAndStartAsync(false, "test")
	require.Eventually(t, func() bool { return d.updatePending.Load() }, time.Second, 5*time.Millisecond)

	// The namespace starts up while our pass is still queued.
	d.active().runtime.SetStatusForTest(namespace.NsStatusStarting)
	d.reloadMu.Unlock()

	select {
	case args := <-got:
		assert.False(t, args.startNotRegen,
			"the pass must use the status it saw AFTER acquiring reloadMu (STARTING → Regenerate), "+
				"not the STOPPED it was clicked on")
	case <-time.After(5 * time.Second):
		t.Fatal("pass never reached the reload")
	}
}

// newUpdateStartTestDaemon builds a Daemon carrying a REAL namespace.Runtime
// parked at the given status (a real Start would spawn the runtimeLoop and need
// a live Docker client — same constraint newAppsTestDaemonRabbit documents).
func newUpdateStartTestDaemon(t *testing.T, nsID string, st namespace.NsRuntimeStatus) *Daemon {
	t.Helper()
	rt := namespace.NewRuntime(&namespace.Config{ID: nsID}, planStubDocker{}, t.TempDir())
	t.Cleanup(rt.Shutdown)
	rt.SetStatusForTest(st)
	return &Daemon{activeNs: &activeNamespace{
		runtime:  rt,
		nsConfig: &namespace.Config{ID: nsID},
	}}
}

type reloadExArgs struct{ force, startNotRegen, refreshImages bool }

// captureReloadEx installs the reloadExFn seam and returns a channel carrying the
// arguments the pass actually reached the reload with.
func captureReloadEx(d *Daemon) <-chan reloadExArgs {
	got := make(chan reloadExArgs, 4)
	d.reloadExFn = func(force, startNotRegen, refreshImages bool) error {
		got <- reloadExArgs{force, startNotRegen, refreshImages}
		return nil
	}
	return got
}

// TestUpdateAndStart_ReachesReloadWithRefreshImages is the headline contract:
// whatever the queue does, the reload it finally invokes must carry
// refreshImages=true. That flag is the entire point of the action — it is what
// pre-pulls :snapshot digests so the hash diff can see a dev re-push; with it
// false the pass runs to completion and recreates nothing.
func TestUpdateAndStart_ReachesReloadWithRefreshImages(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "plain", true: "force"}[force], func(t *testing.T) {
			d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopped)
			got := captureReloadEx(d)

			d.updateAndStartAsync(force, "test")

			select {
			case args := <-got:
				assert.True(t, args.refreshImages, "Update & Start must always refresh :snapshot digests")
				assert.Equal(t, force, args.force, "the force flag must survive the queue")
				assert.True(t, args.startNotRegen, "a STOPPED namespace must be Started, not Regenerated")
			case <-time.After(5 * time.Second):
				t.Fatal("pass never reached the reload")
			}
		})
	}
}

// TestUpdateAndStart_StartingNamespaceRegenerates is the end-to-end regression
// pin for the status re-sample: clicking again while the namespace is STARTING
// must Regenerate. Mapping it to Start makes Runtime.Start bail on its CAS and
// the click vanishes — the exact silent loss this whole change removes.
func TestUpdateAndStart_StartingNamespaceRegenerates(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStarting)
	got := captureReloadEx(d)

	d.updateAndStartAsync(false, "test")

	select {
	case args := <-got:
		assert.False(t, args.startNotRegen,
			"STARTING means the runtime loop is alive — the pass must Regenerate, not Start")
		assert.True(t, args.refreshImages)
	case <-time.After(5 * time.Second):
		t.Fatal("pass never reached the reload")
	}
}

// TestUpdateAndStart_SkipsWhenActiveNamespaceChanged: the pass can sit on
// reloadMu while the user switches namespace. It must not then run a git pull and
// a :snapshot pre-pull against a namespace nobody clicked.
func TestUpdateAndStart_SkipsWhenActiveNamespaceChanged(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "now-active", namespace.NsStatusStopped)
	got := captureReloadEx(d)

	d.updateAndStartAsync(false, "clicked-on-this-one")

	select {
	case args := <-got:
		t.Fatalf("pass ran against the wrong namespace: %+v", args)
	case <-time.After(300 * time.Millisecond):
		// Expected: skipped. The queue slot must still be released.
	}
	require.Eventually(t, func() bool { return !d.updatePending.Load() }, 2*time.Second, 5*time.Millisecond,
		"a skipped pass must still free the queue slot")
}

// TestStartNamespaceHTTP_RaisesUpdatingAndReportsItOnGetNamespace covers the
// HTTP wiring, which is otherwise untested: the flag must be up once the handler
// has answered, and GET /namespace is what actually carries it to the UI.
// Deleting either line leaves every other Go test green, because the worker's
// post-lock re-assert masks the handler's raise.
//
// Note what this does NOT falsify: it samples after ServeHTTP returns, so moving
// the raise below writeJSON would still pass. The raise-before-response ordering
// is argued in the handler comment (a fast pass could otherwise clear the flag
// first, leaving it stuck on) and is not observable from here.
func TestStartNamespaceHTTP_RaisesUpdatingAndReportsItOnGetNamespace(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopped)
	d.activeNs.appDefs = []appdef.ApplicationDef{{Name: "gateway", Image: "citeck/gateway:snapshot"}}
	// Seam so the pass released at the end of the test does not fall into the
	// real doReloadEx (no store wired in this harness).
	captureReloadEx(d)
	// Hold reloadMu so the pass cannot run (and cannot lower the flag) while we
	// inspect the response — this is the pre-runtime window under test.
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()

	mux := http.NewServeMux()
	d.registerRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.NamespaceStart, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.True(t, d.updateInFlight.Load(),
		"the flag must already be up by the time the handler answers 200")

	// And it must be visible to the UI on the very next fetch.
	nsRec := httptest.NewRecorder()
	mux.ServeHTTP(nsRec, httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody))
	require.Equal(t, http.StatusOK, nsRec.Code, "body=%s", nsRec.Body.String())
	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(nsRec.Body.Bytes(), &dto))
	assert.True(t, dto.Updating, "GET /namespace must report updating:true during the pre-runtime window")
}

// TestGetNamespace_UpdatingIsScopedToTheClickedNamespace: the flag is
// daemon-global but a pass is namespace-pinned, so a namespace the user switched
// to while an unrelated pass sat queued must NOT render "Updating…" (which would
// disable its own Update & Start button for the duration).
func TestGetNamespace_UpdatingIsScopedToTheClickedNamespace(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "other-ns", namespace.NsStatusStopped)
	d.activeNs.appDefs = []appdef.ApplicationDef{{Name: "gateway", Image: "citeck/gateway:snapshot"}}
	// A pass is in flight for a DIFFERENT namespace.
	d.setUpdateInFlight(true, "clicked-ns")

	mux := http.NewServeMux()
	d.registerRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)

	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.False(t, dto.Updating,
		"an update pinned to another namespace must not mark this one as updating")
}

// TestUpdateAndStart_SkipsWhileStopping is the behavioral half of the
// three-way decision: the table test only pins the mapping function, so without
// this, deleting the `action == updateStartSkip` bail stays green and a
// Regenerate gets fired at a namespace that is tearing down.
func TestUpdateAndStart_SkipsWhileStopping(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopping)
	got := captureReloadEx(d)

	d.updateAndStartAsync(false, "test")

	select {
	case args := <-got:
		t.Fatalf("a namespace mid-teardown must not be reloaded at all, got %+v", args)
	case <-time.After(300 * time.Millisecond):
		// Expected: bailed before paying for the git pull.
	}
	require.Eventually(t, func() bool { return !d.updatePending.Load() }, 2*time.Second, 5*time.Millisecond,
		"a skipped pass must still free the queue slot")
}

// TestUpdateAndStart_ReportsFailureToTheUI: a pass that fails must leave
// something the UI can show. Without it the spinner just stops and nothing
// changed — indistinguishable from success, which is the same "my click went
// nowhere" the Updating flag exists to prevent.
func TestUpdateAndStart_ReportsFailureToTheUI(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopped)
	d.activeNs.appDefs = []appdef.ApplicationDef{{Name: "gateway", Image: "citeck/gateway:snapshot"}}
	d.reloadExFn = func(_, _, _ bool) error { return errors.New("git pull failed: dial tcp: timeout") }

	d.updateAndStartAsync(false, "test")
	require.Eventually(t, func() bool { return d.updateLastFailure.Load() != nil }, 5*time.Second, 5*time.Millisecond,
		"a failed pass must record why")

	mux := http.NewServeMux()
	d.registerRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	assert.Contains(t, dto.UpdateError, "git pull failed")
	assert.NotZero(t, dto.UpdateErrorAt, "the occurrence needs a timestamp so the UI shows it exactly once")
	assert.False(t, dto.Updating, "the flag must be down once the pass is over")
}

// TestUpdateAndStart_StoppingRefusalIsReported: the STOPPING skip is a refusal,
// not a crash, but it is still a click that will not be honored — the user has
// to learn that rather than watch the spinner stop for no visible reason.
func TestUpdateAndStart_StoppingRefusalIsReported(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "test", namespace.NsStatusStopping)
	captureReloadEx(d)

	d.updateAndStartAsync(false, "test")
	require.Eventually(t, func() bool { return d.updateLastFailure.Load() != nil }, 5*time.Second, 5*time.Millisecond)
	msg, at := d.updateFailureFor(&namespace.Config{ID: "test"})
	assert.Contains(t, msg, "stopping")
	assert.NotZero(t, at)
}

// TestUpdateFailure_IsScopedAndClearedByANewClick: a failure from a pass aimed
// at another namespace must not pop up here, and a fresh click supersedes the
// previous verdict so a stale error never sits next to a running update.
func TestUpdateFailure_IsScopedAndClearedByANewClick(t *testing.T) {
	d := newUpdateStartTestDaemon(t, "active-ns", namespace.NsStatusStopped)
	d.activeNs.appDefs = []appdef.ApplicationDef{{Name: "gateway", Image: "citeck/gateway:snapshot"}}
	captureReloadEx(d)

	d.recordUpdateFailure("other-ns", "boom")
	msg, _ := d.updateFailureFor(&namespace.Config{ID: "active-ns"})
	assert.Empty(t, msg, "a failure pinned to another namespace must not surface here")

	d.recordUpdateFailure("active-ns", "boom")
	msg, _ = d.updateFailureFor(&namespace.Config{ID: "active-ns"})
	require.Equal(t, "boom", msg)

	// A new click clears it.
	mux := http.NewServeMux()
	d.registerRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, api.NamespaceStart, http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	msg, _ = d.updateFailureFor(&namespace.Config{ID: "active-ns"})
	assert.Empty(t, msg, "accepting a new pass must supersede the previous failure")
}
