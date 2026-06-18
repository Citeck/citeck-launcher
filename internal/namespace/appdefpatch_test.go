package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

func TestAppDefPatchImageFlowsThrough(t *testing.T) {
	generatedAtEdit := appdef.ApplicationDef{Name: "eapps", Image: "eapps:1",
		Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "2g"}}}
	edited := generatedAtEdit
	edited.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "4g"}}

	patch, err := DiffAppDef(generatedAtEdit, edited)
	if err != nil {
		t.Fatal(err)
	}
	if patch == nil {
		t.Fatal("expected non-nil patch")
	}

	generatedNow := appdef.ApplicationDef{Name: "eapps", Image: "eapps:2",
		Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "2g"}}}
	got, err := ApplyAppDefPatch(generatedNow, patch)
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "eapps:2" {
		t.Errorf("image should flow from generation, got %q", got.Image)
	}
	if got.Resources == nil || got.Resources.Limits.Memory != "4g" {
		t.Errorf("manual memory edit must stick, got %+v", got.Resources)
	}
}

// ApplyAppDefPatch must NOT mutate its base argument. handleGetAppConfig uses
// the generated def as BOTH the change-gutter baseline and the patch input; an
// in-place mutation (json.Unmarshal aliasing base's nested pointers/slices)
// rewrote the baseline to equal the patched content, so the diff vanished for
// any field reached through a pointer or slice (probe, startupConditions, …).
func TestApplyAppDefPatchDoesNotMutateBase(t *testing.T) {
	base := appdef.ApplicationDef{
		Name: "eapps", Image: "eapps:2",
		StartupConditions: []appdef.StartupCondition{
			{Probe: &appdef.AppProbeDef{
				HTTP: &appdef.HTTPProbeDef{Path: "/management/health", Port: 17023}, FailureThreshold: 90,
			}},
		},
		LivenessProbe: &appdef.AppProbeDef{FailureThreshold: 3},
		Resources:     &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}},
	}
	edited := base
	sc := *base.StartupConditions[0].Probe
	sc.FailureThreshold = 100001
	edited.StartupConditions = []appdef.StartupCondition{{Probe: &sc}}

	patch, err := DiffAppDef(base, edited)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := ApplyAppDefPatch(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	// base stays pristine (the baseline the diff compares against).
	if got := base.StartupConditions[0].Probe.FailureThreshold; got != 90 {
		t.Errorf("base mutated: failureThreshold = %d, want 90", got)
	}
	// merged carries the edit.
	if got := merged.StartupConditions[0].Probe.FailureThreshold; got != 100001 {
		t.Errorf("merged lost the edit: failureThreshold = %d, want 100001", got)
	}
	// merged must not share the nested pointer with base (else later edits alias).
	if merged.StartupConditions[0].Probe == base.StartupConditions[0].Probe {
		t.Error("merged aliases base's probe pointer")
	}
}

func TestDiffAppDefEqualReturnsNil(t *testing.T) {
	d := appdef.ApplicationDef{Name: "a", Image: "i:1"}
	patch, err := DiffAppDef(d, d)
	if err != nil {
		t.Fatal(err)
	}
	if patch != nil {
		t.Fatalf("expected nil patch, got %s", patch)
	}
}

func TestDiffAppDefIgnoresRuntimeCaches(t *testing.T) {
	base := appdef.ApplicationDef{Name: "a", Image: "i:1", ImageDigest: "sha:old", VolumesContentHash: "h1"}
	edited := appdef.ApplicationDef{Name: "a", Image: "i:1", ImageDigest: "sha:new", VolumesContentHash: "h2"}
	patch, err := DiffAppDef(base, edited)
	if err != nil {
		t.Fatal(err)
	}
	if patch != nil {
		t.Fatalf("digest/vch are runtime caches, must not appear in patch: %s", patch)
	}
}

func TestAppDefPatchPreservesEnvOrderAndPlacement(t *testing.T) {
	gen := appdef.ApplicationDef{Name: "eapps", Image: "eapps:1",
		Environments: appdef.OrderedMap{{Key: "B", Value: "2"}, {Key: "A", Value: "1"}}}
	edited := gen
	// User adds ZZZ at the top and AAA at the bottom (deliberately not alphabetical).
	edited.Environments = appdef.OrderedMap{
		{Key: "ZZZ", Value: "top"}, {Key: "B", Value: "2"}, {Key: "A", Value: "1"}, {Key: "AAA", Value: "bottom"},
	}
	patch, err := DiffAppDef(gen, edited)
	if err != nil || patch == nil {
		t.Fatalf("expected env patch, err=%v patch=%s", err, patch)
	}
	// Generation later bumps the image; apply the patch onto the new gen.
	genNow := appdef.ApplicationDef{Name: "eapps", Image: "eapps:2",
		Environments: appdef.OrderedMap{{Key: "B", Value: "2"}, {Key: "A", Value: "1"}}}
	got, err := ApplyAppDefPatch(genNow, patch)
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "eapps:2" {
		t.Errorf("image must flow from generation, got %q", got.Image)
	}
	wantOrder := []string{"ZZZ", "B", "A", "AAA"}
	if got.Environments.Len() != len(wantOrder) {
		t.Fatalf("env len = %d, want %d (%+v)", got.Environments.Len(), len(wantOrder), got.Environments)
	}
	for i, e := range got.Environments {
		if e.Key != wantOrder[i] {
			t.Errorf("env[%d] = %q, want %q (exact user order/placement must survive)", i, e.Key, wantOrder[i])
		}
	}
}

func TestAppDefPatchReorderOnlyIsCaptured(t *testing.T) {
	gen := appdef.ApplicationDef{Name: "x", Image: "x:1",
		Environments: appdef.OrderedMap{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}}
	edited := gen
	edited.Environments = appdef.OrderedMap{{Key: "B", Value: "2"}, {Key: "A", Value: "1"}} // pure reorder
	patch, err := DiffAppDef(gen, edited)
	if err != nil {
		t.Fatal(err)
	}
	if patch == nil {
		t.Fatal("a pure reorder must be captured (order is meaningful in the editor)")
	}
}
