package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRuntimeWithApps builds a minimal Runtime with the given app defs,
// wired into r.apps as AppRuntime entries (Status left at its zero value —
// callers set what they need). Enough for appsDepsSatisfied/unmetDeps unit
// tests; not a full runtime (no docker client, no dispatcher).
//
// The config is set because ToNamespaceDto reads it unconditionally, and the
// DEPS_WAITING contract is asserted on the DTO — that is the only place the
// waiting dependencies exist, so a test that never builds one would pin
// nothing a reader can see.
func newTestRuntimeWithApps(t *testing.T, defs map[string]appdef.ApplicationDef) *Runtime {
	t.Helper()
	r := &Runtime{
		config:            testConfig(),
		apps:              make(map[string]*AppRuntime, len(defs)),
		manualStoppedApps: make(map[string]bool),
		// setAppStatus emits an event and pokes the signal queue; NewRuntime is
		// the only other place that initializes it, and a nil queue panics.
		signalCh: NewSignalQueue(),
	}
	for name, def := range defs {
		r.apps[name] = &AppRuntime{Name: name, Def: def}
	}
	return r
}

// appDtoByName returns the DTO entry for one app. Fails the test when the app
// is absent, so a caller never asserts on a zero AppDto.
func appDtoByName(t *testing.T, dto api.NamespaceDto, name string) api.AppDto {
	t.Helper()
	for _, a := range dto.Apps {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("app %q not in the namespace DTO", name)
	return api.AppDto{}
}

func TestDepsWaiting_StoppedDependencyHoldsDependent(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"qdrant": {Name: "qdrant"},
		"rag":    {Name: "rag", DependsOn: appdef.StringSet{"qdrant"}},
	})
	r.manualStoppedApps["qdrant"] = true
	r.apps["qdrant"].Status = AppStatusStopped
	r.apps["rag"].Status = AppStatusReadyToStart

	assert.False(t, r.appsDepsSatisfied(r.apps["rag"]),
		"a stopped dependency no longer counts as satisfied")
	// The STATUS is part of the assertion on purpose: the name alone does not
	// tell the operator whether the dependency is starting or was stopped by
	// hand, so dropping it would otherwise pass unnoticed. It is compared as a
	// raw constant because that is exactly what travels — the reader turns
	// "STOPPED" into its own `status.STOPPED` label.
	assert.Equal(t,
		[]api.WaitingDepDto{{App: "qdrant", Status: "STOPPED"}},
		r.unmetDeps(r.apps["rag"]))
}

func TestDepsWaiting_AbsentDependencyStillSatisfied(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"rag": {Name: "rag", DependsOn: appdef.StringSet{"keycloak"}},
	})
	r.apps["rag"].Status = AppStatusReadyToStart

	assert.True(t, r.appsDepsSatisfied(r.apps["rag"]),
		"a dependency outside the current generation still does not block")
}

// TestDepsWaiting_DtoCarriesTheDependencyAndItsStatus pins the WIRE: the
// daemon publishes {dep, status} pairs and no sentence at all, so the reader
// picks the language — the web UI words it from its own asset, in the locale
// the operator chose in that window, not the one daemon.yml configured.
//
// The runtime deliberately no longer renders anything here: it runs on the
// runtimeLoop goroutine, which has no reader, and the package-level i18n.T it
// used to call is a single global locale that is not goroutine-safe.
func TestDepsWaiting_DtoCarriesTheDependencyAndItsStatus(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"qdrant": {Name: "qdrant"},
		"rag":    {Name: "rag", DependsOn: appdef.StringSet{"qdrant"}},
	})
	r.apps["qdrant"].Status = AppStatusStopped
	r.apps["rag"].Status = AppStatusDepsWaiting

	rag := appDtoByName(t, r.ToNamespaceDto(), "rag")
	assert.Equal(t, []api.WaitingDepDto{{App: "qdrant", Status: "STOPPED"}}, rag.WaitingFor)
	assert.Empty(t, rag.StatusText,
		"the waiting sentence must not be rendered into StatusText any more")

	// The dependency moving on is reflected without anything being refreshed:
	// the list is derived from live state when the DTO is built.
	r.apps["qdrant"].Status = AppStatusStarting
	rag = appDtoByName(t, r.ToNamespaceDto(), "rag")
	assert.Equal(t, []api.WaitingDepDto{{App: "qdrant", Status: "STARTING"}}, rag.WaitingFor)
}

