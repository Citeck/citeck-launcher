package namespace

// Infrastructure service generators (mailpit, mongo, pgadmin, postgres,
// zookeeper, rabbitmq) — split out of generator.go by service family.
// Pure code motion; the orchestrating Generate stays in generator.go.

import (
	"fmt"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/docker"
)

// rabbitmqMemLimit is the generator's baseline RabbitMQ memory limit. See the
// generateRabbitMQ comment for the rationale. Hoisted to package scope so
// deriveRabbitMemoryConf can fall back to it when the effective (post-patch)
// def has no resources.
const rabbitmqMemLimit = "1g"

func generateMailpit(ctx *NsGenContext) {
	if ctx.Config.Email != nil {
		return
	}
	app := ctx.GetOrCreateApp(appdef.AppMailpit)
	app.Image = bundleImageOr(ctx, appdef.AppMailpit, "axllent/mailpit:v1.31.0")
	app.Kind = appdef.KindThirdParty
	// Keep the legacy "mailhog" network alias so the SMTP wiring resolves
	// without per-app rewiring: webapps connect via SPRING_MAIL_HOST=mailhog
	// (bundle default), Alfresco via MAIL_HOST=mailhog, and the proxy via
	// MAILHOG_TARGET=mailhog:8025. Mailpit is a drop-in replacement for mailhog
	// on the same SMTP (1025) and web-UI (8025) ports.
	app.NetworkAliases = []string{MailhogHost}
	app.AddPort("1025:1025").AddPort("8025:8025")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "128m"}}
}

func generateMongoDB(ctx *NsGenContext) {
	// Only eproc ever used mongo, and its data now lives in PostgreSQL, so
	// namespaces created from generation 2 onwards run without it
	// (Config.MongoEnabled resolves the flag against the config version, so an
	// existing namespace keeps its database).
	if !ctx.Config.MongoEnabled() {
		return
	}
	img := resolveAppImage(ctx, appdef.AppMongodb, ctx.Config.MongoDB.Image, "mongo:4.0.2")
	img = resolveDependencyImage(ctx, deps.MongoDB, img)
	app := ctx.GetOrCreateApp(appdef.AppMongodb)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	// No extra alias needed — Kotlin uses "mongo" as the hostname
	app.AddPort(fmt.Sprintf("27017:%d", MongoPort))
	app.AddVolume(resolveDependencyVolume(ctx, deps.MongoDB) + ":/data/db")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
	// StartupCondition: same exec probe as liveness, polled until ping
	// succeeds. Without this mongo jumps straight to RUNNING the moment the
	// container is up and liveness (below) starts firing while mongod is
	// still loading WiredTiger / replaying the journal. With FailureThreshold:3
	// that race could restart a healthy mongo within 90 s of start — same
	// failure mode as the pre-fix zookeeper case. Cold-start ping passes
	// within a few seconds on a populated volume, so happy-path startup time
	// is unchanged.
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			Exec:             &appdef.ExecProbeDef{Command: []string{"mongo", "--quiet", "--eval", "db.adminCommand('ping')"}},
			PeriodSeconds:    5,
			FailureThreshold: 60,
			TimeoutSeconds:   5,
		}},
	}
	app.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"mongo", "--quiet", "--eval", "db.adminCommand('ping')"}},
		FailureThreshold: livenessFailureThreshold,
		TimeoutSeconds:   5,
	}
}

func generatePgAdmin(ctx *NsGenContext) {
	// pgAdmin is a desktop-only convenience: its admin UI is published on a
	// fixed host port (5050) for local DB inspection. In server mode only the
	// proxy publishes ports, so the UI is unreachable there — skip it entirely
	// rather than run a 256m container nobody can reach.
	//
	// In desktop mode it's always generated (Kotlin 1.x parity — pgAdmin was on
	// by default). We intentionally do NOT gate on Config.PgAdmin.Enabled: the
	// Web UI never surfaces it as a real toggle (create/edit always send
	// pgAdminEnabled=false), so honoring the flag would mean pgAdmin never
	// appears at all.
	if !config.IsDesktopMode() {
		return
	}
	// Upstream publishes minor tags (9.17), not patch tags (9.17.0).
	fallback := "dpage/pgadmin4:9.17"
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.PgAdmin.Image != "" {
		fallback = string(ctx.WorkspaceConfig.PgAdmin.Image)
	}
	img := resolveAppImage(ctx, appdef.AppPgadmin, ctx.Config.PgAdmin.Image, fallback)
	app := ctx.GetOrCreateApp(appdef.AppPgadmin)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddPort("5050:80")
	app.AddEnv("PGADMIN_DEFAULT_EMAIL", "admin@admin.com")
	app.AddEnv("PGADMIN_DEFAULT_PASSWORD", ctx.Secrets.AdminPasswordOrDefault())
	app.AddVolume("pgadmin2:/var/lib/pgadmin")
	// servers.json pre-registers the postgres connection. The entrypoint
	// auto-imports it from the default /pgadmin4/servers.json on first DB init
	// only — we deliberately do NOT force a re-import on every start, so a
	// reused pgadmin2 volume keeps any servers the user added/edited manually.
	// From-scratch deploys (fresh volume) get the connection pre-filled.
	app.AddVolume("./pgadmin/servers.json:/pgadmin4/servers.json")
	// 400m: pgAdmin 4 9.x idles at ~250m, so 256m has no headroom and OOM-kills
	// under any use (and during the first-init server import). 400m gives
	// comfortable headroom for the import and real browsing without the waste of 512m.
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "400m"}}
}

