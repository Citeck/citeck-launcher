package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func qdrantDesc(t *testing.T) Descriptor {
	t.Helper()
	d, ok := Lookup(Qdrant)
	assert.True(t, ok)
	return d
}

// The ordinary case the ladder exists for: the pin is below every rung, so the
// route is the pin followed by all of them.
func TestRouteFromBelowTheLadderWalksEveryRung(t *testing.T) {
	route, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.14.1",
		[]string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1"})
	assert.True(t, ok)
	assert.Equal(t, []string{
		"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5",
		"qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1",
	}, route)
}

// The stand already stands on a rung: the rungs at or below it are behind it.
func TestRouteFromARungOnTheLadderDropsWhatIsBehind(t *testing.T) {
	route, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.16.1",
		[]string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1"})
	assert.True(t, ok)
	assert.Equal(t, []string{"qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1"}, route)
}

// A ladder is a handrail, not a timetable: an operator standing between two
// rungs joins it at their own step.
func TestRouteFromBetweenTwoRungsStartsAtThePin(t *testing.T) {
	route, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.16.3",
		[]string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1"})
	assert.True(t, ok)
	assert.Equal(t, []string{"qdrant/qdrant:v1.16.3", "qdrant/qdrant:v1.19.1"}, route)
}

func TestRouteAlreadyOnTheTargetIsJustThePin(t *testing.T) {
	route, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.19.1",
		[]string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1"})
	assert.True(t, ok)
	assert.Equal(t, []string{"qdrant/qdrant:v1.19.1"}, route)
}

// A one-image entry is a one-rung ladder, so the ordinary single-hop case
// needs no second shape anywhere downstream.
func TestRouteOverASingleImageIsTheOrdinaryPair(t *testing.T) {
	route, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.14.1",
		[]string{"qdrant/qdrant:v1.15.5"})
	assert.True(t, ok)
	assert.Equal(t, []string{"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5"}, route)
}

// "Strictly newer than the pin" has no answer for a rung nobody can parse, and
// dropping it would invent a hop the vendor was never asked about.
func TestRouteRefusesAnUnreadableRung(t *testing.T) {
	_, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.14.1",
		[]string{"qdrant/qdrant:latest", "qdrant/qdrant:v1.19.1"})
	assert.False(t, ok)
}

func TestRouteRefusesAnUnreadablePin(t *testing.T) {
	_, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:latest",
		[]string{"qdrant/qdrant:v1.19.1"})
	assert.False(t, ok)
}

func TestRouteRefusesAnEmptyLadder(t *testing.T) {
	_, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.14.1", nil)
	assert.False(t, ok)
}

// A ladder whose rungs are not in ascending order is not a route. Sorting it
// would be the launcher rewriting the author's statement.
func TestRouteRefusesALadderThatIsNotAscending(t *testing.T) {
	_, ok := UpgradeRoute(qdrantDesc(t), "qdrant/qdrant:v1.14.1",
		[]string{"qdrant/qdrant:v1.19.1", "qdrant/qdrant:v1.15.5"})
	assert.False(t, ok)
}
