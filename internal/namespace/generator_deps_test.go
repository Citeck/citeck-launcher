package namespace

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

func depsTestConfig() *Config {
	return &Config{
		ID:             "ns1",
		Authentication: AuthenticationProps{Type: AuthBasic, Users: []string{"admin"}},
		Proxy:          ProxyProps{Port: 80},
	}
}

// depsTestWorkspace declares one webapp id so Generate's webapp loop keeps its
// production shape. That loop treats EVERY bundle application as a webapp when
// the workspace config lists none (`len(wsWebapps) > 0 && !wsWebapps[name]`),
// so an empty workspace config would send the bundle's `postgres:` entry into
// the webapp loop — where the collision guard added in 7047c51 now skips it
// with an error rather than overwriting the infra builder. Keeping the real
// shape here means these tests exercise the gate, not that guard (which has
// its own test below). A real workspace config always lists its webapps, and
// infra is never among them.
//
// The one webapp is the gateway, and generateCfgWithPins puts a gateway into
// every bundle to match: the proxy hard-depends on it, so a bundle without one
// has its proxy pruned and every generation in this file logged an ERROR about
// a missing dependency that has nothing to do with what is being tested.
func depsTestWorkspace() *bundle.WorkspaceConfig {
	return &bundle.WorkspaceConfig{Webapps: []bundle.WebappConfig{{ID: appdef.AppGateway}}}
}

// A data-derived pin is a Docker Hub reference the launcher INVENTED (the
// descriptor's legacy image, or "postgres:<major>" read out of PG_VERSION). On
// a stand that pulls from a mirror, emitting it verbatim would make the
// held-back dependency the one container that cannot be pulled.
func TestHeldBackPinFollowsTheCandidatesRepository(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "mirror.example.com/library/postgres:18"},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: deps.PostgresLegacyImage})
	assert.Equal(t, "mirror.example.com/library/postgres:17", appByName(t, resp, appdef.AppPostgres).Image)
	assert.Equal(t, "mirror.example.com/library/postgres:17", resp.Dependencies[deps.Postgres].Effective)
	u := upgradeFor(t, resp, deps.Postgres)
	assert.Equal(t, "mirror.example.com/library/postgres:17", u.From,
		"the migration's source container is built from the image that can actually be pulled")
	assert.Equal(t, "mirror.example.com/library/postgres:18", u.To)
}

// The same-repository case is every ordinary namespace, and it must stay
// BYTE-identical — this is the other half of the hash-stability golden.
func TestHeldBackPinOnTheSameRepositoryIsUntouched(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:18"},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: deps.PostgresLegacyImage})
	assert.Equal(t, deps.PostgresLegacyImage, appByName(t, resp, appdef.AppPostgres).Image)
}

// A pin read off a real CONTAINER already names the registry the image came
// from — including a mirror the bundle has since moved away from — so it is
// evidence, not a guess, and nothing here may rewrite it.
func TestAContainerDerivedPinKeepsItsOwnRepository(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "mirror.example.com/library/postgres:18"},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: "old-registry.example.com/postgres:17"})
	assert.Equal(t, "old-registry.example.com/postgres:17", appByName(t, resp, appdef.AppPostgres).Image)
}

// A pin the launcher cannot split (a digest reference) has no tag to keep, so
// it is emitted exactly as it stands.
func TestAnUnsplittablePinIsNotRehomed(t *testing.T) {
	const digestPin = "postgres@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "mirror.example.com/library/postgres:18"},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: digestPin})
	assert.Equal(t, digestPin, appByName(t, resp, appdef.AppPostgres).Image)
}

func generateWithPins(t *testing.T, bun *bundle.Def, pins map[deps.ID]string) *GenResp {
	t.Helper()
	return generateCfgWithPins(t, depsTestConfig(), bun, pins)
}