func generatePostgres(ctx *NsGenContext) {
	// The launcher's OWN default does not move; a version bump comes from a
	// BUNDLE ("давай наверное всё-таки дефолт оставим на старой версии, а
	// повышать будем через бандлы"). A stand whose bundle names no postgres at
	// all is not asking for a new major, and offering it one turns the
	// dependency banner into an upgrade the operator never requested — from a
	// candidate that exists nowhere but in this line. `community-rc` already
	// declares 17.11, which is how a stand gets a newer PostgreSQL, and a
	// bundle that declares 18 gets the whole held-back-plus-migration flow.
	//
	// Concrete down to the patch, like every other default this launcher has
	// ever shipped: a floating tag would let even this frozen default resolve
	// afresh at the worst possible moment — the migration engine's pull-image
	// step pulls unconditionally, i.e. while the data is being moved.
	// See TestInfraImageDefaults.
	fallback := "postgres:17.5"
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Postgres.Image != "" {
		fallback = string(ctx.WorkspaceConfig.Postgres.Image)
	}
	img := resolveDependencyImage(ctx, deps.Postgres, bundleImageOr(ctx, appdef.AppPostgres, fallback))
	// The data layout follows the major of the image that will RUN — never the
	// candidate's. For a pinned namespace that is the pin, so an existing 17
	// keeps postgres2 and its explicit PGDATA byte for byte
	// (TestPostgres17PinnedDefIsByteStable); reading the candidate instead
	// would hand a 17 server the 18 layout and an empty volume.
	// An unparsable effective tag (":latest", a digest) falls back to the
	// LEGACY layout: it is the layout every namespace that predates PostgreSQL
	// 18 already has, so guessing it keeps existing data mounted, while
	// guessing 18 would silently start a brand-new empty cluster next to it.
	major := 17
	if v, ok := deps.ParseImageVersion(img); ok {
		major = v.Major
	}
	layout := deps.PostgresLayoutFor(major)
	app := ctx.GetOrCreateApp(appdef.AppPostgres)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.ShmSize = "128m"
	app.AddEnv("POSTGRES_USER", "postgres")
	app.AddEnv("POSTGRES_PASSWORD", "postgres")
	if layout.PGData != "" {
		app.AddEnv("PGDATA", layout.PGData)
	}
	app.AddPort(fmt.Sprintf("14523:%d", PGPort))
	app.AddVolume(resolveDependencyVolume(ctx, deps.Postgres) + ":" + layout.MountPath)
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
			PeriodSeconds: 10,
			// Kotlin-parity: AppProbeDef.failureThreshold default is 10_000
			// (effectively unbounded). The hard ceiling lives in the outer
			// 240s container-running wait in waitStartup, not here. Capping
			// at 60 retries (10 min) silently failed slow-importing realms
			// after a Keycloak DB reset.
			FailureThreshold: 10000,
			TimeoutSeconds:   5,
		}},
	}
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "1g"}}
	app.LivenessProbe = &appdef.AppProbeDef{
		Exec:             &appdef.ExecProbeDef{Command: []string{"pg_isready", "-U", "postgres"}},
		FailureThreshold: livenessFailureThreshold,
		TimeoutSeconds:   5,
	}
}

