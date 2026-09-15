# `minLauncherVersion` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A bundle can declare the launcher it needs; a launcher below that floor refuses to
create or edit a namespace onto it, `LATEST` quietly picks the newest bundle it *can* run, and the
namespace settings gear shows a dot when a newer bundle exists.

**Architecture:** One new top-level bundle key read from the YAML node tree, one comparison
helper wrapping `update.Greater`, and three consumers of them — the create/edit gate on the
daemon's single config-write path (plus `citeck install`, which bypasses the daemon), the
`LATEST` resolution walk, and a cached per-namespace "is there a newer bundle" answer surfaced on
the namespace DTO.

**Safety split, deliberate:** the REFUSAL lives in the write paths and needs nothing from the
resolver. The `LATEST` walk needs the launcher version threaded into `bundle.Resolver`, and a
construction site that forgets `WithLauncherVersion` degrades to today's behaviour (take the
newest, full stop) — it can never let a too-new bundle be written, because that is the write
path's job. Convenience is best-effort per call site; safety is in one place.

**Tech Stack:** Go 1.22+, `gopkg.in/yaml.v3` (node API), `golang.org/x/mod/semver` via
`internal/update`, React 19 + TypeScript + Tailwind, Vitest, testify.

**Spec:** `docs/specs/2026-09-15-min-launcher-version-design.md` — read it first. The plan argues
from it; where this plan and the spec disagree, the spec is right and the plan has a bug.

## Global Constraints

- Branch `feat/min-launcher-version`, cut from `fix/bundle-numeric-tag`. Do not rebase onto
  `master`: the node-tree bundle read this feature depends on exists only from that commit.
- The key's value is read from the **YAML node tree**, never from `Def.Content` or any
  `map[string]any`. In a decoded map an unquoted `2.10` is already `float64(2.1)`.
- Absent or blank `minLauncherVersion` means **no requirement**. This must be an explicit branch:
  `update.Greater("", "2.12.2")` is `true`, so a blank value would otherwise refuse everything.
- Version comparison is `update.Greater(min, current)` and nothing else. No second comparator.
- Bundle version ordering is `bundle.compareBundleVersions`, never string order.
- The check runs on WRITE only. Never on load, reload, activate, `handleGetNamespaceEdit`,
  the reload planner, `citeck setup`, or anything the runtime does.
- Every new user-visible string needs all 8 locales — `internal/i18n/locales/*.json` (server) and
  `web/src/locales/*.ts` (web). `locales.test.ts` fails on a key present in one and missing in another.
- `make check` must be green before the last commit. It does not touch the running daemon.

---

### Task 1: Read `minLauncherVersion` off the bundle

**Files:**
- Modify: `internal/bundle/bundle.go` (the `Def` struct, ~line 79)
- Modify: `internal/bundle/resolver.go` (`parseBundleFile`, the top-level walk, and a new reader
  beside `parseBundleDependencies`)
- Test: `internal/bundle/min_launcher_version_test.go` (new)

**Interfaces:**
- Consumes: nothing.
- Produces: `bundle.Def.MinLauncherVersion string` — the raw text of the key, `""` when absent or
  blank. Task 2, 3, 4, 5 and 6 read it.

- [ ] **Step 1: Write the failing test**

Create `internal/bundle/min_launcher_version_test.go`. `parseTestBundle` already exists in
`internal/bundle/dependencies_test.go` (same package) and parses a YAML string with the
`{"core": "nexus.citeck.ru"}` image-repo map.

```go
package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The key is the bundle author's statement about which launcher can run this
// file. It is read from the YAML TEXT for the same reason every image tag is:
// in a decoded map an unquoted 2.10 is already float64(2.1), and a floor
// nobody wrote is worse than no floor at all.
func TestParseBundleFile_MinLauncherVersionIsReadAsText(t *testing.T) {
	def := parseTestBundle(t, `
minLauncherVersion: 2.10
EcosModelApp:
  image:
    repository: core/ecos-model
    tag: "1.0"
`)
	assert.Equal(t, "2.10", def.MinLauncherVersion,
		"2.1 is a floor nobody wrote")
	assert.Contains(t, def.Applications, "EcosModelApp",
		"the key is not an application and must not cost the bundle its apps")
	assert.NotContains(t, def.Applications, "minLauncherVersion")
}

func TestParseBundleFile_MinLauncherVersionQuoted(t *testing.T) {
	def := parseTestBundle(t, `
minLauncherVersion: "2.13.0"
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Equal(t, "2.13.0", def.MinLauncherVersion)
}

