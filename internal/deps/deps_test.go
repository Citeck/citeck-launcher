package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistryOrderAndLookup(t *testing.T) {
	ids := make([]ID, 0, len(All()))
	for _, d := range All() {
		ids = append(ids, d.ID())
	}
	assert.Equal(t, []ID{Postgres, RabbitMQ, Zookeeper, Keycloak, MongoDB, Qdrant}, ids)

	d, ok := Lookup(Postgres)
	require.True(t, ok)
	assert.Equal(t, "postgres", d.AppName())
	_, ok = Lookup("nope")
	assert.False(t, ok)

	byApp, ok := ByApp("mongo")
	require.True(t, ok)
	assert.Equal(t, MongoDB, byApp.ID())
	_, ok = ByApp("emodel")
	assert.False(t, ok)
}

func TestBreakingRules(t *testing.T) {
	cases := []struct {
		id       ID
		from, to string
		breaking bool
	}{
		{Postgres, "postgres:17.5", "postgres:17.11", false},
		{Postgres, "postgres:17.5", "postgres:18", true},
		{Postgres, "postgres:18", "postgres:17.5", true}, // downgrade is a change too
		{Postgres, "postgres:17.5", "postgres:latest", true},
		// A suffix the parser ignores is not a version difference; a tag with
		// no version at all is unreadable, and unreadable is breaking.
		{Postgres, "custom/pg:17-alpine", "postgres:17.5", false},
		{Postgres, "custom/pg:edge", "postgres:17.5", true},
		// A leading "v" is READ (Qdrant publishes nothing else), so this pair
		// is no longer the "unreadable tag" case it used to stand for here:
		// custom/pg:v17 names major 17, and 17 → 17.5 moves no data. Reading it
		// is also what lets the major break below be seen at all — before, both
		// were held back by the same blanket "we cannot read this".
		{Postgres, "custom/pg:v17", "postgres:17.5", false},
		{Postgres, "custom/pg:v17", "postgres:18.1", true},
		// The SAME image cannot move the data, whatever its tag says. Without
		// this an unknown tag would report a permanent, un-actionable upgrade
		// from X to X.
		{Postgres, "postgres:latest", "postgres:latest", false},
		// Two DIFFERENT unknown tags are still breaking, and are NOT compared
		// numerically: an unparsable tag yields a zero Version, so without the
		// ok guard this pair would read as "major 0 to major 0", i.e. not
		// breaking.
		{Postgres, "postgres:latest", "postgres:foo", true},
		{RabbitMQ, "rabbitmq:4.1.2-management", "rabbitmq:4.1.9-management", false},
		{RabbitMQ, "rabbitmq:4.1.2-management", "rabbitmq:4.2.9-management", true},
		{RabbitMQ, "rabbitmq:4.1.2-management", "rabbitmq:5.0.0-management", true},
		{Zookeeper, "zookeeper:3.9.5", "zookeeper:3.9.7", false},
		{Zookeeper, "zookeeper:3.9.5", "zookeeper:3.10.0", true},
		{Keycloak, "keycloak/keycloak:26.4.5", "keycloak/keycloak:26.5.0", false},
		{Keycloak, "keycloak/keycloak:26.4.5", "keycloak/keycloak:27.0.0", true},
		{MongoDB, "mongo:4.0.2", "mongo:4.4.0", false},
		{MongoDB, "mongo:4.0.2", "mongo:5.0.0", true},
	}
	for _, c := range cases {
		t.Run(string(c.id)+" "+c.from+"→"+c.to, func(t *testing.T) {
			d, ok := Lookup(c.id)
			require.True(t, ok)
			assert.Equal(t, c.breaking, Breaking(d, c.from, c.to))
		})
	}
}

// TestWhichDependenciesAreMigratable names the three dependencies this
// launcher ships a plan for. It is deliberately a LIST and not a property: a
// descriptor claiming Migratable() with nothing wired behind it is advertised
// to the user as an upgrade the launcher can perform, and the daemon's wiring
// test (TestEveryMigratableDependencyHasAMigratorAndARollback) is what checks
// the other half.
func TestWhichDependenciesAreMigratable(t *testing.T) {
	migratable := map[ID]bool{Postgres: true, RabbitMQ: true, Zookeeper: true, Qdrant: true}
	for _, d := range All() {
		assert.Equal(t, migratable[d.ID()], d.Migratable(), string(d.ID()))
		assert.NotEmpty(t, d.LegacyImage(), string(d.ID()))
		_, ok := ParseImageVersion(d.LegacyImage())
		assert.True(t, ok, "legacy image of %s must parse", d.ID())
	}
}

// The layouts of 17 and 18 are stated here as LITERALS on purpose: they are a
// release-safety contract, not an implementation detail. The generator mounts
// what PostgresLayoutFor returns and the mount is part of GetHashInput, so a
// value that moves by a byte recreates every user's postgres container on
// upgrade — which is also what internal/namespace pins from the other side
// with its postgres17.hashinput.golden. Deriving the expectation from the
// table would only assert that the table equals itself.
func TestPostgresLayout(t *testing.T) {
	legacy := PostgresLayoutFor(17)
	assert.Equal(t, PostgresLayout{MountPath: "/var/lib/postgresql/data",
		PGData: "/var/lib/postgresql/data", PGVersionRel: "PG_VERSION"}, legacy)
	assert.Equal(t, legacy, PostgresLayoutFor(9))
	// A major below every rule is data the launcher could not identify;
	// answering with the NEWEST layout would start an empty new cluster beside
	// it. 0 is what an unparsable tag yields.
	assert.Equal(t, legacy, PostgresLayoutFor(0))
	assert.Equal(t, legacy, PostgresLayoutFor(-1))

	v18 := PostgresLayoutFor(18)
	assert.Equal(t, PostgresLayout{MountPath: "/var/lib/postgresql",
		PGData: "", PGVersionRel: "18/docker/PG_VERSION"}, v18)
	assert.Equal(t, "19/docker/PG_VERSION", PostgresLayoutFor(19).PGVersionRel)
}

// KnownPostgresDataPaths is what the daemon's pin-seeding probe looks for
// inside a volume when there is no pin and no container to ask: every
// PG_VERSION path it can spell, newest first, so the newest cluster in a
// volume wins. The order is the verdict — a probe that tried the legacy path
// first would read 17 out of a volume that also holds an 18 cluster.
func TestKnownPostgresDataPathsAreTheProbeOrder(t *testing.T) {
	assert.Equal(t, []string{"18/docker/PG_VERSION", "PG_VERSION"}, KnownPostgresDataPaths())

	// The path a given major announces itself at is always one the probe
	// knows, or a namespace could be seeded from data no probe can read.
	for _, major := range []int{9, 17, 18} {
		assert.Contains(t, KnownPostgresDataPaths(), PostgresLayoutFor(major).PGVersionRel, "major %d", major)
	}
}

func TestMigrationResultOK(t *testing.T) {
	assert.True(t, MigrationResult{ID: Postgres, From: "postgres:17", To: "postgres:18"}.OK())
	assert.False(t, MigrationResult{ID: Postgres, Error: "pg_restore failed"}.OK())
}
