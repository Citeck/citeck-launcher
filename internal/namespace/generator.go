package namespace

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"gopkg.in/yaml.v3"
)

// NamespaceGenResp is the result of namespace generation.
type NamespaceGenResp struct {
	Applications []appdef.ApplicationDef
	Files        map[string][]byte
	CloudConfig  map[string]map[string]any // per-app ext cloud config for CloudConfigServer
}

// GenerateOpts holds optional parameters for namespace generation.
type GenerateOpts struct {
	DetachedApps map[string]bool // manually stopped apps excluded from dependency graph
}

// Generate creates container definitions from a namespace config, bundle, and workspace config.
func Generate(cfg *NamespaceConfig, bun *bundle.BundleDef, wsCfg *bundle.WorkspaceConfig, opts ...GenerateOpts) *NamespaceGenResp {
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	if len(opts) > 0 && opts[0].DetachedApps != nil {
		ctx.DetachedApps = opts[0].DetachedApps
	}

	// Load embedded appfiles
	loadAppFiles(ctx)

	// Generate infrastructure services
	generateMailhog(ctx)
	generateMongoDB(ctx)
	generatePgAdmin(ctx)
	generatePostgres(ctx)
	generateZookeeper(ctx)
	generateRabbitMQ(ctx)
	generateKeycloak(ctx)
	generateAlfresco(ctx)

	// Generate webapps from bundle — only for apps declared in workspace config
	// (matching Kotlin: context.workspaceConfig.webappsById.contains(app.key))
	wsWebapps := make(map[string]bool)
	if wsCfg != nil {
		for _, w := range wsCfg.Webapps {
			wsWebapps[w.ID] = true
		}
	}
	for name := range bun.Applications {
		if len(wsWebapps) > 0 && !wsWebapps[name] {
			continue
		}
		generateWebapp(name, ctx)
	}

	// Generate proxy (depends on gateway, onlyoffice)
	generateProxy(ctx)
	generateOnlyOffice(ctx)

	// Build all applications
	apps := make([]appdef.ApplicationDef, 0, len(ctx.Applications))
	for _, b := range ctx.Applications {
		apps = append(apps, b.Build())
	}

	return &NamespaceGenResp{
		Applications: apps,
		Files:        ctx.Files,
		CloudConfig:  ctx.CloudConfig,
	}
}

func generateMailhog(ctx *NsGenContext) {
	app := ctx.GetOrCreateApp(appdef.AppMailhog)
	app.Image = bundleImageOr(ctx, appdef.AppMailhog, "mailhog/mailhog:v1.0.1")
	app.Kind = appdef.KindThirdParty
	app.AddPort("1025:1025").AddPort("8025:8025")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "128m"}}
}

func generateMongoDB(ctx *NsGenContext) {
	img := ctx.Config.MongoDB.Image
	if img == "" {
		img = "mongo:4.0.2"
	}
	app := ctx.GetOrCreateApp(appdef.AppMongodb)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	// No extra alias needed — Kotlin uses "mongo" as the hostname
	app.AddPort(fmt.Sprintf("27017:%d", MongoPort))
	app.AddVolume("mongo2:/data/db")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
}

func generatePgAdmin(ctx *NsGenContext) {
	if !ctx.Config.PgAdmin.Enabled {
		return
	}
	img := ctx.Config.PgAdmin.Image
	if img == "" {
		if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.PgAdmin.Image != "" {
			img = ctx.WorkspaceConfig.PgAdmin.Image
		} else {
			img = bundleImageOr(ctx, appdef.AppPgadmin, "dpage/pgadmin4:9.10.0")
		}
	}
	app := ctx.GetOrCreateApp(appdef.AppPgadmin)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddPort("5050:80")
	app.AddEnv("PGADMIN_DEFAULT_EMAIL", "admin@admin.com")
	app.AddEnv("PGADMIN_DEFAULT_PASSWORD", "admin")
	app.AddVolume("pgadmin2:/var/lib/pgadmin")
	app.AddVolume("./pgadmin/servers.json:/pgadmin4/servers.json")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "256m"}}
}

