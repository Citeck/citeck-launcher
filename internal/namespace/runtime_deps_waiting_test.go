package namespace

import (
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/stretchr/testify/assert"
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
	// unmetDeps formats each entry as "<dep> (<status>)" (see doc comment),
	// so assert against the joined string rather than an exact-element
	// assert.Contains (which would require an unformatted "qdrant" entry).
	assert.Contains(t, strings.Join(r.unmetDeps(r.apps["rag"]), ", "), "qdrant")
}

func TestDepsWaiting_AbsentDependencyStillSatisfied(t *testing.T) {
	r := newTestRuntimeWithApps(t, map[string]appdef.ApplicationDef{
		"rag": {Name: "rag", DependsOn: appdef.StringSet{"keycloak"}},
	})
	r.apps["rag"].Status = AppStatusReadyToStart

	assert.True(t, r.appsDepsSatisfied(r.apps["rag"]),
		"зависимость вне текущей генерации по-прежнему не блокирует")
}
