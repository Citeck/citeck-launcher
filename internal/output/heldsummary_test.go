package output

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/i18n"
)

// FailedSummary exists to keep ONE rule — failed and held are ADDITIVE, never
// alternatives — from drifting between the three wait loops that print it. The
// rule is invisible to every other test in the repo (the CLI's own cases assert
// that a wait ENDS, not what it says), so without this the whole `if held > 0`
// half could be deleted and nothing would go red.
func TestFailedSummary_NamesTheHoldBesideTheFailures(t *testing.T) {
	i18n.InitI18n("en")

	// The WHOLE line, not a Contains of each number: every count here is a
	// single digit, so `Contains("3")` is satisfied by "9/3" just as well as by
	// "3/9" — a check that cannot see the one mistake this function can make,
	// which is putting four numbers into the wrong four slots.
	cases := []struct{ key, withHold, withoutHold string }{
		{
			"cli.startedFailedSummary",
			"3/9 apps started, 2 failed, 4 held by zookeeper, postgres",
			"3/9 apps started, 2 failed",
		},
		{
			"cli.reloadFailedSummary",
			"3/9 running, 2 failed, 4 held by zookeeper, postgres",
			"3/9 running, 2 failed",
		},
	}
	for _, c := range cases {
		if got := FailedSummary(c.key, 3, 9, 2, 4, []string{"zookeeper", "postgres"}); got != c.withHold {
			t.Errorf("%s: got %q, want %q", c.key, got, c.withHold)
		}
		if got := FailedSummary(c.key, 3, 9, 2, 0, []string{"zookeeper"}); got != c.withoutHold {
			t.Errorf("%s: got %q, want %q -- no hold, nothing to name", c.key, got, c.withoutHold)
		}
	}
}

// HeldSummary is the pure-hold verdict: how many are held, out of how many, and
// which DETACHED apps to start. The held apps themselves are never named —
// they release themselves once the root is back.
func TestHeldSummary_NamesTheRootsAndTheCounts(t *testing.T) {
	i18n.InitI18n("en")

	// Whole line, for the reason spelled out above: "9 of 2 apps" contains the
	// same two digits as "2 of 9 apps".
	const want = "2 of 9 apps are waiting for dependencies you stopped: zookeeper. " +
		"Start them to release the rest."
	if got := HeldSummary(2, 9, []string{"zookeeper"}); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
