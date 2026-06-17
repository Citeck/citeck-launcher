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

func TestMigrateLegacyFullOverrideBecomesFlowingPatch(t *testing.T) {
	// Legacy full-override (≤2.6 stored the WHOLE edited def). The user changed
	// env (B, +C) but left image as it was at the time.
	legacy := &namespace.NsPersistedState{
		EditedApps: map[string]appdef.ApplicationDef{"eapps": {
			Name: "eapps", Image: "eapps:1",
			Environments: appdef.OrderedMap{{Key: "A", Value: "1"}, {Key: "B", Value: "9"}, {Key: "C", Value: "3"}},
		}},
	}
	genAtMigrate := []appdef.ApplicationDef{{Name: "eapps", Image: "eapps:1",
		Environments: appdef.OrderedMap{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}}}

	patches := migrateLegacyAppPatches(legacy, genAtMigrate)
	if patches["eapps"] == nil {
		t.Fatal("full override must migrate to a patch")
	}
	// Later generation bumps the image; the migrated patch must let it flow
	// through while keeping the user's env.
	genNow := appdef.ApplicationDef{Name: "eapps", Image: "eapps:2",
		Environments: appdef.OrderedMap{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}}
	got, err := namespace.ApplyAppDefPatch(genNow, patches["eapps"])
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "eapps:2" {
		t.Errorf("image must flow from new generation, got %q", got.Image)
	}
	if b, _ := got.Environments.Get("B"); b != "9" {
		t.Errorf("user env edit B=9 must survive migration, got %q", b)
	}
	if c, _ := got.Environments.Get("C"); c != "3" {
		t.Errorf("user-added env C=3 must survive migration, got %q", c)
	}
}
