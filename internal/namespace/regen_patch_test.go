package namespace

import (
	"encoding/json"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

func TestApplyEditPatchHelper(t *testing.T) {
	r := &Runtime{editedAppPatches: map[string]json.RawMessage{
		"eapps": json.RawMessage(`{"resources":{"limits":{"memory":"4g"}}}`),
	}}
	gen := appdef.ApplicationDef{Name: "eapps", Image: "eapps:2",
		Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "2g"}}}
	got := r.applyEditPatch(gen)
	if got.Image != "eapps:2" {
		t.Errorf("image must flow from generation: %q", got.Image)
	}
	if got.Resources.Limits.Memory != "4g" {
		t.Errorf("patched memory must win: %q", got.Resources.Limits.Memory)
	}
}

func TestApplyEditPatchNoPatchReturnsGen(t *testing.T) {
	r := &Runtime{editedAppPatches: map[string]json.RawMessage{}}
	gen := appdef.ApplicationDef{Name: "x", Image: "x:1"}
	if got := r.applyEditPatch(gen); got.Image != "x:1" {
		t.Errorf("no patch → unchanged def, got %q", got.Image)
	}
}
