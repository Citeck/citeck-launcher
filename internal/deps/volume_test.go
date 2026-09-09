package deps

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenerationOneIsTodaysVolumeNames is the release-safety test of the whole
// generation counter. The names are LITERALS on purpose: they are what every
// launcher before the counter emitted, they are part of GetHashInput through
// the generated volume mount, and a single byte of drift here recreates every
// user's data container on upgrade — for PostgreSQL onto an empty new volume.
//
// Generation 1 is spelled with the suffix "2" because "2" was never a version:
// it is what the Kotlin launcher happened to call its second volume layout.
// The counter starts where the on-disk names already are.
func TestGenerationOneIsTodaysVolumeNames(t *testing.T) {
	cases := []struct {
		id               ID
		gen1, gen2, gen3 string
	}{
		{Postgres, "postgres2", "postgres3", "postgres4"},
		{RabbitMQ, "rabbitmq2", "rabbitmq3", "rabbitmq4"},
		{Zookeeper, "zookeeper2", "zookeeper3", "zookeeper4"},
		// The generator emits "mongo2:/data/db", so the base is the APP name
		// and not the dependency id ("mongodb").
		{MongoDB, "mongo2", "mongo3", "mongo4"},
		// Keycloak keeps its state in the PostgreSQL database and has no
		// volume of its own, at any generation.
		{Keycloak, "", "", ""},
	}
	for _, c := range cases {
		t.Run(string(c.id), func(t *testing.T) {
			d, ok := Lookup(c.id)
			require.True(t, ok)
			assert.Equal(t, c.gen1, VolumeName(d, 1))
			assert.Equal(t, c.gen2, VolumeName(d, 2))
			assert.Equal(t, c.gen3, VolumeName(d, 3))
			// A generation the counter cannot hold is generation 1: an absent
			// or zero VolumeGen in a state file written before the counter
			// existed means "the volume every namespace has always used", and
			// VolumeName has to agree with DependencyState.Gen about that.
			assert.Equal(t, c.gen1, VolumeName(d, 0))
			assert.Equal(t, c.gen1, VolumeName(d, -3))
		})
	}
}

// The counter's whole point is that a volume name is NOT derived from the
// version: only the pin's generation decides it.
func TestVolumeNameDoesNotDependOnTheVersion(t *testing.T) {
	d, ok := Lookup(Postgres)
	require.True(t, ok)
	assert.Equal(t, "postgres2", VolumeName(d, 1))
	assert.Equal(t, "postgres3", VolumeName(d, 2))
	// Generation 2 for PostgreSQL happens to spell the same string the 18
	// layout used before the counter — a coincidence worth pinning, because it
	// is what leaves a namespace already migrated in a dev build unstranded.
	assert.Equal(t, "/var/lib/postgresql", PostgresLayoutFor(18).MountPath,
		"sanity: the 18 layout still exists and no longer carries a volume")
}

func TestParseVolumeNameRoundTripsEveryDependency(t *testing.T) {
	for _, d := range All() {
		if d.VolumeBase() == "" {
			// Keycloak: no volume, so no name to parse back.
			_, _, ok := ParseVolumeName(VolumeName(d, 1))
			assert.False(t, ok, "%s has no volume", d.ID())
			continue
		}
		for gen := 1; gen <= 12; gen++ {
			name := VolumeName(d, gen)
			gotID, gotGen, ok := ParseVolumeName(name)
			require.True(t, ok, "%s gen %d (%q)", d.ID(), gen, name)
			assert.Equal(t, d.ID(), gotID, name)
			assert.Equal(t, gen, gotGen, name)
		}
	}
}

// omitempty is what keeps an existing state file's meaning: a launcher that
// predates the counter reads the image and ignores nothing, and a state file
// this launcher writes for an unmigrated namespace is byte-identical to the
// one it read.
func TestDependencyStateOmitsAnAbsentGeneration(t *testing.T) {
	b, err := json.Marshal(DependencyState{Image: "postgres:17"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"image":"postgres:17"}`, string(b))

	b, err = json.Marshal(DependencyState{Image: "postgres:18", VolumeGen: 2})
	require.NoError(t, err)
	assert.JSONEq(t, `{"image":"postgres:18","volumeGen":2}`, string(b))

	var st DependencyState
	require.NoError(t, json.Unmarshal([]byte(`{"image":"postgres:17"}`), &st))
	assert.Equal(t, 1, st.Gen())
}

func TestParseVolumeNameRejections(t *testing.T) {
	for _, name := range []string{
		"",
		"pgadmin2",   // a real launcher volume, but not a dependency's data
		"postgres",   // no generation suffix at all
		"postgres0",  // suffix 0 would be generation -1
		"postgres1",  // suffix 1 would be generation 0; gen 1 spells itself "2"
		"postgresX",  // not a number
		"postgres02", // not the spelling VolumeName produces
		"postgres 2",
		"2",
		"mongodb2",  // the id, not the volume base the generator emits
		"keycloak2", // keycloak has no data volume
		"postgres2x",
		"postgres-2",
	} {
		t.Run(name, func(t *testing.T) {
			id, gen, ok := ParseVolumeName(name)
			assert.False(t, ok, "%q must not parse", name)
			assert.Empty(t, string(id))
			assert.Zero(t, gen)
		})
	}
}

// The seeding probe walks generations downwards from this constant, so it is
// the ceiling on how far a migrated namespace can be recognized. Ten is more
// upgrades than any of these dependencies has shipped since the launcher
// existed, and the walk only runs where there is no pin.
func TestMaxProbedVolumeGenIsAWorkableCeiling(t *testing.T) {
	assert.GreaterOrEqual(t, MaxProbedVolumeGen, 10)
	d, ok := Lookup(Postgres)
	require.True(t, ok)
	assert.Equal(t, "postgres"+strconv.Itoa(MaxProbedVolumeGen+1), VolumeName(d, MaxProbedVolumeGen))
}

// DependencyState.Gen is the one place "absent means generation 1" is decided,
// so a state file written before the counter existed keeps its meaning.
func TestDependencyStateGenNormalizes(t *testing.T) {
	assert.Equal(t, 1, DependencyState{Image: "postgres:17"}.Gen())
	assert.Equal(t, 1, DependencyState{Image: "postgres:17", VolumeGen: 0}.Gen())
	assert.Equal(t, 1, DependencyState{Image: "postgres:17", VolumeGen: -2}.Gen())
	assert.Equal(t, 1, DependencyState{Image: "postgres:17", VolumeGen: 1}.Gen())
	assert.Equal(t, 4, DependencyState{Image: "postgres:17", VolumeGen: 4}.Gen())
}
