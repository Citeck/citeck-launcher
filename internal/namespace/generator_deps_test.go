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

// generateWithPins is the image-only entry point: most tests in this file are
// about the IMAGE gate, and pins carrying no generation are what every
// existing namespace has. Tests about the volume generation call
// generateWithStates directly.
func generateWithPins(t *testing.T, bun *bundle.Def, pins map[deps.ID]string) *GenResp {
	t.Helper()
	return generateCfgWithStates(t, depsTestConfig(), bun, statesOf(pins))
}

func generateWithStates(t *testing.T, bun *bundle.Def, states map[deps.ID]deps.DependencyState) *GenResp {
	t.Helper()
	return generateCfgWithStates(t, depsTestConfig(), bun, states)
}

func generateCfgWithPins(t *testing.T, cfg *Config, bun *bundle.Def, pins map[deps.ID]string) *GenResp {
	t.Helper()
	return generateCfgWithStates(t, cfg, bun, statesOf(pins))
}

// statesOf turns image-only pins into dependency states with no recorded
// generation — i.e. generation 1, the volume every namespace has always used.
func statesOf(pins map[deps.ID]string) map[deps.ID]deps.DependencyState {
	if pins == nil {
		return nil
	}
	out := make(map[deps.ID]deps.DependencyState, len(pins))
	for id, img := range pins {
		out[id] = deps.DependencyState{Image: img}
	}
	return out
}

// generateCfgWithStates runs one generation against the caller's bundle, which
// is completed with the gateway depsTestWorkspace declares (see there). The
// gateway carries no dataSources, so it contributes no init action, no
// dependency and no env to any infra def — the byte-stability goldens are
// unaffected by its presence.
func generateCfgWithStates(t *testing.T, cfg *Config, bun *bundle.Def, states map[deps.ID]deps.DependencyState) *GenResp {
	t.Helper()
	apps := map[string]bundle.AppDef{appdef.AppGateway: {Image: "citeck/gateway:1.0.0"}}
	if bun != nil {
		maps.Copy(apps, bun.Applications)
	}
	resp, err := Generate(cfg, &bundle.Def{Applications: apps}, depsTestWorkspace(),
		SystemSecrets{JWT: "j", OIDC: "o"}, GenerateOpts{DependencyStates: states})
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

// A namespace with no pin runs the candidate, and with no bundle entry either
// the candidate is the launcher's own default — which is PostgreSQL 17, the
// version every stand already runs. A fresh install is a 17 install.
func TestNoPinAppliesCandidateAndTheDefaultIs17(t *testing.T) {
	resp := generateWithPins(t, nil, nil)
	pg := appByName(t, resp, appdef.AppPostgres)
	assert.Equal(t, "postgres:17.5", pg.Image)
	// postgres2, not postgres3: the volume follows the pin's GENERATION, and a
	// namespace with no pin has never migrated. The MOUNT PATH is what the
	// image dictates, and that does follow the version — 17 keeps the legacy
	// layout, PGDATA and all.
	assert.Contains(t, pg.Volumes, "postgres2:/var/lib/postgresql/data")
	pgData, hasPGData := pg.Environments.Get("PGDATA")
	assert.True(t, hasPGData, "the 17 layout names PGDATA explicitly")
	assert.Equal(t, "/var/lib/postgresql/data", pgData)
	assert.Empty(t, resp.DependencyUpgrades)
	assert.Equal(t, DependencyGen{Effective: "postgres:17.5", Candidate: "postgres:17.5"}, resp.Dependencies[deps.Postgres])
}

// PostgreSQL 18 still arrives — from a BUNDLE that asks for it. On an unpinned
// namespace there is no data to protect, so it applies straight away, with the
// 18 layout: the parent mount and no PGDATA (the image defaults it to
// /var/lib/postgresql/<major>/docker). This is the half of the old
// "…AndDefaultIs18" test that is still true, said about the one thing that can
// still choose 18.
func TestABundleAsksForPostgres18AndAnUnpinnedNamespaceGetsIt(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:18.6"}}}
	resp := generateWithPins(t, bun, nil)
	pg := appByName(t, resp, appdef.AppPostgres)
	assert.Equal(t, "postgres:18.6", pg.Image)
	assert.Contains(t, pg.Volumes, "postgres2:/var/lib/postgresql")
	_, hasPGData := pg.Environments.Get("PGDATA")
	assert.False(t, hasPGData, "18 layout must leave PGDATA to the image default")
	assert.Empty(t, resp.DependencyUpgrades, "there is no pin, so nothing is being held back")
	assert.Equal(t, DependencyGen{Effective: "postgres:18.6", Candidate: "postgres:18.6"}, resp.Dependencies[deps.Postgres])
}

// A bundle that asks for 18 on a namespace whose data is on 17 is the whole
// point of the ruling: the launcher does not move the data by itself, it holds
// 17 and OFFERS the migration. Nothing about 18 was removed with the default.
func TestABundleAskingForPostgres18IsHeldAndOffered(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppPostgres: {Image: "postgres:18.6"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: "postgres:17.5"})
	assert.Equal(t, "postgres:17.5", appByName(t, resp, appdef.AppPostgres).Image)
	up := upgradeFor(t, resp, deps.Postgres)
	require.NotNil(t, up)
	assert.Equal(t, "postgres:18.6", up.To)
	assert.True(t, up.Migratable, "and this launcher still ships the 17 → 18 migration")
	assert.False(t, up.BundleOlder)
}