// Absent and blank both mean "no requirement". Blank has to be spelled out:
// the comparator treats an unparseable floor as newer than everything, so a
// blank value left as-is would refuse every launcher.
func TestParseBundleFile_MinLauncherVersionAbsentOrBlank(t *testing.T) {
	absent := parseTestBundle(t, `
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Equal(t, "", absent.MinLauncherVersion)

	blank := parseTestBundle(t, `
minLauncherVersion: "   "
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Equal(t, "", blank.MinLauncherVersion, "a blank floor is no floor")
}

// A shape that is not a scalar is not a version. It costs the key, never the
// bundle — the same price an unreadable dependency entry pays.
func TestParseBundleFile_MinLauncherVersionWrongShape(t *testing.T) {
	def := parseTestBundle(t, `
minLauncherVersion:
  - 2.13.0
EcosModelApp:
  image: core/ecos-model:1.0
`)
	assert.Equal(t, "", def.MinLauncherVersion)
	assert.Contains(t, def.Applications, "EcosModelApp")
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./internal/bundle/ -run TestParseBundleFile_MinLauncherVersion -v`
Expected: compile error — `def.MinLauncherVersion` undefined.

- [ ] **Step 3: Add the field**

In `internal/bundle/bundle.go`, inside `type Def struct`, after `CiteckApps`:

```go
	// MinLauncherVersion is the launcher version this bundle declares it needs,
	// as the author's own text ("2.13.0"). Empty means no requirement — both
	// when the key is absent and when it is blank, because an unreadable floor
	// compares as newer than every release (see update.Greater) and a blank one
	// would otherwise refuse everybody.
	//
	// Only launchers from the release that introduced the check enforce it;
	// every older launcher, Go and Kotlin alike, ignores the key. So it guards
	// nothing retroactively — see AGENTS.md.
	MinLauncherVersion string `json:"minLauncherVersion,omitempty" yaml:"minLauncherVersion,omitempty"`
```

`omitempty` matters: `Def` is serialised into the namespace's `CachedBundle`
(`internal/namespace/state.go`) and read by `internal/h2migrate`.

- [ ] **Step 4: Read the key**

In `internal/bundle/resolver.go`, next to `parseBundleDependencies`, add the reader and the key
constant:

```go
// bundleMinLauncherVersionKey is the top-level bundle key carrying the launcher
// floor. Named here rather than inlined so the list of top-level keys that are
// NOT applications is readable in one place (the other is
// bundleDependenciesKey).
const bundleMinLauncherVersionKey = "minLauncherVersion"

// parseBundleMinLauncherVersion reads the floor from the YAML text.
//
// From the text, not from the generic map, for the same reason the image tags
// are: `minLauncherVersion: 2.10` is a YAML float, and a map decode hands back
// 2.1 — a floor the author never wrote, one patch release too low, silently.
//
// A shape that is not a scalar answers "" and costs the key alone. The bundle
// keeps its applications: a launcher that cannot read the floor is in no worse
// a position than one that predates the key entirely.
func parseBundleMinLauncherVersion(root *yaml.Node) string {
	node := followAlias(root)
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for _, e := range mappingEntries(node) {
		if e.key != bundleMinLauncherVersionKey {
			continue
		}
		v := followAlias(e.node)
		if v == nil || v.Kind != yaml.ScalarNode {
			return ""
		}
		return strings.TrimSpace(v.Value)
	}
	return ""
}
```

- [ ] **Step 5: Wire it into `parseBundleFile`**

In `parseBundleFile`, after `rootNode` is unmarshalled, add:

```go
	minLauncherVersion := parseBundleMinLauncherVersion(documentRoot(&rootNode))
```

In the top-level loop, extend the existing skip:

```go
		// Neither of these is an application. `dependencies` MUST be skipped
		// (an entry id colliding with the entry schema's own key would be read
		// as an app named "dependencies"); `minLauncherVersion` is a scalar and
		// would be ignored anyway — it is named here so the non-application
		// keys are one list rather than an inference from a type switch.
		if appName == bundleDependenciesKey || appName == bundleMinLauncherVersionKey {
			continue
		}
```

And carry it into the `Def`:

```go
	def := &Def{
		Key:                Key{Version: version},
		Applications:       applications,
		Dependencies:       dependencies,
		CiteckApps:         citeckApps,
		MinLauncherVersion: minLauncherVersion,
		Content:            raw,
	}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/bundle/ -v -run TestParseBundleFile_MinLauncherVersion`
Expected: 4 tests PASS.

Then the whole package: `go test ./internal/bundle/` — expected `ok`.

- [ ] **Step 7: Mutation-check the text read**

Temporarily change `parseBundleMinLauncherVersion` to read from the generic map instead
(`fmt.Sprint(raw[bundleMinLauncherVersionKey])`) and run
`go test ./internal/bundle/ -run TestParseBundleFile_MinLauncherVersionIsReadAsText`.
Expected: FAIL with `2.1` vs `2.10`. Revert the mutation.

A green test here would mean the test is not holding the rule — the whole reason the key is read
from the node tree.

- [ ] **Step 8: Commit**

```bash
git add internal/bundle/bundle.go internal/bundle/resolver.go internal/bundle/min_launcher_version_test.go
git commit -m "feat(bundle): read minLauncherVersion from the YAML text"
```

---

### Task 2: The comparison — does this launcher clear the floor?

**Files:**
- Create: `internal/bundle/launcher_floor.go`
- Test: `internal/bundle/launcher_floor_test.go`

**Interfaces:**
- Consumes: `Def.MinLauncherVersion` (Task 1).
- Produces:
  - `func NeedsNewerLauncher(minLauncherVersion, launcherVersion string) bool` — true when the
    floor is above the launcher. Blank floor → false. Blank launcher version → false.
  - `func (d *Def) NeedsNewerLauncher(launcherVersion string) bool` — nil-safe method form.

`internal/bundle` may import `internal/update`: `update` depends only on `internal/fsutil`, so
there is no cycle (verified with `go list -deps`).

- [ ] **Step 1: Write the failing test**

```go
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
		{"the v prefix is normalised on both sides", "v2.13.0", "2.12.2", true},
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
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./internal/bundle/ -run 'TestNeedsNewerLauncher|TestDefNeedsNewerLauncher' -v`
Expected: compile error — `NeedsNewerLauncher` undefined.

- [ ] **Step 3: Implement**

```go
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
// Two behaviours are inherited rather than written, and both are wanted:
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bundle/ -run 'TestNeedsNewerLauncher|TestDefNeedsNewerLauncher' -v`
Expected: all subtests PASS.

- [ ] **Step 5: Mutation-check the argument order**

Swap the arguments in the implementation (`update.Greater(current, floor)`) and re-run.
Expected: FAIL on "floor above the launcher" AND on "a dev build is never refused". Revert.

The second failure is the one that matters — the dev-build row is the only thing standing between
a swapped comparison and a developer who cannot create a namespace.

- [ ] **Step 6: Commit**

```bash
git add internal/bundle/launcher_floor.go internal/bundle/launcher_floor_test.go
git commit -m "feat(bundle): one comparison for the launcher floor, measured"
```

---

### Task 3: `LATEST` takes the newest bundle this launcher can run

**Files:**
- Modify: `internal/bundle/resolver.go` (`Resolver` struct, a new `WithLauncherVersion`, the
  `LATEST` branch of `Resolve`, and `findLatestBundle`'s neighbourhood)
- Modify: `internal/daemon/namespace_loader.go:255`, `internal/daemon/server.go:697`,
  `internal/daemon/routes_ns.go:316`, `internal/daemon/routes_ns.go:1165`,
  `internal/daemon/routes_reloadplan.go:128`
- Test: `internal/bundle/latest_runnable_test.go` (new)

**Interfaces:**
- Consumes: `NeedsNewerLauncher` (Task 2), `Def.MinLauncherVersion` (Task 1).
- Produces:
  - `func (r *Resolver) WithLauncherVersion(v string) *Resolver` — chainable, like
    `WithWorkspaceRepo`. Unset (the default) keeps today's behaviour exactly.
  - `func LatestRunnableBundle(bundlesDir, launcherVersion string, log *slog.Logger) (string, error)` —
    the newest version key in `bundlesDir` that this launcher can run. Used again in Task 6.
  - `func ReadMinLauncherVersion(bundlesDir, key string) string` — the floor of one version key,
    `""` when absent. Used by Task 5.
  - `func readBundleMinLauncherVersion(path string) string` — the path-shaped form, used by
    Task 6 inside the same package.

- [ ] **Step 1: Write the failing test**

```go
package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/assert"
)

// writeBundleDir lays out a bundles directory: version key → file body.
func writeBundleDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for key, body := range files {
		path := filepath.Join(dir, key+".yaml")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	return dir
}

const appOnly = "EcosModelApp:\n  image: core/ecos-model:1.0\n"

func floored(v string) string {
	return "minLauncherVersion: \"" + v + "\"\n" + appOnly
}

// LATEST means "the newest one you can run". Refusing instead would leave an
// old launcher unable to create ANY namespace from a repo that has moved on,
// Quick Start included — the bricking this feature avoids at load time,
// transplanted to create time.
func TestLatestRunnableBundle_SkipsWhatThisLauncherCannotRun(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.1": appOnly,
		"2026.2": appOnly,
		"2026.3": floored("2.13.0"),
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.2", got)
}

func TestLatestRunnableBundle_TakesTheNewestWhenItFits(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2": appOnly,
		"2026.3": floored("2.12.0"),
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.3", got)
}

// The walk does not stop at the first floor it cannot clear: a repo can publish
// a newer bundle with a LOWER floor than the one below it.
func TestLatestRunnableBundle_KeepsWalkingPastABlockedRung(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.1": appOnly,
		"2026.2": floored("2.12.0"),
		"2026.3": floored("2.13.0"),
	})
	got, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.2", got)
}