func generatePostgres(ctx *NsGenContext) {
	fallback := "postgres:17.5"
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Postgres.Image != "" {
		fallback = ctx.WorkspaceConfig.Postgres.Image
	}
	img := bundleImageOr(ctx, appdef.AppPostgres, fallback)
	app := ctx.GetOrCreateApp(appdef.AppPostgres)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.ShmSize = "128m"
	app.AddEnv("POSTGRES_USER", "postgres")
	app.AddEnv("POSTGRES_PASSWORD", "postgres")
	app.AddEnv("PGDATA", "/var/lib/postgresql/data")
	app.AddPort(fmt.Sprintf("14523:%d", PGPort))
	app.AddVolume("postgres2:/var/lib/postgresql/data")
	app.AddVolume("./postgres/init_db_and_user.sh:/init_db_and_user.sh")
	app.AddVolume("./postgres/postgresql.conf:/etc/postgresql/postgresql.conf")
	app.AddVolume("./postgres/pg_hba.conf:/etc/postgresql/pg_hba.conf")
	app.Cmd = []string{"-c", "config_file=/etc/postgresql/postgresql.conf"}
	app.StartupConditions = []appdef.StartupCondition{
		{Log: &appdef.LogStartupCondition{Pattern: ".*database system is ready to accept connections.*"}},
		{Probe: &appdef.AppProbeDef{Exec: &appdef.ExecProbeDef{
			Command: []string{"/bin/sh", "-c", "psql -U postgres -d postgres -c 'SELECT 1' || exit 1"},
		}}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
}

func generateZookeeper(ctx *NsGenContext) {
	fallback := "zookeeper:3.9.4"
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Zookeeper.Image != "" {
		fallback = ctx.WorkspaceConfig.Zookeeper.Image
	}
	img := bundleImageOr(ctx, appdef.AppZookeeper, fallback)
	app := ctx.GetOrCreateApp(appdef.AppZookeeper)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddPort(fmt.Sprintf("2181:%d", ZKPort))
	app.AddPort(fmt.Sprintf("%d:8080", ctx.NextPort()))
	app.AddEnv("ZOO_AUTOPURGE_PURGEINTERVAL", "1")
	app.AddEnv("ZOO_AUTOPURGE_SNAPRETAINCOUNT", "3")
	app.AddEnv("ALLOW_ANONYMOUS_LOGIN", "yes")
	app.AddEnv("ZOO_DATA_DIR", "/citeck/zookeeper/data")
	app.AddEnv("ZOO_DATA_LOG_DIR", "/citeck/zookeeper/datalog")
	app.AddVolume("zookeeper2:/citeck/zookeeper")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
	app.InitContainers = []appdef.InitContainerDef{{
		Image: UtilsImage,
		Cmd:   []string{"/bin/sh", "-c", "mkdir -p /zkdir/data /zkdir/datalog"},
		Volumes: []string{"zookeeper2:/zkdir"},
	}}
}

func generateRabbitMQ(ctx *NsGenContext) {
	img := bundleImageOr(ctx, appdef.AppRabbitmq, "rabbitmq:4.1.2-management")
	app := ctx.GetOrCreateApp(appdef.AppRabbitmq)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddPort(fmt.Sprintf("5672:%d", RMQPort))
	app.AddPort("15672:15672")
	app.AddEnv("RABBITMQ_DEFAULT_USER", "admin")
	app.AddEnv("RABBITMQ_DEFAULT_PASS", "admin")
	app.AddEnv("RABBITMQ_DEFAULT_VHOST", "/")
	app.AddEnv("RABBITMQ_MANAGEMENT_ALLOW_WEB_ACCESS", "true")
	app.AddVolume("rabbitmq2:/var/lib/rabbitmq")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "256m"}}
}

func generateKeycloak(ctx *NsGenContext) {
	dbName := "citeck_keycloak"

	// Always create keycloak DB in postgres — avoids DB restart when keycloak is later enabled
	if pgApp := ctx.Applications[appdef.AppPostgres]; pgApp != nil {
		pgApp.InitActions = append(pgApp.InitActions, appdef.AppInitAction{
			Exec: []string{"sh", "-c", "/init_db_and_user.sh " + dbName},
		})
	}

	if ctx.Config.Authentication.Type != AuthKeycloak {
		return
	}

	kcFallback := "keycloak/keycloak:26.4.5"
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Keycloak.Image != "" {
		kcFallback = ctx.WorkspaceConfig.Keycloak.Image
	}
	img := bundleImageOr(ctx, appdef.AppKeycloak, kcFallback)
	app := ctx.GetOrCreateApp(appdef.AppKeycloak)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddEnv("KC_BOOTSTRAP_ADMIN_USERNAME", "admin")
	app.AddEnv("KC_BOOTSTRAP_ADMIN_PASSWORD", "admin")
	app.AddEnv("KC_HOSTNAME_STRICT_HTTPS", fmt.Sprintf("%v", ctx.TLSEnabled()))
	app.AddDependsOn(appdef.AppPostgres)
	app.AddVolume("./keycloak/ecos-app-realm.json:/opt/keycloak/data/import/ecos-app-realm.json")
	app.AddVolume("./keycloak/healthcheck.sh:/healthcheck.sh")

	// All keycloak config via command args (not env), including --import-realm
	app.Cmd = []string{
		"start",
		"--hostname=" + ctx.ProxyBaseURL() + "/ecos-idp/auth/",
		"--http-enabled=true",
		"--health-enabled=true",
		"--db=postgres",
		"--hostname-backchannel-dynamic=true",
		fmt.Sprintf("--db-url=jdbc:postgresql://%s:%d/%s", PGHost, PGPort, dbName),
		"--db-username=" + dbName,
		"--db-password=" + dbName,
		"--proxy-headers=xforwarded",
		"--import-realm",
	}

	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{Exec: &appdef.ExecProbeDef{
			Command: []string{"bash", "/healthcheck.sh"},
		}}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}

	// For custom hosts: generate script to update redirect URIs in realm
	if ctx.ProxyHost() != "localhost" || ctx.TLSEnabled() {
		baseURL := ctx.ProxyBaseURL()
		script := fmt.Sprintf(`#!/bin/bash
KCADM=/opt/keycloak/bin/kcadm.sh
$KCADM config credentials --server http://localhost:8080 \
    --realm master --user admin --password admin
CID=$($KCADM get clients -r ecos-app \
    -q clientId=ecos-proxy-app --fields id \
    --format csv --noquotes | head -1)
[ -n "$CID" ] && $KCADM update "clients/$CID" \
    -r ecos-app -s 'redirectUris=["%s*"]'
`, baseURL)

		ctx.Files["keycloak/update-redirect-uris.sh"] = []byte(script)
		app.AddVolume("./keycloak/update-redirect-uris.sh:/opt/keycloak/scripts/update-redirect-uris.sh")
		app.InitActions = append(app.InitActions, appdef.AppInitAction{
			Exec: []string{"sh", "-c", "bash /opt/keycloak/scripts/update-redirect-uris.sh"},
		})
	}
}

