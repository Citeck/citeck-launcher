package namespace

import (
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRuntimeWithApps builds a minimal Runtime with the given app defs,
// wired into r.apps as AppRuntime entries (Status left at its zero value —
// callers set what they need). Enough for appsDepsSatisfied/unmetDeps unit
// tests; not a full runtime (no docker client, no dispatcher).
func newTestRuntimeWithApps(t *testing.T, defs map[string]appdef.ApplicationDef) *Runtime {
	t.Helper()
	r := &Runtime{
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
	// unmetDeps formats each entry as "<dep> (<status>)" (see doc comment), so
	// assert against the joined string rather than an exact-element
	// assert.Contains (which would require an unformatted "qdrant" entry). The
	// status is part of the assertion on purpose: the name alone does not tell
	// the operator whether the dependency is starting or was stopped by hand,
	// and dropping the "(%s)" would otherwise pass unnoticed.
	assert.Contains(t, strings.Join(r.unmetDeps(r.apps["rag"]), ", "), "qdrant (STOPPED)")
}

func TestDepsWaiting_AbsentDependencyStillSatisfied(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"rag": {Name: "rag", DependsOn: appdef.StringSet{"keycloak"}},
	})
	r.apps["rag"].Status = AppStatusReadyToStart

	assert.True(t, r.appsDepsSatisfied(r.apps["rag"]),
		"зависимость вне текущей генерации по-прежнему не блокирует")
}

// TestDepsWaiting_StatusTextIsClearedOnceTheAppStarts: the waiting text is
// written on every tick the app is held, so something has to erase it when the
// hold ends — otherwise a healthy RUNNING app keeps showing "Waiting for:
// qdrant (STOPPED)" forever. beginStartingUnderLock is that something; no test
// covered it, so deleting the line left the suite green.
func TestDepsWaiting_StatusTextIsClearedOnceTheAppStarts(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"qdrant": {Name: "qdrant"},
		"rag":    {Name: "rag", DependsOn: appdef.StringSet{"qdrant"}},
	})
	r.apps["qdrant"].Status = AppStatusRunning
	rag := r.apps["rag"]
	rag.Status = AppStatusDepsWaiting
	rag.StatusText = "Waiting for: qdrant (STOPPED)"

	require.True(t, r.appsDepsSatisfied(rag), "the dependency is running — nothing left to wait for")
	r.beginStartingUnderLock(rag, nil)

	assert.Empty(t, rag.StatusText,
		"a started app must not keep the sentence explaining why it was held")
}
