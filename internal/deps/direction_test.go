package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MovesBackwards is plain arithmetic and, unlike "is this breaking", it is the
// same question for every dependency: which components make a move BREAKING
// differs per dependency (majors for PostgreSQL, minors for RabbitMQ), but
// 4.2.9 → 4.1.8 goes backwards for everyone.
func TestMovesBackwards(t *testing.T) {
	cases := []struct {
		name     string
		from, to Version
		want     bool
	}{
		{"same", Version{Major: 17, Minor: 5}, Version{Major: 17, Minor: 5}, false},
		{"major forwards", Version{Major: 17}, Version{Major: 18}, false},
		{"major backwards", Version{Major: 18, Minor: 6}, Version{Major: 17, Minor: 5}, true},
		{"minor forwards", Version{Major: 4, Minor: 1, Patch: 8}, Version{Major: 4, Minor: 2, Patch: 9}, false},
		{"minor backwards", Version{Major: 4, Minor: 2, Patch: 9}, Version{Major: 4, Minor: 1, Patch: 8}, true},
		{"patch forwards", Version{Major: 4, Minor: 2, Patch: 3}, Version{Major: 4, Minor: 2, Patch: 9}, false},
		{"patch backwards", Version{Major: 4, Minor: 2, Patch: 9}, Version{Major: 4, Minor: 2, Patch: 3}, true},
		// A higher minor does not rescue a lower major, and a higher patch does
		// not rescue a lower minor: the components are ordered, not summed.
		{"lower major higher minor", Version{Major: 18, Minor: 0}, Version{Major: 17, Minor: 11}, true},
		{"lower minor higher patch", Version{Major: 3, Minor: 9, Patch: 0}, Version{Major: 3, Minor: 8, Patch: 6}, true},
		{"zero versions", Version{}, Version{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, MovesBackwards(c.from, c.to))
		})
	}
}

// BundleOlder is the whole of what survives of "a bundle must never move a
// dependency's data backwards": the REPORTING half. A backwards move that is
// breaking is already held back by the generator, but it would be described as
// an "upgrade available", which is wrong. A backwards move that is NOT
// breaking (a patch revert) applies silently, exactly as it does today — that
// is the user's ruling of 2026-09-09 and it is asserted here, not merely
// omitted, so nobody re-adds a direction arm to Breaking by accident.
func TestBundleOlder(t *testing.T) {
	cases := []struct {
		id              ID
		pinned, offered string
		want            bool
	}{
		// Held today, and backwards: this is the case the fact exists for.
		{Postgres, "postgres:18.6", "postgres:17.5", true},
		{RabbitMQ, "rabbitmq:4.2.9-management", "rabbitmq:4.1.8-management", true},
		{Zookeeper, "zookeeper:3.9.5", "zookeeper:3.8.6", true},
		{Keycloak, "keycloak/keycloak:26.4.5", "keycloak/keycloak:25.0.6", true},
		{MongoDB, "mongo:5.0.0", "mongo:4.0.28", true},
		// Backwards but NOT breaking: it applies silently, so there is nothing
		// to report. Patch reverts are deliberately not held.
		{Postgres, "postgres:17.11", "postgres:17.2", false},
		{RabbitMQ, "rabbitmq:4.2.9-management", "rabbitmq:4.2.3-management", false},
		{Zookeeper, "zookeeper:3.9.5", "zookeeper:3.9.2", false},
		// Forwards, breaking or not: never "older".
		{Postgres, "postgres:17.5", "postgres:18.6", false},
		{Postgres, "postgres:17.5", "postgres:17.11", false},
		{RabbitMQ, "rabbitmq:4.1.2-management", "rabbitmq:4.2.9-management", false},
		// The same reference is not a move at all.
		{Postgres, "postgres:latest", "postgres:latest", false},
		// An unreadable tag on either side is HELD (Breaking says so), but the
		// direction is unknown — and reporting "the bundle offers an older
		// version" off a tag nobody can order would be an invention.
		{Postgres, "postgres:18.6", "postgres:latest", false},
		{Postgres, "postgres:latest", "postgres:17.5", false},
		{Postgres, "postgres:latest", "postgres:foo", false},
		// No pin at all is the generator's own arm, not this one.
		{Postgres, "", "postgres:17.5", false},
	}
	for _, c := range cases {
		t.Run(string(c.id)+" "+c.pinned+"→"+c.offered, func(t *testing.T) {
			d, ok := Lookup(c.id)
			require.True(t, ok)
			assert.Equal(t, c.want, BundleOlder(d, c.pinned, c.offered))
		})
	}
}

// The user's ruling of 2026-09-09 in one assertion: Breaking answers the
// DESCRIPTOR'S FORMAT RULE and nothing else. Direction is a separate fact
// carried separately, so a same-format backwards move still applies. Asserting
// it here rather than relying on TestBreakingRules keeps the two rules from
// being conflated the next time somebody reaches for "and it must go
// forwards".
func TestBreakingIsTheFormatRuleAndNotADirection(t *testing.T) {
	for _, c := range []struct {
		id              ID
		pinned, offered string
	}{
		{Postgres, "postgres:17.11", "postgres:17.2"},
		{RabbitMQ, "rabbitmq:4.2.9-management", "rabbitmq:4.2.3-management"},
		{Zookeeper, "zookeeper:3.9.5", "zookeeper:3.9.2"},
	} {
		d, ok := Lookup(c.id)
		require.True(t, ok)
		assert.False(t, Breaking(d, c.pinned, c.offered),
			"%s %s → %s is a patch revert: it must keep applying silently", c.id, c.pinned, c.offered)
		assert.False(t, BundleOlder(d, c.pinned, c.offered),
			"%s %s → %s applies, so there is nothing to report", c.id, c.pinned, c.offered)
	}
}
