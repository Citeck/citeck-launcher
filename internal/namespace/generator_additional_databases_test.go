package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dbGateway = "llm-gateway"

// gatewayWithDatabase is an additionalApps entry that keeps its data in the
// stand's own postgres, the way the LiteLLM gateway does.
func gatewayWithDatabase(dbs ...string) bundle.AdditionalAppProps {
	return bundle.AdditionalAppProps{
		Name:  dbGateway,
		Image: "example/gateway:1.0.0",
		Environments: map[string]string{
			"DATABASE_URL": "postgresql://gateway:gateway@${PG_HOST}:${PG_PORT}/gateway",
		},
		PostgresDatabases: dbs,
	}
}

func generateWithDatabases(t *testing.T, ws *bundle.WorkspaceConfig, secrets map[string]string) *GenResp {
	t.Helper()
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), &bundle.Def{}, ws, SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{NamespaceSecrets: secrets})
	require.NoError(t, err)
	return resp
}

// initDBCount counts the init actions of the stand's postgres that create db.
func initDBCount(t *testing.T, resp *GenResp, db string) int {
	t.Helper()
	pg := findGeneratedApp(resp, appdef.AppPostgres)
	require.NotNil(t, pg, "the stand's postgres is generated")
	n := 0
	for _, a := range pg.InitActions {
		if len(a.Exec) == 3 && a.Exec[2] == "/init_db_and_user.sh "+db {
			n++
		}
	}
	return n
}

func TestAdditionalAppDatabaseIsCreatedInStandPostgres(t *testing.T) {
	resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{gatewayWithDatabase("gateway")}), nil)

	app := findGeneratedApp(resp, dbGateway)
	require.NotNil(t, app)
	assert.Contains(t, []string(app.DependsOn), appdef.AppPostgres, "the service waits for the stand's postgres")
	url, ok := app.Environments.Get("DATABASE_URL")
	require.True(t, ok)
	assert.Equal(t, "postgresql://gateway:gateway@"+PGHost+":5432/gateway", url)

	assert.Equal(t, 1, initDBCount(t, resp, "gateway"))
}

func TestAdditionalAppDatabasesAreSortedAndDeduplicated(t *testing.T) {
	second := gatewayWithDatabase("gateway")
	second.Name = "gateway-worker"
	resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{
		gatewayWithDatabase("gateway_spend", "gateway", "gateway"),
		second,
	}), nil)

	assert.Equal(t, 1, initDBCount(t, resp, "gateway"), "one database, however many entries ask for it")
	assert.Equal(t, 1, initDBCount(t, resp, "gateway_spend"))

	pg := findGeneratedApp(resp, appdef.AppPostgres)
	var ours []string
	for _, a := range pg.InitActions {
		if len(a.Exec) >= 3 && (a.Exec[2] == "/init_db_and_user.sh gateway" || a.Exec[2] == "/init_db_and_user.sh gateway_spend") {
			ours = append(ours, a.Exec[2])
		}
	}
	assert.Equal(t, []string{"/init_db_and_user.sh gateway", "/init_db_and_user.sh gateway_spend"}, ours,
		"a stable order keeps the postgres deployment hash stable")
}

func TestAdditionalAppDatabasesGlobalSortIsOrderIndependent(t *testing.T) {
	appA := bundle.AdditionalAppProps{Name: "svc-a", Image: "example/a:1.0.0", PostgresDatabases: []string{"zeta", "alpha"}}
	appB := bundle.AdditionalAppProps{Name: "svc-b", Image: "example/b:1.0.0", PostgresDatabases: []string{"middle", "alpha"}}

	respAB := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{appA, appB}), nil)
	respBA := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{appB, appA}), nil)

	pgAB := findGeneratedApp(respAB, appdef.AppPostgres)
	pgBA := findGeneratedApp(respBA, appdef.AppPostgres)
	require.NotNil(t, pgAB)
	require.NotNil(t, pgBA)
	assert.Equal(t, pgAB.InitActions, pgBA.InitActions, "reordering additionalApps must not reorder postgres InitActions")
	assert.Equal(t, pgAB.GetHashInput(), pgBA.GetHashInput(), "reordering additionalApps must not change postgres deployment hash")
}

