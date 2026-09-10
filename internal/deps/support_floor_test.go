package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The floors are a CONTRACT with the user, not an implementation detail: they
// are the oldest versions the platform has been tested on, agreed one by one,
// and a release that quietly lowers one claims support for something nobody
// verified. So the table is written out here as well, and every registered
// descriptor must appear in it — a new dependency arriving without a floor is
// a dependency whose oldest supported version nobody decided.
func TestEveryDependencyHasTheAgreedSupportFloor(t *testing.T) {
	want := map[ID]Version{
		Postgres:  {Major: 17},
		RabbitMQ:  {Major: 4, Minor: 1},
		Zookeeper: {Major: 3, Minor: 9},
		Keycloak:  {Major: 26, Minor: 4},
		MongoDB:   {Major: 4},
	}
	got := make(map[ID]Version, len(All()))
	for _, d := range All() {
		floor := d.SupportFloor()
		require.NotEqual(t, Version{}, floor, "%s has no support floor", d.ID())
		got[d.ID()] = floor
	}
	assert.Equal(t, want, got)
}

// The floor is a VERSION compared with the registry's own ordering, not a
// string: "4.1.2-management" is not orderable as text (it sorts after
// "4.10.0-management"), and every other rule in this package reads a version
// out of the tag with ParseVersion. It is also a strict floor — the floor
// itself is supported.
func TestBelowSupportFloor(t *testing.T) {
	cases := []struct {
		id    ID
		image string
		below bool
	}{
		{Postgres, "postgres:1.1.1", true},
		{Postgres, "postgres:16.9", true},
		{Postgres, "postgres:17", false},   // the floor itself is supported
		{Postgres, "postgres:17.0", false}, // …spelled either way
		{Postgres, "postgres:18.6", false},
		// The floor asks about the VERSION, so where the image comes from is
		// not part of the question: a private-registry copy of an unsupported
		// version is just as unsupported.
		{Postgres, "registry.example.com:5000/postgres:16.9", true},
		// An unparsable tag names no version, so there is nothing to compare
		// with the floor. It is held back by Breaking, which is a different
		// rule with a different message.
		{Postgres, "postgres:latest", false},
		{Postgres, "postgres:17@sha256:abc", false},
		{Postgres, "", false},
		{RabbitMQ, "rabbitmq:4.0.9-management", true},
		{RabbitMQ, "rabbitmq:4.1.0-management", false},
		{RabbitMQ, "rabbitmq:3.13.7-management", true},
		{Zookeeper, "zookeeper:3.8.4", true},
		{Zookeeper, "zookeeper:3.9", false},
		{Keycloak, "keycloak/keycloak:26.3.5", true},
		{Keycloak, "keycloak/keycloak:26.4", false},
		{Keycloak, "keycloak/keycloak:27.0", false},
		{MongoDB, "mongo:3.6", true},
		{MongoDB, "mongo:4.0", false},
	}
	for _, c := range cases {
		d, ok := Lookup(c.id)
		require.True(t, ok, "%s is not registered", c.id)
		assert.Equal(t, c.below, BelowSupportFloor(d, c.image), "%s %s", c.id, c.image)
	}
}
