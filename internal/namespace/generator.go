package namespace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/appfiles"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"gopkg.in/yaml.v3"
)

// GenResp is the result of namespace generation.
type GenResp struct {
	Applications          []appdef.ApplicationDef
	Files                 map[string][]byte
	CloudConfig           map[string]map[string]any // per-app ext cloud config for CloudConfigServer
	DependsOnDetachedApps map[string]bool           // apps whose reattachment triggers regeneration
}

// GenerateOpts holds optional parameters for namespace generation.
type GenerateOpts struct {
	DetachedApps map[string]bool // manually stopped apps excluded from dependency graph
	SecretReader SecretReader    // resolves "secret:" references in config (nil = no resolution)
}

// Generate creates container definitions from a namespace config, bundle, and workspace config.
// Returns an error if a fatal generation step fails (e.g. rendering the Keycloak
// init script); callers should abort the reload/start on error rather than
// deploy a half-configured namespace.
func Generate(cfg *Config, bun *bundle.Def, wsCfg *bundle.WorkspaceConfig, secrets SystemSecrets, opts ...GenerateOpts) (*GenResp, error) {
	ctx := NewNsGenContext(cfg, bun)
	ctx.WorkspaceConfig = wsCfg
	ctx.Secrets = secrets
	if len(opts) > 0 {
		if opts[0].DetachedApps != nil {
			ctx.DetachedApps = opts[0].DetachedApps
		}
		if opts[0].SecretReader != nil {
			ctx.SecretReader = opts[0].SecretReader
		}
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
	if err := generateKeycloak(ctx); err != nil {
		return nil, fmt.Errorf("generate keycloak: %w", err)
	}
	generateAlfresco(ctx)
	generateObserver(ctx)

	// Generate webapps from bundle — only for apps declared in workspace config
	// (matching Kotlin: context.workspaceConfig.webappsById.contains(app.key))
	// Sort names for deterministic port assignment via NextPort().
	wsWebapps := make(map[string]bool)
	if wsCfg != nil {
		for _, w := range wsCfg.Webapps {
			wsWebapps[w.ID] = true
		}
	}
	webappNames := make([]string, 0, len(bun.Applications))
	for name := range bun.Applications {
		if len(wsWebapps) > 0 && !wsWebapps[name] {
			continue
		}
		webappNames = append(webappNames, name)
	}
	sort.Strings(webappNames)
	for _, name := range webappNames {
		generateWebapp(name, ctx)
	}

	// Generate proxy (depends on gateway, onlyoffice)
	generateProxy(ctx)
	generateOnlyOffice(ctx)

	// Server mode: only proxy publishes ports — all other apps are internal to Docker network.
	// Desktop mode: all ports published for local debugging (CloudConfigServer, direct DB access, etc.)
	if !config.IsDesktopMode() {
		for _, b := range ctx.Applications {
			if b.Name != appdef.AppProxy {
				b.Ports = nil
			}
		}
	}

	// Build all applications
	apps := make([]appdef.ApplicationDef, 0, len(ctx.Applications))
	for _, b := range ctx.Applications {
		apps = append(apps, b.Build())
	}

	// Fill VolumesContentHash for each app so the deployment hash changes
	// when any bind-mount source file's content changes — triggering a
	// container recreate. Mirrors Kotlin's NsRuntimeFiles.getPathsContentHash
	// hooked into ApplicationDef.hashField.
	for i := range apps {
		apps[i].VolumesContentHash = computeVolumesContentHash(&apps[i], ctx.Files)
	}

	// Compute DependsOnDetachedApps: detached apps that are referenced as dependencies
	// by other (non-detached) apps. Restarting these triggers regeneration.
	dependsOnDetached := make(map[string]bool)
	if len(ctx.DetachedApps) > 0 {
		for _, a := range apps {
			if ctx.DetachedApps[a.Name] {
				continue
			}
			for dep := range a.DependsOn {
				if ctx.DetachedApps[dep] {
					dependsOnDetached[dep] = true
				}
			}
		}
	}

	return &GenResp{
		Applications:          apps,
		Files:                 ctx.Files,
		CloudConfig:           ctx.CloudConfig,
		DependsOnDetachedApps: dependsOnDetached,
	}, nil
}

func generateMailhog(ctx *NsGenContext) {
	if ctx.Config.Email != nil {
		return
	}
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
	app.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"mongo", "--quiet", "--eval", "db.adminCommand('ping')"}},
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}
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
	app.AddEnv("PGADMIN_DEFAULT_PASSWORD", ctx.Secrets.AdminPasswordOrDefault())
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
		{Probe: &appdef.AppProbeDef{
			Exec: &appdef.ExecProbeDef{
				Command: []string{"/bin/sh", "-c", "psql -U postgres -d postgres -c 'SELECT 1' || exit 1"},
			},
			PeriodSeconds:    10,
			FailureThreshold: 60,
			TimeoutSeconds:   5,
		}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
	app.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"pg_isready", "-U", "postgres"}},
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}
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
	app.AddPort("17018:8080") // fixed admin port — outside webapp counter range
	app.AddEnv("ZOO_AUTOPURGE_PURGEINTERVAL", "1")
	app.AddEnv("ZOO_AUTOPURGE_SNAPRETAINCOUNT", "3")
	app.AddEnv("ZOO_4LW_COMMANDS_WHITELIST", "srvr,mntr,ruok,stat")
	app.AddEnv("ALLOW_ANONYMOUS_LOGIN", "yes")
	app.AddEnv("ZOO_DATA_DIR", "/citeck/zookeeper/data")
	app.AddEnv("ZOO_DATA_LOG_DIR", "/citeck/zookeeper/datalog")
	app.AddVolume("zookeeper2:/citeck/zookeeper")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
	app.LivenessProbe = &appdef.AppProbeDef{
		HTTP:             &appdef.HTTPProbeDef{Path: "/commands/ruok", Port: 8080},
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}
	app.InitContainers = []appdef.InitContainerDef{{
		Image:   UtilsImage,
		Cmd:     []string{"/bin/sh", "-c", "mkdir -p /zkdir/data /zkdir/datalog"},
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
	app.AddEnv("RABBITMQ_DEFAULT_PASS", ctx.Secrets.AdminPasswordOrDefault())
	app.AddEnv("RABBITMQ_DEFAULT_VHOST", "/")
	app.AddEnv("RABBITMQ_MANAGEMENT_ALLOW_WEB_ACCESS", "true")
	app.AddVolume("rabbitmq2:/var/lib/rabbitmq")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "256m"}}
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			Exec:             &appdef.ExecProbeDef{Command: []string{"rabbitmq-diagnostics", "check_running", "-q"}},
			PeriodSeconds:    5,
			FailureThreshold: 24,
			TimeoutSeconds:   10,
		}},
	}
	app.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"rabbitmq-diagnostics", "check_running", "-q"}},
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}

	// Ensure the stable "citeck" SA user exists in RabbitMQ. Webapps connect
	// as this user so admin-password changes never require recreating webapp
	// containers. InitActions run after the startup probe (see runtime_app.go).
	//
	// Idempotent: add_user fails when the user exists — swallow that, then
	// always set the current password (keeps SA synced with _citeck_sa). The
	// "monitoring" tag grants management-UI read access; AMQP publish/consume
	// is covered by vhost "/" full permissions below.
	if ctx.Secrets.CiteckSA != "" {
		saPass := ctx.Secrets.CiteckSA
		app.InitActions = append(app.InitActions,
			appdef.AppInitAction{Exec: []string{"rabbitmqctl", "add_user", CiteckSAUser, saPass}},
			appdef.AppInitAction{Exec: []string{"rabbitmqctl", "change_password", CiteckSAUser, saPass}},
			appdef.AppInitAction{Exec: []string{"rabbitmqctl", "set_user_tags", CiteckSAUser, "monitoring"}},
			appdef.AppInitAction{Exec: []string{"rabbitmqctl", "set_permissions", "-p", "/", CiteckSAUser, ".*", ".*", ".*"}},
		)
	}
}