// TestDepsWaiting_OnlyAWaitingAppReportsDependencies: unmetDeps answers "which
// dependencies are not RUNNING", which is also true of a RUNNING app whose
// dependency has just died — and that app is not waiting on anything. The
// status gate in appWaitingForDeps is the only thing keeping the field honest.
func TestDepsWaiting_OnlyAWaitingAppReportsDependencies(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"qdrant": {Name: "qdrant"},
		"rag":    {Name: "rag", DependsOn: appdef.StringSet{"qdrant"}},
	})
	r.apps["qdrant"].Status = AppStatusFailed
	r.apps["rag"].Status = AppStatusRunning

	assert.Empty(t, appDtoByName(t, r.ToNamespaceDto(), "rag").WaitingFor,
		"a RUNNING app is not waiting for anything, whatever its dependency is doing")
}

// TestDepsWaiting_TheHoldStopsBeingReportedOnceTheAppStarts: the DEPS_WAITING
// list is published for as long as the app is held, so something has to stop
// reporting it when the hold ends — otherwise a healthy app keeps showing
// "Waiting for: qdrant" forever. Here that something is the status gate, which
// needs no clearing on any of the paths out of DEPS_WAITING.
func TestDepsWaiting_TheHoldStopsBeingReportedOnceTheAppStarts(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"qdrant": {Name: "qdrant"},
		"rag":    {Name: "rag", DependsOn: appdef.StringSet{"qdrant"}},
	})
	r.apps["qdrant"].Status = AppStatusRunning
	rag := r.apps["rag"]
	rag.Status = AppStatusDepsWaiting

	require.True(t, r.appsDepsSatisfied(rag), "the dependency is running — nothing left to wait for")
	r.beginStartingUnderLock(rag, nil)

	require.Equal(t, AppStatusStarting, rag.Status)
	assert.Empty(t, appDtoByName(t, r.ToNamespaceDto(), "rag").WaitingFor,
		"a started app must not keep reporting why it was held")
}

// An app held in DEPS_WAITING by a DETACHED dependency is a PROBLEM that will
// not resolve itself, which is what NS STALLED means — as opposed to RUNNING,
// which says the namespace is whole and usable. checkStatus used to skip only
// the detached app itself, so the held dependent kept `allRunning` false and
// never set the failure flag: the namespace sat in STARTING forever. That is
// not cosmetic, because the reconciler and every app's liveness probe are gated
// on NS RUNNING/STALLED (runtime_loop.go) — one `citeck stop postgres` silently
// disabled crash recovery and liveness for the WHOLE namespace, and
// `citeck start` never stopped polling. STALLED ends the wait, restores both,
// and does not claim a namespace is usable when part of it is not up.
func TestAHoldByADetachedDependencyStallsTheNamespace(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"postgres": {Name: "postgres"},
		"emodel":   {Name: "emodel", DependsOn: appdef.StringSet{"postgres"}},
		"gateway":  {Name: "gateway"},
	})
	r.manualStoppedApps["postgres"] = true
	r.apps["postgres"].Status = AppStatusStopped
	r.apps["emodel"].Status = AppStatusDepsWaiting
	r.apps["gateway"].Status = AppStatusRunning
	r.status = NsStatusStarting

	r.checkStatus()

	assert.Equal(t, NsStatusStalled, r.status,
		"a hold is a problem that will not resolve itself, not a finished start")
}

// …and the namespace comes back on its own the moment the operator starts the
// dependency again: the hold is gone, nothing is stuck, so STALLED returns to
// STARTING and the ordinary path takes it to RUNNING. Without this the status
// would be a one-way door and the stand would read "problem" forever.
func TestStartingTheDependencyAgainLiftsTheStall(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"postgres": {Name: "postgres"},
		"emodel":   {Name: "emodel", DependsOn: appdef.StringSet{"postgres"}},
	})
	r.manualStoppedApps["postgres"] = true
	r.apps["postgres"].Status = AppStatusStopped
	r.apps["emodel"].Status = AppStatusDepsWaiting
	r.status = NsStatusStarting
	r.checkStatus()
	require.Equal(t, NsStatusStalled, r.status)

	// `citeck start postgres`: the detach is cleared and the app comes up.
	delete(r.manualStoppedApps, "postgres")
	r.apps["postgres"].Status = AppStatusRunning

	r.checkStatus()
	assert.Equal(t, NsStatusStarting, r.status, "the problem is gone, so the namespace is coming up again")

	r.apps["emodel"].Status = AppStatusRunning
	r.checkStatus()
	assert.Equal(t, NsStatusRunning, r.status)
}