// An unknown launcher version enforces nothing, so LATEST is the plain latest.
func TestLatestRunnableBundle_NoLauncherVersionTakesTheNewest(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2": appOnly,
		"2026.3": floored("2.13.0"),
	})
	got, err := LatestRunnableBundle(dir, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "2026.3", got)
}

// Nothing runnable at all is not "no bundles" — the repo HAS bundles, this
// launcher just cannot run any of them. The error has to say so, because
// "no bundles found" would send the operator to look at a repo that is fine.
func TestLatestRunnableBundle_NothingRunnable(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.3": floored("2.13.0"),
	})
	_, err := LatestRunnableBundle(dir, "2.12.2", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLauncherTooOldForAllBundles)
	assert.Contains(t, err.Error(), "2.13.0", "the error must name the floor to update to")
}
```

- [ ] **Step 2: Run the test and watch it fail**

Run: `go test ./internal/bundle/ -run TestLatestRunnableBundle -v`
Expected: compile error — `LatestRunnableBundle` and `ErrLauncherTooOldForAllBundles` undefined.

- [ ] **Step 3: Implement the walk**

In `internal/bundle/launcher_floor.go`:

```go
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bundle/ -run TestLatestRunnableBundle -v`
Expected: 5 tests PASS.

- [ ] **Step 5: Thread the version into the resolver**

In `internal/bundle/resolver.go`, add to `type Resolver struct`:

```go
	// launcherVersion is this build's version, used ONLY to resolve LATEST to
	// the newest bundle this launcher can run. Empty (the default) keeps the
	// historical behaviour: LATEST is the newest version, full stop.
	//
	// It is not a safety mechanism and must not become one — a construction
	// site that forgets WithLauncherVersion loses the convenience, never the
	// refusal. The refusal lives on the config WRITE paths, which do not go
	// through the resolver.
	launcherVersion string
```

and the chainable setter next to `WithWorkspaceOverlay`:

```go
// WithLauncherVersion tells the resolver which launcher it is running inside,
// so LATEST can skip bundles that declare a higher minLauncherVersion.
// Chainable.
func (r *Resolver) WithLauncherVersion(v string) *Resolver {
	r.launcherVersion = v
	return r
}
```

In `Resolve`, replace the LATEST branch:

```go
	key := ref.Key
	if strings.EqualFold(key, "LATEST") {
		latest, latestErr := LatestRunnableBundle(bundlesDir, r.launcherVersion, r.log())
		if latestErr != nil {
			return nil, latestErr
		}
		key = latest
	}
```

- [ ] **Step 6: Set it at every resolver that resolves for real use**

Append `.WithLauncherVersion(<version>)` to the chains at:

- `internal/daemon/namespace_loader.go:255` — the loader takes its inputs in a struct; add a
  `LauncherVersion string` field to it, fill it from `d.version` at the call site, and pass it.
- `internal/daemon/server.go:697` (`doReloadEx`) — `d.version`.
- `internal/daemon/routes_ns.go:316` (`resolveLatestBundleKey`, the create path's LATEST probe) — `d.version`.
- `internal/daemon/routes_ns.go:1165` (`handleBundleRepoPull`) — `d.version`.
- `internal/daemon/routes_reloadplan.go:128` — `d.version`.

Leave `internal/cli/upgrade.go:80` alone: it lists versions, it does not resolve LATEST.

- [ ] **Step 7: Prove the create path sets it**

Add to `internal/daemon/routes_ns_crud_test.go`:

```go
// The create path resolves LATEST, so it is the one that must not hand back a
// bundle this launcher cannot run. A construction site that forgets
// WithLauncherVersion loses exactly this.
func TestCreateNamespace_LatestSkipsABundleThisLauncherCannotRun(t *testing.T) {
	d := newTestDaemonWithBundles(t, map[string]string{
		"2026.2": "EcosModelApp:\n  image: core/ecos-model:1.0\n",
		"2026.3": "minLauncherVersion: \"9.9.9\"\nEcosModelApp:\n  image: core/ecos-model:1.0\n",
	})
	d.version = "2.12.2"

	key, err := d.resolveLatestBundleKey("ws-target", "community")
	require.NoError(t, err)
	assert.Equal(t, "2026.2", key)
}
```

If `newTestDaemonWithBundles` does not exist, build it from the fixture helper
`TestCreateNamespace_LatestUnsyncedRepoRefused` already uses in the same file — reuse that
daemon-construction path rather than inventing a second one.

- [ ] **Step 8: Run the tests**

Run: `go test ./internal/bundle/ ./internal/daemon/ -run 'LatestRunnable|LatestSkips|CreateNamespace'`
Expected: `ok` for both packages.

- [ ] **Step 9: Commit**

```bash
git add internal/bundle/ internal/daemon/
git commit -m "feat(bundle): LATEST resolves to the newest bundle this launcher can run"
```

---

### Task 4: Refuse a config write that names a too-new bundle

**Files:**
- Modify: `internal/api/dto.go` (the `ErrCode…` const block)
- Modify: `internal/daemon/ns_config_store.go` (`persistNamespaceConfig`)
- Modify: `internal/daemon/routes_ns.go` (map the error on create/edit),
  `internal/daemon/routes_config.go` (map it on upgrade/raw PUT)
- Modify: `internal/i18n/locales/{en,ru,zh,es,de,fr,pt,ja}.json`
- Test: `internal/daemon/ns_launcher_floor_test.go` (new)

**Interfaces:**
- Consumes: `Def.NeedsNewerLauncher` (Task 2), `Resolver` (Task 3).
- Produces: `api.ErrCodeLauncherTooOld = "LAUNCHER_TOO_OLD"`, HTTP 409, localized body.

- [ ] **Step 1: Write the failing test**

Create `internal/daemon/ns_launcher_floor_test.go`. The daemon fixture is
`newNsCrudTestDaemon(t)` from `internal/daemon/routes_ns_crud_test.go:22` (same package): SQLite
store, unlocked secret service, routes mounted, no runtime and no docker.

```go
package daemon

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/config"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// writeFlooredWorkspace lays out a workspace repo whose `community` bundle repo
// IS the workspace repo (no url → shouldUseLocalBundles), one file per entry:
// version key → the minLauncherVersion it declares ("" for none).
func writeFlooredWorkspace(t *testing.T, wsID string, floors map[string]string) {
	t.Helper()
	repoDir := config.WorkspaceRepoDir(wsID)
	require.NoError(t, os.MkdirAll(filepath.Join(repoDir, "community"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "workspace-v1.yml"),
		[]byte("bundleRepos:\n  - id: community\n    name: Community\n    path: community\n"), 0o600))
	for key, floor := range floors {
		body := "EcosModelApp:\n  image: core/ecos-model:1.0\n"
		if floor != "" {
			body = "minLauncherVersion: \"" + floor + "\"\n" + body
		}
		require.NoError(t, os.WriteFile(
			filepath.Join(repoDir, "community", key+".yaml"), []byte(body), 0o600))
	}
}

