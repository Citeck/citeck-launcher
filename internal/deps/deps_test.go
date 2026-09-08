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
	assert.Equal(t, []ID{Postgres, RabbitMQ, Zookeeper, Keycloak, MongoDB}, ids)

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
		{Postgres, "custom/pg:v17", "postgres:17.5", true},
		// Two unknown tags are NOT compared numerically: an unparsable tag
		// yields a zero Version, so without the ok guard this pair would read
		// as "major 0 to major 0", i.e. not breaking.
		{Postgres, "postgres:latest", "postgres:edge", true},
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

func TestOnlyPostgresIsMigratable(t *testing.T) {
	for _, d := range All() {
		assert.Equal(t, d.ID() == Postgres, d.Migratable(), string(d.ID()))
		assert.NotEmpty(t, d.LegacyImage(), string(d.ID()))
		_, ok := ParseImageVersion(d.LegacyImage())
		assert.True(t, ok, "legacy image of %s must parse", d.ID())
	}
}

func TestPostgresLayout(t *testing.T) {
	legacy := PostgresLayoutFor(17)
	assert.Equal(t, PostgresLayout{Volume: "postgres2", MountPath: "/var/lib/postgresql/data",
		PGData: "/var/lib/postgresql/data", PGVersionRel: "PG_VERSION"}, legacy)
	assert.Equal(t, legacy, PostgresLayoutFor(9))

	v18 := PostgresLayoutFor(18)
	assert.Equal(t, PostgresLayout{Volume: "postgres3", MountPath: "/var/lib/postgresql",
		PGData: "", PGVersionRel: "18/docker/PG_VERSION"}, v18)
	assert.Equal(t, "19/docker/PG_VERSION", PostgresLayoutFor(19).PGVersionRel)
}

func TestMigrationResultOK(t *testing.T) {
	assert.True(t, MigrationResult{ID: Postgres, From: "postgres:17", To: "postgres:18"}.OK())
	assert.False(t, MigrationResult{ID: Postgres, Error: "pg_restore failed"}.OK())
}
