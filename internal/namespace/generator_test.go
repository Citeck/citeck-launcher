package namespace

import (
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlatMapToYAML_NestedKeys(t *testing.T) {
	m := map[string]any{
		"ecos.webapp.dataSources.main.url":      "jdbc:postgresql://postgres:5432/mydb",
		"ecos.webapp.dataSources.main.username": "mydb",
		"ecos.webapp.dataSources.main.password": "mydb",
	}
	result := flatMapToYAML(m)

	// Verify key parts appear in nested YAML
	if !strings.Contains(result, "dataSources:") {
		t.Errorf("expected nested dataSources key in YAML, got:\n%s", result)
	}
	if !strings.Contains(result, "url: jdbc:postgresql://postgres:5432/mydb") {
		t.Errorf("expected datasource URL in YAML, got:\n%s", result)
	}
	if !strings.Contains(result, "username: mydb") {
		t.Errorf("expected username in YAML, got:\n%s", result)
	}
}

func TestFlatMapToYAML_SingleKey(t *testing.T) {
	m := map[string]any{
		"server.port": 8080,
	}
	result := flatMapToYAML(m)
	if !strings.Contains(result, "server:") || !strings.Contains(result, "port: 8080") {
		t.Errorf("expected nested server.port, got:\n%s", result)
	}
}

func TestFlatMapToYAML_Empty(t *testing.T) {
	result := flatMapToYAML(map[string]any{})
	if strings.TrimSpace(result) != "{}" {
		t.Errorf("expected empty YAML object, got: %q", result)
	}
}

func TestRewriteDataSourceURLForLocalhost_JDBC(t *testing.T) {
	input := "jdbc:postgresql://postgres:5432/mydb?schema=public"
	expected := "jdbc:postgresql://localhost:14523/mydb?schema=public"
	result := rewriteDataSourceURLForLocalhost(input, "jdbc:")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestRewriteDataSourceURLForLocalhost_MongoDB(t *testing.T) {
	input := "mongodb://mongo:27017/emodel"
	expected := "mongodb://localhost:27017/emodel"
	result := rewriteDataSourceURLForLocalhost(input, "mongodb:")
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestRewriteDataSourceURLForLocalhost_NoMatch(t *testing.T) {
	input := "redis://redis:6379"
	result := rewriteDataSourceURLForLocalhost(input, "redis:")
	if result != input {
		t.Errorf("expected unchanged URL, got %q", result)
	}
}

func TestGenerateWebapp_FiltersByWorkspaceConfig(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel":  {Image: "nexus.citeck.ru/emodel:1.0"},
			"unknown": {Image: "nexus.citeck.ru/unknown:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{ID: "emodel"},
		},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)

	hasEmodel := false
	hasUnknown := false
	for _, app := range resp.Applications {
		if app.Name == "emodel" {
			hasEmodel = true
		}
		if app.Name == "unknown" {
			hasUnknown = true
		}
	}
	if !hasEmodel {
		t.Fatal("expected emodel to be generated (it's in workspace config)")
	}
	if hasUnknown {
		t.Fatal("expected unknown to be filtered out (not in workspace config)")
	}
}

func TestProxyBaseURL_Port0(t *testing.T) {
	ctx := makeCtx(0, "localhost", false)
	url := ctx.ProxyBaseURL()
	if strings.Contains(url, ":0") {
		t.Errorf("port 0 should be omitted, got %s", url)
	}
	if url != "http://localhost" {
		t.Errorf("expected http://localhost, got %s", url)
	}
}

func TestGetHash_Deterministic(t *testing.T) {
	def := appdef.ApplicationDef{
		Name:  "test",
		Image: "test:1.0",
		Environments: map[string]string{
			"A": "1",
			"B": "2",
		},
		Ports:   []string{"8080:8080"},
		Volumes: []string{"/data:/data"},
	}
	h1 := def.GetHash()
	h2 := def.GetHash()
	if h1 != h2 {
		t.Errorf("hash should be deterministic, got %s vs %s", h1, h2)
	}
	if h1 == "" {
		t.Error("hash should not be empty")
	}
}

func TestGetHash_DifferentImage(t *testing.T) {
	d1 := appdef.ApplicationDef{Name: "test", Image: "test:1.0"}
	d2 := appdef.ApplicationDef{Name: "test", Image: "test:2.0"}
	if d1.GetHash() == d2.GetHash() {
		t.Error("different images should produce different hashes")
	}
}

func TestProcessWebappDataSources_JDBC(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID: "emodel",
			DefaultProps: bundle.WebappDefaultProps{
				DataSources: map[string]bundle.DataSourceConfig{
					"main": {URL: "jdbc:postgresql://${PG_HOST}:${PG_PORT}/emodel"},
				},
			},
		}},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)

	// Verify cloud config file was generated
	content, ok := resp.Files["app/emodel/props/application-launcher.yml"]
	if !ok {
		t.Fatal("expected application-launcher.yml for emodel")
	}
	yamlStr := string(content)
	if !strings.Contains(yamlStr, "postgres:5432/emodel") {
		t.Errorf("expected resolved JDBC URL in cloud config, got:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "username: emodel") {
		t.Errorf("expected username in cloud config, got:\n%s", yamlStr)
	}

	// Verify ext cloud config was generated for CloudConfigServer
	extCfg, ok := resp.CloudConfig["emodel"]
	if !ok {
		t.Fatal("expected ext cloud config for emodel")
	}
	extURL, ok := extCfg["ecos.webapp.dataSources.main.url"]
	if !ok {
		t.Fatal("expected datasource URL in ext cloud config")
	}
	if !strings.Contains(extURL.(string), "localhost:14523") {
		t.Errorf("expected localhost URL in ext cloud config, got: %s", extURL)
	}

	// Verify postgres init action was added
	var pgApp *appdef.ApplicationDef
	for i := range resp.Applications {
		if resp.Applications[i].Name == "postgres" {
			pgApp = &resp.Applications[i]
			break
		}
	}
	if pgApp == nil {
		t.Fatal("expected postgres app in generated applications")
	}
	foundInitAction := false
	for _, ia := range pgApp.InitActions {
		if len(ia.Exec) > 0 && strings.Contains(strings.Join(ia.Exec, " "), "init_db_and_user.sh emodel") {
			foundInitAction = true
			break
		}
	}
	if !foundInitAction {
		t.Error("expected postgres init action for emodel database")
	}

	// Verify emodel depends on postgres
	var emodelApp *appdef.ApplicationDef
	for i := range resp.Applications {
		if resp.Applications[i].Name == "emodel" {
			emodelApp = &resp.Applications[i]
			break
		}
	}
	if emodelApp == nil {
		t.Fatal("expected emodel app")
	}
	if !emodelApp.DependsOn["postgres"] {
		t.Error("expected emodel to depend on postgres")
	}
}