func generateAlfresco(ctx *NsGenContext) {
	if ctx.WorkspaceConfig == nil || !ctx.WorkspaceConfig.Alfresco.Enabled {
		return
	}

	// Check bundle has an Alfresco image before registering any containers
	alfImage := bundleImageOr(ctx, appdef.AppAlfresco, "")
	if alfImage == "" {
		return
	}

	// 1. Alfresco Postgres (separate from main postgres)
	alfPg := ctx.GetOrCreateApp(appdef.AppAlfPostgres)
	alfPg.Image = "postgres:9.4"
	alfPg.Kind = appdef.KindThirdParty
	alfPg.AddEnv("POSTGRES_USER", "postgres")
	alfPg.AddEnv("POSTGRES_PASSWORD", "postgres")
	alfPg.AddPort("54329:5432")
	alfPg.AddVolume("alf_postgres:/var/lib/postgresql/data")
	alfPg.AddVolume("./postgres/init_db_and_user.sh:/init_db_and_user.sh")
	alfPg.InitActions = []appdef.AppInitAction{
		{Exec: []string{"sh", "-c", "/init_db_and_user.sh alfresco"}},
		{Exec: []string{"sh", "-c", "/init_db_and_user.sh alf_flowable"}},
	}
	alfPg.StartupConditions = []appdef.StartupCondition{
		{Log: &appdef.LogStartupCondition{Pattern: ".*database system is ready to accept connections.*"}},
		{Probe: &appdef.AppProbeDef{Exec: &appdef.ExecProbeDef{
			Command: []string{"/bin/sh", "-c", "psql -U postgres -d postgres -c 'SELECT 1' || exit 1"},
		}}},
	}
	alfPg.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}

	// 2. Alfresco app
	alfApp := ctx.GetOrCreateApp(appdef.AppAlfresco)
	alfApp.Image = alfImage
	alfApp.Kind = appdef.KindThirdParty
	alfPort := ctx.NextPort()
	alfApp.AddPort(fmt.Sprintf("%d:8080", alfPort))
	alfApp.AddDependsOn(appdef.AppAlfPostgres)
	alfApp.AddVolume("alf_content:/content")
	alfApp.AddVolume("./alfresco/alfresco_additional.properties:/tmp/alfresco/alfresco_additional.properties")
	alfApp.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{HTTP: &appdef.HttpProbeDef{Path: "/alfresco/s/citeck/ecos/eureka-status", Port: alfPort}}},
	}
	alfApp.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "6g"}}
	alfApp.AddEnv("ALFRESCO_USER_STORE_ADMIN_PASSWORD", "fefdbb615556a4b1dbb36e7935d77cf2")
	alfApp.AddEnv("USE_EXTERNAL_AUTH", "true")
	alfApp.AddEnv("SOLR_HOST", appdef.AppAlfSolr)
	alfApp.AddEnv("SOLR_PORT", "8080")
	alfApp.AddEnv("DB_HOST", appdef.AppAlfPostgres)
	alfApp.AddEnv("DB_PORT", "5432")
	alfApp.AddEnv("DB_NAME", "alfresco")
	alfApp.AddEnv("DB_USERNAME", "alfresco")
	alfApp.AddEnv("DB_PASSWORD", "alfresco")
	alfApp.AddEnv("ALFRESCO_HOSTNAME", appdef.AppAlfresco)
	alfApp.AddEnv("ALFRESCO_PROTOCOL", "http")
	alfApp.AddEnv("SHARE_HOSTNAME", appdef.AppAlfresco)
	alfApp.AddEnv("SHARE_PROTOCOL", "http")
	alfApp.AddEnv("SHARE_PORT", "80")
	alfApp.AddEnv("ALFRESCO_PORT", "8080")
	alfApp.AddEnv("FLOWABLE_URL", "http://localhost")
	alfApp.AddEnv("MAIL_HOST", MailhogHost)
	alfApp.AddEnv("MAIL_PORT", "1025")
	alfApp.AddEnv("MAIL_FROM_DEFAULT", "citeck@ecos24.ru")
	alfApp.AddEnv("MAIL_PROTOCOL", "smtp")
	alfApp.AddEnv("MAIL_SMTP_AUTH", "false")
	alfApp.AddEnv("MAIL_SMTP_STARTTLS_ENABLE", "false")
	alfApp.AddEnv("MAIL_SMTPS_AUTH", "false")
	alfApp.AddEnv("MAIL_SMTPS_STARTTLS_ENABLE", "false")
	alfApp.AddEnv("FLOWABLE_DB_HOST", appdef.AppAlfPostgres)
	alfApp.AddEnv("FLOWABLE_DB_PORT", "5432")
	alfApp.AddEnv("FLOWABLE_DB_NAME", "alf_flowable")
	alfApp.AddEnv("FLOWABLE_DB_USERNAME", "alf_flowable")
	alfApp.AddEnv("FLOWABLE_DB_PASSWORD", "alf_flowable")
	alfApp.AddEnv("JAVA_OPTS", "-Xms4G -Xmx4G -Duser.country=EN -Duser.language=en -Djava.security.egd=file:///dev/urandom -Djavamelody.authorized-users=admin:admin")

	// 3. Alfresco Solr
	alfSolr := ctx.GetOrCreateApp(appdef.AppAlfSolr)
	alfSolr.Image = "nexus.citeck.ru/ess:1.1.0"
	alfSolr.Kind = appdef.KindThirdParty
	alfSolr.AddPort("38080:8080")
	alfSolr.AddVolume("alf_solr_data:/opt/solr4_data")
	alfSolr.AddEnv("TWEAK_SOLR", "true")
	alfSolr.AddEnv("JAVA_OPTS", "-Xms512m -Xmx512m")
	alfSolr.AddEnv("ALFRESCO_HOST", appdef.AppAlfresco)
	alfSolr.AddEnv("ALFRESCO_PORT", "8080")
	alfSolr.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
}

