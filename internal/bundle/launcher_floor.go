package bundle

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/citeck/citeck-launcher/internal/update"
	"gopkg.in/yaml.v3"
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

// ErrLauncherTooOldForAllBundles is returned when a bundles directory HAS
// versions but every one of them declares a floor above this launcher. It is
// deliberately distinct from ErrNoBundles: "no bundles found" would send the
// operator to inspect a repository that is perfectly fine, when the thing to
// change is the launcher.
var ErrLauncherTooOldForAllBundles = errors.New("every bundle requires a newer launcher")

// LatestRunnableBundle answers the newest version key in bundlesDir that this
// launcher can run: the list is already newest-first, so it walks down and
// takes the first bundle whose floor is cleared.
//
// It keeps walking past a blocked rung rather than stopping there — a repo may
// publish a newer bundle with a LOWER floor than the one below it, and stopping
// early would hide a version that fits.
//
// Every skipped version is logged with the floor it wanted. Silence here is
// what makes an operator believe the repo has not moved on.
func LatestRunnableBundle(bundlesDir, launcherVersion string, log *slog.Logger) (string, error) {
	if log == nil {
		log = slog.Default()
	}
	versions := ListBundleVersions(bundlesDir)
	if len(versions) == 0 {
		return "", fmt.Errorf("%w in %s", ErrNoBundles, bundlesDir)
	}
	if strings.TrimSpace(launcherVersion) == "" {
		return versions[0], nil
	}
	var highestFloor string
	for _, key := range versions {
		path := findBundleFile(bundlesDir, key)
		if path == "" {
			continue
		}
		floor := readBundleMinLauncherVersion(path)
		if !NeedsNewerLauncher(floor, launcherVersion) {
			return key, nil
		}
		if highestFloor == "" {
			highestFloor = floor
		}
		log.Warn("Skipping a bundle this launcher is too old for",
			"version", key, "needs", floor, "launcher", launcherVersion)
	}
	return "", fmt.Errorf("%w (newest needs %s, this launcher is %s)",
		ErrLauncherTooOldForAllBundles, highestFloor, launcherVersion)
}

// ReadMinLauncherVersion answers the floor declared by one version key in a
// bundles directory, or "" when the key has no file, the file is unreadable, or
// it declares nothing. Exported because `citeck install` needs the same answer
// for a version the operator just picked (Task 5), and a second file reader
// there would be a second place for the node-vs-map rule to drift.
func ReadMinLauncherVersion(bundlesDir, key string) string {
	path := findBundleFile(bundlesDir, key)
	if path == "" {
		return ""
	}
	return readBundleMinLauncherVersion(path)
}

// readBundleMinLauncherVersion reads ONLY the floor out of a bundle file, so the
// LATEST walk pays a parse per skipped version rather than a full bundle
// resolution (alias maps, image repos, dependencies) it would throw away.
func readBundleMinLauncherVersion(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from findBundleFile
	if err != nil {
		return ""
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return ""
	}
	return parseBundleMinLauncherVersion(documentRoot(&root))
}
