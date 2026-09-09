package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ver(t *testing.T, image string) Version {
	t.Helper()
	v, ok := ParseImageVersion(image)
	require.True(t, ok, "the test's own image %q must parse", image)
	return v
}

func support(t *testing.T, id ID, from, to string) VendorSupport {
	t.Helper()
	d, ok := Lookup(id)
	require.True(t, ok)
	return d.UpgradeSupport(ver(t, from), ver(t, to))
}

// TestRabbitUpgradeMatrix is constraints.md's whitelist, pair by pair. RabbitMQ
// does NOT publish a "next minor only" rule — 3.13 may go straight to 4.2 while
// 4.1 may not go to 4.3 — so the table is the knowledge and this test is its
// statement.
func TestRabbitUpgradeMatrix(t *testing.T) {
	cases := []struct {
		from, to string
		allowed  bool
		via      string
		why      string
	}{
		{from: "rabbitmq:3.11.18", to: "rabbitmq:3.12.14", allowed: true},
		{from: "rabbitmq:3.12.14", to: "rabbitmq:3.13.7", allowed: true},
		{from: "rabbitmq:3.13.7", to: "rabbitmq:4.0.9", allowed: true},
		{from: "rabbitmq:3.13.7", to: "rabbitmq:4.1.2", allowed: true},
		{from: "rabbitmq:3.13.7", to: "rabbitmq:4.2.9", allowed: true},
		{from: "rabbitmq:4.0.9", to: "rabbitmq:4.1.2", allowed: true},
		{from: "rabbitmq:4.0.9", to: "rabbitmq:4.2.9", allowed: true},
		// The real case today: the stand's pin against the 2026.3-RC3 bundle.
		{from: "rabbitmq:4.1.2-management", to: "rabbitmq:4.2.9-management", allowed: true},
		{from: "rabbitmq:4.2.9", to: "rabbitmq:4.3.5", allowed: true},
		// 4.3 is reachable ONLY from 4.2.
		{from: "rabbitmq:4.1.2", to: "rabbitmq:4.3.5", via: "4.2"},
		{from: "rabbitmq:3.13.7", to: "rabbitmq:4.3.5", via: "4.2",
			why: "the highest permitted hop that itself reaches 4.3"},
		// Nothing at or below 3.12 may enter 4.x directly.
		{from: "rabbitmq:3.12.14", to: "rabbitmq:4.0.9", via: "3.13"},
		{from: "rabbitmq:3.12.14", to: "rabbitmq:4.2.9", via: "3.13"},
		// From 3.11 the path is 3.12 → 3.13 → 4.x, and Via names the NEXT hop:
		// telling the operator "3.13" would name a version they cannot reach in
		// one move, and telling them "no path" would be false.
		{from: "rabbitmq:3.11.18", to: "rabbitmq:4.2.9", via: "3.12"},
		// Backwards is unsupported at every granularity, and has no path.
		{from: "rabbitmq:4.2.9", to: "rabbitmq:4.1.2", via: ""},
		{from: "rabbitmq:4.3.5", to: "rabbitmq:4.2.9", via: ""},
		{from: "rabbitmq:4.1.9", to: "rabbitmq:4.1.2", via: "",
			why: "a backwards patch move is still backwards"},
		// A patch move inside one series is not breaking and never reaches the
		// migrator, but the answer still has to be honest.
		{from: "rabbitmq:4.1.2", to: "rabbitmq:4.1.9", allowed: true},
		// A series the table does not know yet is refused with no path rather
		// than guessed: offering an unpublished hop is the worse mistake.
		{from: "rabbitmq:4.3.5", to: "rabbitmq:4.4.0", via: ""},
	}
	for _, c := range cases {
		t.Run(c.from+"→"+c.to, func(t *testing.T) {
			got := support(t, RabbitMQ, c.from, c.to)
			assert.Equal(t, c.allowed, got.Allowed, c.why)
			assert.Equal(t, c.via, got.Via, c.why)
			if c.allowed {
				assert.Empty(t, got.Via, "an allowed hop needs no intermediate")
			}
		})
	}
}

// ZooKeeper publishes no hop table: the on-disk format has been unchanged
// since 3.5, so the only rule is the pre-3.5 floor — below it the data may
// predate zookeeper.snapshot.trust.empty, and there is no intermediate version
// that would fix that, hence Via "".
func TestZookeeperUpgradeSupport(t *testing.T) {
	cases := []struct {
		from, to string
		allowed  bool
		via      string
	}{
		{from: "zookeeper:3.8.6", to: "zookeeper:3.9.5", allowed: true},
		{from: "zookeeper:3.9.5", to: "zookeeper:3.9.7", allowed: true},
		{from: "zookeeper:3.9.5", to: "zookeeper:3.10.0", allowed: true},
		{from: "zookeeper:3.5.0", to: "zookeeper:3.9.5", allowed: true},
		{from: "zookeeper:3.4.14", to: "zookeeper:3.9.5"},
		{from: "zookeeper:3.4.14", to: "zookeeper:3.5.0"},
		{from: "zookeeper:3.9.5", to: "zookeeper:3.8.6"},
	}
	for _, c := range cases {
		t.Run(c.from+"→"+c.to, func(t *testing.T) {
			got := support(t, Zookeeper, c.from, c.to)
			assert.Equal(t, c.allowed, got.Allowed)
			assert.Equal(t, c.via, got.Via, "a pre-3.5 refusal has no intermediate to name")
		})
	}
}

