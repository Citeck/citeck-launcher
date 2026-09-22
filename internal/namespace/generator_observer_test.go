package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// observerBundle carries what a release that ships the observer names: the
// service itself, and its database in the `dependencies:` section, where a
// third-party image is invisible to launchers that have no version gate.
func observerBundle() *bundle.Def {
	return &bundle.Def{
		Applications: map[string]bundle.AppDef{
			appdef.AppObserver: {Image: "citeck/observer:1.1.0"},
		},
		Dependencies: map[string]bundle.AppDef{
			appdef.AppObsPostgres: {Image: "postgres:18.1"},
		},
	}
}

func observerWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: appdef.AppEmodel}}}
}

// The entry condition, and the whole point of the rework: the observer follows
// the BUNDLE. There is no namespace.yml flag any more — whether a stand has the
// service is a property of the release it runs, and a per-stand flag was a
// second place to keep that in sync.
func TestObserver_FollowsTheBundleAndNotAFlag(t *testing.T) {
	config.ResetDesktopMode()

	t.Run("no image in the bundle, no observer", func(t *testing.T) {
		resp, err := Generate(basicCfg(), &bundle.Def{}, observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)
		assert.Nil(t, findGeneratedApp(resp, appdef.AppObserver))
		assert.Nil(t, findGeneratedApp(resp, appdef.AppObsPostgres),
			"the database exists for the observer and must not outlive it")
		assert.NotContains(t, resp.Dependencies, deps.ObserverPostgres,
			"a namespace without it must not be seeded or pinned for it")
	})

	t.Run("image in the bundle, observer and its database", func(t *testing.T) {
		resp, err := Generate(basicCfg(), observerBundle(), observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)

		obs := findGeneratedApp(resp, appdef.AppObserver)
		require.NotNil(t, obs)
		assert.Equal(t, "citeck/observer:1.1.0", obs.Image)
		require.NotNil(t, findGeneratedApp(resp, appdef.AppObsPostgres))
		assert.Contains(t, resp.Dependencies, deps.ObserverPostgres,
			"the database is a registered dependency: it must be reported, or nothing pins it")
	})
}

// WillGenerateObserver is a RESTATEMENT of the condition above, used by the pin
// seeding that runs before Generate. The dangerous direction is answering FALSE
// wrongly — no pin means the bundle's image is applied to an existing cluster.
func TestObserver_PredictionMatchesTheGenerator(t *testing.T) {
	config.ResetDesktopMode()
	assert.True(t, WillGenerateObserver(basicCfg(), observerBundle(), observerWorkspace()))
	assert.False(t, WillGenerateObserver(basicCfg(), &bundle.Def{}, observerWorkspace()))
	assert.False(t, WillGenerateObserver(nil, observerBundle(), observerWorkspace()))
	assert.False(t, WillGenerateObserver(basicCfg(), nil, observerWorkspace()))
}

// The database's image comes from the bundle's `dependencies:` section and goes
// through the same gate the stand's own PostgreSQL does — which is the whole
// reason it was registered.
func TestObserver_DatabaseImageComesFromTheDependenciesSection(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), observerBundle(), observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
	require.NoError(t, err)

	pg := findGeneratedApp(resp, appdef.AppObsPostgres)
	require.NotNil(t, pg)
	assert.Equal(t, "postgres:18.1", pg.Image)
	assert.Equal(t, "postgres:18.1", resp.Dependencies[deps.ObserverPostgres].Effective)
}

// A namespace whose observer database already runs 17 does not follow a bundle
// offering 18: the major is where PostgreSQL's format break sits, so the move is
// held back and reported as an upgrade this launcher can perform — separately
// from the stand's own database, which has its own pin.
func TestObserver_DatabaseIsHeldBackAndReportedIndependently(t *testing.T) {
	config.ResetDesktopMode()
	resp, err := Generate(basicCfg(), observerBundle(), observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DependencyStates: map[deps.ID]deps.DependencyState{
			deps.ObserverPostgres: {Image: "postgres:17.5"},
		}})
	require.NoError(t, err)

	pg := findGeneratedApp(resp, appdef.AppObsPostgres)
	require.NotNil(t, pg)
	assert.Equal(t, "postgres:17.5", pg.Image, "the pin decides what runs, not the bundle")

	var found bool
	for _, up := range resp.DependencyUpgrades {
		if up.ID == deps.ObserverPostgres {
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
		resp, err := Generate(basicCfg(), observerBundle(), observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)
		pg := findGeneratedApp(resp, appdef.AppObsPostgres)
		require.NotNil(t, pg)
		assert.Contains(t, pg.Volumes, "obs_postgres2:/var/lib/postgresql")
		_, hasPGDATA := pg.Environments.Get("PGDATA")
		assert.False(t, hasPGDATA, "from 18 the image defaults PGDATA under the parent mount")
	})

	t.Run("17: data directory mounted, PGDATA explicit", func(t *testing.T) {
		bun := observerBundle()
		bun.Dependencies[appdef.AppObsPostgres] = bundle.AppDef{Image: "postgres:17.5"}
		resp, err := Generate(basicCfg(), bun, observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)
		pg := findGeneratedApp(resp, appdef.AppObsPostgres)
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
	resp, err := Generate(basicCfg(), observerBundle(), observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DependencyStates: map[deps.ID]deps.DependencyState{
			deps.ObserverPostgres: {Image: "postgres:18.1", VolumeGen: 2},
		}})
	require.NoError(t, err)

	pg := findGeneratedApp(resp, appdef.AppObsPostgres)
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

		resp, err := Generate(basicCfg(), observerBundle(), observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)

		pg := findGeneratedApp(resp, appdef.AppObsPostgres)
		require.NotNil(t, pg)
		assert.Contains(t, pg.Ports, "14524:5432",
			"without this an observer run outside the launcher cannot reach its database")

		obs := findGeneratedApp(resp, appdef.AppObserver)
		require.NotNil(t, obs)
		for _, want := range []string{"17016:17016", "17015:17015", "17017:17017", "17014:17014/udp"} {
			assert.Containsf(t, obs.Ports, want, "observer must publish %s", want)
		}

		// The cloud config is the other half of that workflow: it hands a
		// locally started observer the same endpoints as localhost.
		cc := resp.CloudConfig[appdef.AppObserver]
		require.NotNil(t, cc)
		assert.Equal(t, 14524, cc["database.port"])
		assert.Equal(t, "localhost", cc["database.host"])
	})

	t.Run("server mode publishes only the proxy", func(t *testing.T) {
		config.ResetDesktopMode()
		resp, err := Generate(basicCfg(), observerBundle(), observerWorkspace(), SystemSecrets{JWT: "j", OIDC: "o"})
		require.NoError(t, err)

		pg := findGeneratedApp(resp, appdef.AppObsPostgres)
		require.NotNil(t, pg)
		assert.Empty(t, pg.Ports, "server mode exposes nothing but the proxy")
	})
}
