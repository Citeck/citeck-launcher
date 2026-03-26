package namespace

import (
	"strings"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
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
	cfg := &NamespaceConfig{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.BundleDef{
		Applications: map[string]bundle.BundleAppDef{
			"emodel":  {Image: "nexus.citeck.ru/emodel:1.0"},
			"unknown": {Image: "nexus.citeck.ru/unknown:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{
			{ID: "emodel"},
		},
	}

	resp := Generate(cfg, bun, wsCfg)

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
		t.Error("expected emodel to be generated (it's in workspace config)")
	}
	if hasUnknown {
		t.Error("expected unknown to be filtered out (not in workspace config)")
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
	cfg := &NamespaceConfig{
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
	bun := &bundle.BundleDef{
		Applications: map[string]bundle.BundleAppDef{
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

	resp := Generate(cfg, bun, wsCfg)

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
	cfg := &NamespaceConfig{
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
	bun := &bundle.BundleDef{
		Applications: map[string]bundle.BundleAppDef{
			"emodel": {Image: "nexus.citeck.ru/emodel:1.0"},
		},
	}
	wsCfg := &bundle.WorkspaceConfig{
		Webapps: []bundle.WebappConfig{{ID: "emodel"}},
	}

	resp := Generate(cfg, bun, wsCfg)

	content, ok := resp.Files["app/emodel/props/application-launcher.yml"]
	if !ok {
		t.Fatal("expected application-launcher.yml for emodel with cloudConfig-only entries")
	}
	if !strings.Contains(string(content), "custom-value") {
		t.Errorf("expected custom.property in cloud config YAML, got:\n%s", string(content))
	}
}