func generateOnlyOffice(ctx *NsGenContext) {
	fallback := "onlyoffice/documentserver:9.1.0.1"
	memLimit := "3g"
	if ctx.WorkspaceConfig != nil {
		if ctx.WorkspaceConfig.OnlyOffice.Image != "" {
			fallback = ctx.WorkspaceConfig.OnlyOffice.Image
		}
		if ctx.WorkspaceConfig.OnlyOffice.MemoryLimit != "" {
			memLimit = ctx.WorkspaceConfig.OnlyOffice.MemoryLimit
		}
	}
	img := bundleImageOr(ctx, appdef.AppOnlyoffice, fallback)
	app := ctx.GetOrCreateApp(appdef.AppOnlyoffice)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddEnv("JWT_ENABLED", "false")
	app.AddEnv("ALLOW_PRIVATE_IP_ADDRESS", "true")
	app.AddPort("8070:80")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: memLimit}}
}

func generateProxy(ctx *NsGenContext) {
	// Get gateway port
	gatewayPort := "8094"
	if gw, ok := ctx.Applications[appdef.AppGateway]; ok {
		if p, ok := gw.Environments["SERVER_PORT"]; ok {
			gatewayPort = p
		}
	}

	app := ctx.GetOrCreateApp(appdef.AppProxy)
	hasInitActions := false

	if !ctx.DetachedApps[appdef.AppOnlyoffice] {
		app.AddEnv("ONLYOFFICE_TARGET", OnlyofficeHost)
		app.AddDependsOn(appdef.AppOnlyoffice)
	}

	switch ctx.Config.Authentication.Type {
	case AuthBasic:
		users := ctx.Config.Authentication.Users
		pairs := make([]string, len(users))
		for i, u := range users {
			pairs[i] = u + ":" + u
		}
		app.AddEnv("BASIC_AUTH_ACCESS", strings.Join(pairs, ","))

	case AuthKeycloak:
		app.AddEnv("EIS_TARGET", KKHost+":8080")
		app.AddEnv("ENABLE_OIDC_FULL_ACCESS", "true")
		app.AddEnv("CLIENT_ID", "ecos-proxy-app")
		app.AddEnv("EIS_SCHEME", "http")
		app.AddEnv("EIS_ID", KKHost+":8080")
		app.AddEnv("REALM_ID", "ecos-app")
		app.AddEnv("EIS_LOCATION", "auth")
		app.AddEnv("REDIRECT_LOGOUT_URI", ctx.ProxyBaseURL())
		app.AddEnv("CLIENT_SECRET", "2996117d-9a33-4e06-b48a-867ce6a235db")

		// Update lua file with correct scheme and URLs
		luaKey := "proxy/lua_oidc_full_access.lua"
		if luaBytes, ok := ctx.Files[luaKey]; ok {
			lua := string(luaBytes)
			lua = strings.Replace(lua, `redirect_uri_scheme = "http"`, fmt.Sprintf(`redirect_uri_scheme = "%s"`, ctx.ProxyScheme()), 1)
			lua = strings.Replace(lua, `redirect_after_logout_uri = "http://localhost/ecos-idp/auth/realms/ecos-app/protocol/openid-connect/logout"`,
				fmt.Sprintf(`redirect_after_logout_uri = "%s/ecos-idp/auth/realms/ecos-app/protocol/openid-connect/logout"`, ctx.ProxyBaseURL()), 1)
			lua = strings.Replace(lua, `post_logout_redirect_uri = "http://localhost"`,
				fmt.Sprintf(`post_logout_redirect_uri = "%s"`, ctx.ProxyBaseURL()), 1)
			ctx.Files[luaKey] = []byte(lua)
		}

		app.AddVolume("./proxy/lua_oidc_full_access.lua:/tmp/lua_oidc_full_access.lua:ro")
		app.InitActions = append(app.InitActions, appdef.AppInitAction{
			Exec: []string{"sh", "-c", "cp /tmp/lua_oidc_full_access.lua /etc/nginx/includes/lua_oidc_full_access.lua"},
		})
		app.InitActions = append(app.InitActions, appdef.AppInitAction{
			Exec: []string{"sh", "-c",
				"sed -i -e '/location \\/ecos-idp\\/auth\\/ {/a\\\n" +
					"    rewrite ^/ecos-idp/auth/(.*)\\$ /\\$1 break;\n' " +
					"-e 's|http://keycloak:8080/auth/|http://keycloak:8080/|g' /etc/nginx/conf.d/default.conf"},
		})
		hasInitActions = true
	}

	app.AddEnv("RABBITMQ_TARGET", fmt.Sprintf("%s:15672", RMQHost))
	app.AddEnv("ENABLE_LOGGING", "warn")
	app.AddEnv("ENABLE_SERVER_STATUS", "true")
	app.AddEnv("MAILHOG_TARGET", MailhogHost+":8025")
	app.AddEnv("ECOS_PAGE_TITLE", "Citeck Launcher")

	proxyImg := ctx.Config.Proxy.Image
	if proxyImg == "" {
		proxyImg = bundleImageOr(ctx, appdef.AppProxy, "")
	}
	app.Image = proxyImg

	proxyTarget := fmt.Sprintf("%s:%s", appdef.AppGateway, gatewayPort)
	containerPort := 80
	if ctx.TLSEnabled() {
		containerPort = 443
	}

	var startupProbe *appdef.AppProbeDef
	if ctx.TLSEnabled() {
		startupProbe = &appdef.AppProbeDef{Exec: &appdef.ExecProbeDef{
			Command: []string{"sh", "-c", "curl -sf -o /dev/null http://localhost:80/eis.json"},
		}}
	} else {
		startupProbe = &appdef.AppProbeDef{HTTP: &appdef.HttpProbeDef{Path: "/eis.json", Port: 80}}
	}

	app.AddEnv("DEFAULT_LOCATION_V2", "true")
	app.AddEnv("GATEWAY_TARGET", fmt.Sprintf("%s:%s", appdef.AppGateway, gatewayPort))
	app.AddEnv("ECOS_INIT_DELAY", "0")
	alfrescoEnabled := ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Alfresco.Enabled && ctx.Applications[appdef.AppAlfresco] != nil
	if alfrescoEnabled {
		app.AddEnv("ALFRESCO_ENABLED", "true")
		proxyTarget = fmt.Sprintf("%s:8080", appdef.AppAlfresco)
		app.AddDependsOn(appdef.AppAlfresco)
	} else {
		app.AddEnv("ALFRESCO_ENABLED", "false")
	}
	app.AddEnv("PROXY_TARGET", proxyTarget)
	app.AddPort(fmt.Sprintf("%d:%d", ctx.Config.Proxy.Port, containerPort))
	app.AddDependsOn(appdef.AppGateway)
	app.Kind = appdef.KindCiteckCore
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "128m"}}
	app.StartupConditions = []appdef.StartupCondition{{Probe: startupProbe}}

	if ctx.TLSEnabled() {
		tls := ctx.Config.Proxy.TLS
		app.AddEnv("ENABLE_HTTPS", "true")
		app.AddEnv("SERVER_TLS_CERT", "/app/tls/server.crt")
		app.AddEnv("SERVER_TLS_KEY", "/app/tls/server.key")
		app.AddVolume(tls.CertPath + ":/app/tls/server.crt:ro")
		app.AddVolume(tls.KeyPath + ":/app/tls/server.key:ro")
	}

	if hasInitActions {
		app.InitActions = append(app.InitActions, appdef.AppInitAction{
			Exec: []string{"sh", "-c", "nginx -s reload"},
		})
	}
}

