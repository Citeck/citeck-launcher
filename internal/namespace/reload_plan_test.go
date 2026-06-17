package namespace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

// --- DiffHashInputLines (pure) ---

func TestDiffHashInputLines_AddedRemovedChanged(t *testing.T) {
	current := "name=app\nimage=img:1\nenv:A=1\nenv:B=2\n"
	next := "name=app\nimage=img:2\nenv:A=1\nenv:C=3\n"

	added, removed := DiffHashInputLines(current, next)
	assert.Equal(t, []string{"image=img:2", "env:C=3"}, added)
	assert.Equal(t, []string{"image=img:1", "env:B=2"}, removed)
}

func TestDiffHashInputLines_Identical(t *testing.T) {
	in := "name=app\nimage=img:1\n"
	added, removed := DiffHashInputLines(in, in)
	assert.Empty(t, added)
	assert.Empty(t, removed)
}

func TestDiffHashInputLines_DuplicateLines(t *testing.T) {
	// Two identical vol= lines on one side, one on the other: only the
	// multiplicity delta is reported, not both occurrences.
	current := "vol=data:/d\nvol=data:/d\n"
	next := "vol=data:/d\n"
	added, removed := DiffHashInputLines(current, next)
	assert.Empty(t, added)
	assert.Equal(t, []string{"vol=data:/d"}, removed)

	added, removed = DiffHashInputLines(next, current)
	assert.Equal(t, []string{"vol=data:/d"}, added)
	assert.Empty(t, removed)
}

func TestDiffHashInputLines_EmptySides(t *testing.T) {
	added, removed := DiffHashInputLines("", "name=app\n")
	assert.Equal(t, []string{"name=app"}, added)
	assert.Empty(t, removed)

	added, removed = DiffHashInputLines("name=app\n", "")
	assert.Empty(t, added)
	assert.Equal(t, []string{"name=app"}, removed)
}

// --- PlanRegenerate (Runtime) ---

// planDef builds a minimal ApplicationDef with a pre-set digest so
// PlanRegenerate doesn't consult the mock docker for it.
func planDef(name, image string, env map[string]string) appdef.ApplicationDef {
	return appdef.ApplicationDef{
		Name:         name,
		Image:        image,
		ImageDigest:  "sha256:" + image,
		Environments: appdef.OrderedMapFromMap(env),
	}
}

func planEntryByName(t *testing.T, entries []ReloadPlanEntry, name string) ReloadPlanEntry {
	t.Helper()
	for _, e := range entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no plan entry for %q in %+v", name, entries)
	return ReloadPlanEntry{}
}

func TestPlanRegenerate_VerdictClassification(t *testing.T) {
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())

	keepDef := planDef("keep-app", "img-keep:1", map[string]string{"A": "1"})
	recreateOld := planDef("recreate-app", "img-rec:1", map[string]string{"OLD": "x"})
	recreateNew := planDef("recreate-app", "img-rec:2", map[string]string{"NEW": "y"})
	detachedDef := planDef("detached-app", "img-det:1", nil)
	removeDef := planDef("remove-app", "img-rem:1", nil)
	createDef := planDef("create-app", "img-new:1", nil)

	// Current runtime state: keep + recreate + detached + soon-removed.
	r.apps = map[string]*AppRuntime{
		"keep-app":     {Name: "keep-app", Status: AppStatusRunning, Def: keepDef},
		"recreate-app": {Name: "recreate-app", Status: AppStatusRunning, Def: recreateOld},
		"detached-app": {Name: "detached-app", Status: AppStatusStopped, Def: detachedDef},
		"remove-app":   {Name: "remove-app", Status: AppStatusRunning, Def: removeDef},
	}
	r.manualStoppedApps = map[string]bool{"detached-app": true}

	desired := []appdef.ApplicationDef{keepDef, recreateNew, detachedDef, createDef}
	entries := r.PlanRegenerate(context.Background(), desired)
	require.Len(t, entries, 5)

	assert.Equal(t, PlanVerdictKeep, planEntryByName(t, entries, "keep-app").Verdict)
	assert.Equal(t, PlanVerdictCreate, planEntryByName(t, entries, "create-app").Verdict)
	assert.Equal(t, PlanVerdictDetached, planEntryByName(t, entries, "detached-app").Verdict)
	assert.Equal(t, PlanVerdictRemove, planEntryByName(t, entries, "remove-app").Verdict)

	rec := planEntryByName(t, entries, "recreate-app")
	assert.Equal(t, PlanVerdictRecreate, rec.Verdict)
	assert.Contains(t, rec.DiffAdded, "image=img-rec:2")
	assert.Contains(t, rec.DiffAdded, "imageDigest=sha256:img-rec:2")
	assert.Contains(t, rec.DiffAdded, "env:NEW=y")
	assert.Contains(t, rec.DiffRemoved, "image=img-rec:1")
	assert.Contains(t, rec.DiffRemoved, "env:OLD=x")
}