// The narrow shape of that rule, asserted on the predicate itself rather than
// on the namespace status: in checkStatus a dependency that is merely slow is
// ALSO not RUNNING, so it keeps the namespace in STARTING on its own and a
// status-level test would pass no matter how wide the predicate got (verified
// by mutation — widening it to "every DEPS_WAITING app is settled" left such a
// test green). What must hold here is the meaning: only a hold the user created
// counts as settled.
func TestOnlyADetachedDependencyMakesTheHoldSettled(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"postgres": {Name: "postgres"},
		"emodel":   {Name: "emodel", DependsOn: appdef.StringSet{"postgres"}},
	})
	r.apps["emodel"].Status = AppStatusDepsWaiting

	r.apps["postgres"].Status = AppStatusStarting
	assert.False(t, r.heldByDetachedDepsUnderLock(r.apps["emodel"]),
		"the dependency is still coming up on its own: that is waiting, not a user's decision")

	r.apps["postgres"].Status = AppStatusStopped
	assert.False(t, r.heldByDetachedDepsUnderLock(r.apps["emodel"]),
		"a stopped but NOT detached dependency is not the user's decision to detach")

	r.manualStoppedApps["postgres"] = true
	assert.True(t, r.heldByDetachedDepsUnderLock(r.apps["emodel"]))
}

// A dependent waiting on two dependencies, one detached and one still starting,
// is waiting on the one that can still move.
func TestAMixedHoldIsNotSettled(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"postgres":  {Name: "postgres"},
		"zookeeper": {Name: "zookeeper"},
		"emodel":    {Name: "emodel", DependsOn: appdef.StringSet{"postgres", "zookeeper"}},
	})
	r.manualStoppedApps["postgres"] = true
	r.apps["postgres"].Status = AppStatusStopped
	r.apps["zookeeper"].Status = AppStatusStarting
	r.apps["emodel"].Status = AppStatusDepsWaiting

	assert.False(t, r.heldByDetachedDepsUnderLock(r.apps["emodel"]),
		"one of the dependencies may still come up on its own")
}

// An app that is not in DEPS_WAITING at all is never "held" — the predicate
// gates a skip in checkStatus, so a true here would hide a genuinely pending or
// failing app from the namespace status.
func TestOnlyADepsWaitingAppCanBeHeld(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"postgres": {Name: "postgres"},
		"emodel":   {Name: "emodel", DependsOn: appdef.StringSet{"postgres"}},
	})
	r.manualStoppedApps["postgres"] = true
	r.apps["postgres"].Status = AppStatusStopped
	r.apps["emodel"].Status = AppStatusStartFailed

	assert.False(t, r.heldByDetachedDepsUnderLock(r.apps["emodel"]))
}

// A DEPS_WAITING app whose dependencies are all satisfied is about to move to
// STARTING on the next tick — it is pending, not held, and calling it settled
// would let the namespace report RUNNING while an app has not been started yet.
// (The loop over unmet deps answers "true" vacuously without this guard.)
func TestAnAppWithNoUnmetDependenciesIsNotHeld(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"postgres": {Name: "postgres"},
		"emodel":   {Name: "emodel", DependsOn: appdef.StringSet{"postgres"}},
	})
	r.apps["postgres"].Status = AppStatusRunning
	r.apps["emodel"].Status = AppStatusDepsWaiting

	assert.False(t, r.heldByDetachedDepsUnderLock(r.apps["emodel"]))
}

// The settling rule has to be TRANSITIVE, or it only moves the hang one link up
// the chain. Every webapp depends on zookeeper and rabbitmq, and the proxy
// depends on the gateway — so in a default namespace with no rag in it,
// `citeck stop zookeeper` leaves gateway held (its only unmet dep is detached)
// while proxy waits on gateway, which is in DEPS_WAITING and NOT in
// manualStoppedApps. A one-level rule counts proxy as pending and the namespace
// sits in STARTING forever, which is the whole defect this rule exists to fix.
func TestTheSettlingRuleFollowsTheWholeChain(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"zookeeper": {Name: "zookeeper"},
		"gateway":   {Name: "gateway", DependsOn: appdef.StringSet{"zookeeper"}},
		"proxy":     {Name: "proxy", DependsOn: appdef.StringSet{"gateway"}},
	})
	r.manualStoppedApps["zookeeper"] = true
	r.apps["zookeeper"].Status = AppStatusStopped
	r.apps["gateway"].Status = AppStatusDepsWaiting
	r.apps["proxy"].Status = AppStatusDepsWaiting
	r.status = NsStatusStarting

	assert.True(t, r.heldByDetachedDepsUnderLock(r.apps["proxy"]),
		"proxy waits on gateway, which is itself held by a detached zookeeper")

	r.checkStatus()
	assert.Equal(t, NsStatusStalled, r.status)
}