// PostgreSQL, Keycloak and MongoDB publish no hop table this launcher models:
// any forward move is as supported as the vendor gets, and what holds a
// namespace back is IsBreaking plus whether a migrator exists.
func TestDependenciesWithNoHopTableAllowAnyForwardMove(t *testing.T) {
	cases := []struct {
		id       ID
		from, to string
		allowed  bool
	}{
		{Postgres, "postgres:17.5", "postgres:18", true},
		{Postgres, "postgres:17.5", "postgres:19", true},
		{Postgres, "postgres:18", "postgres:17.5", false},
		{Keycloak, "keycloak/keycloak:26.4.5", "keycloak/keycloak:27.0.0", true},
		{Keycloak, "keycloak/keycloak:27.0.0", "keycloak/keycloak:26.4.5", false},
		{MongoDB, "mongo:4.0.2", "mongo:5.0.0", true},
		{MongoDB, "mongo:5.0.0", "mongo:4.0.2", false},
	}
	for _, c := range cases {
		t.Run(string(c.id)+" "+c.from+"→"+c.to, func(t *testing.T) {
			got := support(t, c.id, c.from, c.to)
			assert.Equal(t, c.allowed, got.Allowed)
			assert.Empty(t, got.Via, "these dependencies never name an intermediate")
		})
	}
}

// IsBreaking and UpgradeSupport are two DIFFERENT questions and must not be
// conflated: "does moving the data need a migration rather than a plain
// recreate" is ours, "does the vendor permit this hop at all" is theirs. All
// four combinations exist and each one means something different downstream.
func TestBreakingAndVendorSupportAreDifferentQuestions(t *testing.T) {
	d, ok := Lookup(RabbitMQ)
	require.True(t, ok)

	breakingAndAllowed := func(from, to string) {
		t.Helper()
		assert.True(t, Breaking(d, from, to), "%s → %s must be held back", from, to)
		assert.True(t, support(t, RabbitMQ, from, to).Allowed, "%s → %s is a published hop", from, to)
	}
	// Held back by us, permitted by them: this is what a migration is for.
	breakingAndAllowed("rabbitmq:4.1.2-management", "rabbitmq:4.2.9-management")

	// Held back by us AND forbidden by them: the operator must be told which
	// intermediate to take, not offered a migration.
	assert.True(t, Breaking(d, "rabbitmq:4.1.2", "rabbitmq:4.3.5"))
	blocked := support(t, RabbitMQ, "rabbitmq:4.1.2", "rabbitmq:4.3.5")
	assert.False(t, blocked.Allowed)
	assert.Equal(t, "4.2", blocked.Via)

	// Not held back and permitted: a patch move, which the generator applies
	// on the next start and no migration ever sees.
	assert.False(t, Breaking(d, "rabbitmq:4.1.2-management", "rabbitmq:4.1.9-management"))
	assert.True(t, support(t, RabbitMQ, "rabbitmq:4.1.2", "rabbitmq:4.1.9").Allowed)
}

// IsBreaking must be UNCHANGED by this feature for every dependency: the rule
// shape decides what the generator silently applies, and nothing about a
// vendor matrix or a new migrator may move it. Asserted as the rule shape
// itself, so widening any of them fails here as well as in TestBreakingRules.
func TestIsBreakingRuleShapesAreUnchanged(t *testing.T) {
	minor := map[ID]bool{RabbitMQ: true, Zookeeper: true}
	for _, d := range All() {
		t.Run(string(d.ID()), func(t *testing.T) {
			sameMajorDifferentMinor := d.IsBreaking(Version{Major: 4, Minor: 1}, Version{Major: 4, Minor: 2})
			assert.Equal(t, minor[d.ID()], sameMajorDifferentMinor,
				"minor-breaking dependencies are exactly rabbitmq and zookeeper")
			assert.True(t, d.IsBreaking(Version{Major: 4, Minor: 1}, Version{Major: 5, Minor: 1}),
				"a major change is breaking for every dependency")
			assert.False(t, d.IsBreaking(Version{Major: 4, Minor: 1, Patch: 2}, Version{Major: 4, Minor: 1, Patch: 9}),
				"a patch change is breaking for no dependency")
		})
	}
}