func TestProcessWebappDataSources_CloudConfigOnly(t *testing.T) {
	// Test that cloudConfig from namespace config is merged even when there are no datasources
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps: map[string]WebappProps{
			"emodel": {
				CloudConfig: map[string]any{
					"custom.property": "custom-value",
				},
			},
		},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{ID: "emodel"}},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)

	content, ok := resp.Files["app/emodel/props/application-launcher.yml"]
	if !ok {
		t.Fatal("expected application-launcher.yml for emodel with cloudConfig-only entries")
	}
	if !strings.Contains(string(content), "custom-value") {
		t.Errorf("expected custom.property in cloud config YAML, got:\n%s", string(content))
	}
}

func TestPerAppMemoryLimitOverridesGlobal(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"eproc": {Image: "nexus.citeck.ru/eproc:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		DefaultWebappProps: bundle.WebappDefaultProps{MemoryLimit: "1g"},
		Webapps: []bundle.WebappConfig{{
			ID:           "eproc",
			DefaultProps: bundle.WebappDefaultProps{MemoryLimit: "2g"},
		}},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)
	for _, app := range resp.Applications {
		if app.Name == "eproc" {
			if app.Resources == nil || app.Resources.Limits.Memory != "2g" {
				t.Errorf("expected per-app memoryLimit=2g to override global 1g, got %+v", app.Resources)
			}
			return
		}
	}
	t.Error("expected eproc app")
}

