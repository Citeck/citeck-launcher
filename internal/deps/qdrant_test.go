package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQdrantTagsCarryALeadingV is the reason ParseImageVersion learned to skip
// a leading "v" at all: Qdrant publishes NO tag without it. Every bundle in
// the field names "qdrant/qdrant:v1.14.1", and a launcher that reads that tag
// as unparsable holds the dependency back PERMANENTLY — deps.Breaking answers
// true for an unreadable tag on either side, so registering qdrant without
// this would have produced a dependency that can never be upgraded and whose
// only refusal message is about the tag.
func TestQdrantTagsCarryALeadingV(t *testing.T) {
	d, ok := Lookup(Qdrant)
	require.True(t, ok)

	v, ok := d.ParseVersion("qdrant/qdrant:v1.14.1")
	require.True(t, ok, "the tag every bundle ships must parse")
	assert.Equal(t, 1, v.Major)
	assert.Equal(t, 14, v.Minor)
	assert.Equal(t, 1, v.Patch)
	// Raw keeps the tag EXACTLY as written, "v" and all: it is what a message
	// names, and an operator told to pull "1.14.1" would be sent after a tag
	// that does not exist on Docker Hub.
	assert.Equal(t, "v1.14.1", v.Raw)
}

// A "v" is skipped only where a VERSION follows it. Anything else stays
// unknown, which is the safe answer (the caller holds the dependency back).
func TestLeadingVIsNotAWildcard(t *testing.T) {
	for _, image := range []string{
		"qdrant/qdrant:latest",
		"qdrant/qdrant:velocity",
		"qdrant/qdrant:v",
		"qdrant/qdrant:vv1.14.1",
	} {
		_, ok := ParseImageVersion(image)
		assert.False(t, ok, "%q names no version", image)
	}
}

// TestQdrantUpgradeSupport states the vendor's rule: storage compatibility
// spans exactly ONE minor (data written by 1.16.x is read by 1.17.x), skipping
// a minor is not supported, and nothing is documented across a major.
func TestQdrantUpgradeSupport(t *testing.T) {
	cases := []struct {
		from, to string
		allowed  bool
		via      string
		why      string
	}{
		{from: "qdrant/qdrant:v1.14.1", to: "qdrant/qdrant:v1.15.5", allowed: true},
		{from: "qdrant/qdrant:v1.14.1", to: "qdrant/qdrant:v1.14.3", allowed: true,
			why: "a patch move inside one series"},
		{from: "qdrant/qdrant:v1.14.1", to: "qdrant/qdrant:v1.14.1", allowed: true,
			why: "the same version is not a move"},
		{from: "qdrant/qdrant:v1.14.1", to: "qdrant/qdrant:v1.16.0", via: "1.15",
			why: "a skipped minor names the one hop the operator can make now"},
		{from: "qdrant/qdrant:v1.14.1", to: "qdrant/qdrant:v1.19.0", via: "1.15"},
		// Across a major the vendor documents nothing, so there is no path to
		// name — "go through 1.99 first" would be an invention.
		{from: "qdrant/qdrant:v1.19.0", to: "qdrant/qdrant:v2.0.0", via: ""},
		// Backwards is unsupported at every granularity and has no path.
		{from: "qdrant/qdrant:v1.15.5", to: "qdrant/qdrant:v1.14.1", via: ""},
		{from: "qdrant/qdrant:v1.14.3", to: "qdrant/qdrant:v1.14.1", via: "",
			why: "a backwards patch move is still backwards"},
	}
	for _, c := range cases {
		t.Run(c.from+"->"+c.to, func(t *testing.T) {
			got := support(t, Qdrant, c.from, c.to)
			assert.Equal(t, c.allowed, got.Allowed, c.why)
			assert.Equal(t, c.via, got.Via, c.why)
		})
	}
}

// A minor bump is a data move for Qdrant — that is what makes the one-minor
// span a rule at all — while a patch bump inside a series is not.
func TestQdrantBreakingRule(t *testing.T) {
	d, ok := Lookup(Qdrant)
	require.True(t, ok)
	assert.True(t, Breaking(d, "qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5"))
	assert.False(t, Breaking(d, "qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.14.3"))
	assert.True(t, d.Migratable(), "this launcher ships a qdrant migrator")
}

// The support floor is the version RAG shipped with: nothing older has ever
// been on a stand, so an operator choosing one would be choosing a version the
// platform has never run.
func TestQdrantSupportFloor(t *testing.T) {
	d, ok := Lookup(Qdrant)
	require.True(t, ok)
	assert.False(t, BelowSupportFloor(d, "qdrant/qdrant:v1.14.1"), "the floor itself is supported")
	assert.False(t, BelowSupportFloor(d, "qdrant/qdrant:v1.15.5"))
	assert.True(t, BelowSupportFloor(d, "qdrant/qdrant:v1.13.6"))
}
