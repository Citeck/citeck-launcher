package bundle

import (
	"strings"

	"github.com/citeck/citeck-launcher/internal/update"
)

// NeedsNewerLauncher reports whether a bundle declaring minLauncherVersion asks
// for more than the launcher running this code.
//
// It is update.Greater and nothing else, on purpose: a second comparator would
// drift from the one the auto-updater uses, and then "your launcher is too old"
// and "an update is available" could disagree about the same two strings.
//
// Two behaviors are inherited rather than written, and both are wanted:
//
//   - A DEV BUILD is never refused. `make` stamps a non-semver version
//     ("dev-20260915-124919"), which sorts highest, so no floor is above it.
//   - An UNREADABLE floor refuses a released launcher, because it sorts highest
//     too. Fail-closed is the right direction here: the key exists to stop a
//     launcher that does not understand something, and ignoring a requirement we
//     cannot read reverses exactly that.
//
// Blank on either side means "nothing to enforce": a bundle with no floor, or a
// launcher whose own version is unknown (no ldflags at all, e.g. `go run`).
func NeedsNewerLauncher(minLauncherVersion, launcherVersion string) bool {
	floor := strings.TrimSpace(minLauncherVersion)
	current := strings.TrimSpace(launcherVersion)
	if floor == "" || current == "" {
		return false
	}
	return update.Greater(floor, current)
}

// NeedsNewerLauncher is the Def-shaped form. A nil Def demands nothing, so
// callers holding an unresolved bundle need no nil check of their own.
func (d *Def) NeedsNewerLauncher(launcherVersion string) bool {
	if d == nil {
		return false
	}
	return NeedsNewerLauncher(d.MinLauncherVersion, launcherVersion)
}