// nsConfigYAML marshals a minimal namespace config naming one bundle. If
// namespace.ValidateYAML rejects it for a missing field, copy the minimal
// VALID config from an existing daemon test rather than inventing fields here —
// the point of this helper is the bundle ref, not the rest of the schema.
func nsConfigYAML(t *testing.T, name, repo, key string) []byte {
	t.Helper()
	raw, err := namespace.MarshalNamespaceConfig(&namespace.Config{
		ID:        "ns1",
		Name:      name,
		BundleRef: bundle.Ref{Repo: repo, Key: key},
	})
	require.NoError(t, err)
	return raw
}

// The write path is the gate. It asks what the config being WRITTEN names, not
// whether the bundle ref changed — see the two tests below for why that
// distinction is the whole design.
func TestPersistNamespaceConfig_RefusesABundleThisLauncherCannotRun(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	d.version = "2.12.2"
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": "", "2026.3": "2.13.0"})

	err := d.persistNamespaceConfig("ws1", "ns1", nsConfigYAML(t, "X", "community", "2026.3"))

	var floor *errLauncherTooOld
	require.ErrorAs(t, err, &floor)
	assert.Equal(t, "2.13.0", floor.needs)
	assert.Equal(t, "2.12.2", floor.current)

	rows, listErr := d.store.ListNamespaces("ws1")
	require.NoError(t, listErr)
	assert.Empty(t, rows, "a refused write must leave nothing behind")
}

// THE WAY BACK. A namespace already bound to a too-new bundle — written by a
// newer launcher on the same machine, or by hand — must still be editable ONTO
// something this launcher can run. If the gate asked "did the ref change" this
// would be refused too, and the namespace would be a dead end.
func TestPersistNamespaceConfig_AllowsMovingOffATooNewBundle(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": "", "2026.3": "2.13.0"})

	// Written while no floor was enforced (an unknown launcher version enforces
	// nothing) — this stands in for "some newer launcher wrote it".
	d.version = ""
	require.NoError(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X", "community", "2026.3")))

	d.version = "2.12.2"
	assert.NoError(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X", "community", "2026.2")),
		"moving OFF a too-new bundle is the way out and must be allowed")

	assert.Error(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X renamed", "community", "2026.3")),
		"an edit that KEEPS the too-new bundle is still refused")
}

// A bundle that is not on disk yields no refusal: there is nothing to read, and
// the create path refuses an unsynced LATEST with its own code.
func TestPersistNamespaceConfig_UnresolvableBundleIsNotARefusal(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d, _ := newNsCrudTestDaemon(t)
	d.version = "2.12.2"
	writeFlooredWorkspace(t, "ws1", map[string]string{"2026.2": ""})

	assert.NoError(t, d.persistNamespaceConfig("ws1", "ns1",
		nsConfigYAML(t, "X", "community", "2099.1")))
}

// The HTTP shape of the refusal, and the promise that nothing is persisted.
// Modelled on TestCreateNamespace_LatestUnsyncedRepoRefused.
func TestCreateNamespace_PinnedTooNewBundleRefusedAndNothingPersisted(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	d.version = "2.12.2"
	writeFlooredWorkspace(t, "ws-target", map[string]string{"2026.2": "", "2026.3": "2.13.0"})

	body := `{"name":"X","authType":"BASIC","users":["admin"],` +
		`"bundleRepo":"community","bundleKey":"2026.3","workspaceId":"ws-target"}`
	req := httptest.NewRequest("POST", api.Namespaces, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), api.ErrCodeLauncherTooOld)
	assert.Contains(t, rec.Body.String(), "2.13.0",
		"the refusal must name the version to update to, or it is not actionable")

	rows, err := d.store.ListNamespaces("ws-target")
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused create must leave no namespace behind")
}

// Opening the edit dialog is a READ. Gating it would take away the only control
// that can move the namespace off the bundle it is stuck on.
func TestGetNamespaceEdit_StillOpensOnATooNewBundle(t *testing.T) {
	t.Setenv("CITECK_HOME", t.TempDir())
	d, mux := newNsCrudTestDaemon(t)
	writeFlooredWorkspace(t, "wsMain", map[string]string{"2026.3": "2.13.0"})
	d.version = ""
	require.NoError(t, d.persistNamespaceConfig("wsMain", "ns1",
		nsConfigYAML(t, "X", "community", "2026.3")))
	d.version = "2.12.2"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/ns1/edit", http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), api.ErrCodeLauncherTooOld)
}

var _ = errors.Is // keep the import if the final file does not need it
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/daemon/ -run 'LauncherCannotRun|MovingOffATooNew|StillOpensOnATooNew|PinnedTooNew' -v`
Expected: compile error — `api.ErrCodeLauncherTooOld` undefined.

- [ ] **Step 3: Add the error code**

In `internal/api/dto.go`, inside the `ErrCode…` const block (the block has an AST gate test,
`internal/api/errcodes_test.go`, requiring the `ErrCode` prefix and a doc comment):

```go
	// ErrCodeLauncherTooOld is returned (HTTP 409) when the namespace config
	// being written names a bundle whose minLauncherVersion is above this
	// launcher's version. It is raised on WRITE only — creating, editing,
	// upgrading — never on load: refusing to load would leave the operator
	// unable to open the namespace and pick a different bundle, which is the
	// only way out of the situation.
	ErrCodeLauncherTooOld = "LAUNCHER_TOO_OLD"
```

- [ ] **Step 4: Gate the single write path**

In `internal/daemon/ns_config_store.go`:

```go
// errLauncherTooOld carries the refusal out of persistNamespaceConfig so each
// route can map it to its own response shape.
type errLauncherTooOld struct {
	bundleRef string
	needs     string
	current   string
}

func (e *errLauncherTooOld) Error() string {
	return fmt.Sprintf("bundle %s needs launcher %s, this is %s", e.bundleRef, e.needs, e.current)
}