// generateCfgWithPins runs one generation against the caller's bundle, which is
// completed with the gateway depsTestWorkspace declares (see there). The gateway
// carries no dataSources, so it contributes no init action, no dependency and no
// env to any infra def — the byte-stability golden is unaffected by its presence.
func generateCfgWithPins(t *testing.T, cfg *Config, bun *bundle.Def, pins map[deps.ID]string) *GenResp {
	t.Helper()
	apps := map[string]bundle.AppDef{appdef.AppGateway: {Image: "citeck/gateway:1.0.0"}}
	if bun != nil {
		maps.Copy(apps, bun.Applications)
	}
	resp, err := Generate(cfg, &bundle.Def{Applications: apps}, depsTestWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"}, GenerateOpts{DependencyPins: pins})
	require.NoError(t, err)
	return resp
}

// upgradeFor finds the reported upgrade for one dependency: every generation
// here also emits mongo (these configs predate the flag), so an assertion on
// resp.DependencyUpgrades[0] alone would not say which dependency it is about,
// and an upgrade added to a fixture later would shift the index under it. Every
// identity assertion in this file goes through here; the two ORDER tests below
// index deliberately, because the index is what they are about.
func upgradeFor(t *testing.T, resp *GenResp, id deps.ID) *DependencyUpgrade {
	t.Helper()
	for i := range resp.DependencyUpgrades {
		if resp.DependencyUpgrades[i].ID == id {
			return &resp.DependencyUpgrades[i]
		}
	}
	return nil
}

// TestPostgres17PinnedDefIsByteStable is the hash-stability contract: a
// namespace pinned at PostgreSQL 17 must generate exactly the def it did before
// dependency pins existed, or rolling the feature out recreates every postgres.
// The golden file was captured on the pre-feature generator (image postgres:17.5).
func TestPostgres17PinnedDefIsByteStable(t *testing.T) {
	resp := generateWithPins(t, nil, map[deps.ID]string{deps.Postgres: "postgres:17.5"})
	pg := appByName(t, resp, appdef.AppPostgres)
	require.NotNil(t, pg)
	got := pg.GetHashInput()
	golden := filepath.Join("testdata", "postgres17.hashinput.golden")
	if *updateGolden {
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

func TestPinHoldsBreakingCandidateAndReportsUpgrade(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:18"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: "postgres:17.5"})
	pg := appByName(t, resp, appdef.AppPostgres)
	assert.Equal(t, "postgres:17.5", pg.Image)
	assert.Contains(t, pg.Volumes, "postgres2:/var/lib/postgresql/data")
	require.Len(t, resp.DependencyUpgrades, 1, "nothing else may be held back")
	up := upgradeFor(t, resp, deps.Postgres)
	require.NotNil(t, up)
	assert.Equal(t, DependencyUpgrade{ID: deps.Postgres, App: "postgres", From: "postgres:17.5", To: "postgres:18", Migratable: true}, *up)
	assert.Equal(t, DependencyGen{Effective: "postgres:17.5", Candidate: "postgres:18"}, resp.Dependencies[deps.Postgres])
}

func TestPinLetsANonBreakingCandidateThrough(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:17.11"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: "postgres:17.5"})
	assert.Equal(t, "postgres:17.11", appByName(t, resp, appdef.AppPostgres).Image)
	assert.Empty(t, resp.DependencyUpgrades)
}

func TestNoPinAppliesCandidateAndDefaultIs18(t *testing.T) {
	resp := generateWithPins(t, nil, nil)
	pg := appByName(t, resp, appdef.AppPostgres)
	assert.Equal(t, "postgres:18", pg.Image)
	assert.Contains(t, pg.Volumes, "postgres3:/var/lib/postgresql")
	_, hasPGData := pg.Environments.Get("PGDATA")
	assert.False(t, hasPGData, "18 layout must leave PGDATA to the image default")
	assert.Empty(t, resp.DependencyUpgrades)
	assert.Equal(t, DependencyGen{Effective: "postgres:18", Candidate: "postgres:18"}, resp.Dependencies[deps.Postgres])
}

func TestNonMigratableDependencyIsHeldAndReported(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppRabbitmq: {Image: "rabbitmq:4.2.9-management"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.RabbitMQ: "rabbitmq:4.1.2-management"})
	assert.Equal(t, "rabbitmq:4.1.2-management", appByName(t, resp, appdef.AppRabbitmq).Image)
	require.Len(t, resp.DependencyUpgrades, 1, "nothing else may be held back")
	up := upgradeFor(t, resp, deps.RabbitMQ)
	require.NotNil(t, up)
	assert.False(t, up.Migratable, "this launcher ships no RabbitMQ migration")
}

func TestUnknownCandidateTagIsHeld(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:latest"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: "postgres:17.5"})
	assert.Equal(t, "postgres:17.5", appByName(t, resp, appdef.AppPostgres).Image)
	require.Len(t, resp.DependencyUpgrades, 1, "nothing else may be held back")
	up := upgradeFor(t, resp, deps.Postgres)
	require.NotNil(t, up)
	assert.Equal(t, "postgres:latest", up.To, "the unreadable tag is what is being offered")
}

func TestUpgradesAreReportedInRegistryOrder(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppRabbitmq: {Image: "rabbitmq:5.0.0-management"},
		appdef.AppPostgres: {Image: "postgres:18"},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{
		deps.Postgres: "postgres:17.5", deps.RabbitMQ: "rabbitmq:4.1.2-management",
	})
	require.Len(t, resp.DependencyUpgrades, 2)
	assert.Equal(t, deps.Postgres, resp.DependencyUpgrades[0].ID)
	assert.Equal(t, deps.RabbitMQ, resp.DependencyUpgrades[1].ID)
}

// TestUpgradesAreReorderedFromGeneratorOrder pins sortedUpgrades itself: the
// generators run zookeeper BEFORE rabbitmq (Generate), while the registry —
// which is the display order everywhere — lists rabbitmq first. Postgres and
// rabbitmq alone cannot see the difference, since they are in the same relative
// order in both.
func TestUpgradesAreReorderedFromGeneratorOrder(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppZookeeper: {Image: "zookeeper:3.10.0"},
		appdef.AppRabbitmq:  {Image: "rabbitmq:4.2.9-management"},
	}}
	resp := generateWithPins(t, bun, map[deps.ID]string{
		deps.Zookeeper: "zookeeper:3.9.5", deps.RabbitMQ: "rabbitmq:4.1.2-management",
	})
	require.Len(t, resp.DependencyUpgrades, 2)
	assert.Equal(t, deps.RabbitMQ, resp.DependencyUpgrades[0].ID)
	assert.Equal(t, deps.Zookeeper, resp.DependencyUpgrades[1].ID)
}