func generateKeycloak(ctx *NsGenContext) error {
	dbName := "citeck_keycloak"

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
	// Use strict HTTPS if TLS is enabled or if external host (behind reverse proxy)
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
		fmt.Sprintf("--db-url=jdbc:postgresql://%s:%d/%s", PGHost, PGPort, dbName),
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
			PeriodSeconds:    10,
			FailureThreshold: 60,
			TimeoutSeconds:   5,
		}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
	app.LivenessProbe = &appdef.AppProbeDef{
		HTTP:             &appdef.HTTPProbeDef{Path: "/health/live", Port: 9000}, // Keycloak 26+ management interface
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
	alfPg.AddEnv("PGDATA", "/var/lib/postgresql/data")
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
	alfApp.Kind = appdef.KindCiteckAdditional
	alfPort := 17019 // fixed port for alfresco — not part of webapp counter
	alfApp.AddPort(fmt.Sprintf("%d:8080", alfPort))
	alfApp.AddDependsOn(appdef.AppAlfPostgres)
	alfApp.AddVolume("alf_content:/content")
	alfApp.AddVolume("./alfresco/alfresco_additional.properties:/tmp/alfresco/alfresco_additional.properties")
	alfApp.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{HTTP: &appdef.HTTPProbeDef{Path: "/alfresco/s/citeck/ecos/eureka-status", Port: 8080}}},
	}
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

	// Substitute RMQ credentials in alfresco properties (embedded appfile
	// has hardcoded "admin" — replace with the generated admin password).
	alfPropsKey := "alfresco/alfresco_additional.properties"
	if alfProps, ok := ctx.Files[alfPropsKey]; ok && ctx.Secrets.AdminPassword != "" {
		updated := strings.ReplaceAll(string(alfProps),
			"rabbitmq.export.server.password=admin", "rabbitmq.export.server.password="+ctx.Secrets.AdminPassword)
		updated = strings.ReplaceAll(updated,
			"rabbitmq.server.password=admin", "rabbitmq.server.password="+ctx.Secrets.AdminPassword)
		ctx.Files[alfPropsKey] = []byte(updated)
	}

	// 3. Alfresco Solr
	alfSolr := ctx.GetOrCreateApp(appdef.AppAlfSolr)
	alfSolr.Image = "nexus.citeck.ru/ess:1.1.0"
	alfSolr.Kind = appdef.KindCiteckAdditional
	alfSolr.AddPort("38080:8080")
	alfSolr.AddVolume("alf_solr_data:/opt/solr4_data")
	alfSolr.AddEnv("TWEAK_SOLR", "true")
	alfSolr.AddEnv("JAVA_OPTS", "-Xms1G -Xmx1G")
	alfSolr.AddEnv("ALFRESCO_HOST", appdef.AppAlfresco)
	alfSolr.AddEnv("ALFRESCO_PORT", "8080")
	alfSolr.AddEnv("ALFRESCO_INDEX_TRANSFORM_CONTENT", "false")
	alfSolr.AddEnv("ALFRESCO_RECORD_UNINDEXED_NODES", "false")
	alfSolr.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
}