// RabbitMQ's minor bumps are held back AND this launcher ships a plan for
// them, which is the pair the dashboard banner splits on: Migratable is what
// tells "upgrade available: run the migration" apart from "upgrade available,
// requires a newer launcher". Keycloak covers the non-migratable side
// (TestKeycloakMajorBumpIsHeldByThePin).
func TestRabbitMQMinorBumpIsHeldAndMigratable(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{appdef.AppRabbitmq: {Image: "rabbitmq:4.2.9-management"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.RabbitMQ: "rabbitmq:4.1.2-management"})
	assert.Equal(t, "rabbitmq:4.1.2-management", appByName(t, resp, appdef.AppRabbitmq).Image)
	require.Len(t, resp.DependencyUpgrades, 1, "nothing else may be held back")
	up := upgradeFor(t, resp, deps.RabbitMQ)
	require.NotNil(t, up)
	assert.True(t, up.Migratable, "this launcher ships a RabbitMQ migration")
	assert.False(t, up.VendorBlocked, "and the vendor permits 4.1 -> 4.2")
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
		GenerateOpts{DependencyStates: map[deps.ID]deps.DependencyState{deps.Postgres: {Image: "postgres:17.5"}}})
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

// TestInfraImageDefaults pins the exact fallback image of every generator that
// goes through the dependency gate: what a namespace runs when neither the
// bundle nor the workspace config names an image is the launcher's OWN choice,
// and it is a release decision, not an implementation detail.
//
// Every one of them is CONCRETE, down to the patch. A floating tag would let
// the launcher's default move without a release — and the one moment it is
// guaranteed to be re-resolved is inside a migration's pull-image step, i.e.
// exactly while the data is being moved (the runtime never re-pulls a
// KindThirdParty image that already exists locally).
//
// None of them MOVES, either: a version bump is a bundle's decision, not the
// launcher's ("давай наверное всё-таки дефолт оставим на старой версии, а
// повышать будем через бандлы"). postgres:18 was proposed as a default on this
// branch and withdrawn before release — a bundle that names no postgres is not
// asking for a new major, and it was the one thing that could put an upgrade
// banner on a stand with nothing behind it. Every value here is what Kotlin
// v1.3.9 shipped, or the smallest step from it: postgres:17.5,
// rabbitmq:4.1.2-management, mongo:4.0.2 verbatim, zookeeper 3.9.4 → 3.9.5 and
// keycloak 26.4.x.
func TestInfraImageDefaults(t *testing.T) {
	cfg := depsTestConfig()
	cfg.Authentication = AuthenticationProps{Type: AuthKeycloak, Users: []string{"admin"}}
	resp := generateCfgWithStates(t, cfg, nil, nil)
	for _, tc := range []struct{ app, want string }{
		{appdef.AppPostgres, "postgres:17.5"},
		{appdef.AppRabbitmq, "rabbitmq:4.1.2-management"},
		{appdef.AppZookeeper, "zookeeper:3.9.5"},
		{appdef.AppMongodb, "mongo:4.0.2"},
		{appdef.AppKeycloak, "keycloak/keycloak:26.4.5"},
	} {
		t.Run(tc.app, func(t *testing.T) {
			assert.Equal(t, tc.want, appByName(t, resp, tc.app).Image)
		})
	}
}