func generateZookeeper(ctx *NsGenContext) {
	fallback := "zookeeper:3.9.5"
	if ctx.WorkspaceConfig != nil && ctx.WorkspaceConfig.Zookeeper.Image != "" {
		fallback = string(ctx.WorkspaceConfig.Zookeeper.Image)
	}
	img := resolveDependencyImage(ctx, deps.Zookeeper, bundleImageOr(ctx, appdef.AppZookeeper, fallback))
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
	// One name, two mounts: the init container mkdirs the data and datalog
	// subdirectories the two ZOO_* env vars point at, so it must be handed the
	// volume the server will read. GetHashInput carries an init container's
	// IMAGE only, so a divergence here would recreate nothing and surface as a
	// server that cannot write its snapshots.
	zkVolume := resolveDependencyVolume(ctx, deps.Zookeeper)
	app.AddVolume(zkVolume + ":/citeck/zookeeper")
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: "512m"}}
	// StartupCondition: poll the admin server until /commands/ruok is up. The
	// ZK process opens 2181 in a few seconds but the embedded Jetty admin
	// (port 8080) lags 10–30 s behind on cold start, depending on JVM warmup.
	// Without a startup probe the app jumps straight to RUNNING and the
	// liveness probe (below) starts checking immediately, racing the admin
	// server start and accumulating spurious failures → restart loop
	// (observed: ↻8 within 30 min of testing).
	//
	// PeriodSeconds:5 + FailureThreshold:60 caps the wait at 5 minutes — well
	// past the worst observed cold-start. Startup time itself isn't extended
	// for the happy path: the first ruok succeeds within ~15 s and the app
	// transitions to RUNNING then, identical to the pre-fix path. Only the
	// "admin still warming" window is now correctly classified as STARTING
	// instead of RUNNING-with-failing-liveness.
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			HTTP:             &appdef.HTTPProbeDef{Path: "/commands/ruok", Port: 8080},
			PeriodSeconds:    5,
			FailureThreshold: 60,
			TimeoutSeconds:   5,
		}},
	}
	app.LivenessProbe = &appdef.AppProbeDef{
		HTTP:             &appdef.HTTPProbeDef{Path: "/commands/ruok", Port: 8080},
		FailureThreshold: livenessFailureThreshold,
		TimeoutSeconds:   5,
	}
	app.InitContainers = []appdef.InitContainerDef{{
		Image:   UtilsImage,
		Cmd:     []string{"/bin/sh", "-c", "mkdir -p /zkdir/data /zkdir/datalog"},
		Volumes: []string{zkVolume + ":/zkdir"},
	}}
}

// rabbitmqMemoryConf returns a rabbitmq.conf snippet telling the broker its
// real memory budget. RabbitMQ cannot reliably read a container's cgroup limit
// (it detects host RAM and sets the watermark to a fraction of that), so without
// this it never self-throttles and the cgroup OOM-killer terminates the broker —
// and the rabbitmqctl init actions — before the watermark ever applies.
// total_memory_available_override_value is derived in BYTES from the same limit
// string that fills HostConfig.Memory (docker.ParseMemory), so the override can
// never drift from the cgroup limit. relative 0.6 is RabbitMQ's default fraction,
// leaving ~40% headroom for the transient rabbitmqctl Erlang VMs that share the
// cgroup.
func rabbitmqMemoryConf(memLimit string) string {
	return fmt.Sprintf(
		"total_memory_available_override_value = %d\n"+
			"vm_memory_high_watermark.relative = 0.6\n",
		docker.ParseMemory(memLimit),
	)
}

