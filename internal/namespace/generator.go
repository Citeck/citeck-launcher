package namespace

import (
	"fmt"
	"strings"

	"github.com/niceteck/citeck-launcher/internal/appdef"
	"github.com/niceteck/citeck-launcher/internal/bundle"
)

// NamespaceGenResp is the result of namespace generation.
type NamespaceGenResp struct {
	Applications []appdef.ApplicationDef
	Files        map[string][]byte
}

// Generate creates container definitions from a namespace config and bundle.
func Generate(cfg *NamespaceConfig, bun *bundle.BundleDef) *NamespaceGenResp {
	ctx := NewNsGenContext(cfg, bun)

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

	// Generate webapps from bundle
	for name := range bun.Applications {
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
	}
}

func generateMailhog(ctx *NsGenContext) {
	app := ctx.GetOrCreateApp(appdef.AppMailhog)
	app.Image = bundleImageOr(ctx, appdef.AppMailhog, "mailhog/mailhog:v1.0.1")
	app.Kind = appdef.KindThirdParty
	app.AddPort("8025:8025").AddPort("1025:1025")
	app.AddVolume("mailhog:/maildir")
	app.AddEnv("MH_STORAGE", "maildir")
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
	app.AddPort(fmt.Sprintf("27017:%d", MongoPort))
	app.AddVolume("mongodb:/data/db")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
}

func generatePgAdmin(ctx *NsGenContext) {
	if !ctx.Config.PgAdmin.Enabled {
		return
	}
	img := ctx.Config.PgAdmin.Image
	if img == "" {
		img = bundleImageOr(ctx, appdef.AppPgadmin, "dpage/pgadmin4:9.10.0")
	}
	app := ctx.GetOrCreateApp(appdef.AppPgadmin)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddPort("5050:80")
	app.AddEnv("PGADMIN_DEFAULT_EMAIL", "admin@citeck.ru")
	app.AddEnv("PGADMIN_DEFAULT_PASSWORD", "admin")
	app.AddEnv("PGADMIN_CONFIG_SERVER_MODE", "False")
	app.AddVolume("./pgadmin/servers.json:/pgadmin4/servers.json:ro")
	app.AddDependsOn(appdef.AppPostgres)
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "256m"}}
}

func generatePostgres(ctx *NsGenContext) {
	img := bundleImageOr(ctx, appdef.AppPostgres, "postgres:17.5")
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
	img := bundleImageOr(ctx, appdef.AppZookeeper, "zookeeper:3.9.4")
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
	if ctx.Config.Authentication.Type != AuthKeycloak {
		return
	}
	img := bundleImageOr(ctx, appdef.AppKeycloak, "quay.io/keycloak/keycloak:26.0")
	app := ctx.GetOrCreateApp(appdef.AppKeycloak)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddEnv("KC_DB", "postgres")
	app.AddEnv("KC_DB_URL", fmt.Sprintf("jdbc:postgresql://%s:%d/keycloak", PGHost, PGPort))
	app.AddEnv("KC_DB_USERNAME", "keycloak")
	app.AddEnv("KC_DB_PASSWORD", "keycloak")
	app.AddEnv("KC_HOSTNAME", ctx.ProxyBaseURL()+"/ecos-idp")
	app.AddEnv("KC_HTTP_RELATIVE_PATH", "/")
	app.AddEnv("KC_PROXY_HEADERS", "xforwarded")
	app.AddEnv("KC_HTTP_ENABLED", "true")
	app.AddEnv("KC_HEALTH_ENABLED", "true")
	app.AddEnv("KEYCLOAK_ADMIN", "admin")
	app.AddEnv("KEYCLOAK_ADMIN_PASSWORD", "admin")
	app.AddPort("8080:8080")
	app.AddDependsOn(appdef.AppPostgres)
	app.AddVolume("./keycloak/healthcheck.sh:/healthcheck.sh:ro")
	app.Cmd = []string{"start"}
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{Exec: &appdef.ExecProbeDef{
			Command: []string{"bash", "/healthcheck.sh"},
		}}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
}

func generateOnlyOffice(ctx *NsGenContext) {
	img := bundleImageOr(ctx, appdef.AppOnlyoffice, "onlyoffice/documentserver:9.1.0.1")
	app := ctx.GetOrCreateApp(appdef.AppOnlyoffice)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddEnv("JWT_SECRET", JWTSecret)
	app.AddPort("8980:80")
	app.ShmSize = "256m"
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "3g"}}
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
	app.AddEnv("PROXY_TARGET", proxyTarget)
	app.AddEnv("ECOS_INIT_DELAY", "0")
	app.AddEnv("ALFRESCO_ENABLED", "false")
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

	// Java opts
	var javaOpts string
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
	}

	if app.Resources == nil {
		app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
	}

	app.AddEnv("SERVER_PORT", fmt.Sprintf("%d", port))
	app.AddEnv("SPRING_PROFILES_ACTIVE", "dev,launcher")
	app.AddEnv("ECOS_WEBAPP_RABBITMQ_HOST", RMQHost)
	app.AddEnv("ECOS_WEBAPP_ZOOKEEPER_HOST", ZKHost)
	app.AddEnv("ECOS_INIT_DELAY", "0")
	app.AddEnv("SPRING_CLOUD_CONFIG_ENABLED", "false")
	app.AddEnv("SPRING_CONFIG_IMPORT", "")
	app.AddEnv("ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET", JWTSecret)
	app.AddPort(fmt.Sprintf("%d:%d", port, port))
	app.AddDependsOn(ZKHost)
	app.AddDependsOn(RMQHost)

	if javaOpts != "" {
		app.AddEnv("JAVA_OPTS", strings.TrimSpace(javaOpts))
	}

	// Startup probe: HTTP health check
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{HTTP: &appdef.HttpProbeDef{
			Path: "/management/health",
			Port: port,
		}}},
	}

	// Data source dependencies
	if needsPostgres(name) {
		app.AddDependsOn(appdef.AppPostgres)
	}
	if needsMongo(name) {
		app.AddDependsOn(appdef.AppMongodb)
	}
}

// UtilsImage is the default init container image for volume setup.
const UtilsImage = "registry.citeck.ru/community/launcher-utils:1.0"

var coreApps = map[string]bool{
	appdef.AppProxy: true, appdef.AppGateway: true, appdef.AppEproc: true,
	appdef.AppEmodel: true, appdef.AppUiserv: true, appdef.AppEapps: true,
	appdef.AppHistory: true, appdef.AppNotifications: true, appdef.AppTransformations: true,
}

func webappKind(name string) appdef.ApplicationKind {
	if coreApps[name] {
		return appdef.KindCiteckCore
	}
	return appdef.KindCiteckAdditional
}

func needsPostgres(name string) bool {
	switch name {
	case appdef.AppEmodel, appdef.AppUiserv, appdef.AppHistory, appdef.AppNotifications, appdef.AppEapps:
		return true
	}
	return false
}

func needsMongo(name string) bool {
	return name == appdef.AppEproc
}

func bundleImageOr(ctx *NsGenContext, name, fallback string) string {
	if app, ok := ctx.Bundle.Applications[name]; ok && app.Image != "" {
		return app.Image
	}
	return fallback
}

func loadAppFiles(ctx *NsGenContext) {
	// Appfiles are loaded from embedded resources at daemon startup.
	// For now, this is a placeholder — the daemon will populate ctx.Files
	// from the go:embed filesystem before calling Generate().
}