// TestPostgresDefaultDefIsTheDefAPinned17NamespaceAlreadyRuns is the tripwire
// for the fallback above, and it deliberately shares postgres17.hashinput.golden
// with TestPostgres17PinnedDefIsByteStable instead of carrying a golden of its
// own. image= is part of GetHashInput, so moving the default recreates the
// postgres container of every namespace whose EFFECTIVE image is the fallback —
// and the two goldens being byte-identical is exactly the claim worth pinning:
// the launcher's unprompted default IS what a 17-pinned namespace already runs,
// so a fresh install and a namespace that predates dependency pins converge on
// one container. A second file holding the same bytes could only drift.
//
// The two generations differ in every input a reader would expect to matter —
// this one has NO pin at all, its sibling is pinned at postgres:17.5 — so the
// identity is a property of the generator, not of one fixture.
func TestPostgresDefaultDefIsTheDefAPinned17NamespaceAlreadyRuns(t *testing.T) {
	assertGoldenHashInput(t, appByName(t, generateWithStates(t, nil, nil), appdef.AppPostgres),
		"postgres17.hashinput.golden")
}

// TestABundleThatNamesNoPostgresOffersNothing is the regression the whole
// ruling came from: on the user's own stand, a bundle repo declaring no
// postgres image at all still raised "Доступно обновление зависимости: postgres
// postgres:17.5 → postgres:18.6", because the 18.6 was the launcher's own
// fallback and nothing else in the world had asked for it. With the default
// back at 17.5 there is no candidate to hold anything back from, so the
// generator reports no upgrade and the dependency list reads up-to-date
// (TestABundleThatNamesNoPostgresListsUpToDate, internal/daemon).
//
// deps.PostgresLegacyImage ("postgres:17") is the second half: it is what
// seeding derives for a namespace whose pin was read out of PG_VERSION, so it
// is what a MIGRATED-FROM-1.x stand carries. 17 → 17.5 is the same major, so it
// applies silently — the pin follows the container once it runs — and it must
// not be announced either.
func TestABundleThatNamesNoPostgresOffersNothing(t *testing.T) {
	for _, pin := range []string{"postgres:17.5", deps.PostgresLegacyImage} {
		t.Run(pin, func(t *testing.T) {
			resp := generateWithPins(t, nil, map[deps.ID]string{deps.Postgres: pin})
			assert.Nil(t, upgradeFor(t, resp, deps.Postgres),
				"a bundle that names no postgres asks for nothing, so nothing may be offered")
			assert.Empty(t, resp.DependencyUpgrades)
			assert.Equal(t, "postgres:17.5", resp.Dependencies[deps.Postgres].Candidate,
				"the candidate is the launcher's own default, and it stays on 17")
			pg := appByName(t, resp, appdef.AppPostgres)
			assert.Contains(t, pg.Volumes, "postgres2:/var/lib/postgresql/data",
				"and the data keeps the layout it has always had")
		})
	}
}