func TestAlfrescoContainerDefs(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"alfresco": {Image: "citeck/alfresco:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Alfresco: bundle.AlfrescoProps{Enabled: true},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)

	findApp := func(name string) *appdef.ApplicationDef {
		for i := range resp.Applications {
			if resp.Applications[i].Name == name {
				return &resp.Applications[i]
			}
		}
		return nil
	}

	// Alfresco app
	alfApp := findApp("alfresco")
	if alfApp == nil {
		t.Fatal("expected alfresco app")
	}
	if alfApp.Kind != appdef.KindCiteckAdditional {
		t.Errorf("expected alfresco kind=KindCiteckAdditional, got %d", alfApp.Kind)
	}

	// Alfresco postgres — should have PGDATA
	alfPg := findApp("alf-postgres")
	if alfPg == nil {
		t.Fatal("expected alf-postgres app")
	}
	if alfPg.Environments["PGDATA"] != "/var/lib/postgresql/data" {
		t.Errorf("expected PGDATA on alf-postgres, got %q", alfPg.Environments["PGDATA"])
	}

	// Alfresco solr
	alfSolr := findApp("alf-solr")
	if alfSolr == nil {
		t.Fatal("expected alf-solr app")
	}
	if alfSolr.Kind != appdef.KindCiteckAdditional {
		t.Errorf("expected alf-solr kind=KindCiteckAdditional, got %d", alfSolr.Kind)
	}
	if alfSolr.Environments["JAVA_OPTS"] != "-Xms1G -Xmx1G" {
		t.Errorf("expected solr JAVA_OPTS=-Xms1G -Xmx1G, got %q", alfSolr.Environments["JAVA_OPTS"])
	}
}

func TestNamespaceLevelDataSources(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps: map[string]WebappProps{
			"emodel": {
				DataSources: map[string]bundle.DataSourceConfig{
					"secondary": {URL: "jdbc:postgresql://${PG_HOST}:${PG_PORT}/custom_db"},
				},
			},
		},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID: "emodel",
			DefaultProps: bundle.WebappDefaultProps{
				DataSources: map[string]bundle.DataSourceConfig{
					"main": {URL: "jdbc:postgresql://${PG_HOST}:${PG_PORT}/emodel"},
				},
			},
		}},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)
	content := string(resp.Files["app/emodel/props/application-launcher.yml"])

	// Both workspace datasource and namespace datasource should be present
	if !strings.Contains(content, "postgres:5432/emodel") {
		t.Errorf("expected workspace datasource URL, got:\n%s", content)
	}
	if !strings.Contains(content, "postgres:5432/custom_db") {
		t.Errorf("expected namespace datasource URL, got:\n%s", content)
	}
}

func TestWebappDefaultProps_ImageOverride(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID: "emodel",
			DefaultProps: bundle.WebappDefaultProps{
				Image: "custom-registry/emodel:2.0",
			},
		}},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)
	for _, app := range resp.Applications {
		if app.Name == "emodel" {
			if app.Image != "custom-registry/emodel:2.0" {
				t.Errorf("expected workspace default image override, got %s", app.Image)
			}
			return
		}
	}
	t.Error("expected emodel app")
}

func TestDeepMergeMaps(t *testing.T) {
	dst := map[string]any{
		"a.b": 1,
		"a.c": 2,
	}
	src := map[string]any{
		"a.c": 3,
		"a.d": 4,
	}
	deepMergeMaps(dst, src)
	if dst["a.b"] != 1 {
		t.Errorf("expected a.b=1, got %v", dst["a.b"])
	}
	if dst["a.c"] != 3 {
		t.Errorf("expected a.c=3 (src wins), got %v", dst["a.c"])
	}
	if dst["a.d"] != 4 {
		t.Errorf("expected a.d=4, got %v", dst["a.d"])
	}
}

func TestDeepMergeMaps_NestedRecursion(t *testing.T) {
	dst := map[string]any{
		"outer": map[string]any{
			"existing": "keep",
			"shared":   "old",
		},
	}
	src := map[string]any{
		"outer": map[string]any{
			"shared": "new",
			"added":  "yes",
		},
	}
	deepMergeMaps(dst, src)

	outer := dst["outer"].(map[string]any)
	if outer["existing"] != "keep" {
		t.Errorf("expected existing=keep, got %v", outer["existing"])
	}
	if outer["shared"] != "new" {
		t.Errorf("expected shared=new, got %v", outer["shared"])
	}
	if outer["added"] != "yes" {
		t.Errorf("expected added=yes, got %v", outer["added"])
	}
}