// launcherFloorRefusal resolves the bundle the config names and reports why it
// may not be written, or nil when it may.
//
// Resolution is OFFLINE: this runs on every config write, including a rename,
// and a write that waits on git is a worse bug than a floor noticed one sync
// late. A bundle that is not on disk yields no refusal — there is nothing to
// read, and the create path already refuses an unsynced LATEST with
// BUNDLE_NOT_SYNCED.
func (d *Daemon) launcherFloorRefusal(wsID string, cfg *namespace.Config) *errLauncherTooOld {
	if cfg == nil || cfg.BundleRef.IsEmpty() || d.version == "" {
		return nil
	}
	resolver := bundle.NewResolverWithAuth(config.BundlesDataDir(wsID), makeTokenLookup(d.secretReaderFunc())).
		WithLauncherVersion(d.version).
		WithOffline(true)
	res, err := resolver.Resolve(cfg.BundleRef)
	if err != nil || res == nil || res.Bundle == nil {
		return nil
	}
	if !res.Bundle.NeedsNewerLauncher(d.version) {
		return nil
	}
	return &errLauncherTooOld{
		bundleRef: cfg.BundleRef.String(),
		needs:     res.Bundle.MinLauncherVersion,
		current:   d.version,
	}
}
```

and call it at the top of `persistNamespaceConfig`, after `ValidateYAML`:

```go
	if refusal := d.launcherFloorRefusal(wsID, cfg); refusal != nil {
		return refusal
	}
```

If the resolver has no `WithOffline`, add one in the same chainable style as
`WithLauncherVersion` (the `offline` field already exists on the struct).

- [ ] **Step 5: Map it on every route**

`internal/daemon/routes_ns.go` — create already has `createNamespaceError`; wrap:

```go
	var floor *errLauncherTooOld
	if errors.As(err, &floor) {
		writeErrorCode(w, http.StatusConflict, api.ErrCodeLauncherTooOld,
			strings.Join(d.translatorFor(r).RenderAll([]msg.Message{
				msg.New("bundle.msg.launcherTooOld",
					"bundle", floor.bundleRef, "needs", floor.needs, "current", floor.current),
			}), " "))
		return
	}
```

Do the same in the edit handler (`handlePutNamespaceEdit`), the upgrade handler
(`handleUpgradeNamespace`) and the raw config PUT (`handlePutConfig`). Four call sites — that is
the price of four write routes; the alternative (mapping inside `persistNamespaceConfig`) would
put HTTP concerns in the store layer.

- [ ] **Step 6: Add the sentence in 8 locales**

`internal/i18n/locales/en.json`:

```json
  "bundle.msg.launcherTooOld": "Bundle {bundle} needs launcher {needs} or newer; this launcher is {current}. Update the launcher, or pick an earlier bundle."
```

`ru.json`:

```json
  "bundle.msg.launcherTooOld": "Бандл {bundle} требует лончер {needs} или новее, а этот — {current}. Обновите лончер или выберите бандл постарше."
