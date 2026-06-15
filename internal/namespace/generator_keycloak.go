package namespace

// Keycloak generator — split out of generator.go by service family.
// Pure code motion; the orchestrating Generate stays in generator.go.

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/appfiles"
)

func generateKeycloak(ctx *NsGenContext) error {
	dbName := KeycloakDBName

	// Always create keycloak DB in postgres — avoids DB restart when keycloak is later enabled
	if pgApp := ctx.Applications[appdef.AppPostgres]; pgApp != nil {
		pgApp.InitActions = append(pgApp.InitActions, appdef.AppInitAction{
			Exec: []string{"sh", "-c", "/init_db_and_user.sh " + dbName},
		})
	}

	if ctx.Config.Authentication.Type != AuthKeycloak {
		return nil
	}

	kcFallback := "keycloak/keycloak:26.4.5"
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Keycloak.Image != "" {
		kcFallback = ctx.WorkspaceConfig.Keycloak.Image
	}
	img := bundleImageOr(ctx, appdef.AppKeycloak, kcFallback)
	app := ctx.GetOrCreateApp(appdef.AppKeycloak)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	// KC_BOOTSTRAP_ADMIN_* is consumed by keycloak on first container boot
	// only (when the database is empty). We seed the master-realm admin
	// with the same generated password used for the ecos-app realm admin
	// so there's one credential for the user to remember across both
	// realms. Falls back to "admin" in tests where ctx.Secrets is empty.
	app.AddEnv("KC_BOOTSTRAP_ADMIN_USERNAME", "admin")
	app.AddEnv("KC_BOOTSTRAP_ADMIN_PASSWORD", ctx.Secrets.AdminPasswordOrDefault())
	// Use strict HTTPS if TLS is enabled or if external host (behind reverse proxy).
	// Same caveat as namespace.ProxyScheme(): wrong for raw-IP HTTP-only installs
	// without a TLS terminator. Will revisit together with proxy.publicScheme.
	strictHTTPS := ctx.TLSEnabled() || !ctx.IsLocalHost()
	app.AddEnv("KC_HOSTNAME_STRICT_HTTPS", fmt.Sprintf("%v", strictHTTPS))
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
		"--db-url=" + KeycloakDBJDBCURL(),
		"--db-username=" + dbName,
		"--db-password=" + dbName,
		"--proxy-headers=xforwarded",
		"--import-realm",
	}

	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			Exec: &appdef.ExecProbeDef{
				Command: []string{"bash", "/healthcheck.sh"},
			},
			PeriodSeconds: 10,
			// Kotlin-parity: AppProbeDef.failureThreshold default is 10_000.
			// Large realm imports on fresh DB easily exceed 10 min — keep the
			// startup window effectively unbounded (outer 240s wait still
			// gates container-up). Liveness threshold stays at 3 below.
			FailureThreshold: 10000,
			TimeoutSeconds:   5,
		}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
	// Publish KC management port (container 9000) on a fixed infra-admin
	// host port — 17013 sits in the same cluster as ZK admin 17018 and
	// Alfresco 17019 and is high enough to avoid the popular 9000 collision
	// space (Portainer, SonarQube, Adminer, MinIO, php-fpm). Without the
	// publish, the HTTP liveness probe falls back to
	// http://<container-ip>:9000, which is unreachable from the host on
	// rootless Docker (Linux) and Docker Desktop (macOS) — container IP
	// isn't routed across the user-namespace / VM boundary — so liveness
	// fails 3× ≈ 30s and KC enters a restart loop the moment auth switches
	// to KEYCLOAK. In server mode this port is stripped by the non-proxy
	// strip block (only proxy publishes), and the probe falls back to the
	// container IP, which IS reachable under rootful Docker on a Linux
	// server. The HTTP probe Port stays at the *container-side* 9000;
	// reconciler.GetPublishedPort translates that to 17013 on the host.
	app.AddPort(fmt.Sprintf("%d:%d", KCManagementHostPort, KCManagementContainerPort))
	app.LivenessProbe = &appdef.AppProbeDef{
		HTTP:             &appdef.HTTPProbeDef{Path: "/health/live", Port: KCManagementContainerPort},
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}

	// Generate kcadm init script. Uses a dedicated "citeck" service account
	// in the master realm so that admin password changes and snapshot
	// imports don't break launcher operations. The script body lives in
	// internal/appfiles/embedded/keycloak/init.sh.tmpl; see RenderKeycloakInitScript.
	{
		script, err := appfiles.RenderKeycloakInitScript(appfiles.KeycloakInitParams{
			SAUser:        CiteckSAUser,
			SAPassword:    ctx.Secrets.CiteckSA,
			LegacySAUser:  LegacyCiteckSAUser,
			AdminPassword: ctx.Secrets.AdminPasswordOrDefault(),
			BaseURL:       ctx.ProxyBaseURL(),
			OIDCSecret:    ctx.Secrets.OIDC,
			ProxyPublic:   ctx.ProxyHost() != "localhost" || ctx.TLSEnabled(),
			DBUrl:         KeycloakDBJDBCURL(),
			DBUser:        dbName,
			DBPass:        dbName,
		})
		if err != nil {
			// Surface rendering failures: a missing init script breaks admin
			// password application, SA bootstrap, and OIDC client setup.
			// Aborting generation is preferable to silently producing a
			// namespace that cannot authenticate itself.
			return fmt.Errorf("render keycloak init script: %w", err)
		}
		ctx.Files["keycloak/update-client-config.sh"] = []byte(script)
		app.AddVolume("./keycloak/update-client-config.sh:/opt/keycloak/scripts/update-client-config.sh")
		app.InitActions = append(app.InitActions, appdef.AppInitAction{
			Exec: []string{"sh", "-c", "bash /opt/keycloak/scripts/update-client-config.sh"},
		})
	}
	return nil
}
