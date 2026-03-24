package namespace

import (
	"testing"

	"github.com/niceteck/citeck-launcher/internal/appdef"
)

func TestGracefulShutdownOrder(t *testing.T) {
	apps := []*AppRuntime{
		{Name: appdef.AppPostgres, Def: appdef.ApplicationDef{Kind: appdef.KindThirdParty}},
		{Name: appdef.AppGateway, Def: appdef.ApplicationDef{Kind: appdef.KindCiteckCore}},
		{Name: appdef.AppProxy, Def: appdef.ApplicationDef{Kind: appdef.KindCiteckCore}},
		{Name: appdef.AppKeycloak, Def: appdef.ApplicationDef{Kind: appdef.KindThirdParty}},
		{Name: appdef.AppZookeeper, Def: appdef.ApplicationDef{Kind: appdef.KindThirdParty}},
		{Name: appdef.AppEmodel, Def: appdef.ApplicationDef{Kind: appdef.KindCiteckCore}},
	}

	ordered := GracefulShutdownOrder(apps)

	if len(ordered) != 6 {
		t.Fatalf("expected 6 apps, got %d", len(ordered))
	}

	// Proxy should be first
	if ordered[0].Name != appdef.AppProxy {
		t.Errorf("expected proxy first, got %s", ordered[0].Name)
	}

	// Infrastructure (postgres, zookeeper) should be last
	last2 := ordered[len(ordered)-2:]
	infraNames := map[string]bool{appdef.AppPostgres: true, appdef.AppZookeeper: true}
	for _, app := range last2 {
		if !infraNames[app.Name] {
			t.Errorf("expected infrastructure last, got %s", app.Name)
		}
	}

	// Keycloak should be before infrastructure
	kkIdx := -1
	pgIdx := -1
	for i, app := range ordered {
		if app.Name == appdef.AppKeycloak {
			kkIdx = i
		}
		if app.Name == appdef.AppPostgres {
			pgIdx = i
		}
	}
	if kkIdx >= pgIdx {
		t.Errorf("keycloak (idx %d) should be before postgres (idx %d)", kkIdx, pgIdx)
	}
}

func TestDefaultReconcilerConfig(t *testing.T) {
	cfg := DefaultReconcilerConfig()
	if !cfg.Enabled {
		t.Error("reconciler should be enabled by default")
	}
	if cfg.IntervalSeconds != 60 {
		t.Errorf("expected 60s interval, got %d", cfg.IntervalSeconds)
	}
	if !cfg.LivenessEnabled {
		t.Error("liveness should be enabled by default")
	}
}
