package namespace

// Proxy (nginx front) generator — split out of generator.go by service family.
// Pure code motion; the orchestrating Generate stays in generator.go.

import (
	"fmt"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
)

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
		oidcSecret := ctx.Secrets.OIDC
		app.AddEnv("CLIENT_SECRET", oidcSecret)

		// Update lua file with correct scheme, URLs, and OIDC secret
		luaKey := "proxy/lua_oidc_full_access.lua"
		if luaBytes, ok := ctx.Files[luaKey]; ok {
			lua := string(luaBytes)
			lua = strings.Replace(lua, `redirect_uri_scheme = "http"`, fmt.Sprintf(`redirect_uri_scheme = %q`, ctx.ProxyScheme()), 1)
			lua = strings.Replace(lua, `redirect_after_logout_uri = "http://localhost/ecos-idp/auth/realms/ecos-app/protocol/openid-connect/logout"`,
				fmt.Sprintf(`redirect_after_logout_uri = "%s/ecos-idp/auth/realms/ecos-app/protocol/openid-connect/logout"`, ctx.ProxyBaseURL()), 1)
			lua = strings.Replace(lua, `post_logout_redirect_uri = "http://localhost"`,
				fmt.Sprintf(`post_logout_redirect_uri = %q`, ctx.ProxyBaseURL()), 1)
			lua = strings.Replace(lua, `client_secret = "2996117d-9a33-4e06-b48a-867ce6a235db"`,
				fmt.Sprintf(`client_secret = %q`, oidcSecret), 1)
			ctx.Files[luaKey] = []byte(lua)
		}

		// Substitute OIDC secret + admin password hash in realm JSON.
		// Realm is imported by keycloak on first container start only; on
		// existing installs the password can be changed via
		// `citeck setup admin-password` which drives the keycloak admin API.
		realmKey := "keycloak/ecos-app-realm.json"
		if realmBytes, ok := ctx.Files[realmKey]; ok {
			oldSecret := `"secret": "2996117d-9a33-4e06-b48a-867ce6a235db"` //nolint:gosec // template placeholder, not a real credential
			newSecret := `"secret": "` + oidcSecret + `"`
			realm := strings.Replace(string(realmBytes), oldSecret, newSecret, 1)
			ctx.Files[realmKey] = []byte(realm)
		}

		app.AddVolume("./proxy/lua_oidc_full_access.lua:/tmp/lua_oidc_full_access.lua:ro")
		app.InitActions = append(app.InitActions,
			appdef.AppInitAction{
				Exec: []string{"sh", "-c", "cp /tmp/lua_oidc_full_access.lua /etc/nginx/includes/lua_oidc_full_access.lua"},
			},
			appdef.AppInitAction{
				Exec: []string{"sh", "-c",
					"sed -i -e '/location \\/ecos-idp\\/auth\\/ {/a\\\n" +
						"    rewrite ^/ecos-idp/auth/(.*)\\$ /\\$1 break;\n' " +
						"-e 's|http://keycloak:8080/auth/|http://keycloak:8080/|g' /etc/nginx/conf.d/default.conf"},
			},
		)
		hasInitActions = true
	}

	app.AddEnv("RABBITMQ_TARGET", fmt.Sprintf("%s:15672", RMQHost))
	app.AddEnv("ENABLE_LOGGING", "warn")
	app.AddEnv("ENABLE_SERVER_STATUS", "true")
	// Mailhog target only when mailhog is present (no external email configured)
	if ctx.Config.Email == nil {
		app.AddEnv("MAILHOG_TARGET", MailhogHost+":8025")
	}
	app.AddEnv("ECOS_PAGE_TITLE", "Citeck Launcher")

	proxyImg := ctx.Config.Proxy.Image
	if proxyImg == "" {
		proxyImg = bundleImageOr(ctx, appdef.AppProxy, "")
	}
	app.Image = proxyImg

	proxyTarget := fmt.Sprintf("%s:%s", appdef.AppGateway, gatewayPort)
	hasTLSCert := ctx.TLSEnabled() && ctx.Config.Proxy.TLS.CertPath != ""
	containerPort := 80
	if hasTLSCert {
		containerPort = 443
	}

	var startupProbe *appdef.AppProbeDef
	if hasTLSCert {
		startupProbe = &appdef.AppProbeDef{Exec: &appdef.ExecProbeDef{
			Command: []string{"sh", "-c", "curl -sf -o /dev/null http://localhost:80/eis.json"},
		}}
	} else {
		startupProbe = &appdef.AppProbeDef{HTTP: &appdef.HTTPProbeDef{Path: "/eis.json", Port: 80}}
	}
	startupProbe.PeriodSeconds = 10
	startupProbe.FailureThreshold = 30
	startupProbe.TimeoutSeconds = 5

	app.AddEnv("DEFAULT_LOCATION_V2", "true")
	app.AddEnv("GATEWAY_TARGET", fmt.Sprintf("%s:%s", appdef.AppGateway, gatewayPort))
	app.AddEnv("ECOS_INIT_DELAY", "0")
	alfrescoEnabled := ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Alfresco.Enabled && ctx.Applications[appdef.AppAlfresco] != nil && !ctx.DetachedApps[appdef.AppAlfresco]
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

	if hasTLSCert {
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