```

Translate the VALUE for `zh`, `es`, `de`, `fr`, `pt`, `ja` — do not leave English there; the
locale completeness test checks values, not just keys.

- [ ] **Step 7: Run the tests**

Run: `go test ./internal/daemon/ ./internal/api/ ./internal/i18n/`
Expected: `ok` for all three.

- [ ] **Step 8: Mutation-check the way back**

Change the gate to refuse whenever the bundle ref CHANGES (rather than on the resulting ref) and
re-run `TestPersistNamespaceConfig_AllowsMovingOffATooNewBundle`.
Expected: FAIL. Revert.

That test is the way back; without it the feature can ship as a trap.

- [ ] **Step 9: Commit**

```bash
git add internal/api/ internal/daemon/ internal/i18n/
git commit -m "feat(daemon): refuse a config write onto a bundle this launcher is too old for"
```

---

### Task 5: `citeck install` obeys the same floor

**Files:**
- Modify: `internal/cli/install.go` (`resolveRelease`, ~line 996)
- Modify: `internal/i18n/locales/*.json` (one more key, 8 files)
- Test: `internal/cli/install_launcher_floor_test.go` (new)

**Interfaces:**
- Consumes: `bundle.NeedsNewerLauncher`, `bundle.Def.MinLauncherVersion`, `cli.BuildInfo.Version`
  (already read at `internal/cli/install.go:144` as `ver := info.Version`).
- Produces: nothing for later tasks.

`citeck install` does NOT go through the daemon — it marshals the namespace config and writes it
with `fsutil.AtomicWriteFile` (`internal/cli/install.go:301-307`). A daemon-side gate alone leaves
the CLI able to create exactly the namespace the gate exists to prevent.

`resolveRelease` ends in `pickRelease`, an interactive TUI, so the floor check goes in a
separate pure function that `resolveRelease` calls with the already-picked ref. That function is
what the test drives — a test that has to answer a terminal picker tests the picker, not the rule.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/install_launcher_floor_test.go`:

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/bundle"
)

// writeInstallBundles lays out a bundles directory: version key → the floor it
// declares ("" for none).
func writeInstallBundles(t *testing.T, floors map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for key, floor := range floors {
		body := "EcosModelApp:\n  image: core/ecos-model:1.0\n"
		if floor != "" {
			body = "minLauncherVersion: \"" + floor + "\"\n" + body
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, key+".yaml"), []byte(body), 0o600))
	}
	return dir
}

// citeck install writes the namespace config ITSELF (fsutil.AtomicWriteFile),
// bypassing the daemon, so the floor has to be checked here or the CLI can
// create exactly the namespace the daemon's gate exists to prevent.
func TestCheckReleaseFloor_RefusesABundleAboveTheFloor(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": "9.9.9"})

	err := checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2026.3"}, "2.12.2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "9.9.9", "the message must name the version to update to")
	assert.Contains(t, err.Error(), "2.12.2", "and the version the operator is on")
}

func TestCheckReleaseFloor_AcceptsABundleAtTheFloor(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": "2.12.2"})
	assert.NoError(t, checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2026.3"}, "2.12.2"))
}

func TestCheckReleaseFloor_NoFloorNoRefusal(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": ""})
	assert.NoError(t, checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2026.3"}, "2.12.2"))
}

// A version whose file is not there is not a refusal — the picker could only
// have offered it from a listing, and a missing file is the repo's problem, not
// the launcher's floor.
func TestCheckReleaseFloor_MissingFileIsNotARefusal(t *testing.T) {
	dir := writeInstallBundles(t, map[string]string{"2026.3": ""})
	assert.NoError(t, checkReleaseFloor(dir, bundle.Ref{Repo: "community", Key: "2099.1"}, "2.12.2"))
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/cli/ -run TestCheckReleaseFloor -v`
Expected: compile error — `checkReleaseFloor` undefined.

- [ ] **Step 3: Implement**

In `internal/cli/install.go`, beside `resolveRelease`:

```go
// checkReleaseFloor refuses a picked release whose bundle declares a
// minLauncherVersion above this build.
//
// It exists apart from resolveRelease because resolveRelease ends in an
// interactive picker: the rule has to be reachable by a test that does not have
// to answer a terminal. A bundle file that is not on disk is not a refusal.
func checkReleaseFloor(bundlesDir string, ref bundle.Ref, launcherVersion string) error {
	floor := bundle.ReadMinLauncherVersion(bundlesDir, ref.Key)
	if !bundle.NeedsNewerLauncher(floor, launcherVersion) {
		return nil
	}
	return fmt.Errorf("%s", t("install.release.launcherTooOld",
		"bundle", ref.String(), "needs", floor, "current", launcherVersion))
}
```

`bundle.ReadMinLauncherVersion(bundlesDir, key string) string` is the exported form of the
`readBundleMinLauncherVersion` helper added in Task 3 — export it there (it resolves the file with
`findBundleFile` and answers "" when it is missing) rather than growing a second file reader here.

Then call it from `resolveRelease`, after `ref, err := pickRelease(withVersions)` and BEFORE
`nsCfg.BundleRef = ref`:

```go
	if err := checkReleaseFloor(bundlesDirFor(ref.Repo), ref, launcherVersion); err != nil {
		return err
	}
```

Thread `launcherVersion` in as a parameter from `info.Version`, which the install command already
reads at `internal/cli/install.go:144` — do not reach for a package-level variable. `bundlesDirFor`
is the same directory `discoverRepos` already listed versions from; reuse that lookup rather than
recomputing the path.

- [ ] **Step 4: Add the key in 8 locales**

`internal/i18n/locales/en.json`:

```json
  "install.release.launcherTooOld": "Bundle {bundle} needs launcher {needs} or newer; this launcher is {current}. Update the launcher, or pick an earlier release."
```

Translate the value in the other seven.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/cli/ ./internal/i18n/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/ internal/i18n/
git commit -m "feat(cli): citeck install refuses a bundle above this launcher's floor"
```

---

### Task 6: Work out whether a newer bundle exists

**Files:**
- Modify: `internal/bundle/launcher_floor.go` (add the query)
- Modify: `internal/daemon/server.go` (`activeNamespace`: one field),
  `internal/daemon/namespace_loader.go` (fill on load), `internal/daemon/server.go:697`
  (fill on reload), `internal/daemon/routes_ns.go:1165` (refill after a pull)
- Test: `internal/bundle/newer_bundle_test.go` (new)

**Interfaces:**
- Consumes: `ListBundleVersions`, `compareBundleVersions`, `readBundleMinLauncherVersion`,
  `NeedsNewerLauncher`.
- Produces:

```go
// NewerBundle describes a version above the one a namespace runs.
type NewerBundle struct {
	Version string // the newest version key above Current
	// RequiresLauncher is the floor of that version when this launcher cannot
	// run it, and "" when it can. Empty means "you can switch to it now".
	RequiresLauncher string
}

func FindNewerBundle(bundlesDir, currentKey, launcherVersion string) *NewerBundle
```

- [ ] **Step 1: Write the failing test**

```go
func TestFindNewerBundle_NothingNewer(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.1": appOnly, "2026.2": appOnly})
	assert.Nil(t, FindNewerBundle(dir, "2026.2", "2.12.2"))
}

func TestFindNewerBundle_NewerAndRunnable(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.2": appOnly, "2026.3": appOnly})
	got := FindNewerBundle(dir, "2026.2", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "2026.3", got.Version)
	assert.Equal(t, "", got.RequiresLauncher, "we can run it — nothing to ask the operator for")
}

func TestFindNewerBundle_NewerButNeedsANewerLauncher(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.2": appOnly, "2026.3": floored("2.13.0")})
	got := FindNewerBundle(dir, "2026.2", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "2026.3", got.Version)
	assert.Equal(t, "2.13.0", got.RequiresLauncher)
}

// A runnable version between the current one and a blocked one is the one to
// name: the operator can act on it today, and telling them to update the
// launcher instead would be advice they do not need.
func TestFindNewerBundle_PrefersTheRunnableOneBelowABlockedOne(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2": appOnly,
		"2026.3": appOnly,
		"2026.4": floored("2.13.0"),
	})
	got := FindNewerBundle(dir, "2026.2", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "2026.3", got.Version)
	assert.Equal(t, "", got.RequiresLauncher)
}

// compareBundleVersions ranks EVERY unscoped version above EVERY scoped one, so
// without a scope filter a namespace pinned inside archive/ would be told that
// all of mainline is newer.
func TestFindNewerBundle_ScopeIsNotCrossed(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{
		"2026.2":          appOnly,
		"archive/2025.5":  appOnly,
		"archive/2025.6":  appOnly,
	})
	assert.Nil(t, FindNewerBundle(dir, "2026.2", "2.12.2"),
		"an unscoped namespace is not behind because archive/ has files")

	got := FindNewerBundle(dir, "archive/2025.5", "2.12.2")
	require.NotNil(t, got)
	assert.Equal(t, "archive/2025.6", got.Version)
}

// A namespace whose bundle is unresolved has no current version to compare.
func TestFindNewerBundle_NoCurrentVersion(t *testing.T) {
	dir := writeBundleDir(t, map[string]string{"2026.2": appOnly})
	assert.Nil(t, FindNewerBundle(dir, "", "2.12.2"))
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/bundle/ -run TestFindNewerBundle -v`
Expected: compile error — `FindNewerBundle` undefined.

- [ ] **Step 3: Implement**

```go
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
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bundle/ -run TestFindNewerBundle -v`
Expected: 6 tests PASS.

- [ ] **Step 5: Cache it on the active namespace**

In `internal/daemon/server.go`, add to `activeNamespace` beside `dependencyUpgrades`:

```go
	// newerBundle is the last computed answer to "does this namespace's repo
	// have a version above the one it runs" — nil when it does not. Computed
	// where the bundle is already resolved (load, reload, explicit pull) and
	// NOT in handleGetNamespace, which runs on every SSE-triggered refetch: a
	// directory walk per refetch buys nothing, because the answer can only
	// change when the repo is synced.
	//
	// It therefore reflects what has been SYNCED, not what exists upstream, and
	// it must never trigger a sync of its own. A namespace list that waits on
	// git is a worse bug than a late dot.
	newerBundle *bundle.NewerBundle
```

Fill it right after the bundle resolves, at all three sites:

```go
	// The BundlesRepo entry by id — bundle.findBundleRepo is unexported, and a
	// three-line loop here is cheaper than exporting it for one caller.
	var repoEntry bundle.BundlesRepo
	for _, br := range act.workspaceConfig.BundleRepos {
		if br.ID == act.nsConfig.BundleRef.Repo {
			repoEntry = br
			break
		}
	}
	act.newerBundle = bundle.FindNewerBundle(
		d.resolveBundleDir(repoEntry), act.bundleDef.Key.Version, d.version)
```

Guard the whole block with `act.workspaceConfig != nil && act.bundleDef != nil && act.nsConfig != nil`:
a namespace whose bundle failed to resolve has no current version, and `FindNewerBundle` would
answer nil anyway — but reaching through a nil `workspaceConfig` would panic on the load path,
which is the one path that must never panic.

`act.bundleDef.Key.Version` — the RESOLVED version, not `nsConfig.BundleRef.Key`, which may still
be the symbolic `LATEST` (`internal/namespace/runtime_dto.go:78-83` encodes the same rule for
display).

- [ ] **Step 6: Run the daemon package**

Run: `go test ./internal/daemon/`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/bundle/ internal/daemon/
git commit -m "feat(bundle): work out whether a namespace's repo has a newer bundle"
```

---

### Task 7: Carry the answer to the client

**Files:**
- Modify: `internal/api/dto.go` (`NamespaceDto` + a small DTO type)
- Modify: `internal/daemon/routes_config.go` (`handleGetNamespace`, ~line 216)
- Modify: `web/src/lib/types.ts` (`NamespaceDto`)
- Test: `internal/daemon/routes_config_newer_bundle_test.go` (new)

**Interfaces:**
- Consumes: `activeNamespace.newerBundle` (Task 6).
- Produces: `NamespaceDto.NewerBundle *NewerBundleDto` with
  `{Version string; RequiresLauncher string}` → TS `newerBundle?: { version: string; requiresLauncher?: string }`.

- [ ] **Step 1: Write the failing test**

Create `internal/daemon/routes_config_newer_bundle_test.go`. `handleGetNamespace` needs a live
runtime, so use `newAppsTestDaemonRabbit(t, status)` — the same fixture
`internal/daemon/routes_ns_header_bundle_test.go:36` uses for the other config-derived DTO field.

```go
package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/citeck/citeck-launcher/internal/api"
	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/namespace"
)

// The indicator's data rides the namespace DTO, like dependencyUpgrades: it is
// the daemon's knowledge (activeNamespace), not the runtime's.
func TestGetNamespace_CarriesTheNewerBundle(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	d.activeNs.newerBundle = &bundle.NewerBundle{Version: "2026.3"}

	req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())

	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.NewerBundle)
	assert.Equal(t, "2026.3", dto.NewerBundle.Version)
	assert.Equal(t, "", dto.NewerBundle.RequiresLauncher,
		"empty means the operator can switch to it right now")
}

func TestGetNamespace_CarriesTheLauncherFloorOfTheNewerBundle(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	d.activeNs.newerBundle = &bundle.NewerBundle{Version: "2026.3", RequiresLauncher: "2.13.0"}

	req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var dto api.NamespaceDto
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &dto))
	require.NotNil(t, dto.NewerBundle)
	assert.Equal(t, "2.13.0", dto.NewerBundle.RequiresLauncher)
}

// Absent, not present-and-empty: the client branches on the field existing.
func TestGetNamespace_NoNewerBundleIsAbsentFromTheBody(t *testing.T) {
	mux, d := newAppsTestDaemonRabbit(t, namespace.NsStatusStopped)
	d.activeNs.newerBundle = nil

	req := httptest.NewRequest(http.MethodGet, api.Namespace, http.NoBody)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	assert.NotContains(t, rec.Body.String(), "newerBundle")
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/daemon/ -run TestGetNamespace_ -v`
Expected: compile error — `dto.NewerBundle` undefined.

- [ ] **Step 3: Add the DTO**

In `internal/api/dto.go`, next to `DependencyUpgradeDto`:

```go
// NewerBundleDto tells the client that the namespace's bundle repo has a
// version above the one it runs. Absent when it does not.
type NewerBundleDto struct {
	// Version is the newest version key above the running one, in the same
	// scope (an archive/ namespace is not told about mainline).
	Version string `json:"version"`
	// RequiresLauncher is the minLauncherVersion of that bundle when this
	// launcher cannot run it, and "" when it can. Empty means the operator can
	// switch right now; set means the next move is updating the launcher.
	RequiresLauncher string `json:"requiresLauncher,omitempty"`
}
```

and on `NamespaceDto`:

```go
	// NewerBundle is set when this namespace's bundle repo has a version above
	// the one it runs. Computed on load/reload/pull (see activeNamespace), so
	// it reflects what has been synced.
	NewerBundle *NewerBundleDto `json:"newerBundle,omitempty"`
```

- [ ] **Step 4: Fill it in `handleGetNamespace`**

Right after the `BundleError` block:

```go
	if nb := act.newerBundle; nb != nil {
		dto.NewerBundle = &api.NewerBundleDto{
			Version:          nb.Version,
			RequiresLauncher: nb.RequiresLauncher,
		}
	}
```

- [ ] **Step 5: Mirror the type on the front end**

In `web/src/lib/types.ts`, beside `dependencyUpgrades` on `NamespaceDto`:

```ts
  // Set when this namespace's bundle repo has a version above the one it runs.
  // `requiresLauncher` present means we cannot switch to it yet — the next move
  // is updating the launcher, not opening the settings dialog.
  newerBundle?: { version: string; requiresLauncher?: string }
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/daemon/ ./internal/api/ && cd web && pnpm run build && cd ..`
Expected: `ok` and a clean `tsc -b`.

- [ ] **Step 7: Commit**

```bash
git add internal/api/ internal/daemon/ web/src/lib/types.ts
git commit -m "feat(api): carry the newer-bundle answer on the namespace DTO"
```

---

### Task 8: The dot on the namespace settings gear

**Files:**
- Modify: `web/src/components/TabBar.tsx` (the gear button, ~lines 63-70)
- Modify: `web/src/locales/{en,ru,de,es,fr,ja,pt,zh}.ts`
- Test: `web/src/components/TabBar.test.tsx` (new — check first whether one exists)

**Interfaces:**
- Consumes: `NamespaceDto.newerBundle` (Task 7).
- Produces: nothing for later tasks.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/TabBar.test.tsx`. The store-setting pattern is
`DependencyUpgradeBanner.test.tsx:26` (`useDashboardStore.setState({ namespace: … })`); TabBar also
reads the router, so it has to be rendered inside a `MemoryRouter` at `/` — the gear only exists on
the dashboard route (`onDashboard = location.pathname === '/'`).

```tsx
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, beforeEach } from 'vitest'
import { TabBar } from './TabBar'
import { useDashboardStore } from '../lib/store'
import type { NamespaceDto } from '../lib/types'

function setNamespace(newerBundle?: NamespaceDto['newerBundle']) {
  useDashboardStore.setState({
    namespace: {
      id: 'n1', name: 'Citeck #1', status: 'STOPPED',
      bundleRef: 'community:2026.2', apps: [], newerBundle,
    } as NamespaceDto,
  })
}

function renderTabBar() {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <TabBar />
    </MemoryRouter>,
  )
}

// The gear is the control that acts on the message, so the message lives on the
// gear. An element has ONE title, so while the dot shows, the gear's title IS
// the indicator sentence — otherwise the dot appears and says nothing.
describe('TabBar namespace gear', () => {
  beforeEach(() => setNamespace(undefined))

  it('keeps the plain tooltip and shows no dot when nothing is newer', () => {
    const { container } = renderTabBar()
    const gear = screen.getByTitle('Namespace config')
    expect(gear).toBeTruthy()
    expect(container.querySelector('.bg-emerald-500')).toBeNull()
    expect(container.querySelector('.bg-amber-500')).toBeNull()
  })

  it('names the version we can switch to, with an emerald dot', () => {
    setNamespace({ version: '2026.3' })
    const { container } = renderTabBar()
    const gear = container.querySelector('button[title*="2026.3"]')
    expect(gear).not.toBeNull()
    expect(gear!.getAttribute('title')).toContain('settings')
    expect(container.querySelector('.bg-emerald-500')).not.toBeNull()
    expect(container.querySelector('.bg-amber-500')).toBeNull()
  })

  it('asks for a launcher update, with an amber dot, when we cannot run it', () => {
    setNamespace({ version: '2026.3', requiresLauncher: '2.13.0' })
    const { container } = renderTabBar()
    const gear = container.querySelector('button[title*="2026.3"]')
    expect(gear).not.toBeNull()
    expect(gear!.getAttribute('title')).toContain('2.13.0')
    expect(container.querySelector('.bg-amber-500')).not.toBeNull()
    expect(container.querySelector('.bg-emerald-500')).toBeNull()
  })
})
```

The literal `'Namespace config'` is the English value of `dashboard.nsConfig`; if the test locale
default is not English, assert against `t('dashboard.nsConfig')` the way the other component tests
in this directory do rather than hardcoding a translation.

- [ ] **Step 2: Run them and watch them fail**

Run: `cd web && pnpm run test -- TabBar`
Expected: FAIL — no dot, and the title never mentions the version.

- [ ] **Step 3: Implement**

Replace the gear button in `web/src/components/TabBar.tsx`:

```tsx
          {/* Click → typed namespace-edit form. The dot is the same 6x6 corner
              dot UpdateNotification uses for the launcher's own update, and the
              button's title BECOMES the indicator sentence while it shows: an
              element has one title, and the meaning of the dot is the more
              useful thing to say. Each sentence names the next move — without
              that the operator is told a fact and left with nowhere to go. */}
          <button
            type="button"
            className="relative p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted focus:outline-none"
            title={
              namespace.newerBundle
                ? namespace.newerBundle.requiresLauncher
                  ? t('namespace.newerBundle.needsLauncher.tooltip', {
                      version: namespace.newerBundle.version,
                      min: namespace.newerBundle.requiresLauncher,
                    })
                  : t('namespace.newerBundle.tooltip', { version: namespace.newerBundle.version })
                : t('dashboard.nsConfig')
            }
            onClick={() => setNsEditOpen(true)}
          >
            <Settings size={14} />
            {namespace.newerBundle && (
              <span
                className={`absolute right-1 top-1 h-1.5 w-1.5 rounded-full ${
                  namespace.newerBundle.requiresLauncher ? 'bg-amber-500' : 'bg-emerald-500'
                }`}
              />
            )}
          </button>