func generateWebapp(name string, ctx *NsGenContext) {
	// Check if explicitly disabled
	if wp, ok := ctx.Config.Webapps[name]; ok && wp.Enabled != nil && !*wp.Enabled {
		return
	}

	bundleApp, ok := ctx.Bundle.Applications[name]
	if !ok {
		return
	}

	port := ctx.NextPort()
	app := ctx.GetOrCreateApp(name)
	app.Image = bundleApp.Image
	app.Kind = webappKind(name)

	// Apply workspace default props: three-layer merge
	// Level 1: workspace-level DefaultWebappProps (global defaults for all webapps)
	// Level 2: per-app DefaultProps from workspace config
	// Level 3: namespace config overrides (applied later below)
	if ctx.WorkspaceConfig != nil {
		// Level 1: global workspace defaults
		applyWebappDefaults(app, &ctx.WorkspaceConfig.DefaultWebappProps, ctx.Config)

		// Level 2: per-app workspace defaults
		for _, wsCfg := range ctx.WorkspaceConfig.Webapps {
			if wsCfg.ID == name {
				applyWebappDefaults(app, &wsCfg.DefaultProps, ctx.Config)
				break
			}
		}
	}

	// Java opts from namespace config (overrides workspace defaults)
	var javaOpts string
	var springProfiles string
	var debugPort int
	if wp, ok := ctx.Config.Webapps[name]; ok {
		if wp.HeapSize != "" {
			javaOpts = fmt.Sprintf("-Xmx%s -Xms%s", wp.HeapSize, wp.HeapSize)
		}
		if wp.JavaOpts != "" {
			javaOpts += " " + wp.JavaOpts
		}
		if wp.ServerPort > 0 {
			port = wp.ServerPort
		}
		if wp.Image != "" {
			app.Image = wp.Image
		}
		if wp.MemoryLimit != "" {
			app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: wp.MemoryLimit}}
		}
		for k, v := range wp.Environments {
			app.AddEnv(k, v)
		}
		springProfiles = wp.SpringProfiles
		debugPort = wp.DebugPort
	}

	// debugPort: add JDWP agent to JAVA_OPTS (preserve workspace-set JAVA_OPTS if namespace didn't set heapSize)
	if debugPort > 0 {
		if javaOpts == "" {
			javaOpts = app.Environments["JAVA_OPTS"] // keep workspace defaults
		}
		javaOpts += fmt.Sprintf(" -agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=*:%d", debugPort)
		app.AddPort(fmt.Sprintf("%d:%d", debugPort, debugPort))
	}

	if app.Resources == nil {
		app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
	}

	app.AddEnv("SERVER_PORT", fmt.Sprintf("%d", port))

	// Spring profiles: start with "dev,launcher", append custom profiles from config
	profiles := []string{"dev", "launcher"}
	if springProfiles != "" {
		for _, p := range strings.Split(springProfiles, ",") {
			p = strings.TrimSpace(p)
			if p != "" && p != "dev" && p != "launcher" {
				profiles = append(profiles, p)
			}
		}
	}
	app.AddEnv("SPRING_PROFILES_ACTIVE", strings.Join(profiles, ","))
	app.AddEnv("ECOS_WEBAPP_RABBITMQ_HOST", RMQHost)
	app.AddEnv("ECOS_WEBAPP_ZOOKEEPER_HOST", ZKHost)
	app.AddEnv("ECOS_INIT_DELAY", "0")
	app.AddEnv("SPRING_CLOUD_CONFIG_ENABLED", "false") // CloudConfigServer on :8761 is for local debug only
	app.AddEnv("SPRING_CONFIG_IMPORT", "")
	app.AddEnv("ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET", JWTSecret)
	app.AddPort(fmt.Sprintf("%d:%d", port, port))
	app.AddDependsOn(ZKHost)
	app.AddDependsOn(RMQHost)

	if javaOpts != "" {
		app.AddEnv("JAVA_OPTS", strings.TrimSpace(javaOpts))
	}

	// Process data sources from workspace config
	processWebappDataSources(name, app, ctx)

	// Startup probe: HTTP health check
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{HTTP: &appdef.HttpProbeDef{
			Path: "/management/health",
			Port: port,
		}}},
	}

	// EAPPS special handling: add init containers from bundle citeckApps
	if name == appdef.AppEapps && ctx.Bundle != nil && len(ctx.Bundle.CiteckApps) > 0 {
		for _, citeckApp := range ctx.Bundle.CiteckApps {
			app.InitContainers = append(app.InitContainers, appdef.InitContainerDef{
				Image: citeckApp.Image,
				Environments: map[string]string{
					"ECOS_APPS_TARGET_DIR": "/run/ecos-apps",
				},
				Volumes: []string{fmt.Sprintf("./app/%s/ecos-apps:/run/ecos-apps", name)},
			})
		}
		app.AddEnv("ECOS_WEBAPP_EAPPS_ADDITIONAL_ARTIFACTS_LOCATIONS", "/run/ecos-artifacts")
		app.AddVolume(fmt.Sprintf("./app/%s/ecos-apps:/run/ecos-artifacts/app/ecosapp", name))
	}
}

