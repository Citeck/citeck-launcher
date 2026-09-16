package migrate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathReadsItsEnds(t *testing.T) {
	p := Path{"a:1", "a:2", "a:3"}
	assert.Equal(t, "a:1", p.From())
	assert.Equal(t, "a:3", p.To())
	assert.Equal(t, 3, p.Len())
}

func TestPathHopsAreTheAdjacentPairs(t *testing.T) {
	p := Path{"a:1", "a:2", "a:3"}
	assert.Equal(t, [][2]string{{"a:1", "a:2"}, {"a:2", "a:3"}}, p.Hops())
}

// The rungs are what the plan CLIMBS: everything after the version the data
// currently runs on.
func TestPathRungsExcludeTheStartingPoint(t *testing.T) {
	assert.Equal(t, []string{"a:2", "a:3"}, Path{"a:1", "a:2", "a:3"}.Rungs())
	assert.Equal(t, []string{"a:2"}, Path{"a:1", "a:2"}.Rungs())
}

// A path of one is "already there", and it must not read as a hop from a
// version to itself.
func TestAPathOfOneHasNoHops(t *testing.T) {
	p := Path{"a:1"}
	assert.Equal(t, "a:1", p.From())
	assert.Equal(t, "a:1", p.To())
	assert.Empty(t, p.Hops())
	assert.Empty(t, p.Rungs())
}

func TestAnEmptyPathAnswersEmptyEnds(t *testing.T) {
	var p Path
	assert.Empty(t, p.From())
	assert.Empty(t, p.To())
	assert.Empty(t, p.Hops())
}