func TestPlanRegenerate_DetachedWinsOverHashChange(t *testing.T) {
	// A detached app whose definition changed must still report "detached" —
	// doRegenerate never starts manualStoppedApps, so claiming "recreate"
	// would lie about the visible outcome.
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	oldDef := planDef("det", "img:1", nil)
	newDef := planDef("det", "img:2", nil)
	r.apps = map[string]*AppRuntime{"det": {Name: "det", Status: AppStatusStopped, Def: oldDef}}
	r.manualStoppedApps = map[string]bool{"det": true}

	entries := r.PlanRegenerate(context.Background(), []appdef.ApplicationDef{newDef})
	require.Len(t, entries, 1)
	assert.Equal(t, PlanVerdictDetached, entries[0].Verdict)
}

func TestPlanRegenerate_EditedLockedOverrideKeeps(t *testing.T) {
	// Edited+locked apps substitute the edited definition exactly like
	// doRegenerate — a bundle-side change must NOT produce a recreate verdict
	// when the lock pins the running (edited) definition.
	// The applied def carries no ImageDigest (a patch can change the image, so
	// ApplyAppDefPatch clears the cache); PlanRegenerate re-resolves it. Pin the
	// resolver so the resolved digest matches the running def's planDef digest.
	md := newMockDocker()
	md.imageDigests = map[string]string{"img:edited": "sha256:img:edited", "img:bundle": "sha256:img:bundle"}
	r := newRuntimeForTest(testConfig(), md, t.TempDir())
	editedDef := planDef("app", "img:edited", map[string]string{"E": "1"})
	bundleDef := planDef("app", "img:bundle", nil)

	// The patch is the delta from the generated (bundle) baseline to the edited
	// def; applying it onto bundleDef reproduces editedDef exactly → Keep.
	patch, err := DiffAppDef(bundleDef, editedDef)
	require.NoError(t, err)
	r.apps = map[string]*AppRuntime{"app": {Name: "app", Status: AppStatusRunning, Def: editedDef}}
	r.editedAppPatches = map[string]json.RawMessage{"app": patch}

	entries := r.PlanRegenerate(context.Background(), []appdef.ApplicationDef{bundleDef})
	require.Len(t, entries, 1)
	assert.Equal(t, PlanVerdictKeep, entries[0].Verdict)
}

func TestPlanRegenerate_ResolvesDigestFromLocalCache(t *testing.T) {
	// Desired defs without a digest resolve it from the local Docker cache
	// (mock returns "sha256:mock-digest-<img>") — the same source
	// doRegenerate uses. A current def carrying that digest → keep.
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	currentDef := appdef.ApplicationDef{Name: "app", Image: "img:1", ImageDigest: "sha256:mock-digest-img:1"}
	r.apps = map[string]*AppRuntime{"app": {Name: "app", Status: AppStatusRunning, Def: currentDef}}

	desired := []appdef.ApplicationDef{{Name: "app", Image: "img:1"}} // no digest
	entries := r.PlanRegenerate(context.Background(), desired)
	require.Len(t, entries, 1)
	assert.Equal(t, PlanVerdictKeep, entries[0].Verdict)
}

func TestPlanRegenerate_SnapshotTagHintOnKeep(t *testing.T) {
	// :snapshot images drop any cached digest and re-resolve from the local
	// cache; a kept app is flagged so callers can warn that a real reload
	// re-pulls the tag first.
	md := newMockDocker()
	r := newRuntimeForTest(testConfig(), md, t.TempDir())

	img := "repo/app:snapshot"
	currentDef := appdef.ApplicationDef{Name: "app", Image: img, ImageDigest: "sha256:mock-digest-" + img}
	r.apps = map[string]*AppRuntime{"app": {Name: "app", Status: AppStatusRunning, Def: currentDef}}

	// Desired def carries a stale digest — the snapshot rule must discard it
	// and re-resolve, matching the running container → keep + hint.
	desired := []appdef.ApplicationDef{{Name: "app", Image: img, ImageDigest: "sha256:stale"}}
	entries := r.PlanRegenerate(context.Background(), desired)
	require.Len(t, entries, 1)
	assert.Equal(t, PlanVerdictKeep, entries[0].Verdict)
	assert.True(t, entries[0].SnapshotTag)
}

func TestPlanRegenerate_EmptyRuntimeAllCreate(t *testing.T) {
	// Reload on a stopped namespace regenerates from an empty app map —
	// everything is a create (and the real reload would start it all).
	r := newRuntimeForTest(testConfig(), newMockDocker(), t.TempDir())
	desired := []appdef.ApplicationDef{planDef("a", "img-a:1", nil), planDef("b", "img-b:1", nil)}

	entries := r.PlanRegenerate(context.Background(), desired)
	require.Len(t, entries, 2)
	for _, e := range entries {
		assert.Equal(t, PlanVerdictCreate, e.Verdict)
	}
}