// applyWebappDefaults applies a WebappDefaultProps layer to an app builder.
func applyWebappDefaults(app *AppBuilder, props *bundle.WebappDefaultProps, cfg *NamespaceConfig) {
	if props == nil {
		return
	}
	for k, v := range props.Environments {
		app.AddEnv(k, resolveTemplateVarsWithConfig(v, cfg))
	}
	if props.HeapSize != "" {
		app.AddEnv("JAVA_OPTS", fmt.Sprintf("-Xmx%s -Xms%s", props.HeapSize, props.HeapSize))
	}
	if props.MemoryLimit != "" && app.Resources == nil {
		app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: props.MemoryLimit}}
	}
}

// processWebappDataSources reads datasource definitions from workspace config
// and generates:
//   - Spring cloud config YAML (application-launcher.yml) with all datasource URLs
//   - Volume mount for the config file
//   - SPRING_CONFIG_ADDITIONALLOCATION env var
//   - Init actions on postgres to create databases
//   - Dependencies on postgres/mongodb
//   - extCloudConfig for CloudConfigServer (datasources with localhost URLs + published ports)
//
// This mirrors the Kotlin NamespaceGenerator.processDataSource + cloud config logic.
func processWebappDataSources(appName string, app *AppBuilder, ctx *NsGenContext) {
	if ctx.WorkspaceConfig == nil {
		return
	}

	// Find webapp in workspace config
	var dataSources map[string]bundle.DataSourceConfig
	for _, webapp := range ctx.WorkspaceConfig.Webapps {
		if webapp.ID == appName {
			dataSources = webapp.DefaultProps.DataSources
			break
		}
	}

	pgApp := ctx.Applications[appdef.AppPostgres]

	// Build cloud config maps for both local (webappCloudConfig) and external (extCloudConfig)
	webappCloudConfig := make(map[string]any)
	extCloudConfig := make(map[string]any)

	for dsKey, dsCfg := range dataSources {
		url := resolveTemplateVars(dsCfg.URL)
		dsPrefix := "ecos.webapp.dataSources." + dsKey

		if strings.HasPrefix(url, "jdbc:") {
			app.AddDependsOn(appdef.AppPostgres)
			dbName := extractDBName(url)

			// Add init action on postgres to create DB
			if dbName != "" && pgApp != nil {
				pgApp.InitActions = append(pgApp.InitActions, appdef.AppInitAction{
					Exec: []string{"sh", "-c", "/init_db_and_user.sh " + dbName},
				})
			}

			// Local config (container-to-container URLs)
			webappCloudConfig[dsPrefix+".url"] = url
			if dbName != "" {
				webappCloudConfig[dsPrefix+".username"] = dbName
				webappCloudConfig[dsPrefix+".password"] = dbName
			}
			if dsCfg.XA {
				webappCloudConfig[dsPrefix+".xa"] = true
			}

			// Ext config (localhost URLs with published ports, for local debugging)
			extURL := rewriteDataSourceURLForLocalhost(url, "jdbc:")
			extCloudConfig[dsPrefix+".url"] = extURL
			if dbName != "" {
				extCloudConfig[dsPrefix+".username"] = dbName
				extCloudConfig[dsPrefix+".password"] = dbName
			}

			// Also set env vars for "main" datasource (Spring Boot standard)
			if dsKey == "main" {
				app.AddEnv("SPRING_DATASOURCE_USERNAME", dbName)
				app.AddEnv("SPRING_DATASOURCE_PASSWORD", dbName)
				app.AddEnv("SPRING_DATASOURCE_URL", url)
			}
		} else if strings.HasPrefix(url, "mongodb:") {
			app.AddDependsOn(appdef.AppMongodb)

			webappCloudConfig[dsPrefix+".url"] = url
			extCloudConfig[dsPrefix+".url"] = rewriteDataSourceURLForLocalhost(url, "mongodb:")

			app.AddEnv("SPRING_DATA_MONGODB_URI", url)
		}
	}

	// Merge webappProps.cloudConfig from namespace config (arbitrary per-webapp Spring properties)
	if wp, ok := ctx.Config.Webapps[appName]; ok {
		for k, v := range wp.CloudConfig {
			webappCloudConfig[k] = v
		}
	}

	// License and bundle-key injection for eapps
	if appName == appdef.AppEapps && ctx.WorkspaceConfig != nil && len(ctx.WorkspaceConfig.Licenses) > 0 {
		var licenseStrings []string
		for _, lic := range ctx.WorkspaceConfig.Licenses {
			if data, err := json.Marshal(lic); err == nil {
				licenseStrings = append(licenseStrings, string(data))
			}
		}
		webappCloudConfig["ecos.webapp.license.instances"] = licenseStrings
		bundleKey := ctx.Bundle.Key.Version
		webappCloudConfig["citeck.bundle.key"] = bundleKey
		extCloudConfig["citeck.bundle.key"] = bundleKey
		if ctx.Bundle.Content != nil {
			bundleContent, _ := json.Marshal(ctx.Bundle.Content)
			webappCloudConfig["citeck.bundle.content"] = string(bundleContent)
			extCloudConfig["citeck.bundle.content"] = string(bundleContent)
		}
	}

	// Always write cloud config YAML and mount props directory (matching Kotlin behavior).
	// Even when empty, the mount must exist for hand-placed Spring config files.
	configPath := fmt.Sprintf("app/%s/props/application-launcher.yml", appName)
	if len(webappCloudConfig) > 0 {
		ctx.Files[configPath] = []byte(flatMapToYAML(webappCloudConfig))
	} else {
		ctx.Files[configPath] = []byte("{}\n")
	}
	app.AddEnv("SPRING_CONFIG_ADDITIONALLOCATION", "/run/java.io/spring-props/")
	app.AddVolume(fmt.Sprintf("./app/%s/props:/run/java.io/spring-props/", appName))

	// Store ext cloud config for CloudConfigServer
	if len(extCloudConfig) > 0 {
		ctx.CloudConfig[appName] = extCloudConfig
	}
}

