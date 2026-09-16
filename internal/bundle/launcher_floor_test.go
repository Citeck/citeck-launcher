package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The table is measured against the real comparator, not reasoned about. Two
// rows are load-bearing and both come from the SAME clause in update.Greater
// ("an invalid version sorts highest"): a dev build is never refused, and an
// unreadable floor refuses a released launcher. Neither is an explicit branch
// in the comparator, so if they are not asserted by name a later cleanup will
// remove them without a test going red.
func TestNeedsNewerLauncher(t *testing.T) {
	cases := []struct {
		name    string
		floor   string
		current string
		want    bool
	}{
		{"floor above the launcher", "2.13.0", "2.12.2", true},
		{"equal is not above", "2.12.2", "2.12.2", false},
		{"floor below the launcher", "2.12.2", "2.13.0", false},
		{"a two-part floor means .0", "2.13", "2.12.2", true},
		{"a two-part floor is cleared by a patch above it", "2.13", "2.13.5", false},
		{"the v prefix is normalized on both sides", "v2.13.0", "2.12.2", true},
		{"a dev build is never refused", "2.13.0", "dev-20260915-124919", false},
		{"an unreadable floor refuses a release", "nonsense", "2.12.2", true},
		{"an unreadable floor still spares a dev build", "nonsense", "dev-20260915", false},
		{"a release candidate is not the release", "2.13.0", "2.13.0-rc1", true},
		{"no floor, no refusal", "", "2.12.2", false},
		{"a blank floor is no floor", "   ", "2.12.2", false},
		{"an unknown launcher version refuses nothing", "2.13.0", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, NeedsNewerLauncher(c.floor, c.current))
		})
	}
}

func TestDefNeedsNewerLauncher(t *testing.T) {
	var nilDef *Def
	assert.False(t, nilDef.NeedsNewerLauncher("2.12.2"), "a nil bundle demands nothing")
	assert.True(t, (&Def{MinLauncherVersion: "2.13.0"}).NeedsNewerLauncher("2.12.2"))
	assert.False(t, (&Def{}).NeedsNewerLauncher("2.12.2"))
}
