package deps

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLegacyImagesAreTheExactReferencesProbed pins every invented pin as a
// LITERAL, because the one property that matters about them cannot be checked
// from inside this process: the tag has to EXIST in the registry. A
// LegacyImage() only reaches a container when the dependency is HELD BACK, so
// a tag that 404s is invisible until a bundle offers a different major — and
// then it is a stand whose Keycloak cannot start, naming an image that has
// never existed.
//
// Evidence, probed read-only against the Docker Hub v2 API on 2026-09-09 (the
// plan's R0 section carries the digests):
//
//	postgres:17               → 17.11    exists
//	rabbitmq:4.1-management   → 4.1.8-management  exists
//	zookeeper:3.9             → 3.9.5    exists
//	mongo:4.0                 → 4.0.28   exists
//	keycloak/keycloak:26      → 404. Keycloak publishes NO bare-major tag.
//	qdrant/qdrant:v1.14     → 404. Qdrant publishes no floating minor tag
//	                            either, so its legacy image names a concrete
//	                            patch (probed 2026-09-15).
//	qdrant/qdrant:v1.14.1     → exists, and it is the only version RAG has ever
//	                            shipped with — every bundle carrying EcosRagApp
//	                            names it.
//	keycloak/keycloak:26.4    → exists (sha256:9409c59b…, 2025-12-01), and it
//	                            is the minor of keycloak/keycloak:26.4.5, the
//	                            value both the Kotlin 1.3.9 launcher and every
//	                            Go 2.x release have emitted — so it is also the
//	                            truthful answer to "what has this namespace
//	                            been running".
//
// Re-verifying these is a human/CI-with-network job; changing one of these
// strings without doing so is how the Keycloak defect got in.
func TestLegacyImagesAreTheExactReferencesProbed(t *testing.T) {
	want := map[ID]string{
		Postgres:  "postgres:17",
		RabbitMQ:  "rabbitmq:4.1-management",
		Zookeeper: "zookeeper:3.9",
		Keycloak:  "keycloak/keycloak:26.4",
		MongoDB:   "mongo:4.0",
		Qdrant:    "qdrant/qdrant:v1.14.1",
	}
	for _, d := range All() {
		assert.Equal(t, want[d.ID()], d.LegacyImage(), string(d.ID()))
	}
	assert.Equal(t, "postgres:17", PostgresLegacyImage)
}

// TestLegacyImagesFloatOnlyOverNonBreakingComponents is the rule that makes a
// floating legacy tag SAFE rather than merely convenient.
//
// A LegacyImage() is a reference the launcher INVENTS for data whose version
// it could not read: for PostgreSQL it is a MAJOR out of PG_VERSION, for
// RabbitMQ/ZooKeeper/MongoDB it is nothing more than "a volume exists", and
// for Keycloak it is "the PostgreSQL data exists, so this namespace has run".
// A component the tag does not name is therefore a component the launcher does
// not know — so re-resolving the tag must never be able to produce a version
// the descriptor itself would call a data move.
//
// The test maxes out every omitted component and asserts the move is not
// breaking. That is what forbids a legacy tag like "postgres" (no version at
// all) or "rabbitmq:4-management" (floating over the MINOR, which is exactly
// what minorBreaking calls a format change).
func TestLegacyImagesFloatOnlyOverNonBreakingComponents(t *testing.T) {
	const maxComponent = 9999
	for _, d := range All() {
		t.Run(string(d.ID()), func(t *testing.T) {
			img := d.LegacyImage()
			parsed, ok := d.ParseVersion(img)
			require.True(t, ok, "legacy image %q must carry a readable version", img)

			named := numericComponents(t, parsed.Raw)
			require.GreaterOrEqual(t, named, 1, "legacy image %q names no version component", img)

			maxed := parsed
			if named < 2 {
				maxed.Minor = maxComponent
			}
			if named < 3 {
				maxed.Patch = maxComponent
			}
			assert.False(t, d.IsBreaking(parsed, maxed),
				"legacy image %q floats over a component %s calls breaking: %v → %v",
				img, d.ID(), parsed, maxed)
		})
	}
}

// numericComponents counts the version components a tag actually NAMES —
// "4.1-management" names two, "17" names one. It reads the leading numeric
// prefix the same way ParseImageVersion does, so the two cannot disagree about
// what the tag said.
func numericComponents(t *testing.T, tag string) int {
	t.Helper()
	// trimVersionTagV, not a second copy of the rule: Qdrant's tags carry a
	// leading "v" and a counter that did not skip it would read every one of
	// them as naming NO component.
	numeric := trimVersionTagV(tag)
	end := 0
	for end < len(numeric) && (numeric[end] == '.' || (numeric[end] >= '0' && numeric[end] <= '9')) {
		end++
	}
	parts := strings.Split(strings.TrimSuffix(numeric[:end], "."), ".")
	n := 0
	for _, p := range parts {
		if _, err := strconv.Atoi(p); err != nil {
			break
		}
		n++
	}
	return n
}