func generateObserver(ctx *NsGenContext) {
	if !ctx.Config.Observer.Enabled {
		return
	}

	obsImage := ctx.Config.Observer.Image
	if obsImage == "" {
		obsImage = bundleImageOr(ctx, appdef.AppObserver, "citeck/observer:1.1.0")
	}

	const (
		// Observer ports: 17014–17017 (before ZK admin 17018, Alfresco 17019, webapps 17020+)
		obsLogUDP   = 17014 // UDP log receiver
		obsOTLPHTTP = 17015 // OTLP HTTP/protobuf receiver
		obsHTTP     = 17016 // HTTP API + embedded UI
		obsGRPC     = 17017 // OTLP gRPC receiver
		obsPGPort   = 14524 // published port for observer-postgres (local debugging)
		obsDBName   = "observer"
		obsDBUser   = "observer"
		obsDBPass   = "observer"
	)

	// 1. Observer Postgres — separate instance tuned for observability workload:
	// heavy writes (span/metric ingestion), aggregation queries, JSONB GIN lookups
	obsPg := ctx.GetOrCreateApp(appdef.AppObsPostgres)
	obsPg.Image = "postgres:18"
	obsPg.Kind = appdef.KindThirdParty
	obsPg.AddEnv("POSTGRES_DB", obsDBName)
	obsPg.AddEnv("POSTGRES_USER", obsDBUser)
	obsPg.AddEnv("POSTGRES_PASSWORD", obsDBPass)
	obsPg.AddEnv("PGDATA", "/var/lib/postgresql/data")
	obsPg.AddPort(fmt.Sprintf("%d:%d", obsPGPort, PGPort))
	obsPg.AddVolume("obs_postgres:/var/lib/postgresql/data")
	obsPg.Cmd = []string{
		"-c", "shared_buffers=256MB",
		"-c", "work_mem=32MB",
		"-c", "maintenance_work_mem=128MB",
		"-c", "effective_cache_size=1GB",
		"-c", "random_page_cost=1.1",
		"-c", "checkpoint_completion_target=0.9",
		"-c", "wal_buffers=16MB",
		"-c", "max_wal_size=1GB",
		"-c", "min_wal_size=256MB",
	}
	obsPg.StartupConditions = []appdef.StartupCondition{
		{Log: &appdef.LogStartupCondition{Pattern: ".*database system is ready to accept connections.*"}},
		{Probe: &appdef.AppProbeDef{
			Exec: &appdef.ExecProbeDef{
				Command: []string{"/bin/sh", "-c", fmt.Sprintf("pg_isready -U %s || exit 1", obsDBUser)},
			},
			PeriodSeconds:    10,
			FailureThreshold: 60,
			TimeoutSeconds:   5,
		}},
	}
	obsPg.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
	obsPg.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"pg_isready", "-U", obsDBUser}},
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}

	// 2. citeck-observer — env var names match the observer's Config struct
	// (reflection-based: database.host → DATABASE_HOST, zookeeper.hosts → ZOOKEEPER_HOSTS, etc.)
	obs := ctx.GetOrCreateApp(appdef.AppObserver)
	obs.Image = obsImage
	obs.Kind = appdef.KindThirdParty

	// Core server — all ports explicit (don't rely on observer defaults)
	obs.AddEnv("SERVER_MODE", "dev")
	obs.AddEnv("SERVER_PORT", fmt.Sprintf("%d", obsHTTP))
	obs.AddEnv("OTLP_GRPC_PORT", fmt.Sprintf("%d", obsGRPC))
	obs.AddEnv("OTLP_HTTP_PORT", fmt.Sprintf("%d", obsOTLPHTTP))
	obs.AddEnv("LOG_RECEIVER_UDP_PORT", fmt.Sprintf("%d", obsLogUDP))

	// Observer's own database
	obs.AddEnv("DATABASE_HOST", ObsPGHost)
	obs.AddEnv("DATABASE_PORT", fmt.Sprintf("%d", PGPort))
	obs.AddEnv("DATABASE_NAME", obsDBName)
	obs.AddEnv("DATABASE_USER", obsDBUser)
	obs.AddEnv("DATABASE_PASSWORD", obsDBPass)
	obs.AddEnv("DATABASE_TLS_SSL_MODE", "disable")

	// ZooKeeper discovery
	obs.AddEnv("ZOOKEEPER_HOSTS", fmt.Sprintf("%s:%d", ZKHost, ZKPort))
	obs.AddEnv("DISCOVERY_HOST", appdef.AppObserver)
	obs.AddEnv("DISCOVERY_APP_NAME", appdef.AppObserver)

	// Auth — same JWT secret as all webapps
	obs.AddEnv("AUTH_JWT_SECRET", ctx.Secrets.JWT)
	obs.AddEnv("CORS_ALLOWED_ORIGINS", "*")

	// Infrastructure monitoring — RabbitMQ via Management API. Uses the
	// stable "citeck" SA (monitoring tag) so admin-password rotations don't
	// invalidate observer's credentials.
	obs.AddEnv("RMQ_MONITOR_ENABLED", "true")
	obs.AddEnv("RMQ_MONITOR_URL", fmt.Sprintf("http://%s:15672", RMQHost))
	obsRMQUser := "admin"
	obsRMQPass := ctx.Secrets.AdminPasswordOrDefault()
	if ctx.Secrets.CiteckSA != "" {
		obsRMQUser = CiteckSAUser
		obsRMQPass = ctx.Secrets.CiteckSA
	}
	obs.AddEnv("RMQ_MONITOR_USER", obsRMQUser)
	obs.AddEnv("RMQ_MONITOR_PASSWORD", obsRMQPass)

	// Infrastructure monitoring — PostgreSQL via pg_stat views
	obs.AddEnv("PG_MONITOR_ENABLED", "true")
	pgTarget := fmt.Sprintf(`[{"name":"citeck","host":"%s","port":%d,"user":"postgres","password":"postgres"}]`, PGHost, PGPort) //nolint:gocritic // sprintfQuotedString: JSON template requires literal quotes, not %q
	obs.AddEnv("PG_MONITOR_TARGETS", pgTarget)

	// Infrastructure monitoring — ZooKeeper via "mntr" command
	obs.AddEnv("ZK_MONITOR_ENABLED", "true")
	obs.AddEnv("ZK_MONITOR_HOSTS", fmt.Sprintf("%s:%d", ZKHost, ZKPort))

	obs.AddPort(fmt.Sprintf("%d:%d", obsHTTP, obsHTTP))
	obs.AddPort(fmt.Sprintf("%d:%d", obsGRPC, obsGRPC))
	obs.AddPort(fmt.Sprintf("%d:%d", obsOTLPHTTP, obsOTLPHTTP))
	obs.AddPort(fmt.Sprintf("%d:%d/udp", obsLogUDP, obsLogUDP))
	obs.AddDependsOn(appdef.AppObsPostgres)
	obs.AddDependsOn(appdef.AppZookeeper)
	obs.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			HTTP:             &appdef.HTTPProbeDef{Path: "/health", Port: obsHTTP},
			PeriodSeconds:    10,
			FailureThreshold: 30,
			TimeoutSeconds:   5,
		}},
	}
	obs.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
	obs.LivenessProbe = &appdef.AppProbeDef{
		HTTP:             &appdef.HTTPProbeDef{Path: "/health", Port: obsHTTP},
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}

	// 3. Cloud config for CloudConfigServer (local debugging: "stop in launcher, run locally")
	extCloudConfig := map[string]any{
		// Server — explicit ports for local debugging
		"server.port":           obsHTTP,
		"otlp.grpc_port":        obsGRPC,
		"otlp.http_port":        obsOTLPHTTP,
		"log_receiver.udp_port": obsLogUDP,
		// Observer's own database (localhost with published port)
		"database.host":         "localhost",
		"database.port":         obsPGPort,
		"database.name":         obsDBName,
		"database.user":         obsDBUser,
		"database.password":     obsDBPass,
		"database.tls.ssl_mode": "disable",
		// ZooKeeper
		"zookeeper.hosts": "localhost:2181",
		// Auth
		"auth.jwt_secret": ctx.Secrets.JWT,
		// Infrastructure monitoring — RabbitMQ (citeck SA; see above)
		"rmq_monitor.enabled":  true,
		"rmq_monitor.url":      "http://localhost:15672",
		"rmq_monitor.user":     obsRMQUser,
		"rmq_monitor.password": obsRMQPass,
		// Infrastructure monitoring — ZooKeeper
		"zk_monitor.enabled": true,
		"zk_monitor.hosts":   "localhost:2181",
		// Infrastructure monitoring — main PostgreSQL (webapp databases)
		"pg_monitor.enabled": true,
	}
	ctx.CloudConfig[appdef.AppObserver] = extCloudConfig
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
		applyWebappDefaults(app, &ctx.WorkspaceConfig.DefaultWebappProps, ctx)

		// Level 2: per-app workspace defaults
		for _, wsCfg := range ctx.WorkspaceConfig.Webapps {
			if wsCfg.ID == name {
				applyWebappDefaults(app, &wsCfg.DefaultProps, ctx)
				break
			}
		}
	}

	// Java opts from namespace config (overrides workspace defaults)
	var javaOpts string
	var springProfiles string
	var debugPort int
	if wp, ok := ctx.Config.Webapps[name]; ok { //nolint:nestif // config override logic is inherently nested
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
		for p := range strings.SplitSeq(springProfiles, ",") {
			p = strings.TrimSpace(p)
			if p != "" && p != "dev" && p != "launcher" {
				profiles = append(profiles, p)
			}
		}
	}
	app.AddEnv("SPRING_PROFILES_ACTIVE", strings.Join(profiles, ","))
	addWebappInfraEnv(app, ctx)
	app.AddEnv("ECOS_WEBAPP_ZOOKEEPER_HOST", ZKHost)
	app.AddEnv("ECOS_INIT_DELAY", "0")
	app.AddEnv("SPRING_CLOUD_CONFIG_ENABLED", "false") // CloudConfigServer on :8761 is for local debug only
	app.AddEnv("SPRING_CONFIG_IMPORT", "")
	app.AddEnv("ECOS_WEBAPP_WEB_AUTHENTICATORS_JWT_SECRET", ctx.Secrets.JWT)
	app.AddPort(fmt.Sprintf("%d:%d", port, port))
	app.AddDependsOn(ZKHost)
	app.AddDependsOn(RMQHost)
	// Always declare the keycloak dependency so the webapp's deployment hash
	// stays stable across auth-mode switches (KEYCLOAK ↔ BASIC). In BASIC mode
	// the keycloak container is not generated; waitForDeps treats missing deps
	// as satisfied, so startup ordering still works correctly. Without this,
	// flipping auth mode churns every webapp container unnecessarily.
	app.AddDependsOn(appdef.AppKeycloak)

	if javaOpts != "" {
		app.AddEnv("JAVA_OPTS", strings.TrimSpace(javaOpts))
	}

	processWebappDataSources(name, app, ctx)
	configureWebappProbes(name, app, ctx, port)

	// EAPPS special handling: add init containers from bundle citeckApps
	applyEappsInitContainers(name, app, ctx)

	// External SMTP for notifications app
	if ctx.Config.Email != nil && name == appdef.AppNotifications {
		applyEmailConfig(app, ctx)
	}

	// External S3 for content app
	if ctx.Config.S3 != nil && name == appdef.AppContent {
		applyS3Config(app, ctx)
	}
}

