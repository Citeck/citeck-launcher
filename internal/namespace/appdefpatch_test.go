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