func generateRabbitMQ(ctx *NsGenContext) {
	img := resolveDependencyImage(ctx, deps.RabbitMQ, bundleImageOr(ctx, appdef.AppRabbitmq, "rabbitmq:4.1.2-management"))
	app := ctx.GetOrCreateApp(appdef.AppRabbitmq)
	app.Image = img
	app.Kind = appdef.KindThirdParty
	app.AddPort(fmt.Sprintf("5672:%d", RMQPort))
	app.AddPort("15672:15672")
	app.AddEnv("RABBITMQ_DEFAULT_USER", "admin")
	app.AddEnv("RABBITMQ_DEFAULT_PASS", ctx.Secrets.AdminPasswordOrDefault())
	app.AddEnv("RABBITMQ_DEFAULT_VHOST", "/")
	app.AddEnv("RABBITMQ_MANAGEMENT_ALLOW_WEB_ACCESS", "true")
	app.AddVolume(resolveDependencyVolume(ctx, deps.RabbitMQ) + ":/var/lib/rabbitmq")
	// 1g (was 512m; the legacy value was 256m) — two independent pressures set the
	// floor:
	//   (1) Startup: RabbitMQ 4.x + management plus the transient Erlang VMs the
	//       rabbitmqctl init actions spawn OOM-kill (exitCode 137) below ~512m,
	//       stranding the citeck SA without vhost "/" permissions → every webapp
	//       fails AMQP auth.
	//   (2) Steady state: on the enterprise bundle (24 webapps holding AMQP
	//       connections + queues) the broker's working set (~350m) crossed the
	//       0.6×512m=307m high-watermark and raised a memory alarm that blocks all
	//       publishers. 1g moves the watermark to 0.6×1g=614m, well above enterprise
	//       steady state, while keeping the 40% cgroup headroom (see
	//       rabbitmqMemoryConf).
	// The limit is a cap, not a reservation: community rabbit (~60-100m) never
	// approaches it, so smaller bundles pay nothing for the enterprise headroom.
	app.Resources = &appdef.AppResourcesDef{Limits: appdef.LimitsDef{Memory: rabbitmqMemLimit}}
	// Memory-override conf so the broker self-throttles below the cgroup limit
	// instead of being OOM-killed (see rabbitmqMemoryConf). Mounted into conf.d,
	// which the official image loads alphabetically after its own
	// 10-defaultuser.conf, so RABBITMQ_DEFAULT_USER/PASS are preserved.
	ctx.Files["rabbitmq/citeck-memory.conf"] = []byte(rabbitmqMemoryConf(rabbitmqMemLimit))
	app.AddVolume("./rabbitmq/citeck-memory.conf:/etc/rabbitmq/conf.d/20-citeck-memory.conf:ro")
	// The probe deliberately leaves InitialDelaySeconds unset, and the default
	// waitForProbe applies (5 s) is load-bearing rather than cosmetic. This
	// exec lands as ROOT — the image declares no USER, it drops privileges
	// inside its own entrypoint — with HOME=/var/lib/rabbitmq, and an Erlang
	// tool that finds no $HOME/.erlang.cookie CREATES one, mode 0400, owned by
	// whoever ran it. The entrypoint's repair (`find /var/lib/rabbitmq
	// ! -user rabbitmq -exec chown rabbitmq {} +`) runs ONCE, before the
	// server, so a cookie that appears after it is never chowned and the
	// broker — which runs as 999 — dies with
	// `Error when reading /var/lib/rabbitmq/.erlang.cookie: eacces`.
	// Measured on a fresh volume in this exact shape (4.1.2-management and
	// 4.2.9-management alike): the broker writes its own cookie ~1.3 s after
	// container start, an exec at <= 0.45 s kills it 7 times out of 7, and one
	// at >= 0.78 s never did. The first probe fires ~5 s after the container
	// started (T14 dispatches the probe worker the moment StartContainer
	// returns), so the margin is ~10x — and it holds under starvation, because
	// the exec's own Erlang VM is slowed by the same shortage: at --cpus 0.25
	// and --cpus 0.1 an exec at t0+5.004 s left the cookie at 999:999 and the
	// broker running, 8 runs out of 8. Do NOT set InitialDelaySeconds to 0
	// here, and do not front this probe with an un-delayed one.
	app.StartupConditions = []appdef.StartupCondition{
		{Probe: &appdef.AppProbeDef{
			Exec:             &appdef.ExecProbeDef{Command: []string{"rabbitmq-diagnostics", "check_running", "-q"}},
			PeriodSeconds:    5,
			FailureThreshold: 24,
			TimeoutSeconds:   10,
		}},
	}
	// No LivenessProbe (Kotlin v1.3.8 parity). rabbitmq-diagnostics spawns a
	// short-lived Erlang VM for the check; with the memory headroom now in place
	// that is survivable, but a default-aggressive liveness (3× / 30s) would
	// still restart a healthy broker mid-operation. Init actions run after the
	// startup probe; restarting the container while they're in flight strands
	// set_permissions / set_user_tags in a "rabbit app not running" state,
	// breaking webapp auth. StartupConditions still gate readiness; container
	// exit codes still surface real crashes without polling.

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

// deriveRabbitMemoryConf overwrites the RabbitMQ memory conf so its
// total_memory_available_override_value tracks the EFFECTIVE (post-patch) memory
// limit of the rabbitmq app, not the generator constant. Called from Generate
// after patches are applied. A missing OR unparseable effective limit (operator
// deleted resources, or typed a malformed value via `citeck edit`) falls back to
// the const — the override must never be 0, or RabbitMQ believes it has no memory
// and raises an immediate alarm that blocks all publishers.
func deriveRabbitMemoryConf(ctx *NsGenContext, effective []appdef.ApplicationDef) {
	mem := rabbitmqMemLimit
	for i := range effective {
		if effective[i].Name == appdef.AppRabbitmq {
			if effective[i].Resources != nil {
				m := effective[i].Resources.Limits.Memory
				if m != "" && docker.ParseMemory(m) > 0 {
					mem = m
				}
			}
			ctx.Files["rabbitmq/citeck-memory.conf"] = []byte(rabbitmqMemoryConf(mem))
			return
		}
	}
}