// configureWebappProbes sets startup and liveness probes for a webapp.
func configureWebappProbes(name string, app *AppBuilder, ctx *NsGenContext, port int) {
	// Startup probe: 90 × 10s = 15 min — Java webapps can be slow on first start.
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			HTTP: &appdef.HTTPProbeDef{
				Path: "/management/health",
				Port: port,
			},
			PeriodSeconds:    10,
			FailureThreshold: 90,
			TimeoutSeconds:   5,
		}},
	}

	livenessDisabled := false
	if wp, ok := ctx.Config.Webapps[name]; ok {
		livenessDisabled = wp.LivenessDisabled
	}
	if !livenessDisabled {
		app.LivenessProbe = &appdef.AppProbeDef{
			HTTP:             &appdef.HTTPProbeDef{Path: "/management/health", Port: port},
			FailureThreshold: 3,
			TimeoutSeconds:   5,
		}
	}
}

// applyEmailConfig configures external SMTP settings for the notifications app
// via environment variables. Env vars override the bundle default (SPRING_MAIL_HOST=mailhog).
func applyEmailConfig(app *AppBuilder, ctx *NsGenContext) {
	email := ctx.Config.Email
	protocol := "smtp"
	if email.TLS {
		protocol = "smtps"
	}
	app.AddEnv("SPRING_MAIL_HOST", email.Host)
	app.AddEnv("SPRING_MAIL_PORT", fmt.Sprintf("%d", email.Port))
	app.AddEnv("SPRING_MAIL_PROTOCOL", protocol)
	app.AddEnv("ECOS_NOTIFICATIONS_EMAIL_FROM_DEFAULT", email.From)
	app.AddEnv("ECOS_NOTIFICATIONS_EMAIL_FROM_FIXED", email.From)
	if email.Username != "" {
		app.AddEnv("SPRING_MAIL_USERNAME", email.Username)
	}
	if email.Password != "" {
		if pwd, err := resolveSecret(ctx.SecretReader, email.Password); err == nil {
			app.AddEnv("SPRING_MAIL_PASSWORD", pwd)
		} else {
			slog.Warn("Failed to resolve email password secret", "error", err)
		}
	}
}