```

- [ ] **Step 4: Add the two keys in 8 locales**

`web/src/locales/en.ts`:

```ts
  'namespace.newerBundle.tooltip': 'A newer bundle is available: {version}. Open the namespace settings to switch to it.',
  'namespace.newerBundle.needsLauncher.tooltip': 'A newer bundle is available ({version}), but it needs launcher {min} or newer. Update the launcher.',
```

`web/src/locales/ru.ts`:

```ts
  'namespace.newerBundle.tooltip': 'Есть бандл новее: {version}. Откройте настройки неймспейса, чтобы перейти на него.',
  'namespace.newerBundle.needsLauncher.tooltip': 'Есть бандл новее ({version}), но он требует лончер {min} или новее. Обновите лончер.',
```

Translate the values for `de`, `es`, `fr`, `ja`, `pt`, `zh`. `locales.test.ts` checks both key
parity across all 8 files and that a value is not left in English.

- [ ] **Step 5: Run the tests**

Run: `cd web && pnpm run test && pnpm run lint && pnpm run build`
Expected: all green, including `locales.test.ts`.

- [ ] **Step 6: Mutation-check the title swap**

Change the title expression to always render `t('dashboard.nsConfig')` and re-run the TabBar
tests. Expected: the two indicator tests FAIL. Revert.

This is the collision the implementation would otherwise get wrong silently — the dot appears and
says nothing.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/TabBar.tsx web/src/components/TabBar.test.tsx web/src/locales/
git commit -m "feat(web): a dot on the namespace gear when a newer bundle exists"
```

