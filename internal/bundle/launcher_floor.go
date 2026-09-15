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

// LatestRunnableBundle answers the newest version key in bundlesDir that this
// launcher can run: the list is already newest-first, so it walks down and
// takes the first bundle whose floor is cleared.
//
// It keeps walking past a blocked rung rather than stopping there — a repo may
// publish a newer bundle with a LOWER floor than the one below it, and stopping
// early would hide a version that fits.
//
// A floor NEVER fails this call. This function runs on the load and reload
// paths (namespace_loader.go, server.go's doReloadEx, routes_reloadplan.go) as
// well as the create path — a namespace whose persisted BundleRef.Key is the
// literal string "LATEST" is a real, tested state, and refusing to resolve it
// here would leave the operator unable to open a namespace at all, which is
// exactly what this feature's design forbids. When nothing clears the floor,
// the newest version is returned anyway, with a warning logged; the floor
// still bites, once, on the config WRITE path (a later task), where the
// message can name the launcher version to update to.
//
// A missing/unreadable bundles directory is a different class of problem (not
// a floor question at all) and still surfaces as ErrNoBundles / a plain error,
// exactly as findLatestBundle did.
func LatestRunnableBundle(bundlesDir, launcherVersion string, log *slog.Logger) (string, error) {
	if log == nil {
		log = slog.Default()
	}
	if _, err := os.Stat(bundlesDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w in %s", ErrNoBundles, bundlesDir)
		}
		// A genuine I/O error (e.g. permission denied) must not be swallowed
		// into "no versions found" — ListBundleVersions itself would return an
		// empty slice for both, and this caller loses the distinction unless
		// it stats first.
		return "", fmt.Errorf("list bundles in %s: %w", bundlesDir, err)
	}
	versions := ListBundleVersions(bundlesDir)
	if len(versions) == 0 {
		return "", fmt.Errorf("%w in %s", ErrNoBundles, bundlesDir)
	}
	if strings.TrimSpace(launcherVersion) == "" {
		return versions[0], nil
	}
	// versions is newest-first (ListBundleVersions), and the walk below never
	// stops early, so this holds the floor of the FIRST (i.e. NEWEST) skipped
	// version encountered — not the numerically highest floor among all
	// skipped versions, which a name like "highestFloor" would wrongly imply.
	var newestSkippedFloor string
	for _, key := range versions {
		// ReadMinLauncherVersion answers "" for a missing file too — a bundle
		// this walk cannot even find has no floor to enforce, so it is treated
		// as runnable rather than skipped.
		floor := ReadMinLauncherVersion(bundlesDir, key)
		if !NeedsNewerLauncher(floor, launcherVersion) {
			return key, nil
		}
		if newestSkippedFloor == "" {
			newestSkippedFloor = floor
		}
		log.Warn("Skipping a bundle this launcher is too old for",
			"version", key, "needs", floor, "launcher", launcherVersion)
	}
	// Nothing cleared the floor. Answer the NEWEST version anyway rather than
	// an error: this function runs on the load and reload paths too, and a
	// refusal there would leave the operator unable to open the namespace at
	// all — the one thing this feature's design forbids. The floor still bites,
	// once, where it belongs: the config WRITE gate refuses the result and says
	// which launcher version to update to.
	log.Warn("No bundle in this repo clears this launcher's floor; taking the newest",
		"version", versions[0], "newestFloor", newestSkippedFloor, "launcher", launcherVersion)
	return versions[0], nil
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

// NewerBundle describes a version above the one a namespace runs.
type NewerBundle struct {
	Version string // the newest version key above Current
	// RequiresLauncher is the floor of that version when this launcher cannot
	// run it, and "" when it can. Empty means "you can switch to it now".
	RequiresLauncher string
}

// FindNewerBundle reports the newest version above currentKey in the same
// scope, or nil when the namespace already runs the newest one.
//
// Answering "is there a newer one" costs no file I/O: ListBundleVersions is a
// directory walk and the key comes from the filename. Only telling "you can
// switch" from "update the launcher first" opens files, and only the versions
// ABOVE the current one, stopping at the first that fits — the same downward
// walk LATEST does.
//
// When a runnable version sits between the current one and a blocked one, the
// RUNNABLE one is reported: it is the one the operator can act on today.
func FindNewerBundle(bundlesDir, currentKey, launcherVersion string) *NewerBundle {
	currentKey = strings.TrimSpace(currentKey)
	if currentKey == "" || strings.EqualFold(currentKey, "LATEST") {
		return nil
	}
	scope := bundleScopeOf(currentKey)
	var blocked *NewerBundle
	for _, key := range ListBundleVersions(bundlesDir) {
		if bundleScopeOf(key) != scope {
			continue
		}
		if compareBundleVersions(key, currentKey) <= 0 {
			break // the list is newest-first: everything below is older
		}
		path := findBundleFile(bundlesDir, key)
		if path == "" {
			continue
		}
		floor := readBundleMinLauncherVersion(path)
		if !NeedsNewerLauncher(floor, launcherVersion) {
			return &NewerBundle{Version: key}
		}
		if blocked == nil {
			blocked = &NewerBundle{Version: key, RequiresLauncher: floor}
		}
	}
	return blocked
}

// bundleScopeOf is the path before the last '/' ("archive/2025.5" → "archive",
// "2026.2" → ""). compareBundleVersions ranks every unscoped version above
// every scoped one, so comparisons only make sense inside one scope.
func bundleScopeOf(key string) string {
	if idx := strings.LastIndex(key, "/"); idx >= 0 {
		return key[:idx]
	}
	return ""
}