// Keycloak migrates its own schema forward on start and cannot go back, so a
// major bump must be held for the user to decide — and this launcher has no
// migrator for it.
func TestKeycloakMajorBumpIsHeldByThePin(t *testing.T) {
	cfg := depsTestConfig()
	cfg.Authentication = AuthenticationProps{Type: AuthKeycloak}
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppKeycloak: {Image: "keycloak/keycloak:27.0.1"}}}
	resp := generateCfgWithPins(t, cfg, bun, map[deps.ID]string{deps.Keycloak: "keycloak/keycloak:26.4.5"})
	assert.Equal(t, "keycloak/keycloak:26.4.5", appByName(t, resp, appdef.AppKeycloak).Image)
	up := upgradeFor(t, resp, deps.Keycloak)
	require.NotNil(t, up)
	assert.Equal(t, DependencyUpgrade{ID: deps.Keycloak, App: appdef.AppKeycloak,
		From: "keycloak/keycloak:26.4.5", To: "keycloak/keycloak:27.0.1", Migratable: false}, *up)
}

// Mongo takes its image from the namespace config rather than the bundle, so
// its gate sits on a different line and needs its own test.
func TestMongoMajorBumpIsHeldByThePin(t *testing.T) {
	cfg := depsTestConfig()
	cfg.MongoDB = MongoDbProps{Image: "mongo:7.0.5"}
	resp := generateCfgWithPins(t, cfg, nil, map[deps.ID]string{deps.MongoDB: "mongo:4.0.2"})
	assert.Equal(t, "mongo:4.0.2", appByName(t, resp, appdef.AppMongodb).Image)
	up := upgradeFor(t, resp, deps.MongoDB)
	require.NotNil(t, up)
	assert.Equal(t, "mongo:7.0.5", up.To)
	assert.Equal(t, DependencyGen{Effective: "mongo:4.0.2", Candidate: "mongo:7.0.5"}, resp.Dependencies[deps.MongoDB])
}