// rewriteDataSourceURLForLocalhost rewrites a datasource URL to use localhost with published ports.
func rewriteDataSourceURLForLocalhost(url, prefix string) string {
	if strings.HasPrefix(url, "jdbc:postgresql://") {
		// jdbc:postgresql://postgres:5432/dbname -> jdbc:postgresql://localhost:14523/dbname
		url = strings.Replace(url, fmt.Sprintf("%s:%d", PGHost, PGPort), "localhost:14523", 1)
	} else if strings.HasPrefix(url, "mongodb://") {
		// mongodb://mongo:27017/dbname -> mongodb://localhost:27017/dbname
		url = strings.Replace(url, fmt.Sprintf("%s:%d", MongoHost, MongoPort), "localhost:27017", 1)
	}
	return url
}

// flatMapToYAML converts a flat dot-separated key map into nested YAML.
func flatMapToYAML(m map[string]any) string {
	// Build nested structure from flat keys
	root := make(map[string]any)
	for k, v := range m {
		parts := strings.Split(k, ".")
		current := root
		for i, p := range parts {
			if i == len(parts)-1 {
				current[p] = v
			} else {
				if next, ok := current[p]; ok {
					if nextMap, ok := next.(map[string]any); ok {
						current = nextMap
					} else {
						slog.Warn("flatMapToYAML: key conflict, dropping", "key", k)
						break
					}
				} else {
					next := make(map[string]any)
					current[p] = next
					current = next
				}
			}
		}
	}
	data, _ := yaml.Marshal(root)
	return string(data)
}