// applyS3Config configures external S3 storage settings for the content app.
// Note: bucket and region are stored in namespace.yml S3Config for reference and
// configured at the app level (content-storage endpoint), not passed as env vars here.
func applyS3Config(app *AppBuilder, ctx *NsGenContext) {
	s3 := ctx.Config.S3
	app.AddEnv("ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_URL", s3.Endpoint)
	app.AddEnv("ECOS_ENDPOINT_CONTENT_STORAGE_S3_ENDPOINT_CREDENTIALS", "content-storage-s3-credentials")
	app.AddEnv("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_TYPE", "BASIC")
	app.AddEnv("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_USERNAME", s3.AccessKey)
	if sk, err := resolveSecret(ctx.SecretReader, s3.SecretKey); err == nil {
		app.AddEnv("ECOS_SECRET_CONTENT_STORAGE_S3_CREDENTIALS_PASSWORD", sk)
	} else {
		slog.Warn("Failed to resolve S3 secret key", "error", err)
	}
}

// applyEappsInitContainers adds init containers from bundle citeckApps for the eapps service.
func applyEappsInitContainers(name string, app *AppBuilder, ctx *NsGenContext) {
	if name != appdef.AppEapps || ctx.Bundle == nil || len(ctx.Bundle.CiteckApps) == 0 {
		return
	}
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

// addWebappInfraEnv sets shared infrastructure env vars (RabbitMQ host +
// credentials) on a webapp builder. Extracted from generateWebapp to keep its
// cyclomatic complexity under the linter threshold.
//
// Webapps connect to RabbitMQ as the stable "citeck" SA (not "admin"), so
// admin-password rotations don't churn the webapp container spec. The SA
// is created/synced in RabbitMQ by generateRabbitMQ's InitActions.
func addWebappInfraEnv(app *AppBuilder, ctx *NsGenContext) {
	app.AddEnv("ECOS_WEBAPP_RABBITMQ_HOST", RMQHost)
	if ctx.Secrets.CiteckSA != "" {
		app.AddEnv("ECOS_WEBAPP_RABBITMQ_USERNAME", CiteckSAUser)
		app.AddEnv("ECOS_WEBAPP_RABBITMQ_PASSWORD", ctx.Secrets.CiteckSA)
	}
}

// applyWebappDefaults applies a WebappDefaultProps layer to an app builder.
func applyWebappDefaults(app *AppBuilder, props *bundle.WebappDefaultProps, ctx *NsGenContext) {
	if props == nil {
		return
	}
	if props.Image != "" {
		app.Image = props.Image
	}
	for k, v := range props.Environments {
		app.AddEnv(k, resolveTemplateVarsWithContext(v, ctx))
	}
	if props.HeapSize != "" {
		app.AddEnv("JAVA_OPTS", fmt.Sprintf("-Xmx%s -Xms%s", props.HeapSize, props.HeapSize))
	}
	if props.MemoryLimit != "" {
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

	// Find webapp datasources: workspace defaults, then namespace overrides on top
	dataSources := make(map[string]bundle.DataSourceConfig)
	for _, webapp := range ctx.WorkspaceConfig.Webapps {
		if webapp.ID == appName {
			maps.Copy(dataSources, webapp.DefaultProps.DataSources)
			break
		}
	}
	// Namespace-level dataSources override workspace defaults
	if wp, ok := ctx.Config.Webapps[appName]; ok {
		maps.Copy(dataSources, wp.DataSources)
	}

	pgApp := ctx.Applications[appdef.AppPostgres]

	// Build cloud config maps for both local (webappCloudConfig) and external (extCloudConfig)
	webappCloudConfig := make(map[string]any)
	extCloudConfig := make(map[string]any)

	for dsKey, dsCfg := range dataSources {
		url := resolveTemplateVars(dsCfg.URL)
		dsPrefix := "ecos.webapp.dataSources." + dsKey

		if strings.HasPrefix(url, "jdbc:") { //nolint:nestif // datasource config wiring
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
	// Uses deep merge so nested keys from workspace defaults are preserved.
	if wp, ok := ctx.Config.Webapps[appName]; ok {
		deepMergeMaps(webappCloudConfig, wp.CloudConfig)
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

// deepMergeMaps recursively merges src into dst. For keys present in both maps,
// if both values are map[string]any, they are merged recursively; otherwise src wins.
func deepMergeMaps(dst, src map[string]any) {
	for k, srcVal := range src {
		if dstVal, ok := dst[k]; ok {
			dstMap, dstIsMap := dstVal.(map[string]any)
			srcMap, srcIsMap := srcVal.(map[string]any)
			if dstIsMap && srcIsMap {
				deepMergeMaps(dstMap, srcMap)
				continue
			}
		}
		dst[k] = srcVal
	}
}

// rewriteDataSourceURLForLocalhost rewrites a datasource URL to use localhost with published ports.
func rewriteDataSourceURLForLocalhost(url, _ string) string {
	if strings.HasPrefix(url, "jdbc:postgresql://") {
		// Rewrite postgres host:port to localhost with published port.
		url = strings.Replace(url, fmt.Sprintf("%s:%d", PGHost, PGPort), "localhost:14523", 1)
	} else if strings.HasPrefix(url, "mongodb://") {
		// Rewrite mongo host:port to localhost with published port.
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
			if i == len(parts)-1 { //nolint:nestif // nested map building
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
// resolveTemplateVarsWithContext resolves template variables including
// config- and secrets-dependent ones. Used by applyWebappDefaults when
// populating environment variables from workspace defaults.
//
// ${KK_ADMIN_USER} and ${KK_ADMIN_PASSWORD} resolve to the "citeck" service
// account credentials. Webapps use these to authenticate to Keycloak for
// admin operations (user management, role checks). Using the SA instead
// of admin decouples webapp auth from the user-facing admin password.
func resolveTemplateVarsWithContext(s string, ctx *NsGenContext) string {
	kkEnabled := "false"
	if ctx != nil && ctx.Config != nil && ctx.Config.Authentication.Type == AuthKeycloak {
		kkEnabled = "true"
	}
	// KK_ADMIN_URL is always set (Kotlin NsGenContext.VARS)
	kkAdminURL := fmt.Sprintf("http://%s:8080", KKHost)
	// Use the dedicated "citeck" SA for webapp→Keycloak integration.
	// This avoids coupling webapps to the user-facing admin password.
	kkAdminUser := CiteckSAUser
	kkAdminPassword := "admin" // fallback for tests
	if ctx != nil && ctx.Secrets.CiteckSA != "" {
		kkAdminPassword = ctx.Secrets.CiteckSA
	}
	s = strings.ReplaceAll(s, "${KK_ENABLED}", kkEnabled)
	s = strings.ReplaceAll(s, "${KK_ADMIN_URL}", kkAdminURL)
	s = strings.ReplaceAll(s, "${KK_ADMIN_USER}", kkAdminUser)
	s = strings.ReplaceAll(s, "${KK_ADMIN_PASSWORD}", kkAdminPassword)
	return resolveTemplateVars(s)
}

func resolveTemplateVars(s string) string {
	replacements := map[string]string{
		"${PG_HOST}":         PGHost,
		"${PG_PORT}":         fmt.Sprintf("%d", PGPort),
		"${MONGO_HOST}":      MongoHost,
		"${MONGO_PORT}":      fmt.Sprintf("%d", MongoPort),
		"${ZK_HOST}":         ZKHost,
		"${ZK_PORT}":         fmt.Sprintf("%d", ZKPort),
		"${RMQ_HOST}":        RMQHost,
		"${RMQ_PORT}":        fmt.Sprintf("%d", RMQPort),
		"${MAILHOG_HOST}":    MailhogHost,
		"${ONLYOFFICE_HOST}": OnlyofficeHost,
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
	"integrations": true, "edi": true, appdef.AppContent: true,
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
	maps.Copy(ctx.Files, files)
}

// computeVolumesContentHash returns a deterministic SHA-256 of the content
// of every bind-mount source file referenced by `app` (own volumes + init
// container volumes). Feeds into ApplicationDef.GetHashInput so the
// deployment hash changes when any bind-mounted file content changes —
// prompting the runtime to recreate the container with the fresh content.
//
// Only `./...` host paths are hashed. Named volumes (`pgdata:/var/lib/...`)
// and absolute host paths (`/etc/foo:/inside`) aren't touched — those are
// either Docker-managed state or out-of-scope of the embedded file set.
func computeVolumesContentHash(app *appdef.ApplicationDef, files map[string][]byte) string {
	keys := collectFileKeysFromVolumes(app.Volumes)
	for _, ic := range app.InitContainers {
		for _, k := range collectFileKeysFromVolumes(ic.Volumes) {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys) // stable order — map iteration isn't
	h := sha256.New()
	for _, k := range keys {
		content, ok := files[k]
		if !ok {
			continue
		}
		// Include the key itself so renaming a file produces a different hash
		// even if the content happens to match another file.
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(content)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// collectFileKeysFromVolumes extracts the ctx.Files keys (e.g.
// "postgres/postgresql.conf") from a list of Docker volume specs. Strips
// the leading "./" and everything from the first colon onwards. Skips
// specs that don't reference a bind-mounted file (named volumes, abs
// host paths).
func collectFileKeysFromVolumes(vols []string) []string {
	var keys []string
	for _, v := range vols {
		// "./postgres/postgresql.conf:/etc/postgresql/postgresql.conf[:ro]"
		if !strings.HasPrefix(v, "./") {
			continue
		}
		host := strings.TrimPrefix(v, "./")
		if idx := strings.Index(host, ":"); idx >= 0 {
			host = host[:idx]
		}
		if host != "" {
			keys = append(keys, host)
		}
	}
	return keys
}
