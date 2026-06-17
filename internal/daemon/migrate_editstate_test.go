package daemon

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

func TestMigrateLegacyEditedApps(t *testing.T) {
	gen := []appdef.ApplicationDef{{Name: "eapps", Image: "eapps:1",
		Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "2g"}}}}
	legacy := &namespace.NsPersistedState{
		EditedApps: map[string]appdef.ApplicationDef{"eapps": {Name: "eapps", Image: "eapps:1",
			Resources: &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "4g"}}}},
	}
	patches := migrateLegacyAppPatches(legacy, gen)
	if patches["eapps"] == nil {
		t.Fatal("expected a migrated patch for eapps")
	}
	got, err := namespace.ApplyAppDefPatch(gen[0], patches["eapps"])
	if err != nil {
		t.Fatal(err)
	}
	if got.Resources.Limits.Memory != "4g" {
		t.Errorf("migrated patch must preserve 4g, got %q", got.Resources.Limits.Memory)
	}
}

func TestMigrateLegacyEditedAppsNilWhenEmpty(t *testing.T) {
	if migrateLegacyAppPatches(nil, nil) != nil {
		t.Error("nil state → nil patches")
	}
	if migrateLegacyAppPatches(&namespace.NsPersistedState{}, nil) != nil {
		t.Error("empty legacy edits → nil patches")
	}
}