// An effective image whose tag carries no version (":latest", a digest) gets
// the LEGACY layout. Guessing 18 for data that is almost certainly on the old
// layout would mount a brand-new empty volume and start an empty cluster; the
// legacy guess at worst leaves an 18 server pointed at its own explicit PGDATA.
func TestUnparsableTagKeepsTheLegacyLayout(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:latest"}}}
	pg := appByName(t, generateWithPins(t, bun, nil), appdef.AppPostgres)
	assert.Equal(t, "postgres:latest", pg.Image)
	assert.Contains(t, pg.Volumes, "postgres2:/var/lib/postgresql/data")
	pgData, ok := pg.Environments.Get("PGDATA")
	assert.True(t, ok)
	assert.Equal(t, "/var/lib/postgresql/data", pgData)
}

// TestPinSurvivesAWorkspaceConfigWithNoWebapps closes the one way the gate could
// be bypassed silently. Generate's webapp loop admits EVERY bundle application
// when the workspace config lists no webapps, and GetOrCreateApp then returns
// the infra builder — so without the collision guard a bundle entry named
// "postgres" overwrote the pin-resolved image AFTER resolveDependencyImage had
// recorded the hold: the container ran the breaking candidate while GenResp
// reported it held back, which is worse than either outcome alone.
func TestPinSurvivesAWorkspaceConfigWithNoWebapps(t *testing.T) {
	// The gateway is here for the same reason as in generateCfgWithPins: without
	// it the proxy is pruned and the run logs a missing-dependency ERROR beside
	// the collision ERROR this test is actually about.
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:18"},
		appdef.AppGateway:  {Image: "citeck/gateway:1.0.0"},
	}}
	resp, err := Generate(depsTestConfig(), bun, &bundle.WorkspaceConfig{}, SystemSecrets{JWT: "j", OIDC: "o"},
		GenerateOpts{DependencyPins: map[deps.ID]string{deps.Postgres: "postgres:17.5"}})
	require.NoError(t, err)

	pg := appByName(t, resp, appdef.AppPostgres)
	require.NotNil(t, pg)
	assert.Equal(t, "postgres:17.5", pg.Image, "the emitted def must run the pin, not the held-back candidate")
	assert.Contains(t, pg.Volumes, "postgres2:/var/lib/postgresql/data")
	pgData, hasPGData := pg.Environments.Get("PGDATA")
	assert.True(t, hasPGData)
	assert.Equal(t, "/var/lib/postgresql/data", pgData)
	assert.Equal(t, appdef.KindThirdParty, pg.Kind, "postgres must stay infra, not be redefined as a webapp")
	assert.False(t, pg.IsJVM, "postgres must stay infra, not be redefined as a webapp")

	up := upgradeFor(t, resp, deps.Postgres)
	require.NotNil(t, up, "the hold must still be reported")
	assert.Equal(t, "postgres:18", up.To)
	assert.Equal(t, DependencyGen{Effective: "postgres:17.5", Candidate: "postgres:18"}, resp.Dependencies[deps.Postgres])
}
