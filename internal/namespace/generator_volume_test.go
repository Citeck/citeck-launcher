package namespace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/appdef"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
)

// gen1 is the state every namespace that exists today has: an image and no
// recorded generation. Spelled out rather than passed as `nil` states, because
// what is being pinned is that an ABSENT counter and an explicit 1 mount the
// same volume.
func gen1(image string) deps.DependencyState { return deps.DependencyState{Image: image} }

// volumeNameAt returns the plain (left-hand) name of the mount at
// containerPath among mounts. It is a helper rather than an assert.Contains on
// the whole "name:path" string so a failure says which volume was mounted
// instead of only that the expected one was missing.
func volumeNameAt(t *testing.T, mounts []string, containerPath string) string {
	t.Helper()
	for _, v := range mounts {
		if name, ok := strings.CutSuffix(v, ":"+containerPath); ok {
			return name
		}
	}
	t.Fatalf("no mount at %s among %v", containerPath, mounts)
	return ""
}

// volumeOf is volumeNameAt for an application's own mounts.
func volumeOf(t *testing.T, app *appdef.ApplicationDef, containerPath string) string {
	t.Helper()
	require.NotNil(t, app)
	return volumeNameAt(t, app.Volumes, containerPath)
}

// TestGenerationOneIsTodaysVolumeNames is the hash-stability contract in the
// generator: a namespace whose pins carry no generation — every namespace that
// exists today — must mount exactly the volumes it has always mounted. The
// goldens below pin the same thing byte for byte for two of the four; this one
// states it for all four in one place, and covers the ABSENT counter.
func TestGenerationOneIsTodaysVolumeNames(t *testing.T) {
	cfg := depsTestConfig()
	cfg.MongoDB = MongoDbProps{Image: "mongo:4.0.2"}
	resp := generateCfgWithStates(t, cfg, nil, map[deps.ID]deps.DependencyState{
		deps.Postgres:  gen1("postgres:17.5"),
		deps.RabbitMQ:  gen1("rabbitmq:4.1.2-management"),
		deps.Zookeeper: gen1("zookeeper:3.9.5"),
		deps.MongoDB:   gen1("mongo:4.0.2"),
	})
	assert.Equal(t, "postgres2", volumeOf(t, appByName(t, resp, appdef.AppPostgres), "/var/lib/postgresql/data"))
	assert.Equal(t, "rabbitmq2", volumeOf(t, appByName(t, resp, appdef.AppRabbitmq), "/var/lib/rabbitmq"))
	assert.Equal(t, "zookeeper2", volumeOf(t, appByName(t, resp, appdef.AppZookeeper), "/citeck/zookeeper"))
	assert.Equal(t, "mongo2", volumeOf(t, appByName(t, resp, appdef.AppMongodb), "/data/db"))
}

// A namespace with NO pin at all has never migrated either, so it is
// generation 1 as well — the volume the launcher creates for it is the one a
// later migration will move away from.
//
// PostgreSQL is asked about at its LEGACY mount path here, and that is the
// point rather than an accident: with no pin and no bundle entry the launcher
// runs its own default, which is 17 (TestInfraImageDefaults), and the layout
// follows the major of the image that will run. An unpinned namespace lands on
// exactly the volume AND the path a namespace that predates dependency pins
// already has.
func TestAnUnpinnedNamespaceIsGenerationOne(t *testing.T) {
	cfg := depsTestConfig()
	cfg.MongoDB = MongoDbProps{Image: "mongo:4.0.2"}
	resp := generateCfgWithStates(t, cfg, nil, nil)
	assert.Equal(t, "postgres2", volumeOf(t, appByName(t, resp, appdef.AppPostgres), "/var/lib/postgresql/data"))
	assert.Equal(t, "rabbitmq2", volumeOf(t, appByName(t, resp, appdef.AppRabbitmq), "/var/lib/rabbitmq"))
	assert.Equal(t, "zookeeper2", volumeOf(t, appByName(t, resp, appdef.AppZookeeper), "/citeck/zookeeper"))
	assert.Equal(t, "mongo2", volumeOf(t, appByName(t, resp, appdef.AppMongodb), "/data/db"))
}

// A completed migration advances the counter by one, and the generator must
// follow it — mounting the pre-migration volume would hand the new version the
// old data and leave the migrated copy orphaned.
func TestAMigratedNamespaceMountsTheNextGeneration(t *testing.T) {
	cfg := depsTestConfig()
	cfg.MongoDB = MongoDbProps{Image: "mongo:4.0.2"}
	// The bundle offers what each pin already runs, so nothing here is held
	// back: this test is about the volume alone, and a fixture whose candidate
	// disagreed with its pin would additionally exercise the image gate.
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppRabbitmq: {Image: "rabbitmq:4.2.9-management"},
		// The bundle has to name postgres:18 too, or the candidate is the
		// launcher's own 17 default and this fixture would quietly become a
		// bundle-older HOLD — a second rule on top of the one being tested.
		appdef.AppPostgres: {Image: "postgres:18"}}}
	resp := generateCfgWithStates(t, cfg, bun, map[deps.ID]deps.DependencyState{
		deps.Postgres:  {Image: "postgres:18", VolumeGen: 2},
		deps.RabbitMQ:  {Image: "rabbitmq:4.2.9-management", VolumeGen: 2},
		deps.Zookeeper: {Image: "zookeeper:3.9.5", VolumeGen: 2},
		deps.MongoDB:   {Image: "mongo:4.0.2", VolumeGen: 2},
	})
	assert.Empty(t, resp.DependencyUpgrades, "the premise: this fixture holds nothing back")
	// PostgreSQL 18 also moves the MOUNT PATH; the two rules are independent
	// and this is the one pair where both apply at once.
	assert.Equal(t, "postgres3", volumeOf(t, appByName(t, resp, appdef.AppPostgres), "/var/lib/postgresql"))
	assert.Equal(t, "rabbitmq3", volumeOf(t, appByName(t, resp, appdef.AppRabbitmq), "/var/lib/rabbitmq"))
	assert.Equal(t, "zookeeper3", volumeOf(t, appByName(t, resp, appdef.AppZookeeper), "/citeck/zookeeper"))
	assert.Equal(t, "mongo3", volumeOf(t, appByName(t, resp, appdef.AppMongodb), "/data/db"))
}