---

### Task 9: Documentation and the full gate

**Files:**
- Modify: `AGENTS.md` (a new rule in **Key Technical Decisions**, beside the `dependencies:` rule)
- Create: `changelog/<next-version>/{en,ru,zh,es,de,fr,pt,ja}.md`; modify `changelog/index.json`
- Modify: `docs/config-layers.md` (one line)

**Interfaces:** none.

- [ ] **Step 1: Write the AGENTS.md rule**

A single bullet in the **Key Technical Decisions** section, next to the `dependencies:` section
rule — they are the two halves of one forward-compatibility story. It must say: what the key is;
that it is read from the YAML text and why; that the gate is on the WRITE and never on load, and
that this is what keeps the way back open; that `LATEST` resolves to the newest runnable bundle
while a pinned version refuses; that the floor comparison is `update.Greater` and inherits both
the dev-build exemption and the fail-closed-on-garbage behaviour from the same clause; and that
launchers older than the release adding the check ignore the key entirely, so it protects nobody
retroactively.

- [ ] **Step 2: Write the changelog in 8 locales**

Follow `changelog/AGENTS.md`: no version heading, `##` section groups, one bullet per change,
user-visible effect first. Two bullets — the floor and the indicator. Register the release in
`changelog/index.json`.

- [ ] **Step 3: One line in `docs/config-layers.md`**

The key is a property of the bundle that the layer table describes; say that it gates selection
rather than participating in image precedence.

- [ ] **Step 4: Run the full gate**

Run: `make check`
Expected: `[10/10] PASS`. It does not touch the running daemon — verified repeatedly.

- [ ] **Step 5: Commit**

```bash
git add AGENTS.md changelog/ docs/config-layers.md
git commit -m "docs: record the launcher floor and the newer-bundle indicator"
```

---

## Self-review notes for the executor

Three things in this plan are easy to implement in a way that passes its tests and is still wrong.
Each has a mutation step above; do not skip them.

1. **The argument order in `NeedsNewerLauncher`.** Swapped, it refuses dev builds and accepts
   too-old releases. The dev-build row is the only test that catches it immediately.
2. **The gate asking "did the ref change" instead of "what does the ref name".** Passes the
   refusal tests, breaks the way back.
3. **The gear's title.** A dot with the old tooltip is a dot that says nothing, and no test
   catches it unless the title assertion exists.
