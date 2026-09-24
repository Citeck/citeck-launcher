package namespace

import (
	"os"
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The observer is CONFIGURATION: the launcher has no code for it. These tests
// generate it from testdata/observer-workspace.yml — the reference copy of the
// block both workspace configs carry — and pin that the declared observer and
// its database behave as the built-in ones did: same entry condition (the
// bundle names the images), same gate, layout and volume generation for the
// database, same published ports and cloud config.

const (
	observerApp = "observer"
	observerDB  = "observer-postgres"
	observerDep = deps.ID(observerDB)
)

// observerBundle carries what a release that ships the observer names: the
// service itself, and its database in the `dependencies:` section, where a
// third-party image is invisible to launchers that have no version gate.
func observerBundle() *bundle.Def {
	return &bundle.Def{
		Applications: map[string]bundle.AppDef{
			observerApp: {Image: "citeck/observer:1.1.0"},
		},
		Dependencies: map[string]bundle.AppDef{
			observerDB: {Image: "postgres:18.1"},
		},
	}
}

// observerWorkspace is the reference declaration. It is pure: registering the
// declared database in the dependency registry is generateObserverNs's job.
func observerWorkspace() *bundle.WorkspaceConfig {
	data, err := os.ReadFile("testdata/observer-workspace.yml")
	if err != nil {
		panic(err)
	}
	var ws bundle.WorkspaceConfig
	if err := yaml.Unmarshal(data, &ws); err != nil {
		panic(err)
	}
	if err := bundle.ValidateAdditionalApps(ws.AdditionalApps); err != nil {
		panic(err)
	}
	ws.Webapps = []bundle.WebappConfig{{ID: appdef.AppEmodel}}
	return &ws
}

// observerSecrets is what a namespace holds once it has stored the default.
func observerSecrets() GenerateOpts {
	return GenerateOpts{NamespaceSecrets: map[string]string{"observer-db": "observer"}}
}

// generateObserverNs generates with the reference declaration, having first
// registered its database exactly as the daemon does before it generates.
func generateObserverNs(t *testing.T, bun *bundle.Def, opts GenerateOpts) *GenResp {
	t.Helper()
	ws := observerWorkspace()
	descs := make([]deps.Descriptor, 0, 1)
	for _, s := range DatabaseSpecs(ws) {
		descs = append(descs, s.Descriptor())
	}
	deps.SetExtraDependencies(descs)
	t.Cleanup(deps.ResetExtraDependencies)
	resp, err := Generate(basicCfg(), bun, ws, SystemSecrets{JWT: "j", OIDC: "o", CiteckSA: "sa"}, opts)
	require.NoError(t, err)
	return resp
}

// The entry condition, and the whole point of the rework: the observer follows
// the BUNDLE. There is no namespace.yml flag any more — whether a stand has the
// service is a property of the release it runs, and a per-stand flag was a
// second place to keep that in sync.
func TestObserver_FollowsTheBundleAndNotAFlag(t *testing.T) {
	config.ResetDesktopMode()

	t.Run("no image in the bundle, no observer", func(t *testing.T) {
		resp := generateObserverNs(t, &bundle.Def{}, observerSecrets())
		assert.Nil(t, findGeneratedApp(resp, observerApp))
		assert.Nil(t, findGeneratedApp(resp, observerDB),
			"the database exists for the observer and must not outlive it")
		assert.NotContains(t, resp.Dependencies, observerDep,
			"a namespace without it must not be seeded or pinned for it")
	})

	t.Run("image in the bundle, observer and its database", func(t *testing.T) {
		resp := generateObserverNs(t, observerBundle(), observerSecrets())

		obs := findGeneratedApp(resp, observerApp)
		require.NotNil(t, obs)
		assert.Equal(t, "citeck/observer:1.1.0", obs.Image)
		require.NotNil(t, findGeneratedApp(resp, observerDB))
		assert.Contains(t, resp.Dependencies, observerDep,
			"the database is a registered dependency: it must be reported, or nothing pins it")
	})
}

// The pin seeding runs BEFORE Generate and restates "does this namespace run
// the observer's database" through WillGenerateDatabases. The dangerous
// direction is answering FALSE wrongly — no pin means the bundle's image is
// applied to an existing cluster.
func TestObserver_DatabasePredictionMatchesTheGenerator(t *testing.T) {
	config.ResetDesktopMode()
	ws := observerWorkspace()
	assert.True(t, WillGenerateDatabases(basicCfg(), observerBundle(), ws)[observerDB])
	assert.False(t, WillGenerateDatabases(basicCfg(), &bundle.Def{}, ws)[observerDB])
	assert.Contains(t, WillGenerateDatabases(basicCfg(), &bundle.Def{}, ws), observerDB,
		"answered with an explicit false, or the seeding probes it on every load")
}

// A service whose secret did not arrive is not started, and its database is
// the one place that matters most: initialized with an empty password, it
// keeps it. Neither half runs without the namespace's value.
func TestObserver_NeedsItsNamespaceSecret(t *testing.T) {
	config.ResetDesktopMode()
	resp := generateObserverNs(t, observerBundle(), GenerateOpts{})
	assert.Nil(t, findGeneratedApp(resp, observerDB), "never initialized without its password")
	assert.Nil(t, findGeneratedApp(resp, observerApp), "and the observer goes with its database")
	assert.NotNil(t, findGeneratedApp(resp, appdef.AppPostgres), "the rest of the stand is untouched")

	resp = generateObserverNs(t, observerBundle(), observerSecrets())
	db := findGeneratedApp(resp, observerDB)
	require.NotNil(t, db)
	v, _ := db.Environments.Get("POSTGRES_PASSWORD")
	assert.Equal(t, "observer", v)
	obs := findGeneratedApp(resp, observerApp)
	require.NotNil(t, obs)
	v, _ = obs.Environments.Get("DATABASE_PASSWORD")
	assert.Equal(t, "observer", v, "both halves read the same value")
}

// A bundle that names the observer but not its database gets no observer: the
// dependency prune removes it (the observer cannot run without its database —
// unlike rag, which starts without a vector store).
func TestObserver_WithoutItsDatabaseInTheBundleIsNotGenerated(t *testing.T) {
	config.ResetDesktopMode()
	bun := observerBundle()
	delete(bun.Dependencies, observerDB)
	resp := generateObserverNs(t, bun, observerSecrets())
	assert.Nil(t, findGeneratedApp(resp, observerDB))
	assert.Nil(t, findGeneratedApp(resp, observerApp))
}

// The declared observer carries what the built-in one did: the JWT secret the
// webapps sign with, the citeck service account for RabbitMQ monitoring, and
// the main database as a monitoring target.
func TestObserver_WiresThePlatformSecretsAndTargets(t *testing.T) {
	config.ResetDesktopMode()
	resp := generateObserverNs(t, observerBundle(), observerSecrets())
	obs := findGeneratedApp(resp, observerApp)
	require.NotNil(t, obs)
	for k, want := range map[string]string{
		"AUTH_JWT_SECRET":      "j",
		"RMQ_MONITOR_USER":     CiteckSAUser,
		"RMQ_MONITOR_PASSWORD": "sa",
		"RMQ_MONITOR_URL":      "http://" + RMQHost + ":15672",
		"ZOOKEEPER_HOSTS":      ZKHost + ":2181",
		"PG_MONITOR_TARGETS":   `[{"name":"citeck","host":"postgres","port":5432,"user":"postgres","password":"postgres"}]`,
	} {
		got, ok := obs.Environments.Get(k)
		assert.True(t, ok, k)
		assert.Equal(t, want, got, k)
	}
	// A Citeck service, listed with the other additional apps rather than among
	// the third-party infrastructure (user, 2026-09-24).
	assert.Equal(t, appdef.KindCiteckAdditional, obs.Kind)
	assert.Contains(t, obs.DependsOn, observerDB)
	assert.Contains(t, obs.DependsOn, appdef.AppZookeeper)
}

// The database's image comes from the bundle's `dependencies:` section and goes
// through the same gate the stand's own PostgreSQL does — which is the whole
// reason it was registered.
func TestObserver_DatabaseImageComesFromTheDependenciesSection(t *testing.T) {
	config.ResetDesktopMode()
	resp := generateObserverNs(t, observerBundle(), observerSecrets())

	pg := findGeneratedApp(resp, observerDB)
	require.NotNil(t, pg)
	assert.Equal(t, "postgres:18.1", pg.Image)
	assert.Equal(t, "postgres:18.1", resp.Dependencies[observerDep].Effective)
}

// A namespace whose observer database already runs 17 does not follow a bundle
// offering 18: the major is where PostgreSQL's format break sits, so the move is
// held back and reported as an upgrade this launcher can perform — separately
// from the stand's own database, which has its own pin.
func TestObserver_DatabaseIsHeldBackAndReportedIndependently(t *testing.T) {
	config.ResetDesktopMode()
	opts := observerSecrets()
	opts.DependencyStates = map[deps.ID]deps.DependencyState{
		observerDep: {Image: "postgres:17.5"},
	}
	resp := generateObserverNs(t, observerBundle(), opts)

	pg := findGeneratedApp(resp, observerDB)
	require.NotNil(t, pg)
	assert.Equal(t, "postgres:17.5", pg.Image, "the pin decides what runs, not the bundle")

	var found bool
	for _, up := range resp.DependencyUpgrades {
		if up.ID == observerDep {
			found = true
			assert.Equal(t, "postgres:18.1", up.To)
			assert.True(t, up.Migratable, "the dump/restore plan applies to this cluster too")
		}
	}
	assert.True(t, found, "a held-back cluster must be REPORTED, or the operator never learns of it")

	// And the stand's own database is untouched by any of it.
	main := findGeneratedApp(resp, appdef.AppPostgres)
	require.NotNil(t, main)
	assert.Equal(t, "postgres:17.5", main.Image)
	for _, up := range resp.DependencyUpgrades {
		assert.NotEqual(t, deps.Postgres, up.ID, "nothing about the observer moves the stand's own postgres")
	}
}

// The LAYOUT follows the major that will RUN — the same rule generatePostgres
// applies. Getting this wrong starts an empty cluster beside existing data.
func TestObserver_DatabaseLayoutFollowsTheRunningMajor(t *testing.T) {
	config.ResetDesktopMode()

	t.Run("18: parent mount, no explicit PGDATA", func(t *testing.T) {
		resp := generateObserverNs(t, observerBundle(), observerSecrets())
		pg := findGeneratedApp(resp, observerDB)
		require.NotNil(t, pg)
		assert.Contains(t, pg.Volumes, "obs_postgres2:/var/lib/postgresql")
		_, hasPGDATA := pg.Environments.Get("PGDATA")
		assert.False(t, hasPGDATA, "from 18 the image defaults PGDATA under the parent mount")
	})

	t.Run("17: data directory mounted, PGDATA explicit", func(t *testing.T) {
		bun := observerBundle()
		bun.Dependencies[observerDB] = bundle.AppDef{Image: "postgres:17.5"}
		resp := generateObserverNs(t, bun, observerSecrets())
		pg := findGeneratedApp(resp, observerDB)
		require.NotNil(t, pg)
		assert.Contains(t, pg.Volumes, "obs_postgres2:/var/lib/postgresql/data")
		pgdata, ok := pg.Environments.Get("PGDATA")
		require.True(t, ok)
		assert.Equal(t, "/var/lib/postgresql/data", pgdata)
	})
}

// A completed migration moves this cluster to the next generation, and ONLY
// this cluster: the two pins are independent.
func TestObserver_DatabaseVolumeFollowsItsOwnGenerationCounter(t *testing.T) {
	config.ResetDesktopMode()
	opts := observerSecrets()
	opts.DependencyStates = map[deps.ID]deps.DependencyState{
		observerDep: {Image: "postgres:18.1", VolumeGen: 2},
	}
	resp := generateObserverNs(t, observerBundle(), opts)

	pg := findGeneratedApp(resp, observerDB)
	require.NotNil(t, pg)
	assert.Contains(t, pg.Volumes, "obs_postgres3:/var/lib/postgresql")

	main := findGeneratedApp(resp, appdef.AppPostgres)
	require.NotNil(t, main)
	assert.Contains(t, main.Volumes, "postgres2:/var/lib/postgresql/data",
		"the stand's own database stays on its own generation")
}

// The observer's database publishes its port in DESKTOP mode for the same
// reason the stand's own PostgreSQL publishes 14523: "stop it in the launcher,
// run it from an IDE" needs the database reachable on localhost, and an
// observer started outside the namespace has nowhere else to connect.
//
// Server mode drops every non-proxy publish (see Generate), so the assertion is
// mode-specific on purpose — not because the port is optional there, but
// because nothing but the proxy is reachable from outside at all.
func TestObserver_DatabasePortIsPublishedForRunningItOutsideTheNamespace(t *testing.T) {
	t.Run("desktop", func(t *testing.T) {
		t.Setenv("CITECK_DESKTOP", "true")
		config.ResetDesktopMode()
		defer config.ResetDesktopMode()

		resp := generateObserverNs(t, observerBundle(), observerSecrets())

		pg := findGeneratedApp(resp, observerDB)
		require.NotNil(t, pg)
		assert.Contains(t, pg.Ports, "14524:5432",
			"without this an observer run outside the launcher cannot reach its database")

		obs := findGeneratedApp(resp, observerApp)
		require.NotNil(t, obs)
		for _, want := range []string{"17016:17016", "17015:17015", "17017:17017", "17014:17014/udp"} {
			assert.Containsf(t, obs.Ports, want, "observer must publish %s", want)
		}

		// The cloud config is the other half of that workflow: it hands a
		// locally started observer the same endpoints as localhost.
		cc := resp.CloudConfig[observerApp]
		require.NotNil(t, cc)
		assert.Equal(t, 14524, cc["database.port"])
		assert.Equal(t, "localhost", cc["database.host"])
	})

	t.Run("server mode publishes only the proxy", func(t *testing.T) {
		config.ResetDesktopMode()
		resp := generateObserverNs(t, observerBundle(), observerSecrets())

		pg := findGeneratedApp(resp, observerDB)
		require.NotNil(t, pg)
		assert.Empty(t, pg.Ports, "server mode exposes nothing but the proxy")
	})
}

// The declared probes carry the launcher's liveness tolerance: a config entry
// cannot name the constant, so the reference copy states its value and this
// keeps the two from drifting (TestGeneratorLivenessTolerance does the same
// for every generated probe).
func TestObserver_DeclaredLivenessUsesTheLaunchersTolerance(t *testing.T) {
	config.ResetDesktopMode()
	resp := generateObserverNs(t, observerBundle(), observerSecrets())
	for _, name := range []string{observerApp, observerDB} {
		app := findGeneratedApp(resp, name)
		require.NotNil(t, app, name)
		require.NotNil(t, app.LivenessProbe, name)
		assert.Equal(t, livenessFailureThreshold, app.LivenessProbe.FailureThreshold, name)
	}
}

// A TYPED entry is generated by its own generator and never by the raw-entry
// path. Found on a live stand: once a raw entry no longer needed an image, the
// observer's database also reached generateAdditionalApps, which refused it as
// a collision with the cluster generateDatabases had just emitted — an ERROR in
// every generation's log for a perfectly valid configuration.
func TestObserver_TypedEntryIsNotAlsoTreatedAsARawOne(t *testing.T) {
	config.ResetDesktopMode()
	logs := captureLogs(t)
	resp := generateObserverNs(t, observerBundle(), observerSecrets())
	require.NotNil(t, findGeneratedApp(resp, observerDB))
	assert.NotContains(t, logs.String(), "collides with a built-in app")
}