// ZooKeeper's init container mkdirs the data and datalog subdirectories, so it
// must mount the SAME volume the server does. Nothing else catches a
// divergence: ApplicationDef.GetHashInput hashes an init container's IMAGE
// only (appdef/hash_test.go), so a mismatched volume changes no hash, recreates
// no container, and shows up as a server that cannot write its snapshots.
func TestZookeeperInitContainerMountsTheSameVolumeAsTheApp(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state map[deps.ID]deps.DependencyState
		want  string
	}{
		{"generation 1", map[deps.ID]deps.DependencyState{deps.Zookeeper: gen1("zookeeper:3.9.5")}, "zookeeper2"},
		{"migrated", map[deps.ID]deps.DependencyState{deps.Zookeeper: {Image: "zookeeper:3.9.5", VolumeGen: 3}}, "zookeeper4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zk := appByName(t, generateCfgWithStates(t, depsTestConfig(), nil, tc.state), appdef.AppZookeeper)
			require.Len(t, zk.InitContainers, 1)
			assert.Equal(t, tc.want, volumeOf(t, zk, "/citeck/zookeeper"))
			assert.Equal(t, tc.want, volumeNameAt(t, zk.InitContainers[0].Volumes, "/zkdir"),
				"the init container mkdirs data/ and datalog/ — in the volume the server will read")
		})
	}
}

// TestRabbitMQ41PinnedDefIsByteStable / TestZookeeper39PinnedDefIsByteStable
// are the same contract as TestPostgres17PinnedDefIsByteStable for the two
// generators whose volume line this change also rewrote. Both goldens were
// captured on the PRE-change generator (git archive of HEAD ababa56), so they
// prove the counter's generation 1 is byte-identical to the hardcoded name it
// replaced — not merely self-consistent.
func TestRabbitMQ41PinnedDefIsByteStable(t *testing.T) {
	resp := generateWithStates(t, nil, map[deps.ID]deps.DependencyState{
		deps.RabbitMQ: gen1("rabbitmq:4.1.2-management")})
	assertGoldenHashInput(t, appByName(t, resp, appdef.AppRabbitmq), "rabbitmq41.hashinput.golden")
}

func TestZookeeper39PinnedDefIsByteStable(t *testing.T) {
	resp := generateWithStates(t, nil, map[deps.ID]deps.DependencyState{
		deps.Zookeeper: gen1("zookeeper:3.9.5")})
	assertGoldenHashInput(t, appByName(t, resp, appdef.AppZookeeper), "zookeeper39.hashinput.golden")
}

func assertGoldenHashInput(t *testing.T, app *appdef.ApplicationDef, name string) {
	t.Helper()
	require.NotNil(t, app)
	got := app.GetHashInput()
	golden := filepath.Join("testdata", name)
	if *updateGolden {
		require.NoError(t, os.WriteFile(golden, []byte(got), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, string(want), got)
}

// A hop the DEPENDENCY'S OWN vendor refuses is still held back, but for a
// reason "update the launcher" would misstate: what the operator needs is the
// intermediate version. The generator records the verdict; wording it is the
// daemon's job.
func TestAVendorForbiddenUpgradeIsReportedWithItsIntermediate(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppRabbitmq: {Image: "rabbitmq:4.3.5-management"}}}
	up := upgradeFor(t, generateWithStates(t, bun, map[deps.ID]deps.DependencyState{
		deps.RabbitMQ: gen1("rabbitmq:4.1.2-management")}), deps.RabbitMQ)
	require.NotNil(t, up)
	assert.True(t, up.VendorBlocked, "RabbitMQ does not permit 4.1 → 4.3 in one hop")
	assert.Equal(t, "4.2", up.VendorVia, "and the operator has to be told which hop to take first")
}

// The pair the vendor DOES permit must not be reported as blocked, or every
// ordinary held-back upgrade would be advertised as impossible.
func TestAVendorPermittedUpgradeIsNotBlocked(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppRabbitmq: {Image: "rabbitmq:4.2.9-management"}}}
	up := upgradeFor(t, generateWithStates(t, bun, map[deps.ID]deps.DependencyState{
		deps.RabbitMQ: gen1("rabbitmq:4.1.2-management")}), deps.RabbitMQ)
	require.NotNil(t, up)
	assert.False(t, up.VendorBlocked)
	assert.Empty(t, up.VendorVia)
}

// With an unreadable tag on either side there are no versions to ask the
// vendor's table about. The pin is held anyway (deps.Breaking answers true for
// an unparsable tag), and the preflight's message about the tag is the one the
// operator needs — so the vendor verdict stays empty rather than guessing.
func TestAnUnparsableTagLeavesTheVendorVerdictEmpty(t *testing.T) {
	bun := &bundle.Def{Applications: map[string]bundle.AppDef{
		appdef.AppRabbitmq: {Image: "rabbitmq:latest"}}}
	up := upgradeFor(t, generateWithStates(t, bun, map[deps.ID]deps.DependencyState{
		deps.RabbitMQ: gen1("rabbitmq:4.1.2-management")}), deps.RabbitMQ)
	require.NotNil(t, up)
	assert.False(t, up.VendorBlocked)
	assert.Empty(t, up.VendorVia)
}