// …and only while the whole chain is held. One link that can still come up on
// its own makes everything above it genuinely pending.
func TestAChainWithALiveLinkIsNotSettled(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"zookeeper": {Name: "zookeeper"},
		"gateway":   {Name: "gateway", DependsOn: appdef.StringSet{"zookeeper"}},
		"proxy":     {Name: "proxy", DependsOn: appdef.StringSet{"gateway"}},
	})
	r.apps["zookeeper"].Status = AppStatusStarting
	r.apps["gateway"].Status = AppStatusDepsWaiting
	r.apps["proxy"].Status = AppStatusDepsWaiting

	assert.False(t, r.heldByDetachedDepsUnderLock(r.apps["proxy"]))
}

// A dependency cycle is rejected at generation, but the predicate walks the
// graph and must not recurse forever if one ever reaches the runtime (a
// hand-edited state file, a future generator bug).
func TestTheSettlingWalkTerminatesOnACycle(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"a": {Name: "a", DependsOn: appdef.StringSet{"b"}},
		"b": {Name: "b", DependsOn: appdef.StringSet{"a"}},
	})
	r.apps["a"].Status = AppStatusDepsWaiting
	r.apps["b"].Status = AppStatusDepsWaiting

	assert.False(t, r.heldByDetachedDepsUnderLock(r.apps["a"]),
		"a cycle with no detached dependency in it is held by nothing")
}

// The settled verdict travels on the wire as AppDto.Held, because deriving it
// needs the detach set and a walk of the dependency graph — neither of which a
// client has. The CLI's wait loop counts this flag; a DTO that never carries it
// is a `citeck start` that never returns.
func TestTheDtoCarriesTheHeldVerdict(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"zookeeper": {Name: "zookeeper"},
		"gateway":   {Name: "gateway", DependsOn: appdef.StringSet{"zookeeper"}},
		"proxy":     {Name: "proxy", DependsOn: appdef.StringSet{"gateway"}},
	})
	r.manualStoppedApps["zookeeper"] = true
	r.apps["zookeeper"].Status = AppStatusStopped
	r.apps["gateway"].Status = AppStatusDepsWaiting
	r.apps["proxy"].Status = AppStatusDepsWaiting

	dto := r.ToNamespaceDto()

	assert.True(t, appDtoByName(t, dto, "gateway").Held)
	assert.True(t, appDtoByName(t, dto, "proxy").Held, "the answer must be transitive on the wire too")
	assert.False(t, appDtoByName(t, dto, "zookeeper").Held, "the detached app itself is not \"held\"")
}

// appsDepsSatisfied (the gate that parks an app in DEPS_WAITING) and unmetDeps
// (what the DTO tells the operator it is waiting on) were two loops with the
// same condition spelled twice. They must agree in BOTH directions: a stricter
// gate parks an app while the UI says it waits for nothing, a stricter report
// names a dependency for an app that already started. This pins the
// equivalence over every shape the rule distinguishes — absent from the
// generation, RUNNING, and each non-running status.
func TestTheDepsGateAndTheDepsReportAnswerTheSameQuestion(t *testing.T) {
	statuses := []AppRuntimeStatus{
		AppStatusRunning, AppStatusStopped, AppStatusStarting,
		AppStatusDepsWaiting, AppStatusStartFailed, AppStatusPulling,
	}
	for _, st := range statuses {
		r := &Runtime{apps: map[string]*AppRuntime{
			"present": {Name: "present", Status: st},
		}}
		app := &AppRuntime{Name: "dependent", Def: appdef.ApplicationDef{
			DependsOn: []string{"present", "absent-from-this-generation"},
		}}

		satisfied := r.appsDepsSatisfied(app)
		reported := r.unmetDeps(app)

		require.Equal(t, satisfied, len(reported) == 0,
			"status %s: the gate and the report disagree -- %v vs %v", st, satisfied, reported)
		for _, d := range reported {
			require.NotEqual(t, "absent-from-this-generation", d.App,
				"dependencies outside the generation are not waited on: it is not part of this namespace")
		}
	}
}
