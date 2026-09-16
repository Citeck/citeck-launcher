package deps

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// The pin is ABOVE EVERY rung — the bundle's ladder names only versions
// older than what is already running. This is a distinct shape from
// TestRouteFromARungOnTheLadderDropsWhatIsBehind (which drops SOME rungs):
// here every rung is dropped, and the route is just the pin — "nothing to
// do" — same as already being on the top rung. Downstream this shape never
// reaches the gate's route computation at all: the gate's BundleOlder arm
// answers it first (the candidate itself is older than the pin), so
// UpgradeRoute's correct answer here is never actually consulted in
// production — but it must still be the right answer, since a caller that
// bypassed BundleOlder would otherwise silently see "nothing to do" instead
// of "this is backwards".
func TestRouteWithThePinAboveEveryRungIsJustThePin(t *testing.T) {
	d, ok := Lookup(Postgres)
	require.True(t, ok)
	route, ok := UpgradeRoute(d, "postgres:8", []string{"postgres:3", "postgres:4", "postgres:6", "postgres:7"})
	assert.True(t, ok)
	assert.Equal(t, []string{"postgres:8"}, route)
}

// MalformedLadder is the shape check UpgradeRoute itself refuses to route
// through, exposed so a caller with no pin yet can still say something about
// a bad ladder instead of silently using its last rung.
func TestMalformedLadderAcceptsAWellFormedLadderOfAnyLength(t *testing.T) {
	d := qdrantDesc(t)
	assert.False(t, MalformedLadder(d, nil))
	assert.False(t, MalformedLadder(d, []string{"qdrant/qdrant:v1.15.5"}))
	assert.False(t, MalformedLadder(d, []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1",
	}))
}

func TestMalformedLadderRejectsRungsOutOfOrder(t *testing.T) {
	d := qdrantDesc(t)
	assert.True(t, MalformedLadder(d, []string{"qdrant/qdrant:v1.19.1", "qdrant/qdrant:v1.15.5"}))
}

func TestMalformedLadderRejectsADuplicateRung(t *testing.T) {
	d := qdrantDesc(t)
	assert.True(t, MalformedLadder(d, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.15.5"}))
}

func TestMalformedLadderRejectsAnUnreadableRung(t *testing.T) {
	d := qdrantDesc(t)
	assert.True(t, MalformedLadder(d, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:latest"}))
}