func TestAdditionalAppDatabasesTypedEntriesIgnored(t *testing.T) {
	seaweed := bundle.AdditionalAppProps{
		Name:              "seaweedfs",
		Type:              bundle.AppTypeSeaweedFS,
		Image:             "chrislusf/seaweedfs:4.48",
		PostgresDatabases: []string{"should_not_exist"},
	}
	resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{seaweed}), nil)

	assert.Equal(t, 0, initDBCount(t, resp, "should_not_exist"), "typed SEAWEEDFS must not create stand postgres DBs")
	sw := findGeneratedApp(resp, "seaweedfs")
	require.NotNil(t, sw)
	assert.NotContains(t, []string(sw.DependsOn), appdef.AppPostgres, "typed SEAWEEDFS must not depend on postgres")
}

func TestAdditionalAppDatabasePrunedWhenPostgresAbsent(t *testing.T) {
	ws := wsWithApps([]bundle.AdditionalAppProps{gatewayWithDatabase("gateway")})
	ctx := NewNsGenContext(basicCfg(), &bundle.Def{})
	ctx.WorkspaceConfig = ws

	generateAdditionalApps(ctx)
	require.Contains(t, ctx.Applications, dbGateway)
	assert.Contains(t, []string(ctx.Applications[dbGateway].DependsOn), appdef.AppPostgres)

	pruneAppsWithMissingDeps(ctx)
	assert.NotContains(t, ctx.Applications, dbGateway, "without stand postgres, app requesting postgresDatabases is pruned")

	// Must be a clean no-op when stand postgres is absent.
	wireAdditionalAppDatabases(ctx)
}

func TestAdditionalAppDatabaseIsCreatedEvenWithoutTheService(t *testing.T) {
	// Like Keycloak's database: the postgres definition must not change when
	// the service comes or goes, or switching a feature would recreate it.
	withService := generateWithDatabases(t,
		wsWithApps([]bundle.AdditionalAppProps{gatewayWithDatabase("gateway")}), nil)
	pgWith := findGeneratedApp(withService, appdef.AppPostgres)
	require.NotNil(t, pgWith)

	t.Run("bundle names no image", func(t *testing.T) {
		entry := gatewayWithDatabase("gateway")
		entry.Image = ""
		resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{entry}), nil)
		assert.Nil(t, findGeneratedApp(resp, dbGateway))
		assert.Equal(t, 1, initDBCount(t, resp, "gateway"))
		assert.Equal(t, pgWith.InitActions, findGeneratedApp(resp, appdef.AppPostgres).InitActions,
			"postgres is the same with and without the service")
	})
	t.Run("disabled", func(t *testing.T) {
		entry := gatewayWithDatabase("gateway")
		off := false
		entry.Enabled = &off
		resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{entry}), nil)
		assert.Nil(t, findGeneratedApp(resp, dbGateway))
		assert.Equal(t, 1, initDBCount(t, resp, "gateway"))
	})
	t.Run("secret missing", func(t *testing.T) {
		entry := gatewayWithDatabase("gateway")
		entry.Environments["MASTER_KEY"] = "${secret:gateway-key}"
		resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{entry}), nil)
		assert.Nil(t, findGeneratedApp(resp, dbGateway))
		assert.Equal(t, 1, initDBCount(t, resp, "gateway"))
	})
	t.Run("pruned for a missing dependency", func(t *testing.T) {
		entry := gatewayWithDatabase("gateway")
		entry.DependsOn = []string{"gateway-cache"}
		cache := bundle.AdditionalAppProps{Name: "gateway-cache"} // no image anywhere: not generated
		resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{entry, cache}), nil)
		assert.Nil(t, findGeneratedApp(resp, dbGateway))
		assert.Equal(t, 1, initDBCount(t, resp, "gateway"))
	})
}

func TestAdditionalAppDatabaseInvalidNameIsNotCreated(t *testing.T) {
	// Validation refuses this at workspace load; generation must not pass it
	// to `sh -c` even when handed a config that skipped validation.
	resp := generateWithDatabases(t, wsWithApps([]bundle.AdditionalAppProps{
		gatewayWithDatabase("gateway; rm -rf /"),
	}), nil)
	require.NotNil(t, findGeneratedApp(resp, dbGateway))
	pg := findGeneratedApp(resp, appdef.AppPostgres)
	for _, a := range pg.InitActions {
		if len(a.Exec) > 0 {
			assert.NotContains(t, a.Exec[len(a.Exec)-1], "rm -rf")
		}
	}
}