func TestCloudConfigDeepMerge(t *testing.T) {
	// Workspace sets dataSources, namespace adds cloudConfig — both should be present
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps: map[string]WebappProps{
			"emodel": {
				CloudConfig: map[string]any{
					"ecos.webapp.dataSources.main.xa": true,
				},
			},
		},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{
			ID: "emodel",
			DefaultProps: bundle.WebappDefaultProps{
				DataSources: map[string]bundle.DataSourceConfig{
					"main": {URL: "jdbc:postgresql://${PG_HOST}:${PG_PORT}/emodel"},
				},
			},
		}},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)
	content := string(resp.Files["app/emodel/props/application-launcher.yml"])

	// Both workspace datasource URL and namespace cloudConfig xa should be present
	if !strings.Contains(content, "postgres:5432/emodel") {
		t.Errorf("expected datasource URL from workspace, got:\n%s", content)
	}
	if !strings.Contains(content, "xa: true") {
		t.Errorf("expected xa from namespace cloudConfig deep merge, got:\n%s", content)
	}
}

func TestGeneratorLivenessProbes(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Observer:       ObserverProps{Enabled: true, Image: "citeck/observer:1.0"},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel":  {Image: "nexus.citeck.ru/emodel:1.0"},
			"gateway": {Image: "nexus.citeck.ru/gateway:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{ID: "emodel"},
			{ID: "gateway"},
		},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)

	findApp := func(name string) *appdef.ApplicationDef {
		for i := range resp.Applications {
			if resp.Applications[i].Name == name {
				return &resp.Applications[i]
			}
		}
		return nil
	}

	// Services that must have liveness probes
	for _, name := range []string{
		appdef.AppPostgres,
		appdef.AppZookeeper,
		appdef.AppRabbitmq,
		appdef.AppMongodb,
		appdef.AppKeycloak,
		appdef.AppObserver,
		appdef.AppObsPostgres,
		"emodel",
		"gateway",
	} {
		app := findApp(name)
		if assert.NotNilf(t, app, "expected app %s to be generated", name) {
			assert.NotNilf(t, app.LivenessProbe, "expected liveness probe on %s", name)
		}
	}

	// Services that must NOT have liveness probes
	for _, name := range []string{
		appdef.AppMailhog,
		appdef.AppPgadmin, // disabled by default
		appdef.AppOnlyoffice,
		appdef.AppProxy,
	} {
		app := findApp(name)
		if app != nil {
			assert.Nilf(t, app.LivenessProbe, "expected no liveness probe on %s", name)
		}
	}
}

func TestGeneratorLivenessDisabled(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Webapps: map[string]WebappProps{
			"emodel": {LivenessDisabled: true},
		},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel":  {Image: "nexus.citeck.ru/emodel:1.0"},
			"gateway": {Image: "nexus.citeck.ru/gateway:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{ID: "emodel"},
			{ID: "gateway"},
		},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)

	findApp := func(name string) *appdef.ApplicationDef {
		for i := range resp.Applications {
			if resp.Applications[i].Name == name {
				return &resp.Applications[i]
			}
		}
		return nil
	}

	// emodel should have no liveness probe (disabled)
	emodel := findApp("emodel")
	if assert.NotNil(t, emodel, "expected emodel to be generated") {
		assert.Nil(t, emodel.LivenessProbe, "expected no liveness probe on emodel (disabled)")
	}

	// gateway should still have liveness probe
	gw := findApp("gateway")
	if assert.NotNil(t, gw, "expected gateway to be generated") {
		assert.NotNil(t, gw.LivenessProbe, "expected liveness probe on gateway (not disabled)")
	}
}

func TestGeneratorStartupThresholds(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Observer:       ObserverProps{Enabled: true, Image: "citeck/observer:1.0"},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{ID: "emodel"},
		},
	}

	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{JWT: "test-jwt", OIDC: "test-oidc"})
	require.NoError(t, err)

	for _, app := range resp.Applications {
		for _, sc := range app.StartupConditions {
			if sc.Probe != nil {
				assert.NotZerof(t, sc.Probe.FailureThreshold,
					"startup probe on %s must have explicit FailureThreshold", app.Name)
				assert.NotZerof(t, sc.Probe.TimeoutSeconds,
					"startup probe on %s must have explicit TimeoutSeconds", app.Name)
				assert.NotZerof(t, sc.Probe.PeriodSeconds,
					"startup probe on %s must have explicit PeriodSeconds", app.Name)
			}
		}
	}
}

