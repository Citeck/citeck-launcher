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
		"остановленная зависимость больше не считается удовлетворённой")
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
		"зависимость вне текущей генерации по-прежнему не блокирует")
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