// resolveTemplateVars replaces ${VAR} placeholders in datasource URLs.
// resolveTemplateVarsWithConfig resolves template variables including config-dependent ones.
func resolveTemplateVarsWithConfig(s string, cfg *NamespaceConfig) string {
	kkEnabled := "false"
	if cfg != nil && cfg.Authentication.Type == AuthKeycloak {
		kkEnabled = "true"
	}
	// KK_ADMIN_URL is always set (Kotlin NsGenContext.VARS)
	kkAdminURL := fmt.Sprintf("http://%s:8080", KKHost)
	kkAdminUser := "admin"
	kkAdminPassword := "admin"
	s = strings.ReplaceAll(s, "${KK_ENABLED}", kkEnabled)
	s = strings.ReplaceAll(s, "${KK_ADMIN_URL}", kkAdminURL)
	s = strings.ReplaceAll(s, "${KK_ADMIN_USER}", kkAdminUser)
	s = strings.ReplaceAll(s, "${KK_ADMIN_PASSWORD}", kkAdminPassword)
	return resolveTemplateVars(s)
}

func resolveTemplateVars(s string) string {
	replacements := map[string]string{
		"${PG_HOST}":          PGHost,
		"${PG_PORT}":          fmt.Sprintf("%d", PGPort),
		"${MONGO_HOST}":       MongoHost,
		"${MONGO_PORT}":       fmt.Sprintf("%d", MongoPort),
		"${ZK_HOST}":          ZKHost,
		"${ZK_PORT}":          fmt.Sprintf("%d", ZKPort),
		"${RMQ_HOST}":         RMQHost,
		"${RMQ_PORT}":         fmt.Sprintf("%d", RMQPort),
		"${MAILHOG_HOST}":     MailhogHost,
		"${ONLYOFFICE_HOST}":  OnlyofficeHost,
	}
	for k, v := range replacements {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}

// extractDBName extracts the database name from a JDBC or MongoDB URL.
// jdbc:postgresql://host:port/dbname?params -> dbname
func extractDBName(url string) string {
	idx := strings.LastIndex(url, "/")
	if idx < 0 {
		return ""
	}
	name := url[idx+1:]
	// Strip query parameters
	if qIdx := strings.IndexByte(name, '?'); qIdx >= 0 {
		name = name[:qIdx]
	}
	return name
}

// UtilsImage returns the launcher-utils image from config (supports env override).
var UtilsImage = config.UtilsImage()

var coreApps = map[string]bool{
	appdef.AppProxy: true, appdef.AppGateway: true, appdef.AppEproc: true,
	appdef.AppEmodel: true, appdef.AppUiserv: true, appdef.AppEapps: true,
	appdef.AppHistory: true, appdef.AppNotifications: true, appdef.AppTransformations: true,
}

var coreExtApps = map[string]bool{
	"integrations": true, "edi": true, "content": true,
}

func webappKind(name string) appdef.ApplicationKind {
	if coreApps[name] {
		return appdef.KindCiteckCore
	}
	if coreExtApps[name] {
		return appdef.KindCiteckCoreExtension
	}
	return appdef.KindCiteckAdditional
}

func bundleImageOr(ctx *NsGenContext, name, fallback string) string {
	if app, ok := ctx.Bundle.Applications[name]; ok && app.Image != "" {
		return app.Image
	}
	return fallback
}

func loadAppFiles(ctx *NsGenContext) {
	files, err := appfiles.GetFiles()
	if err != nil {
		return
	}
	for k, v := range files {
		ctx.Files[k] = v
	}
}