// TestCiteckSAWiring verifies the "citeck" SA wiring introduced to decouple
// webapp→RabbitMQ auth from the user-facing admin password:
//   - RabbitMQ gets InitActions that create/sync the "citeck" user (monitoring
//     tag + vhost "/" full perms).
//   - Webapps connect to RabbitMQ as "citeck" via ECOS_WEBAPP_RABBITMQ_USERNAME/
//     _PASSWORD (stable across admin-password changes).
//   - Observer's RMQ_MONITOR_* env uses "citeck" instead of "admin".
func TestCiteckSAWiring(t *testing.T) {
	cfg := &Config{
		Authentication: AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
		Observer:       ObserverProps{Enabled: true, Image: "citeck/observer:1.0"},
	}
	bun := &bundle.Def{
		Applications: map[string]bundle.AppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{ID: "emodel"}},
	}

	const saPass = "citeck-sa-deadbeef-0123456789abc"
	resp, err := Generate(cfg, bun, wsCfg, SystemSecrets{
		JWT:           "test-jwt",
		OIDC:          "test-oidc",
		AdminPassword: "user-admin-pass",
		CiteckSA:      saPass,
	})
	require.NoError(t, err)

	findApp := func(name string) *appdef.ApplicationDef {
		for i := range resp.Applications {
			if resp.Applications[i].Name == name {
				return &resp.Applications[i]
			}
		}
		return nil
	}

	// 1. RabbitMQ InitActions must create the "citeck" user, set the
	// monitoring tag, and grant vhost "/" full perms using the SA password.
	rmq := findApp(appdef.AppRabbitmq)
	require.NotNil(t, rmq)

	var addUser, changePw, setTags, setPerms bool
	for _, ia := range rmq.InitActions {
		joined := strings.Join(ia.Exec, " ")
		switch {
		case strings.Contains(joined, "add_user citeck "+saPass):
			addUser = true
		case strings.Contains(joined, "change_password citeck "+saPass):
			changePw = true
		case strings.Contains(joined, "set_user_tags citeck monitoring"):
			setTags = true
		case strings.Contains(joined, "set_permissions -p / citeck .* .* .*"):
			setPerms = true
		}
	}
	assert.True(t, addUser, "RabbitMQ init must add_user citeck")
	assert.True(t, changePw, "RabbitMQ init must change_password citeck")
	assert.True(t, setTags, "RabbitMQ init must set_user_tags citeck monitoring")
	assert.True(t, setPerms, "RabbitMQ init must set_permissions on citeck for vhost /")

	// RabbitMQ needs a startup probe so InitActions run AFTER the broker is ready.
	require.NotEmpty(t, rmq.StartupConditions, "RabbitMQ must have a startup probe")

	// 2. Webapps must use ECOS_WEBAPP_RABBITMQ_USERNAME=citeck and the SA
	// password (NOT the user-facing admin password).
	emodel := findApp("emodel")
	require.NotNil(t, emodel)
	assert.Equal(t, "citeck", emodel.Environments["ECOS_WEBAPP_RABBITMQ_USERNAME"],
		"webapp must authenticate to RabbitMQ as the citeck SA")
	assert.Equal(t, saPass, emodel.Environments["ECOS_WEBAPP_RABBITMQ_PASSWORD"],
		"webapp RMQ password must be the SA password, not the admin password")
	assert.NotEqual(t, "user-admin-pass", emodel.Environments["ECOS_WEBAPP_RABBITMQ_PASSWORD"],
		"webapp RMQ password must NOT leak the user-facing admin password")

	// 3. Observer's management API monitor uses the citeck SA too.
	obs := findApp(appdef.AppObserver)
	require.NotNil(t, obs)
	assert.Equal(t, "citeck", obs.Environments["RMQ_MONITOR_USER"])
	assert.Equal(t, saPass, obs.Environments["RMQ_MONITOR_PASSWORD"])
}