// A bundle that goes BACKWARDS across a data format is held back — that half
// has always worked, as a side effect of the format rule — but calling it an
// "upgrade available" is wrong: there is nothing to migrate and nothing to
// wait for. The generator records the direction as a FACT beside the vendor
// facts; the sentence is built where every other migration sentence is.
func TestABreakingBackwardsCandidateIsReportedAsBundleOlder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		id        deps.ID
		app       string
		pin       string
		candidate string
	}{
		{"postgres major", deps.Postgres, appdef.AppPostgres, "postgres:18.6", "postgres:17.5"},
		{"rabbitmq minor", deps.RabbitMQ, appdef.AppRabbitmq, "rabbitmq:4.2.9-management", "rabbitmq:4.1.8-management"},
		{"zookeeper minor", deps.Zookeeper, appdef.AppZookeeper, "zookeeper:3.9.5", "zookeeper:3.8.6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bun := &bundle.Def{Applications: map[string]bundle.AppDef{tc.app: {Image: tc.candidate}}}
			resp := generateWithPins(t, bun, map[deps.ID]string{tc.id: tc.pin})
			assert.Equal(t, tc.pin, appByName(t, resp, tc.app).Image, "the data stays where it is")
			up := upgradeFor(t, resp, tc.id)
			require.NotNil(t, up)
			assert.True(t, up.BundleOlder, "the candidate is OLDER than the pin, and saying so is the whole point")
			assert.False(t, up.VendorBlocked,
				"no vendor here permits a downgrade, so 'there is no upgrade path from X to Y' answers a question nobody asked")
			assert.Empty(t, up.VendorVia)
		})
	}
}

// The forward direction is untouched: a held-back FORWARD candidate is still an
// upgrade, and it still carries the vendor's verdict on the hop. Without this
// the test above would pass on a generator that simply stopped asking the
// vendor anything.
func TestAForwardHoldKeepsTheVendorVerdictAndIsNotBundleOlder(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppRabbitmq: {Image: "rabbitmq:4.3.5-management"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.RabbitMQ: "rabbitmq:4.1.2-management"})
	up := upgradeFor(t, resp, deps.RabbitMQ)
	require.NotNil(t, up)
	assert.False(t, up.BundleOlder)
	assert.True(t, up.VendorBlocked, "4.1 -> 4.3 is a hop the vendor does not support")
	assert.Equal(t, "4.2", up.VendorVia)
}

// An unreadable tag is held back by the format rule, but nothing orders it, so
// the generator must not claim the bundle went backwards. The preflight's
// message about the tag is the one the operator needs.
func TestAnUnparsableCandidateIsNotReportedAsBundleOlder(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppPostgres: {Image: "postgres:latest"}}}
	resp := generateWithPins(t, bun, map[deps.ID]string{deps.Postgres: "postgres:18.6"})
	up := upgradeFor(t, resp, deps.Postgres)
	require.NotNil(t, up)
	assert.False(t, up.BundleOlder)
}

// The user's ruling, stated from the generator side: a PATCH revert is not a
// data move, so it applies silently and is not reported at all. ("патчи не
// надо откатывать. В патчах как правило все ок с совместимостью. Только
// «переломы» откатываем.") Holding it would additionally be a dead end — the
// only door back would be `citeck deps upgrade`, which never moves data
// backwards.
func TestAPatchRevertAppliesSilently(t *testing.T) {
	for _, tc := range []struct {
		name      string
		id        deps.ID
		app       string
		pin       string
		candidate string
	}{
		{"postgres", deps.Postgres, appdef.AppPostgres, "postgres:17.11", "postgres:17.2"},
		{"rabbitmq", deps.RabbitMQ, appdef.AppRabbitmq, "rabbitmq:4.2.9-management", "rabbitmq:4.2.3-management"},
		{"zookeeper", deps.Zookeeper, appdef.AppZookeeper, "zookeeper:3.9.5", "zookeeper:3.9.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bun := &bundle.Def{Applications: map[string]bundle.AppDef{tc.app: {Image: tc.candidate}}}
			resp := generateWithPins(t, bun, map[deps.ID]string{tc.id: tc.pin})
			assert.Equal(t, tc.candidate, appByName(t, resp, tc.app).Image)
			assert.Nil(t, upgradeFor(t, resp, tc.id), "nothing is held back, so there is nothing to report")
			assert.Equal(t, DependencyGen{Effective: tc.candidate, Candidate: tc.candidate},
				resp.Dependencies[tc.id])
		})
	}
}
