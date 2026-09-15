# Multi-hop dependency upgrade — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An image value may be a LIST; in the `dependencies:` section that list is a ladder the launcher walks rung by rung in one migration, and everywhere else its first element is taken.

**Architecture:** One YAML decoder in `internal/bundle` turns any image value (scalar, `{repository, tag}` map, or a sequence of either) into an ordered `[]string`. `dependencies:` keeps the whole list; every other reader takes `[0]`. The generator gate computes a ROUTE from the pin through the ladder, asks the vendor about each adjacent pair rather than about (pin, target), and hands the route to the migrator as a `migrate.Path`. The copy-upgrade plan makes ONE copy of the volume and raises it through every rung; the PostgreSQL plan dumps and restores through every rung, reusing one scratch volume, and only the final cluster lands in the next generation. A rung is never skipped, whatever this release's `UpgradeSupport` table says.

**Tech Stack:** Go 1.22+, `gopkg.in/yaml.v3`, testify; Kotlin/Gradle for the 1.x launcher; Docker for the integration tests.

**Spec:** `docs/specs/2026-09-15-multi-hop-dependency-upgrade-design.md`

## Global Constraints

- **A rung is NEVER skipped.** `UpgradeSupport` may only REFUSE a hop the ladder does not name; it may never authorize collapsing a ladder the author wrote. Spec §7.
- **`dependencies:` takes the WHOLE list, target = LAST element. Everything else takes `[0]`.** Spec §3.2.
- **One migration = one journal = one generation increment = rollback to the original.** True for all four migratable dependencies. Spec §6, §7.1.
- **The source volume is only ever READ**, on every rung. Spec §6, §7.1.
- **The migration dump is gzip -1, written and read through a `bash -c` pipe with `set -o pipefail`.** Without pipefail a half-finished `pg_dumpall` is followed by a gzip that exits 0 and the migration restores a truncated cluster. Spec §7.3.
- **A ladder needs NO more disk than a single hop, in either plan — and for PostgreSQL that is true only because of the STEP ORDER.** A dump is removed as soon as the restore that consumed it succeeded, and an intermediate cluster as soon as the next dump has been taken from it; the peak stays `source + one dump + one cluster`, which is today's single-hop peak. Do NOT raise the preflight's requirement: that would refuse a migration that fits. Spec §7.2.
- **An unreadable rung invalidates the whole ladder** — the dependency stays on its pin; no rung is silently skipped. Spec §4.
- **A ladder makes the entry invisible to launchers 2.12.0–2.12.2** (they skip an entry whose `image:` is a sequence). Never write a ladder for `rabbitmq` in a shipped bundle while those versions are in the field. Spec §3.3.
- Locale keys go in **all 8 files** of `internal/i18n/locales/` (en, ru, zh, es, de, fr, pt, ja) with real translations; `internal/cli/i18n_test.go` `TestLocaleCompleteness` enforces parity.
- Every new rule gets a **mutation check**: break the rule in the production code, confirm a test goes red, restore. A rule no test can distinguish from its negation is not covered.
- Build 1.x with `JAVA_HOME=~/.jdks/temurin-21.0.10 ./gradlew …` — the system Java 25 fails Gradle 8.7 with the useless message `* What went wrong: 25.0.2`.
- Run `make check` before the final commit of the series.

---

### Task 1: The image-value decoder, and the two consumption rules

**Files:**
- Modify: `internal/bundle/resolver.go` (add `decodeImageValues`; rewrite `DependencyEntry.UnmarshalYAML`; extend `parseBundleDependencies`; extend `extractBundleImage`)
- Modify: `internal/bundle/bundle.go:71-73` (`AppDef` gains `Images`)
- Test: `internal/bundle/image_values_test.go` (new), `internal/bundle/dependencies_test.go` (extend)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `func decodeImageValues(node *yaml.Node) []string` — package-private, in `internal/bundle`.
  - `bundle.DependencyEntry{ Image string; Images []string }` — `Image` is the LAST element (the target), `Images` the whole ladder.
  - `bundle.AppDef{ Image string; Images []string }` — for a `dependencies:` entry `Image` is the last and `Images` the ladder; for an `applications:` entry `Image` is the FIRST and `Images` is nil.
  - `func (w *WorkspaceConfig) DependencyImageChain(app string) []string` — the workspace section's ladder, registry-resolved, nil when it names none.

- [ ] **Step 1: Write the failing decoder tests**

Create `internal/bundle/image_values_test.go`:

```go
package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

// decodeOne parses a one-key document and hands the value node to the decoder,
// which is how every real call site reaches it.
func decodeOne(t *testing.T, doc string) []string {
	t.Helper()
	var root yaml.Node
	require := yaml.Unmarshal([]byte(doc), &root)
	assert.NoError(t, require)
	// root is a document node; its content[0] is the mapping, whose content[1]
	// is the value of the single key.
	return decodeImageValues(root.Content[0].Content[1])
}

func TestImageValuesAcceptsAScalar(t *testing.T) {
	assert.Equal(t, []string{"postgres:18.6"}, decodeOne(t, "image: postgres:18.6\n"))
}

func TestImageValuesAcceptsARepositoryTagMap(t *testing.T) {
	assert.Equal(t, []string{"postgres:18.6"},
		decodeOne(t, "image:\n  repository: postgres\n  tag: \"18.6\"\n"))
}

// The tag must keep its RAW TEXT: an unquoted 17.10 read through a generic
// map becomes float64(17.1) and the entry then names a version nobody wrote.
func TestImageValuesKeepsATrailingZeroInTheTag(t *testing.T) {
	assert.Equal(t, []string{"postgres:17.10"},
		decodeOne(t, "image:\n  repository: postgres\n  tag: 17.10\n"))
}

func TestImageValuesAcceptsASequenceOfScalars(t *testing.T) {
	assert.Equal(t, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1"},
		decodeOne(t, "image:\n  - qdrant/qdrant:v1.15.5\n  - qdrant/qdrant:v1.19.1\n"))
}

func TestImageValuesAcceptsASequenceOfMaps(t *testing.T) {
	doc := "image:\n" +
		"  - {repository: qdrant/qdrant, tag: v1.15.5}\n" +
		"  - {repository: qdrant/qdrant, tag: v1.19.1}\n"
	assert.Equal(t, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1"}, decodeOne(t, doc))
}

func TestImageValuesAcceptsAMixedSequence(t *testing.T) {
	doc := "image:\n" +
		"  - qdrant/qdrant:v1.15.5\n" +
		"  - {repository: qdrant/qdrant, tag: v1.19.1}\n"
	assert.Equal(t, []string{"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1"}, decodeOne(t, doc))
}

// A rung nobody can read poisons the WHOLE ladder. Dropping just that rung
// would silently produce a hop the vendor was never asked about.
func TestOneUnreadableRungInvalidatesTheWholeLadder(t *testing.T) {
	doc := "image:\n" +
		"  - qdrant/qdrant:v1.15.5\n" +
		"  - {repository: qdrant/qdrant}\n" +
		"  - qdrant/qdrant:v1.19.1\n"
	assert.Nil(t, decodeOne(t, doc))
}

func TestImageValuesReadsNothingOutOfAnEmptySequence(t *testing.T) {
	assert.Nil(t, decodeOne(t, "image: []\n"))
}

func TestImageValuesReadsNothingOutOfAShapeItDoesNotKnow(t *testing.T) {
	assert.Nil(t, decodeOne(t, "image:\n  nested:\n    deeper: 1\n"))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/bundle/ -run TestImageValues -v`
Expected: FAIL — `undefined: decodeImageValues`.

- [ ] **Step 3: Write the decoder**

Add to `internal/bundle/resolver.go`, next to `DependencyEntry`:

```go
// decodeImageValues reads an `image:` value in any of the three shapes the
// launcher accepts and answers them as an ordered list.
//
// The shapes are the plain string ("postgres:18.6"), the {repository, tag}
// map every typed block and bundle entry uses, and a SEQUENCE of either. What
// the sequence MEANS is not decided here: the `dependencies:` section reads it
// as a ladder whose last rung is the target, and every other reader takes the
// first element (see the callers). One decoder rather than two, because the
// rule for reading a tag has drifted into two places before and the result was
// a spelling that worked in the bundle and failed in the workspace config.
//
// It decodes from the yaml.Node rather than from a generic map on purpose: in
// a map an unquoted `tag: 17.10` has already become float64(17.1), and the
// entry then names a version nobody wrote.
//
// A shape it cannot read answers nil, and so does a sequence with ONE
// unreadable element — the whole ladder, not just that rung. A ladder is a
// route, and a route with a hole in it is a hop the vendor was never asked
// about; the dependency staying on its pin is the only honest answer.
func decodeImageValues(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if v := strings.TrimSpace(node.Value); v != "" {
			return []string{v}
		}
		return nil
	case yaml.MappingNode:
		var pair struct {
			Repository string `yaml:"repository"`
			Tag        string `yaml:"tag"`
		}
		if err := node.Decode(&pair); err != nil {
			return nil
		}
		if pair.Repository == "" || pair.Tag == "" {
			return nil
		}
		return []string{pair.Repository + ":" + pair.Tag}
	case yaml.SequenceNode:
		out := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			one := decodeImageValues(item)
			if len(one) != 1 {
				// Either unreadable or itself a sequence. Both poison the
				// ladder: see the doc comment.
				return nil
			}
			out = append(out, one[0])
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/bundle/ -run TestImageValues -v`
Expected: PASS (9 cases).

- [ ] **Step 5: Write the failing consumption-rule tests**

Append to `internal/bundle/dependencies_test.go`:

```go
// The dependencies section is the ONE place a list means a ladder, and the
// target is its LAST rung — that is where the stand is being taken.
func TestABundleDependencyLadderKeepsEveryRungAndTargetsTheLast(t *testing.T) {
	def := parseBundleFileForTest(t, `
dependencies:
  qdrant:
    image:
      - qdrant/qdrant:v1.15.5
      - qdrant/qdrant:v1.16.1
      - qdrant/qdrant:v1.19.1
`)
	entry := def.Dependencies["qdrant"]
	assert.Equal(t, []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1",
	}, entry.Images)
	assert.Equal(t, "qdrant/qdrant:v1.19.1", entry.Image,
		"the target is the last rung: that is where the ladder leads")
}

// Everywhere else a list has no route to walk, so the FIRST element is taken —
// the most conservative rung, the one most likely to match what is already in
// the volume. Same principle as LegacyImage().
func TestABundleApplicationListTakesTheFirstElement(t *testing.T) {
	def := parseBundleFileForTest(t, `
eapps:
  image:
    - harbor/ecos-eapps:1.0.0
    - harbor/ecos-eapps:2.0.0
`)
	assert.Equal(t, "harbor/ecos-eapps:1.0.0", def.Applications["eapps"].Image)
	assert.Nil(t, def.Applications["eapps"].Images,
		"an applications entry has no ladder to offer")
}

func TestAWorkspaceDependencyLadderIsRegistryResolvedAndTargetsTheLast(t *testing.T) {
	ws := parseWorkspaceConfig([]byte(`
imageRepos:
  - id: core
    url: nexus.citeck.ru
webapps: []
dependencies:
  qdrant:
    image:
      - core/qdrant:v1.15.5
      - core/qdrant:v1.19.1
`), "ws.yml", slog.Default())
	assert.Equal(t, []string{"nexus.citeck.ru/qdrant:v1.15.5", "nexus.citeck.ru/qdrant:v1.19.1"},
		ws.DependencyImageChain("qdrant"))
	assert.Equal(t, "nexus.citeck.ru/qdrant:v1.19.1", ws.DependencyImage("qdrant"))
}

func TestAWorkspaceDependencyWithALadderHoleNamesNothing(t *testing.T) {
	ws := parseWorkspaceConfig([]byte(`
imageRepos: []
webapps: []
dependencies:
  qdrant:
    image:
      - qdrant/qdrant:v1.15.5
      - {repository: qdrant/qdrant}
`), "ws.yml", slog.Default())
	assert.Empty(t, ws.DependencyImage("qdrant"))
	assert.Nil(t, ws.DependencyImageChain("qdrant"))
}
```

Add this helper at the bottom of the same file if it does not already exist (check first — `dependencies_test.go` already parses bundles and may have one under another name; reuse it rather than adding a second):

```go
func parseBundleFileForTest(t *testing.T, doc string) *Def {
	t.Helper()
	p := filepath.Join(t.TempDir(), "b.yaml")
	require.NoError(t, os.WriteFile(p, []byte(doc), 0o600))
	def, err := parseBundleFile(p, "1.0.0", nil, nil, slog.Default())
	require.NoError(t, err)
	return def
}
```

- [ ] **Step 6: Run to verify they fail**

Run: `go test ./internal/bundle/ -run 'Ladder|FirstElement' -v`
Expected: FAIL — `entry.Images undefined`, `ws.DependencyImageChain undefined`.

- [ ] **Step 7: Wire the decoder into the four call sites**

In `internal/bundle/resolver.go`:

```go
// DependencyEntry is one entry of the `dependencies:` section — an app id
// mapped to the image, or the LADDER of images, it should run.
//
// (Keep the existing doc comment about why the section exists; add:)
//
// Images is the ladder as written. Image is its LAST rung, which is the
// target — everything that only wants "what should this run" reads Image and
// is unaffected by the ladder's existence.
type DependencyEntry struct {
	Image  string   `yaml:"-"`
	Images []string `yaml:"-"`
}

func (d *DependencyEntry) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Image yaml.Node `yaml:"image"`
	}
	if err := node.Decode(&raw); err != nil {
		// Swallowed on purpose, not overlooked: see the doc comment — an entry
		// shape this launcher cannot read costs only itself, never the
		// workspace's whole config.
		return nil
	}
	values := decodeImageValues(&raw.Image)
	if len(values) == 0 {
		return nil
	}
	d.Images = values
	d.Image = values[len(values)-1]
	return nil
}
```

`WorkspaceConfig.DependencyImage` is unchanged. Add beside it:

```go
// DependencyImageChain answers the LADDER the workspace's `dependencies:`
// section names for one app id, registry-resolved rung by rung, nil when it
// names none. A single-image entry answers a one-rung ladder, so callers need
// no second shape for the ordinary case.
func (w *WorkspaceConfig) DependencyImageChain(app string) []string {
	if w == nil {
		return nil
	}
	raw := w.Dependencies[app].Images
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, image := range raw {
		resolved := w.ResolveImageRef(image)
		if resolved == "" {
			return nil
		}
		out = append(out, resolved)
	}
	return out
}
```

In `parseBundleDependencies`, resolve every rung and keep both:

```go
	for name, entry := range doc.Dependencies {
		images := make([]string, 0, len(entry.Images))
		for _, raw := range entry.Images {
			image := resolveImageRefWithRepos(raw, imageRepoMap)
			if image == "" {
				images = nil
				break
			}
			images = append(images, image)
		}
		if len(images) == 0 {
			logger.Warn("Bundle dependency entry names no image; ignoring it", "app", name)
			continue
		}
		out[name] = AppDef{Image: images[len(images)-1], Images: images}
	}
```

`extractBundleImage` reads the generic map, so it needs the sequence case — and takes `[0]`:

```go
// extractBundleImage extracts one image URL from a bundle entry's `image:`.
//
// A LIST is accepted and its FIRST element taken. Nothing outside the
// `dependencies:` section can walk a ladder — there is no pin, no hold and no
// migration out here — so the only honest reading is the most conservative
// rung, which is the same rule LegacyImage() follows.
func extractBundleImage(entry map[string]any, imageRepoMap map[string]string) string {
	imgObj, ok := entry["image"]
	if !ok {
		return ""
	}
	if list, ok := imgObj.([]any); ok {
		if len(list) == 0 {
			return ""
		}
		imgObj = list[0]
	}
	if s, ok := imgObj.(string); ok {
		return resolveImageRefWithRepos(s, imageRepoMap)
	}
	imgMap, ok := imgObj.(map[string]any)
	if !ok {
		return ""
	}
	return resolveImageURL(strVal(imgMap, "repository"), strVal(imgMap, "tag"), imageRepoMap)
}
```

And in `internal/bundle/bundle.go`:

```go
type AppDef struct {
	Image string `json:"image" yaml:"image"`
	// Images is the LADDER a `dependencies:` entry named, nil for every other
	// source. Image is its last rung.
	Images []string `json:"images,omitempty" yaml:"images,omitempty"`
}
```

- [ ] **Step 8: Run the whole bundle package**

Run: `go test ./internal/bundle/ -v 2>&1 | tail -20`
Expected: PASS, including the pre-existing `dependencies_test.go` cases.

- [ ] **Step 9: Mutation check**

Run each of these, confirm a test goes red, then restore:
1. `decodeImageValues` sequence arm returns `out[:1]` → `TestImageValuesAcceptsASequenceOfScalars` fails.
2. The unreadable-rung arm `continue`s instead of returning nil → `TestOneUnreadableRungInvalidatesTheWholeLadder` fails.
3. `DependencyEntry.UnmarshalYAML` sets `d.Image = values[0]` → `TestABundleDependencyLadderKeepsEveryRungAndTargetsTheLast` fails.
4. `extractBundleImage` takes `list[len(list)-1]` → `TestABundleApplicationListTakesTheFirstElement` fails.

- [ ] **Step 10: Commit**

```bash
git add internal/bundle/
git commit -m "feat(bundle): an image value may be a list; dependencies read it as a ladder"
```

---

### Task 2: Typed workspace blocks accept a list

**Files:**
- Modify: `internal/bundle/resolver.go:136-160` (`PostgresProps`, `KeycloakProps`, `ZookeeperProps`, `OnlyOfficeProps`, `PgAdminWsProps`, `SttSidecarProps`, `QdrantProps` — every `Image string` field)
- Test: `internal/bundle/image_values_test.go` (extend)

**Interfaces:**
- Consumes: `decodeImageValues` from Task 1.
- Produces: `type ImageRef string` in `internal/bundle`, with `UnmarshalYAML` taking the FIRST element. Call sites convert with `string(x.Image)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/bundle/image_values_test.go`:

```go
// A typed block has no ladder to walk either, so a list there takes the first
// element — and it must keep working for the two shapes that already existed.
func TestTypedWorkspaceBlocksAcceptAList(t *testing.T) {
	ws := parseWorkspaceConfig([]byte(`
imageRepos: []
webapps: []
postgres:
  image:
    - postgres:17.5
    - postgres:18.6
keycloak:
  image: keycloak/keycloak:26.4.5
zookeeper:
  image:
    repository: zookeeper
    tag: "3.9.4"
`), "ws.yml", slog.Default())
	assert.Equal(t, "postgres:17.5", string(ws.Postgres.Image))
	assert.Equal(t, "keycloak/keycloak:26.4.5", string(ws.Keycloak.Image))
	assert.Equal(t, "zookeeper:3.9.4", string(ws.Zookeeper.Image))
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/bundle/ -run TestTypedWorkspaceBlocksAcceptAList -v`
Expected: FAIL — `string(ws.Postgres.Image)` on an untyped `string` compiles, but the list value leaves it empty; the assertion on `postgres:17.5` fails.

- [ ] **Step 3: Add `ImageRef` and retype the seven fields**

In `internal/bundle/resolver.go`:

```go
// ImageRef is an image reference in a TYPED config block. It accepts the same
// three shapes decodeImageValues does, and resolves a list to its FIRST
// element: a typed block is read by launchers with no dependency gate, so the
// only honest reading of a list there is the most conservative rung.
//
// It is a named string rather than a struct so that every existing reader
// stays a one-word conversion away, and so the value keeps marshalling back
// out as the plain string it always was.
type ImageRef string

func (r *ImageRef) UnmarshalYAML(node *yaml.Node) error {
	values := decodeImageValues(node)
	if len(values) == 0 {
		*r = ""
		return nil
	}
	*r = ImageRef(values[0])
	return nil
}
```

Change `Image string` to `Image ImageRef` in `PostgresProps`, `KeycloakProps`, `ZookeeperProps`, `OnlyOfficeProps`, `PgAdminWsProps`, `SttSidecarProps`, `QdrantProps`.

- [ ] **Step 4: Fix the call sites**

Run: `go build ./... 2>&1 | head -30`
Every error is a `string(...)` conversion. There are 12 non-test sites; the compiler names each. Add the conversion, change nothing else.

- [ ] **Step 5: Run the build and the affected packages**

Run: `go build ./... && go test ./internal/bundle/ ./internal/namespace/ 2>&1 | tail -10`
Expected: PASS. The `internal/namespace` goldens must not move — a typed block's value is unchanged for every shape that already parsed.

- [ ] **Step 6: Mutation check**

`ImageRef.UnmarshalYAML` takes `values[len(values)-1]` → `TestTypedWorkspaceBlocksAcceptAList` fails on `postgres:17.5`.

- [ ] **Step 7: Commit**

```bash
git add internal/bundle/ internal/namespace/ internal/daemon/
git commit -m "feat(bundle): typed workspace image blocks accept a list and take the first rung"
```

---

### Task 3: `deps.UpgradeRoute` — the route from the pin through the ladder

**Files:**
- Create: `internal/deps/route.go`, `internal/deps/route_test.go`

**Interfaces:**
- Consumes: `deps.Descriptor`, `deps.Version`, `deps.MovesBackwards` (existing).
- Produces: `func UpgradeRoute(d Descriptor, pinned string, ladder []string) ([]string, bool)` — the full path INCLUDING the pin as element 0, `ok == false` when the pin or any rung is unreadable or the ladder is empty.

- [ ] **Step 1: Write the failing test**

Create `internal/deps/route_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/deps/ -run TestRoute -v`
Expected: FAIL — `undefined: UpgradeRoute`.

- [ ] **Step 3: Write the implementation**

Create `internal/deps/route.go`:

```go
package deps

// UpgradeRoute turns a pin and a LADDER of images into the route a migration
// walks: the pin first, then every rung strictly newer than it, in the order
// the author wrote them.
//
// Three rules, and each of them is a refusal rather than a repair:
//
//   - a rung nobody can parse invalidates the WHOLE ladder. "Strictly newer
//     than the pin" has no answer for it, and dropping it would produce a hop
//     across the gap it left — exactly the jump the ladder exists to forbid;
//   - a ladder whose rungs do not ascend is not a route. Sorting it would be
//     the launcher rewriting the author's statement about their own vendor;
//   - a pin nobody can parse has no place on the ladder at all.
//
// A ladder of one image answers the ordinary pair, so nothing downstream needs
// a second shape for the single-hop case. A pin already at the top answers a
// route of length 1, which every caller reads as "nothing to do".
func UpgradeRoute(d Descriptor, pinned string, ladder []string) ([]string, bool) {
	if len(ladder) == 0 {
		return nil, false
	}
	pinV, ok := d.ParseVersion(pinned)
	if !ok {
		return nil, false
	}
	route := []string{pinned}
	prev := pinV
	var ascending Version
	var haveAscending bool
	for _, image := range ladder {
		v, ok := d.ParseVersion(image)
		if !ok {
			return nil, false
		}
		if haveAscending && !MovesBackwards(ascending, v) && compareForRoute(ascending, v) >= 0 {
			return nil, false
		}
		ascending, haveAscending = v, true
		// Rungs at or below the pin are behind the operator; the route starts
		// where they are standing.
		if compareForRoute(v, prev) <= 0 {
			continue
		}
		route = append(route, image)
		prev = v
	}
	return route, true
}

// compareForRoute orders two versions by (major, minor, patch). It is
// expressed over MovesBackwards so the ordering rule stays in ONE place: a
// second comparison here is how "4.10 is older than 4.9" gets reintroduced.
func compareForRoute(a, b Version) int {
	switch {
	case MovesBackwards(b, a): // a is newer than b
		return 1
	case MovesBackwards(a, b):
		return -1
	default:
		return 0
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/deps/ -run TestRoute -v`
Expected: PASS (9 cases).

- [ ] **Step 5: Mutation check**

1. Drop the `compareForRoute(v, prev) <= 0` skip → `TestRouteFromARungOnTheLadderDropsWhatIsBehind` fails.
2. `return nil, false` for an unreadable rung → `continue` → `TestRouteRefusesAnUnreadableRung` fails.
3. Drop the ascending check → `TestRouteRefusesALadderThatIsNotAscending` fails.
4. Start the route empty instead of with the pin → `TestRouteOverASingleImageIsTheOrdinaryPair` fails.

- [ ] **Step 6: Commit**

```bash
git add internal/deps/route.go internal/deps/route_test.go
git commit -m "feat(deps): compute the upgrade route a pin takes through an image ladder"
```

---

### Task 4: The gate asks the vendor about every adjacent pair

**Files:**
- Modify: `internal/namespace/generator_deps.go` (`DependencyUpgrade` gains `Path`; `resolveDependencyImage` takes a chain; `vendorVerdict` walks the route)
- Modify: `internal/namespace/generator_util.go` (add `resolveAppImageChain`, `bundleImageChainOr`)
- Modify: `internal/namespace/generator_infra.go`, `generator_keycloak.go`, `generator_qdrant.go` (call sites)
- Test: `internal/namespace/generator_deps_ladder_test.go` (new)

**Interfaces:**
- Consumes: `deps.UpgradeRoute` (Task 3), `bundle.AppDef.Images` and `WorkspaceConfig.DependencyImageChain` (Task 1).
- Produces:
  - `namespace.DependencyUpgrade.Path []string` — the route including the pin at index 0. Always non-empty for a reported upgrade.
  - `func resolveAppImageChain(ctx *NsGenContext, name, namespaceImage, fallback string) []string`
  - `func bundleImageChainOr(ctx *NsGenContext, name, fallback string) []string`
  - `func resolveDependencyImage(ctx *NsGenContext, id deps.ID, chain []string) string` — signature change: `candidate string` → `chain []string`.

- [ ] **Step 1: Write the failing test**

Create `internal/namespace/generator_deps_ladder_test.go`:

```go
package namespace

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/bundle"
	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ladderCtx builds a generation context whose bundle offers `ladder` for
// `qdrant` and whose namespace is pinned at `pinned`.
func ladderCtx(pinned string, ladder []string) *NsGenContext {
	cfg := DefaultNamespaceConfig()
	bun := &bundle.Def{
		Dependencies: map[string]bundle.AppDef{
			"qdrant": {Image: ladder[len(ladder)-1], Images: ladder},
		},
	}
	ctx := NewNsGenContext(&cfg, bun)
	ctx.DependencyStates = map[deps.ID]deps.DependencyState{
		deps.Qdrant: {Image: pinned, VolumeGen: 1},
	}
	return ctx
}

func heldQdrant(t *testing.T, ctx *NsGenContext) DependencyUpgrade {
	t.Helper()
	for _, u := range ctx.DependencyUpgrades {
		if u.ID == deps.Qdrant {
			return u
		}
	}
	require.Fail(t, "qdrant was not reported as held back")
	return DependencyUpgrade{}
}

// The whole point: a hop the vendor forbids in ONE step is allowed when the
// ladder names the rungs, because every adjacent pair is a hop it permits.
func TestALadderMakesAVendorBlockedJumpReachable(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1",
		"qdrant/qdrant:v1.17.1", "qdrant/qdrant:v1.18.3", "qdrant/qdrant:v1.19.1",
	})
	effective := resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	assert.Equal(t, "qdrant/qdrant:v1.14.1", effective, "the container keeps running the pin")
	held := heldQdrant(t, ctx)
	assert.False(t, held.VendorBlocked, "every adjacent pair is one minor")
	assert.Empty(t, held.VendorVia)
	assert.Equal(t, []string{
		"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.16.1",
		"qdrant/qdrant:v1.17.1", "qdrant/qdrant:v1.18.3", "qdrant/qdrant:v1.19.1",
	}, held.Path)
	assert.Equal(t, "qdrant/qdrant:v1.19.1", held.To)
}

// Without the ladder the same pair is what it always was: blocked, with the
// vendor naming the next hop.
func TestNoLadderLeavesTheVendorRefusalExactlyAsItWas(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{"qdrant/qdrant:v1.19.1"})
	resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	held := heldQdrant(t, ctx)
	assert.True(t, held.VendorBlocked)
	assert.Equal(t, "1.15", held.VendorVia)
	assert.Equal(t, []string{"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.19.1"}, held.Path)
}

// A ladder with a rung missing is refused at the gap, and the vendor names the
// version the ladder should have had.
func TestALadderWithAGapIsBlockedAtTheGap(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{
		"qdrant/qdrant:v1.15.5", "qdrant/qdrant:v1.19.1",
	})
	resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	held := heldQdrant(t, ctx)
	assert.True(t, held.VendorBlocked)
	assert.Equal(t, "1.16", held.VendorVia, "the gap is named, not the ladder's first rung")
}

// A rung nobody can read leaves the dependency on its pin with no route: the
// preflight's message about the tag is the one the operator needs.
func TestAnUnreadableRungLeavesNoRoute(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{
		"qdrant/qdrant:latest", "qdrant/qdrant:v1.19.1",
	})
	effective := resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)
	assert.Equal(t, "qdrant/qdrant:v1.14.1", effective)

	held := heldQdrant(t, ctx)
	assert.Empty(t, held.Path, "no route at all, rather than one with a hole in it")
	assert.False(t, held.VendorBlocked, "the vendor was never asked; the tag is the problem")
}

// A single image behaves as it always did, and the path is the ordinary pair.
func TestASingleImageStillReportsAPlainPair(t *testing.T) {
	ctx := ladderCtx("qdrant/qdrant:v1.14.1", []string{"qdrant/qdrant:v1.15.5"})
	resolveDependencyImage(ctx, deps.Qdrant, ctx.Bundle.Dependencies["qdrant"].Images)

	held := heldQdrant(t, ctx)
	assert.False(t, held.VendorBlocked)
	assert.Equal(t, []string{"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5"}, held.Path)
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/namespace/ -run 'Ladder|VendorRefusal|PlainPair|NoRoute' -v`
Expected: FAIL — `resolveDependencyImage` takes a string, `DependencyUpgrade.Path` undefined.

- [ ] **Step 3: Change the gate**

In `internal/namespace/generator_deps.go`, add to `DependencyUpgrade`:

```go
	// Path is the ROUTE this upgrade takes: the pinned image at index 0, then
	// every rung of the bundle's ladder strictly newer than it, ending at To.
	// A bundle naming one image gives the ordinary two-element pair, so no
	// consumer needs a second shape for the single-hop case.
	//
	// EMPTY means there is no route at all — a rung the version parser cannot
	// read — and it is deliberately not the same as a one-element path: the
	// dependency is held back and the preflight's message about the tag is
	// what the operator needs, so nothing downstream may invent a pair here.
	Path []string
```

Rewrite the gate:

```go
// resolveDependencyImage is the gate every infra generator passes its image
// through. It takes the bundle's whole LADDER rather than one candidate: the
// candidate is its last rung, and the rungs in between are what make a hop the
// vendor forbids in one step reachable in several.
func resolveDependencyImage(ctx *NsGenContext, id deps.ID, chain []string) string {
	candidate := ""
	if len(chain) > 0 {
		candidate = chain[len(chain)-1]
	}
	d, ok := deps.Lookup(id)
	if !ok {
		return candidate
	}
	pinned := ctx.DependencyStates[id].Image
	if pinned == "" || !deps.Breaking(d, pinned, candidate) {
		ctx.DependencyImages[id] = DependencyGen{Effective: candidate, Candidate: candidate}
		return candidate
	}
	effective := rehomePin(d, pinned, candidate)
	older := deps.BundleOlder(d, effective, candidate)
	var blocked bool
	var via string
	var path []string
	if !older {
		// The route is computed from the EFFECTIVE pin, which is what the
		// migration's first container is built from — a rehomed pin names the
		// registry the stand can actually pull from.
		path, blocked, via = routeVerdict(d, effective, rehomeChain(d, effective, chain))
	}
	slog.Info("Dependency image held back by pin",
		"dependency", id, "pinned", pinned, "candidate", candidate, "effective", effective,
		"bundleOlder", older, "rungs", len(path))
	ctx.DependencyImages[id] = DependencyGen{Effective: effective, Candidate: candidate}
	ctx.DependencyUpgrades = append(ctx.DependencyUpgrades, DependencyUpgrade{
		ID: id, App: d.AppName(), From: effective, To: candidate, Migratable: d.Migratable(),
		VendorBlocked: blocked, VendorVia: via, BundleOlder: older, Path: path,
	})
	return effective
}

// rehomeChain applies the pin's registry to every rung, the same way
// rehomePin applies it to the candidate: a ladder is only usable if every rung
// on it can be pulled from the registry this stand actually has.
func rehomeChain(d deps.Descriptor, effectivePin string, chain []string) []string {
	if len(chain) == 0 {
		return nil
	}
	out := make([]string, 0, len(chain))
	for _, rung := range chain {
		out = append(out, rehomePin(d, effectivePin, rung))
	}
	return out
}

// routeVerdict computes the route and asks the vendor about EVERY adjacent
// pair on it, rather than about (pin, target).
//
// That is the whole of the ladder feature on the gate's side. Asking about the
// pair alone is what made a reachable multi-step upgrade report as blocked,
// and the intermediate it then named was one the edit gate refuses to apply.
//
// With no route — an unreadable rung — the verdict is EMPTY rather than
// guessed: the pin is held back anyway (deps.Breaking answers true for an
// unparsable tag) and the preflight's message about the tag is the accurate
// one, the same carve-out the downgrade case gets.
//
// `via` names the GAP: the version the ladder would have needed at the first
// pair the vendor refuses. It is not the ladder's own next rung — that is
// precisely the rung that does not help.
func routeVerdict(d deps.Descriptor, pinned string, chain []string) (path []string, blocked bool, via string) {
	route, ok := deps.UpgradeRoute(d, pinned, chain)
	if !ok {
		return nil, false, ""
	}
	for i := 0; i+1 < len(route); i++ {
		from, okFrom := d.ParseVersion(route[i])
		to, okTo := d.ParseVersion(route[i+1])
		if !okFrom || !okTo { // unreachable: UpgradeRoute parsed them all
			return route, false, ""
		}
		if sup := d.UpgradeSupport(from, to); !sup.Allowed {
			return route, true, sup.Via
		}
	}
	return route, false, ""
}
```

Delete `vendorVerdict` — `routeVerdict` replaces it, and leaving both means two accounts of one refusal.

- [ ] **Step 4: Add the chain resolvers and update the five call sites**

In `internal/namespace/generator_util.go`:

```go
// resolveAppImageChain is resolveAppImage's ladder-aware form: it answers the
// WINNING layer's whole list, so the ladder and the image it resolves to can
// never come from two different layers.
func resolveAppImageChain(ctx *NsGenContext, name, namespaceImage, fallback string) []string {
	if ctx.Bundle != nil {
		if dep := ctx.Bundle.Dependencies[name]; dep.Image != "" {
			warnDiscardedImageLayers(ctx, name, dep.Image, namespaceImage)
			if len(dep.Images) > 0 {
				return dep.Images
			}
			return []string{dep.Image}
		}
		if image := ctx.Bundle.Applications[name].Image; image != "" {
			warnDiscardedImageLayers(ctx, name, image, namespaceImage)
			return []string{image}
		}
	}
	if namespaceImage != "" {
		return []string{namespaceImage}
	}
	if chain := ctx.WorkspaceConfig.DependencyImageChain(name); len(chain) > 0 {
		return chain
	}
	if fallback != "" {
		return []string{fallback}
	}
	return nil
}

// resolveAppImage keeps its signature and its meaning: ONE image, the last
// rung of whatever layer won. Every non-dependency caller uses it unchanged.
func resolveAppImage(ctx *NsGenContext, name, namespaceImage, fallback string) string {
	chain := resolveAppImageChain(ctx, name, namespaceImage, fallback)
	if len(chain) == 0 {
		return ""
	}
	return chain[len(chain)-1]
}

func bundleImageChainOr(ctx *NsGenContext, name, fallback string) []string {
	return resolveAppImageChain(ctx, name, "", fallback)
}
```

Update the five gate call sites to pass a chain:

- `internal/namespace/generator_infra.go:48` → `img = resolveDependencyImage(ctx, deps.MongoDB, []string{img})` (mongo's image has no ladder source; keep the one-element form and say so in a comment)
- `internal/namespace/generator_infra.go:137` → `img := resolveDependencyImage(ctx, deps.Postgres, bundleImageChainOr(ctx, appdef.AppPostgres, fallback))`
- `internal/namespace/generator_infra.go:196` → same shape with `deps.Zookeeper`
- `internal/namespace/generator_infra.go:269` → same shape with `deps.RabbitMQ` and the `"rabbitmq:4.1.2-management"` fallback
- `internal/namespace/generator_keycloak.go:32` → same shape with `deps.Keycloak` and `kcFallback`
- `internal/namespace/generator_qdrant.go:65` → replace the two lines that resolve `image` with:

```go
	chain := resolveAppImageChain(ctx, appdef.AppQdrant, "", "")
	if len(chain) == 0 {
		slog.Error("Bundle has no qdrant image; rag will start without a vector store",
			"app", appdef.AppQdrant)
		return
	}
	image := resolveDependencyImage(ctx, deps.Qdrant, chain)
```

- [ ] **Step 5: Run the namespace package**

Run: `go test ./internal/namespace/ 2>&1 | tail -20`
Expected: PASS, goldens unmoved (`postgres17.hashinput.golden`, `rabbitmq41`, `zookeeper39`).

- [ ] **Step 6: Mutation check**

1. `routeVerdict` asks `d.UpgradeSupport(pinV, targetV)` once instead of per pair → `TestALadderMakesAVendorBlockedJumpReachable` fails.
2. `routeVerdict` returns `via = route[1]`'s series instead of `sup.Via` → `TestALadderWithAGapIsBlockedAtTheGap` fails.
3. `resolveDependencyImage` reads `chain[0]` as the candidate → `TestNoLadderLeavesTheVendorRefusalExactlyAsItWas` fails on `To`.
4. The no-route arm sets `path = []string{pinned, candidate}` → `TestAnUnreadableRungLeavesNoRoute` fails.
5. `rehomeChain` returns `chain` unchanged → add a case with a pin on a private registry and assert every rung carries it; it must fail.

- [ ] **Step 7: Commit**

```bash
git add internal/namespace/
git commit -m "feat(deps): the gate asks the vendor about every rung of the ladder, not the jump"
```

---

### Task 5: `migrate.Path` — the migrator interface takes a route

**Files:**
- Create: `internal/deps/migrate/path.go`, `internal/deps/migrate/path_test.go`
- Modify: `internal/deps/migrate/registry.go:19-36` (the `Migrator` interface)
- Modify: `internal/deps/migrate/postgres.go:51,144`, `qdrant.go:75,85`, `rabbitmq.go:74,87`, `zookeeper.go:88,98`
- Modify: `internal/deps/migrate/copy_upgrade.go` (`CopyPreflight`, `BuildCopyUpgrade`)
- Modify: `internal/deps/migrate/preflight.go` (`NewPreflightResult` call sites stay; `versionProblems` unchanged)
- Modify: `internal/daemon/routes_deps.go` (`resolveMigration`, `handleDependencyPreflight`, `handleDependencyMigrate`, `pairProblem`)

**Interfaces:**
- Consumes: `DependencyUpgrade.Path` (Task 4).
- Produces:
  - `type Path []string` with `From() string`, `To() string`, `Hops() [][2]string`, `Rungs() []string`, `Len() int`.
  - `Migrator.Preflight(ctx context.Context, env Env, path Path) PreflightResult`
  - `Migrator.Plan(ctx context.Context, env Env, path Path, opts PlanOptions) (*Plan, deps.MigrationJournal, error)`
  - `Migrator.SupportsPair(from, to deps.Version) (bool, msg.Message)` — UNCHANGED, it is a question about a pair.
  - `func CopyPreflight(ctx context.Context, env Env, id deps.ID, path Path, supportsPair func(from, to deps.Version) (bool, msg.Message)) (PreflightResult, CopyVolumes, bool)`

- [ ] **Step 1: Write the failing test**

Create `internal/deps/migrate/path_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/deps/migrate/ -run TestPath -v`
Expected: FAIL — `undefined: Path`.

- [ ] **Step 3: Write `Path`**

Create `internal/deps/migrate/path.go`:

```go
package migrate

// Path is the route one migration walks. Path[0] is the image the data runs on
// now, every later element is a rung the bundle's ladder named, and the last
// is the target.
//
// It replaces the (from, to) pair every migrator used to take. The pair was
// not merely narrower — it was a different claim: it said a migration is one
// hop, and every plan built on it was free to assume so. A Path of length 2 IS
// that pair, so the ordinary single-hop case reads identically.
type Path []string

// From is what the data runs on now.
func (p Path) From() string {
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

// To is where the migration ends.
func (p Path) To() string {
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}

func (p Path) Len() int { return len(p) }

// Hops are the adjacent pairs — what the vendor is asked about, one question
// per pair.
func (p Path) Hops() [][2]string {
	if len(p) < 2 {
		return nil
	}
	out := make([][2]string, 0, len(p)-1)
	for i := 0; i+1 < len(p); i++ {
		out = append(out, [2]string{p[i], p[i+1]})
	}
	return out
}

// Rungs are what the plan CLIMBS: every element after the starting point.
func (p Path) Rungs() []string {
	if len(p) < 2 {
		return nil
	}
	return p[1:]
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/deps/migrate/ -run TestPath -v`
Expected: PASS (5 cases).

- [ ] **Step 5: Change the interface and every implementation, mechanically**

This step changes NO behaviour: every migrator takes `path` and uses `path.From()` / `path.To()` exactly where it used `from` / `to`. The ladder is consumed in Tasks 6 and 7.

In `registry.go`:

```go
	// Preflight measures and reports everything that would refuse or endanger
	// the migration, without touching the data. It is given the whole ROUTE:
	// a refusal on any rung refuses the migration, and measuring only the ends
	// would pass a ladder whose middle the vendor forbids.
	Preflight(ctx context.Context, env Env, path Path) PreflightResult
	// Plan runs the preflight again and, if it passes, builds the executable
	// plan plus the journal that will be written ahead of its first step.
	Plan(ctx context.Context, env Env, path Path, opts PlanOptions) (*Plan, deps.MigrationJournal, error)
```

In `CopyPreflight`, replace the `from, to string` parameters with `path Path`, and ask `supportsPair` about EVERY hop:

```go
func CopyPreflight(
	ctx context.Context, env Env, id deps.ID, path Path,
	supportsPair func(from, to deps.Version) (ok bool, problem msg.Message),
) (PreflightResult, CopyVolumes, bool) {
	res := NewPreflightResult(path.From(), path.To())
	res.WasRunning = env.IsRunning()
	// The ends decide whether a migration is needed at all (a downgrade, an
	// unparsable tag, a pair that needs no migration); the HOPS decide whether
	// the route can be walked. Both, in that order.
	if _, _, problems := versionProblems(id, path.From(), path.To()); len(problems) > 0 {
		res.Problems = append(res.Problems, problems...)
		return res, CopyVolumes{}, false
	}
	d, ok := deps.Lookup(id)
	if !ok { // unreachable: versionProblems just looked it up
		res.Problems = append(res.Problems, NotRegisteredProblem(id))
		return res, CopyVolumes{}, false
	}
	for _, hop := range path.Hops() {
		fromV, okFrom := d.ParseVersion(hop[0])
		toV, okTo := d.ParseVersion(hop[1])
		if !okFrom || !okTo {
			res.Problems = append(res.Problems, msg.New("deps.msg.version.unreadableTo", "image", hop[1]))
			return res, CopyVolumes{}, false
		}
		if ok, problem := supportsPair(fromV, toV); !ok {
			res.Problems = append(res.Problems, pairRefusal(problem, hop[0], hop[1]))
			return res, CopyVolumes{}, false
		}
	}
	// … the rest unchanged (source volume existence, copySpace, checkExistingTarget)
}
```

`BuildCopyUpgrade` takes `path Path` in place of `from, to string`; for now it keeps using `path.From()` and `path.To()` and builds the same 11 steps. `PostgresMigrator`, `RabbitMigrator`, `ZookeeperMigrator`, `QdrantMigrator` each change their two signatures and forward the path.

In `internal/daemon/routes_deps.go`:

- `resolveMigration` returns `(path migrate.Path, ok bool)` instead of `(from, to string, ok bool)`. Build it from the upgrade: `path := migrate.Path(upgrade.Path)`. When `upgrade.Path` is EMPTY, refuse with the existing `ErrCodeDependencyNotMigratable` path — there is no route, and the preflight's tag message is reached through the list route's `StatusDetail`.
- `pairProblem(id, from, to)` becomes `routeProblem(id deps.ID, path migrate.Path) msg.Message`, looping `path.Hops()` and returning the first non-empty refusal. Its two carve-outs are unchanged.
- `heldUpgradeStatus` calls `d.routeProblem(desc.ID(), migrate.Path(held.Path))`.
- The three `versionPairMessage(from, to)` calls become `versionPairMessage(path.From(), path.To())`.

- [ ] **Step 6: Build and run every affected package**

Run: `go build ./... && go test ./internal/deps/... ./internal/daemon/ 2>&1 | tail -20`
Expected: PASS. Existing tests construct two-element paths; a `migratetest` fake needs no change.

- [ ] **Step 7: Mutation check**

1. `CopyPreflight` asks `supportsPair` about `(From, To)` only → add a temporary three-rung case whose middle hop is refused; it must fail. Keep that case: name it `TestCopyPreflightRefusesAHopInTheMiddleOfTheRoute`.
2. `Path.To()` returns `p[1]` → `TestPathReadsItsEnds` fails.

- [ ] **Step 8: Commit**

```bash
git add internal/deps/migrate/ internal/daemon/
git commit -m "refactor(migrate): a migrator takes the whole route, not a version pair"
```

---

### Task 6: The copy upgrade raises ONE copy through every rung

**Files:**
- Modify: `internal/deps/migrate/copy_upgrade.go` (`copyRun`, `BuildCopyUpgrade`, `startTemp`, `postUpgrade`, `preUpgrade`)
- Modify: `internal/i18n/locales/{en,ru,zh,es,de,fr,pt,ja}.json` (one new key)
- Test: `internal/deps/migrate/copy_upgrade_ladder_test.go` (new)

**Interfaces:**
- Consumes: `migrate.Path` (Task 5).
- Produces: a plan whose step ids repeat — `pre-upgrade`, `start-new`, `post-upgrade` once per rung (never after the top one), plus `stop-old`/`stop-new`. `CopyStepIDs()` is unchanged: it is the id VOCABULARY, not the plan's length.

- [ ] **Step 1: Write the failing test**

Create `internal/deps/migrate/copy_upgrade_ladder_test.go`:

```go
package migrate

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/citeck/citeck-launcher/internal/deps/migrate/migratetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stepIDs(p *Plan) []string {
	out := make([]string, 0, len(p.Steps))
	for _, s := range p.Steps {
		out = append(out, s.ID)
	}
	return out
}

// A single-hop plan must be what it has always been, step for step: this is
// the release-safety property of the whole feature.
func TestASingleHopPlanIsUnchanged(t *testing.T) {
	env := migratetest.NewFakeEnv(deps.Qdrant, "qdrant/qdrant:v1.14.1", 1)
	plan, _, err := BuildCopyUpgrade(env, qdrantCopySpec(),
		Path{"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5"}, PlanOptions{},
		okPreflight("qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5"))
	require.NoError(t, err)
	assert.Equal(t, CopyStepIDs(), stepIDs(plan))
}

// Three rungs: one copy, one stop-namespace, one create-volume — and the
// start/post/stop trio once per rung.
func TestAThreeRungPlanClimbsOneCopy(t *testing.T) {
	env := migratetest.NewFakeEnv(deps.Qdrant, "qdrant/qdrant:v1.14.1", 1)
	path := Path{
		"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5",
		"qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1",
	}
	plan, j, err := BuildCopyUpgrade(env, qdrantCopySpec(), path, PlanOptions{},
		okPreflight(path.From(), path.To()))
	require.NoError(t, err)

	ids := stepIDs(plan)
	assert.Equal(t, 1, countID(ids, "copy-volume"), "one copy, whatever the ladder's length")
	assert.Equal(t, 1, countID(ids, "create-volume"), "one generation, whatever the ladder's length")
	assert.Equal(t, 1, countID(ids, "stop-namespace"))
	assert.Equal(t, 3, countID(ids, "start-new"), "one start per rung")
	assert.Equal(t, 3, countID(ids, "post-upgrade"))
	assert.Equal(t, 1, countID(ids, "verify"), "the inventory is compared once, at the top")
	assert.Equal(t, path.To(), j.To)
	assert.Equal(t, path.From(), j.From)
	assert.Equal(t, 2, j.ToVolumeGen, "the generation grows by exactly one")
}

// pre-upgrade prepares the NEXT node, so on a ladder it runs before every rung
// — on the old image first, then on each intermediate — and never after the
// last one, where there is no next node to prepare.
func TestPreUpgradeRunsBeforeEveryRungButNotAfterTheLast(t *testing.T) {
	env := migratetest.NewFakeEnv(deps.RabbitMQ, "rabbitmq:4.1.2-management", 1)
	path := Path{"rabbitmq:4.1.2-management", "rabbitmq:4.2.9-management", "rabbitmq:4.3.5-management"}
	plan, _, err := BuildCopyUpgrade(env, rabbitCopySpec(), path, PlanOptions{},
		okPreflight(path.From(), path.To()))
	require.NoError(t, err)

	ids := stepIDs(plan)
	assert.Equal(t, 2, countID(ids, "pre-upgrade"),
		"before 4.2 and before 4.3, never after the top rung")
	// And the order: the last three steps are the top rung's.
	assert.Equal(t, []string{"post-upgrade", "verify", "stop-new"}, ids[len(ids)-3:])
}

func countID(ids []string, want string) int {
	n := 0
	for _, id := range ids {
		if id == want {
			n++
		}
	}
	return n
}

// okPreflight is a preflight that passed, which is all BuildCopyUpgrade reads
// out of it besides the leftover-volume warning.
func okPreflight(from, to string) PreflightResult {
	res := NewPreflightResult(from, to)
	res.OK = true
	return res
}
```

Check `migratetest`'s constructor name before writing — if `NewFakeEnv` has a different signature, use the one the existing `copy_upgrade_test.go` uses and keep this file consistent with it.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/deps/migrate/ -run 'SingleHopPlan|ThreeRung|PreUpgradeRuns' -v`
Expected: FAIL — the plan has a fixed 11 steps.

- [ ] **Step 3: Generalize the plan**

In `copy_upgrade.go`, `copyRun` carries the path and the rung being climbed:

```go
type copyRun struct {
	env  Env
	spec CopySpec
	// path is the whole route. path.From() is the image the copy is first
	// started under; every rung after it is a version the copy is raised
	// through, in order.
	path      Path
	opts      PlanOptions
	toGen     int
	dstVolume string
	srcVolume string
	dataSize  int64

	before Inventory
}
```

`BuildCopyUpgrade` builds the step list:

```go
	steps := []Step{
		{ID: "stop-namespace", Run: r.stopNamespace},
		{ID: "pull-image", Run: r.pullImages},
		{ID: "create-volume", Run: r.createVolume},
		{ID: "copy-volume", Run: r.copyVolume},
		{ID: "start-old", Run: r.startOld},
	}
	rungs := path.Rungs()
	for i, rung := range rungs {
		last := i == len(rungs)-1
		// pre-upgrade prepares the node that is about to START, so it runs on
		// the node BELOW every rung — on the old image for the first, on each
		// intermediate for the rest. After the top rung there is no next node
		// to prepare, so it is not run there; for the first rung it also
		// captures the "before" inventory.
		steps = append(steps, Step{ID: "pre-upgrade", Run: r.preUpgradeAt(i)})
		steps = append(steps, Step{ID: i == 0 ? "stop-old" : "stop-new", Run: r.stopCurrent(i)})
		steps = append(steps, Step{ID: "start-new", Run: r.startRung(rung)})
		steps = append(steps, Step{ID: "post-upgrade", Run: r.postUpgrade})
		if last {
			steps = append(steps, Step{ID: "verify", Run: r.verify})
			steps = append(steps, Step{ID: "stop-new", Run: r.stopNew})
		}
	}
```

Go has no ternary; write the id as a small helper:

```go
// stopIDFor names the step that stops the node below rung i. The first one is
// the OLD image and gets the plan's existing "stop-old" key; every later one
// is an intermediate the plan itself started, which is "stop-new".
func stopIDFor(i int) string {
	if i == 0 {
		return "stop-old"
	}
	return "stop-new"
}
```

The run methods:

```go
// preUpgradeAt runs the dependency's pre-upgrade work on the node that is
// running now, and — on the FIRST rung only — captures the "before" inventory.
//
// The inventory is taken once, from the bottom of the ladder, because that is
// what the verify at the top compares against: an intermediate comparison
// would measure the work a rung legitimately did to the data.
func (r *copyRun) preUpgradeAt(i int) func(context.Context, *Journal, StepProgress) error {
	container := DstContainer
	if i == 0 {
		container = SrcContainer
	}
	return func(ctx context.Context, _ *Journal, p StepProgress) error {
		if r.spec.PreUpgrade != nil {
			if err := r.spec.PreUpgrade(ctx, r.env, container, p); err != nil {
				return fmt.Errorf("pre-upgrade: %w", err)
			}
		}
		if i != 0 {
			return nil
		}
		inv, err := r.spec.Inventory(ctx, r.env, container)
		if err != nil {
			return fmt.Errorf("read the inventory of %s: %w", r.path.From(), err)
		}
		r.before = inv
		return nil
	}
}

func (r *copyRun) stopCurrent(i int) func(context.Context, *Journal, StepProgress) error {
	container := DstContainer
	if i == 0 {
		container = SrcContainer
	}
	return func(ctx context.Context, _ *Journal, _ StepProgress) error {
		if err := r.env.StopRemove(ctx, container); err != nil {
			return fmt.Errorf("remove %s: %w", container, err)
		}
		return nil
	}
}

func (r *copyRun) startRung(image string) func(context.Context, *Journal, StepProgress) error {
	return func(ctx context.Context, _ *Journal, p StepProgress) error {
		return r.startTemp(ctx, image, DstContainer, p)
	}
}

// pullImages pulls EVERY rung before anything irreversible happens. An
// unreachable registry then costs a stopped namespace and nothing else — and
// on a ladder that has to hold for the rung four steps up, not just the first.
func (r *copyRun) pullImages(ctx context.Context, _ *Journal, p StepProgress) error {
	for _, image := range r.path.Rungs() {
		if err := pullImageStep(ctx, r.env, image, p); err != nil {
			return err
		}
	}
	return nil
}
```

`startTemp` opens its step by naming the image, so a repeated `start-new` says which rung it is:

```go
func (r *copyRun) startTemp(ctx context.Context, image, name string, p StepProgress) error {
	p(0, msg.New("deps.msg.progress.starting", "image", image))
	def, err := r.tempDef(image)
	// … unchanged
}
```

`r.from` / `r.to` are gone; every reader uses `r.path.From()` / `r.path.To()`.

- [ ] **Step 4: Add the locale key to all 8 files**

`internal/i18n/locales/en.json`, beside `deps.msg.progress.pulling`:

```json
  "deps.msg.progress.starting": "starting {image}",
```

Translations (same position in each file):
- ru: `"запуск {image}"`
- de: `"{image} wird gestartet"`
- es: `"iniciando {image}"`
- fr: `"démarrage de {image}"`
- pt: `"iniciando {image}"`
- zh: `"正在启动 {image}"`
- ja: `"{image} を起動中"`

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/deps/migrate/ ./internal/cli/ -run 'Ladder|Copy|Locale|Rung|SingleHop' -v 2>&1 | tail -20`
Expected: PASS, including `TestLocaleCompleteness`.

- [ ] **Step 6: Pin that a ladder costs the copy plan no extra space**

Add to `internal/deps/migrate/copy_upgrade_ladder_test.go`:

```go
// A ladder is walked on ONE copy, so the space a copy upgrade demands does not
// depend on how many rungs there are. This is the half of the disk-space rule
// that is a NON-change, and it is worth a test precisely because the other
// half (postgres, one extra dump) is a change: without this, "make the ladder
// ask for more room" would be applied to both plans.
func TestALadderDoesNotRaiseTheCopyPlansSpaceRequirement(t *testing.T) {
	env := migratetest.NewFakeEnv(deps.Qdrant, "qdrant/qdrant:v1.14.1", 1)
	one := QdrantMigrator{}.Preflight(t.Context(), env,
		Path{"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5"})
	many := QdrantMigrator{}.Preflight(t.Context(), env, Path{
		"qdrant/qdrant:v1.14.1", "qdrant/qdrant:v1.15.5",
		"qdrant/qdrant:v1.16.1", "qdrant/qdrant:v1.19.1",
	})
	assert.Equal(t, one.RequiredVolumeBytes, many.RequiredVolumeBytes)
	assert.Equal(t, one.RequiredHostBytes, many.RequiredHostBytes)
	assert.Equal(t, one.RequiredTotalBytes, many.RequiredTotalBytes)
}
```

Run: `go test ./internal/deps/migrate/ -run TestALadderDoesNotRaise -v`
Expected: PASS with no production change — the copy plan takes one copy either way.

- [ ] **Step 7: Run the whole migrate package**

Run: `go test ./internal/deps/... 2>&1 | tail -10`
Expected: PASS.

- [ ] **Step 8: Mutation check**

1. `pullImages` pulls only `r.path.To()` → add an assertion in `TestAThreeRungPlanClimbsOneCopy` that the fake env recorded a pull for every rung; it must fail.
2. The loop appends `create-volume` per rung → `TestAThreeRungPlanClimbsOneCopy` fails on the count.
3. `preUpgradeAt` captures the inventory on every rung → assert `r.before` is the bottom one; it must fail.
4. The `last` guard is dropped so `pre-upgrade` also runs after the top rung → `TestPreUpgradeRunsBeforeEveryRungButNotAfterTheLast` fails.
5. `stopIDFor` always answers `"stop-new"` → `TestASingleHopPlanIsUnchanged` fails.

- [ ] **Step 9: Commit**

```bash
git add internal/deps/migrate/ internal/i18n/locales/
git commit -m "feat(migrate): the copy upgrade raises one copy through every rung of the ladder"
```

---

### Task 7: PostgreSQL walks the ladder in one migration

**Files:**
- Modify: `internal/deps/journal.go` (`MigrationJournal` gains `ScratchVolume`)
- Modify: `internal/deps/deps.go` or `internal/deps/volume*.go` (add `ScratchVolumeName`)
- Modify: `internal/deps/migrate/postgres.go` (`pgRun`, `Plan`)
- Modify: `internal/deps/migrate/postgres_restore.go` / `rollback.go` (`RollbackPostgres` removes the scratch volume)
- Test: `internal/deps/migrate/postgres_ladder_test.go` (new)

**Interfaces:**
- Consumes: `migrate.Path` (Task 5).
- Produces:
  - `deps.MigrationJournal.ScratchVolume string` — the reused intermediate volume, `""` for a single-hop migration.
  - `func deps.ScratchVolumeName(d Descriptor, gen int) string` — `VolumeName(d, gen) + "-hop"`, `""` when the descriptor has no volume of its own.

- [ ] **Step 1: Write the failing test**

Create `internal/deps/migrate/postgres_ladder_test.go`:

```go
package migrate

import (
	"testing"

	"github.com/citeck/citeck-launcher/internal/deps"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The single-hop plan is the 10 steps it has always been.
func TestPostgresSingleHopPlanIsUnchanged(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	plan, j, err := PostgresMigrator{}.Plan(t.Context(), env,
		Path{"postgres:17.5", "postgres:18.6"}, PlanOptions{})
	require.NoError(t, err)
	assert.Equal(t, PostgresStepIDs(), stepIDs(plan))
	assert.Empty(t, j.ScratchVolume, "a single hop needs no intermediate cluster")
}

// Three rungs: one dump/restore cycle per rung, ONE scratch volume reused for
// every intermediate, and only the final cluster in the next generation.
func TestPostgresThreeRungPlanReusesOneScratchVolume(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	path := Path{"postgres:17.5", "postgres:18.6", "postgres:19.2", "postgres:20.1"}
	plan, j, err := PostgresMigrator{}.Plan(t.Context(), env, path, PlanOptions{})
	require.NoError(t, err)

	ids := stepIDs(plan)
	assert.Equal(t, 3, countID(ids, "restore"), "one restore per rung")
	assert.Equal(t, 3, countID(ids, "dump"), "the bottom dump plus one per intermediate")
	assert.Equal(t, 1, countID(ids, "verify"), "compared once, at the top")
	assert.Equal(t, 1, countID(ids, "stop-namespace"))

	assert.Equal(t, 2, j.ToVolumeGen, "the generation grows by exactly one")
	assert.Equal(t, "postgres3", j.CreatedVolume, "the FINAL cluster lands in generation 2")
	assert.Equal(t, "postgres3-hop", j.ScratchVolume,
		"one reused intermediate, journalled so the rollback removes it")
	assert.Equal(t, "postgres2", j.SourceVolume, "the source is never written to")
	assert.Equal(t, "postgres:17.5", j.From)
	assert.Equal(t, "postgres:20.1", j.To)
}

// The rollback must remove BOTH volumes the walk created. A journal written by
// an older launcher has no ScratchVolume and must still roll back.
func TestPostgresRollbackRemovesTheScratchVolumeToo(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	j := &deps.MigrationJournal{
		ID: deps.Postgres, From: "postgres:17.5", To: "postgres:20.1",
		CreatedVolume: "postgres3", ScratchVolume: "postgres3-hop",
		SourceVolume: "postgres2", Step: "restore",
	}
	require.NoError(t, RollbackPostgres(t.Context(), env, j))
	assert.ElementsMatch(t, []string{"postgres3", "postgres3-hop"}, env.RemovedVolumes())
}
```

`newPostgresFakeEnv` and `env.RemovedVolumes()` — reuse whatever `postgres_test.go` and `rollback_test.go` already build. If the fake has no `RemovedVolumes` accessor, add one to `migratetest.FakeEnv`; it is a test seam, not production behaviour.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/deps/migrate/ -run TestPostgres.*Rung -v`
Expected: FAIL — `j.ScratchVolume` undefined.

- [ ] **Step 3: Add the journal field and the scratch name**

In `internal/deps/journal.go`, after `SourceVolume`:

```go
	// ScratchVolume is the ONE intermediate cluster a multi-rung walk reuses.
	// "" for every single-hop migration, which is every migration a launcher
	// before this one could produce.
	//
	// It is a single name rather than a list because at most one intermediate
	// exists at a time: rung i is restored into it, dumped out of it, and the
	// volume is then deleted before rung i+1 recreates it. The peak on disk is
	// therefore source + one cluster + one dump, whatever the ladder's length.
	ScratchVolume string `json:"scratchVolume,omitempty"`
```

In `internal/deps` beside `VolumeName`:

```go
// ScratchVolumeName is the reusable intermediate volume of a multi-rung
// PostgreSQL walk: the next generation's name with a "-hop" suffix.
//
// The suffix is what keeps it OUT of the generation counter. ParseVolumeName
// round-trips through VolumeName, so "postgres3-hop" is not a generation and
// the descending existence walk that seeds a pin cannot mistake a leftover
// scratch volume for a real one.
func ScratchVolumeName(d Descriptor, gen int) string {
	name := VolumeName(d, gen)
	if name == "" {
		return ""
	}
	return name + "-hop"
}
```

- [ ] **Step 4: Generalize the postgres plan**

`pgRun` carries the path, the per-rung dump file and the two target volume names:

```go
type pgRun struct {
	env  Env
	path Path
	opts PlanOptions
	// fromGen is the generation the source cluster lives in; toGen the one the
	// FINAL cluster lands in. Intermediates live in scratchVolume and consume
	// no generation at all.
	fromGen, toGen           int
	srcVolume, dstVolume     string
	scratchVolume            string
	dumpDir                  string
	dataSize                 int64
	source                   Inventory
	// rung is the index of the rung being climbed, 0-based over path.Rungs().
	// The dump file and the target volume of a step are derived from it, so no
	// step has to know how many rungs there are.
	rung int
}

// dumpPathsFor answers the host path, the in-container path and the bind for
// the dump produced BELOW rung i. Per-rung files rather than one reused name:
// a walk that failed halfway leaves the operator a directory whose contents
// say which rung it died on.
func (r *pgRun) dumpPathsFor(i int) (hostPath, inContainer, bind string) {
	name := fmt.Sprintf("hop%d-%s", i, dumpFile)
	return filepath.Join(r.dumpDir, name), path.Join(dumpMount, name), r.dumpDir + ":" + dumpMount
}

// volumeForRung answers the volume rung i is restored into: the scratch volume
// for every intermediate, the next generation's volume for the top rung.
func (r *pgRun) volumeForRung(i int) (volume string, gen int) {
	if i == len(r.path.Rungs())-1 {
		return r.dstVolume, r.toGen
	}
	return r.scratchVolume, scratchGen
}
```

`scratchGen` needs a generation number for `GenerateDefFor`. The def's volume comes from `deps.VolumeName(d, gen)`, so a scratch volume cannot be expressed as a generation — extend the `Env` seam instead: `GenerateDefForVolume(id deps.ID, st deps.DependencyState, volume string)`. Add it to `Env`, implement it in the daemon's `depsEnv` by overriding the volume the generated def mounts, and in `migratetest.FakeEnv`. `GenerateDefFor` becomes a one-line wrapper that passes `deps.VolumeName(...)`, so there is still ONE place a def learns its volume.

The plan:

```go
	steps := []Step{
		{ID: "stop-namespace", Run: r.stopNamespace},
		{ID: "pull-image", Run: r.pullImages},
		{ID: "start-source", Run: r.startSource},
		{ID: "dump", Run: r.dumpAt(0)},
		{ID: "stop-source", Run: r.stopSource},
	}
	rungs := path.Rungs()
	for i := range rungs {
		last := i == len(rungs)-1
		steps = append(steps,
			Step{ID: "create-volume", Run: r.createVolumeFor(i)},
			Step{ID: "start-target", Run: r.startTargetAt(i)},
			Step{ID: "restore", Run: r.restoreAt(i)},
		)
		if last {
			steps = append(steps, Step{ID: "verify", Run: r.verify})
			steps = append(steps, Step{ID: "stop-target", Run: r.stopTarget})
			break
		}
		// The container running rung i is the source of rung i+1's dump: it is
		// already up and already holds the data. Dumping from it rather than
		// starting a second container is what keeps the peak at one cluster.
		steps = append(steps,
			Step{ID: "dump", Run: r.dumpAt(i + 1)},
			Step{ID: "stop-target", Run: r.stopTarget},
			Step{ID: "create-volume", Run: r.discardScratch(i)},
		)
	}
```

`discardScratch(i)` removes the dump below rung i and — for `i > 0` — the scratch volume, so the next `create-volume` can recreate it. It reuses the `create-volume` id because it is the step that makes the next volume available; a new id would be a new locale key for a step the operator does not distinguish.

`createVolumeFor(i)` journals the volume it is about to create **before** creating it, exactly as `createTargetVolume` does today, and writes `j.ScratchVolume` (not `j.CreatedVolume`) for an intermediate.

`dumpAt(i)` is today's `dump` with `r.dumpPathsFor(i)` and the container chosen by `i` (`SrcContainer` for 0, `DstContainer` after); it captures `r.source` only when `i == 0`.

- [ ] **Step 5: Pin the step order that keeps the peak where it is**

The disk-space property of this whole task is a consequence of WHEN things are deleted, so
that is what gets tested — not the numbers, which must not move at all.

Add to `internal/deps/migrate/postgres_ladder_test.go`:

```go
// The requirement must NOT grow with the ladder. It stays at
// `source + one dump + one cluster` — today's single-hop peak — because each
// dump is removed as soon as the restore that consumed it succeeded and each
// intermediate cluster as soon as the next dump has been taken from it.
// Raising the requirement instead would refuse a migration that fits.
func TestPostgresLadderAsksForNoMoreDiskThanOneHop(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	one := PostgresMigrator{}.Preflight(t.Context(), env, Path{"postgres:17.5", "postgres:18.6"})
	many := PostgresMigrator{}.Preflight(t.Context(), env,
		Path{"postgres:17.5", "postgres:18.6", "postgres:19.2", "postgres:20.1"})

	assert.Equal(t, one.RequiredHostBytes, many.RequiredHostBytes)
	assert.Equal(t, one.RequiredVolumeBytes, many.RequiredVolumeBytes)
	assert.Equal(t, one.RequiredTotalBytes, many.RequiredTotalBytes)
}

// …and the order that makes it true. A dump must be gone BEFORE the next one
// is taken, and an intermediate cluster BEFORE the next is created — put the
// deletions at the end of the migration instead and the peak becomes two dumps
// plus a cluster, i.e. the preflight under-requires and the walk dies of
// ENOSPC on an already-stopped namespace.
func TestPostgresLadderDeletesEachDumpBeforeTakingTheNext(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	path := Path{"postgres:17.5", "postgres:18.6", "postgres:19.2", "postgres:20.1"}
	plan, _, err := PostgresMigrator{}.Plan(t.Context(), env, path, PlanOptions{})
	require.NoError(t, err)

	require.NoError(t, runPlanAgainstFake(t, plan, env))

	// The fake records every dump written, every dump removed, every volume
	// created and every volume removed, in order, on one trace.
	assertNeverCoexist(t, env.Trace(), "dump", "dump")
	assertNeverCoexist(t, env.Trace(), "cluster", "cluster")
	// And nothing is left behind by the successful walk except the final one.
	assert.Equal(t, []string{"postgres3"}, env.LiveVolumes())
	assert.Empty(t, env.LiveDumps())
}
```

`assertNeverCoexist(t, trace, kindA, kindB)` walks the trace keeping a live count per kind
and fails the moment a count exceeds one, naming the step that did it. `runPlanAgainstFake`,
`Trace()`, `LiveVolumes()` and `LiveDumps()` are test seams on `migratetest.FakeEnv` — add
whatever of them does not exist yet. They are bookkeeping over the calls the fake already
receives, not new production behaviour.

- [ ] **Step 6: Order the steps so the test passes**

In the plan loop of Task 7 Step 4, the deletions move EARLIER:

```go
	for i := range rungs {
		last := i == len(rungs)-1
		steps = append(steps,
			Step{ID: "create-volume", Run: r.createVolumeFor(i)},
			Step{ID: "start-target", Run: r.startTargetAt(i)},
			Step{ID: "restore", Run: r.restoreAt(i)},
			// The dump this rung was restored from is dead the moment the
			// restore succeeded: a failure anywhere later rolls back to the
			// untouched source, so no dump is ever needed twice. Removing it
			// HERE rather than at the end of the migration is what keeps the
			// peak at one dump plus one cluster. See spec §7.2.
			Step{ID: "restore", Run: r.discardDumpBelow(i)},
		)
		if last {
			steps = append(steps,
				Step{ID: "verify", Run: r.verify},
				Step{ID: "stop-target", Run: r.stopTarget},
			)
			break
		}
		// The container running rung i is the source of the next dump: it is
		// already up and already holds the data. Dumping from it rather than
		// starting a second container is what keeps the peak at ONE cluster.
		steps = append(steps,
			Step{ID: "dump", Run: r.dumpAt(i + 1)},
			Step{ID: "stop-target", Run: r.stopTarget},
			// …and the cluster it came from goes as soon as it has been
			// dumped, before the next create-volume.
			Step{ID: "create-volume", Run: r.discardScratch(i)},
		)
	}
```

`discardDumpBelow(i)` removes the dump produced below rung i and nothing else; it reuses the
`restore` id because it is the tail of that step's work and the operator does not distinguish
them — a new id would be a new locale key for a step nobody reads separately.

`discardScratch(i)` now removes only the scratch VOLUME.

Run: `go test ./internal/deps/migrate/ -run TestPostgresLadder -v`
Expected: PASS.

- [ ] **Step 7: Make the rollback remove both volumes**

In `RollbackPostgres`, wherever it removes `j.CreatedVolume`, remove the union:

```go
// volumesToRemove is every volume this migration created: the target, plus the
// reusable intermediate a multi-rung walk left behind. A journal written by an
// older launcher has no ScratchVolume, so the union is also what keeps this
// readable for a journal this build did not write.
func volumesToRemove(j *deps.MigrationJournal) []string {
	out := make([]string, 0, 2)
	for _, v := range []string{j.CreatedVolume, j.ScratchVolume} {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
```

Keep the existing "report every failure rather than stopping at the first" behaviour.

- [ ] **Step 8: Run the tests**

Run: `go test ./internal/deps/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 9: Mutation check**

1. `volumeForRung` returns `r.dstVolume` for every rung → `TestPostgresThreeRungPlanReusesOneScratchVolume` fails on `ScratchVolume`.
2. `discardScratch` is dropped from the loop → assert the fake env recorded a scratch removal per intermediate; it must fail.
3. `volumesToRemove` returns only `j.CreatedVolume` → `TestPostgresRollbackRemovesTheScratchVolumeToo` fails.
4. `dumpAt` always uses `SrcContainer` → assert the second dump ran against `DstContainer`; it must fail.
5. `toGen` computed as `fromGen + len(rungs)` → `TestPostgresThreeRungPlanReusesOneScratchVolume` fails on `ToVolumeGen`.
6. `discardDumpBelow` is moved to the end of the plan → `TestPostgresLadderDeletesEachDumpBeforeTakingTheNext` fails on the dump count.
7. `discardScratch` is moved to the end of the plan → the same test fails on the cluster count.
8. `checkSpace` is given a per-rung multiplier → `TestPostgresLadderAsksForNoMoreDiskThanOneHop` fails.

- [ ] **Step 10: Commit**

```bash
git add internal/deps/ internal/daemon/
git commit -m "feat(migrate): postgres walks the whole ladder in one migration, reusing one scratch volume"
```

---

### Task 8: The dump is written and read compressed

**Files:**
- Modify: `internal/deps/migrate/postgres.go` (`dumpAt`, `restoreAt`, `dumpPathsFor`, `watchFileGrowth` call)
- Modify: `internal/deps/migrate/postgres_restore.go` (`RestoreCommandPrefix` keeps its job; add `RestoreScript`)
- Test: `internal/deps/migrate/postgres_test.go` (extend), `internal/deps/migrate/postgres_ladder_test.go` (extend)

**Interfaces:**
- Consumes: the multi-rung pg plan from Task 7 (`dumpAt(i)`, `restoreAt(i)`, `dumpPathsFor(i)`).
- Produces:
  - `func DumpScript(outPath string) []string` — the full `bash -c` command the dump step runs.
  - `func RestoreScript(dumpPath string) []string` — the full `bash -c` command the restore step runs.
  - `RestoreCommandPrefix() []string` — UNCHANGED signature and contents; it is now the psql part that appears INSIDE the restore script, and the integration test matches it as a substring.
- Dump files are named `…​.sql.gz`.

**Why this is its own task:** it is not part of the ladder. A single-hop migration gets it too, and keeping it separate means the ladder's tests and this one's cannot mask each other.

- [ ] **Step 1: Write the failing tests**

Append to `internal/deps/migrate/postgres_test.go`:

```go
// The dump is compressed on the way out and decompressed on the way in. A
// cluster's SQL text compresses several-fold, so this is both less disk and
// less I/O — and at gzip -1 the CPU cost is small enough that reading the
// smaller file back can pay for it.
func TestDumpAndRestoreAreCompressed(t *testing.T) {
	dump := DumpScript("/dump/hop0-dump.sql.gz")
	assert.Equal(t, "bash", dump[0])
	assert.Equal(t, "-c", dump[1])
	assert.Contains(t, dump[2], "pg_dumpall")
	assert.Contains(t, dump[2], "gzip -1")
	assert.Contains(t, dump[2], "/dump/hop0-dump.sql.gz")

	restore := RestoreScript("/dump/hop0-dump.sql.gz")
	assert.Equal(t, "bash", restore[0])
	assert.Equal(t, "-c", restore[1])
	assert.Contains(t, restore[2], "gunzip -c /dump/hop0-dump.sql.gz")
	// The psql invocation is the SAME one RestoreCommandPrefix names, so the
	// integration test's identification of the restore's stderr keeps working
	// and the flags have one source.
	assert.Contains(t, restore[2], strings.Join(RestoreCommandPrefix(), " "))
	assert.NotContains(t, restore[2], "-f ", "psql reads the pipe, not a file")
}

// A pipe hides the failure of everything but its last command. Without
// pipefail a pg_dumpall that died halfway is followed by a gzip that exits 0,
// and the migration proceeds to restore a truncated cluster and call it
// verified. Same on the way back: a corrupt archive makes gunzip fail while
// psql exits 0 on the empty input it got.
func TestBothScriptsFailOnAnyStageOfThePipe(t *testing.T) {
	for _, script := range [][]string{
		DumpScript("/dump/d.sql.gz"),
		RestoreScript("/dump/d.sql.gz"),
	} {
		assert.Contains(t, script[2], "set -o pipefail")
		assert.Equal(t, "bash", script[0],
			"dash only grew pipefail in 0.5.12; bash is in every postgres image and is 5.2 there")
	}
}

// The dump's progress is reported as an absolute size with NO percentage: the
// file is compressed, so its size against the cluster's size is not a
// fraction of anything, and a bar that creeps to 15% and then jumps to done
// reads as a stall. Both renderers already draw percent 0 as indeterminate —
// the same choice the copy step makes.
func TestCompressedDumpProgressIsIndeterminate(t *testing.T) {
	var seen []float64
	p := func(pct float64, _ msg.Message) { seen = append(seen, pct) }
	env := newFakeEnvWithFileSize(t, 4096)
	stop := watchFileGrowth(t.Context(), env, "/dump/d.sql.gz", 0, time.Millisecond, p)
	assert.Eventually(t, func() bool { return len(seen) > 0 }, time.Second, time.Millisecond)
	stop()
	for _, pct := range seen {
		assert.Zero(t, pct)
	}
}
```

Append to `internal/deps/migrate/postgres_ladder_test.go`:

```go
// Every rung's dump is compressed, not just the first.
func TestEveryRungsDumpIsCompressed(t *testing.T) {
	env := newPostgresFakeEnv(t, "postgres:17.5", 1)
	path := Path{"postgres:17.5", "postgres:18.6", "postgres:19.2"}
	plan, _, err := PostgresMigrator{}.Plan(t.Context(), env, path, PlanOptions{})
	require.NoError(t, err)
	require.NoError(t, runPlanAgainstFake(t, plan, env))

	for _, cmd := range env.ExecutedCommands() {
		if len(cmd) == 3 && strings.Contains(cmd[2], "pg_dumpall") {
			assert.Contains(t, cmd[2], "gzip -1")
		}
	}
	for _, name := range env.DumpsWritten() {
		assert.True(t, strings.HasSuffix(name, ".sql.gz"), "dump %q is not compressed", name)
	}
}
```

`newFakeEnvWithFileSize`, `ExecutedCommands()` and `DumpsWritten()` are test seams on
`migratetest.FakeEnv` — add whatever does not exist. They are bookkeeping over calls the
fake already receives.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/deps/migrate/ -run 'Compressed|PipeStage|Pipe' -v`
Expected: FAIL — `undefined: DumpScript`, `undefined: RestoreScript`.

- [ ] **Step 3: Write the two scripts**

In `internal/deps/migrate/postgres_restore.go`, beside `RestoreCommandPrefix`:

```go
// gzipLevel is the compression the dump is written at.
//
// 1, not the default 6: the dump is a transient file inside a migration that
// has already stopped the namespace, so the seconds matter and the last few
// percent of ratio do not. SQL text compresses several-fold even at level 1,
// which is the whole of the saving; the same reasoning already picks level 1
// for the JVM heap dumps this launcher configures.
const gzipLevel = "1"

// DumpScript is the command the dump step runs: pg_dumpall piped into gzip.
//
// It is `bash -c` and not `sh -c`, and it sets pipefail, and those two are one
// decision. A pipe reports only its LAST command's status, so without pipefail
// a pg_dumpall that died halfway is followed by a gzip that exits 0 — and the
// migration restores a truncated cluster and verifies it against an inventory
// read from the same half-dumped source. /bin/sh in the postgres image is dash,
// which only grew pipefail in 0.5.12; bash is present (5.2) and has had it
// since forever, so bash is the one that cannot be wrong on an older base.
func DumpScript(outPath string) []string {
	return []string{"bash", "-c", "set -o pipefail; " +
		"pg_dumpall -h 127.0.0.1 -U postgres | gzip -" + gzipLevel + " > " + shellQuote(outPath)}
}

// RestoreScript is the command the restore step runs: the archive decompressed
// into the SAME psql invocation RestoreCommandPrefix names — so the flags have
// one source and the integration test can still identify the restore's own
// stderr by that prefix, now as a substring of this script.
func RestoreScript(dumpPath string) []string {
	return []string{"bash", "-c", "set -o pipefail; " +
		"gunzip -c " + shellQuote(dumpPath) + " | " + strings.Join(RestoreCommandPrefix(), " ")}
}

// shellQuote wraps a path in single quotes for the two scripts above. The
// paths are built by the launcher (the dump directory plus a fixed file name),
// so this guards a path with a space in it rather than hostile input — but a
// dump directory under a user's home is exactly where a space appears.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'''`) + "'"
}
```

- [ ] **Step 4: Use them, and name the files `.sql.gz`**

In `postgres.go`:
- `dumpPathsFor(i)` builds `fmt.Sprintf("hop%d-%s", i, dumpFile)` where `dumpFile` gains the `.gz` suffix — change the constant, not the format string, so the single-hop plan's name follows too.
- `dumpAt(i)` runs `r.env.Exec(ctx, container, DumpScript(inContainer))`, and its error wrapping says `pg_dumpall` as before.
- `restoreAt(i)` runs `r.env.Exec(ctx, DstContainer, RestoreScript(inContainer))`; `restoreErrors(stderr)` is unchanged — psql still writes its ERROR lines to the script's stderr.
- The `watchFileGrowth` call passes **0** as `expected`:

```go
	// 0, not r.dataSize: the file is compressed, so its size is not a fraction
	// of the cluster's. A percentage computed from it creeps to ~15% and then
	// jumps to done, which reads as a stall; both renderers draw 0 as
	// indeterminate, which is the truth.
	stop := watchFileGrowth(ctx, r.env, hostPath, 0, dumpProgressPoll, p)
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/deps/... 2>&1 | tail -20`
Expected: PASS.

- [ ] **Step 6: Mutation check**

1. `DumpScript` drops `set -o pipefail` → `TestBothScriptsFailOnAnyStageOfThePipe` fails.
2. `DumpScript` uses `sh` → the same test fails on `bash`.
3. `RestoreScript` inlines its own psql flags instead of `RestoreCommandPrefix()` → change one flag in the prefix; `TestDumpAndRestoreAreCompressed` fails.
4. `watchFileGrowth` is given `r.dataSize` again → `TestCompressedDumpProgressIsIndeterminate` fails.
5. `dumpFile` loses its `.gz` → `TestEveryRungsDumpIsCompressed` fails.

- [ ] **Step 7: Prove it on real containers**

Run: `go test -tags integration ./internal/daemon/ -run TestIntegration_Postgres17To18 -v -timeout 30m`
Expected: PASS. Under rootless Docker prefix with `unshare --user --map-auto --map-root-user` (postgres runs as uid 999; see AGENTS.md). Record in the report the dump's compressed size against the cluster size — that ratio is the whole justification for this task and belongs in the commit message.

- [ ] **Step 8: Commit**

```bash
git add internal/deps/migrate/
git commit -m "feat(migrate): write and read the migration dump compressed"
```

---

### Task 9: A real-Docker integration test of a three-rung qdrant walk

**Files:**
- Modify: `internal/daemon/deps_copy_integration_test.go` (add `TestIntegration_QdrantLadder114To116`)

**Interfaces:**
- Consumes: everything above. No new production code.

- [ ] **Step 1: Write the test**

Model it on the existing `TestIntegration_Qdrant114To115` in the same file — reuse its env builder, its seeding of two collections / three points / one alias, and its bash transport helper `itQdrantBashGet`. The rungs are `qdrant/qdrant:v1.14.1` → `v1.15.5` → `v1.16.1`; all three tags exist on Docker Hub (verified 2026-09-15; `v1.17.4` does NOT, so do not reach for it).

Assert, after the migration:

```go
	// One copy, one generation, whatever the ladder's length.
	assert.Equal(t, 2, env.DependencyState(deps.Qdrant).Gen())
	assert.Equal(t, "qdrant/qdrant:v1.16.1", env.DependencyState(deps.Qdrant).Image)
	// The data survived every rung.
	assert.Equal(t, map[string]int{"docs": 2, "chunks": 1}, inventoryAfter.Collections)
	assert.Equal(t, map[string]string{"docs-alias": "docs"}, inventoryAfter.Aliases)
	// The source volume is byte-identical to what it was before the walk.
	assert.Equal(t, srcDigestBefore, itVolumeDigest(t, cli, srcVolume))
	// Exactly one volume was created.
	assert.Equal(t, []string{"qdrant2", "qdrant3"}, itLauncherVolumes(t, cli, nsID))
	// Both temp containers are gone and the journal is closed.
	assert.Empty(t, itTempContainers(t, cli))
	assert.Nil(t, rt.MigrationJournal())
```

- [ ] **Step 2: Run it**

Run: `go test -tags integration ./internal/daemon/ -run TestIntegration_QdrantLadder114To116 -v -timeout 20m`
Expected: PASS. Under rootless Docker no `unshare` is needed for qdrant (its container runs as root — verified 2026-09-15).

- [ ] **Step 3: Confirm the single-hop integration test still passes**

Run: `go test -tags integration ./internal/daemon/ -run 'TestIntegration_Qdrant' -v -timeout 30m`
Expected: both PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/deps_copy_integration_test.go
git commit -m "test(deps): a three-rung qdrant walk on real containers, on one copy"
```

---

### Task 10: The 1.x launcher does not break on a list

**Files:**
- Modify: `citeck-launcher-1x/src/main/kotlin/ru/citeck/launcher/core/bundle/BundleUtils.kt:103-127`
- Test: `citeck-launcher-1x/src/test/kotlin/ru/citeck/launcher/core/config/bundle/BundleDefTest.kt` (extend)

**Interfaces:**
- Consumes: nothing from the Go tasks — the two launchers share only the YAML.
- Produces: no new public API; `processApp` reads a list and takes its first element.

- [ ] **Step 1: Write the failing test**

Append to `BundleDefTest.kt`:

```kotlin
    // The 2.x launcher accepts a list of images and, outside the dependencies
    // section, takes the first element. This launcher reads the same files, so
    // a list must not leave it with an empty image — and the element it takes
    // has to be the same one, or the two launchers run different versions off
    // one volume.
    @Test
    fun `an image list takes its first element`() {
        val bundle = readBundle(
            """
            eapps:
              image:
                - harbor/ecos-eapps:1.0.0
                - harbor/ecos-eapps:2.0.0
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:1.0.0")
    }

    @Test
    fun `an image list of repository-tag maps takes its first element`() {
        val bundle = readBundle(
            """
            eapps:
              image:
                - {repository: harbor/ecos-eapps, tag: "1.0.0"}
                - {repository: harbor/ecos-eapps, tag: "2.0.0"}
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:1.0.0")
    }

    @Test
    fun `a plain string image is read`() {
        val bundle = readBundle(
            """
            eapps:
              image: harbor/ecos-eapps:1.0.0
            """.trimIndent()
        )
        assertThat(bundle.applications["eapps"]!!.image).isEqualTo("harbor/ecos-eapps:1.0.0")
    }

    @Test
    fun `an empty image list leaves the app out`() {
        val bundle = readBundle(
            """
            eapps:
              image: []
            """.trimIndent()
        )
        assertThat(bundle.applications).doesNotContainKey("eapps")
    }
```

`readBundle` — reuse whatever helper `BundleDefTest.kt` already uses to go from YAML text to a `BundleDef`; if there is none, add one that calls the same `BundleUtils` entry point the production path uses.

- [ ] **Step 2: Run to verify it fails**

Run: `JAVA_HOME=~/.jdks/temurin-21.0.10 ./gradlew test --tests '*BundleDefTest*'`
Expected: FAIL — the list leaves `image` blank and the app is dropped.

- [ ] **Step 3: Implement**

In `BundleUtils.kt`, add beside `getImageUrl`:

```kotlin
        // The 2.x launcher accepts an image written as a single value or as a
        // LIST. Outside its `dependencies:` section a list has no route to
        // walk — there is no pin, no hold and no migration out here, and there
        // is none of any of that in THIS launcher at all — so the first
        // element is taken: the most conservative rung, the one most likely to
        // match what is already in the volume.
        fun readImage(value: DataValue): String {
            val node = value["/image"]
            val first = if (node.isArray()) {
                if (node.size() == 0) return "" else node[0]
            } else {
                node
            }
            if (first.isTextual()) {
                // A plain "repo/name:tag" string carries its own registry
                // prefix rule, the same one getImageUrl applies to the map
                // form's repository.
                val text = first.asText()
                if (text.isBlank()) return ""
                val repository = text.substringBeforeLast(":", "")
                val tag = text.substringAfterLast(":", "")
                if (repository.isBlank() || tag.isBlank()) return ""
                return getImageUrl(repository, tag)
            }
            return getImageUrl(first["repository"].asText(), first["tag"].asText())
        }
```

and use it in `processApp`:

```kotlin
                val image = readImage(value)
```

Leave `ecosAppsImages` alone: it is already a list of maps, and a list of lists is not a shape anybody writes.

- [ ] **Step 4: Run the tests**

Run: `JAVA_HOME=~/.jdks/temurin-21.0.10 ./gradlew test ktlintCheck`
Expected: BUILD SUCCESSFUL.

- [ ] **Step 5: Mutation check**

1. `readImage` takes `node[node.size() - 1]` → `an image list takes its first element` fails.
2. The `isArray` branch is removed → the two list cases fail.
3. The textual branch is removed → `a plain string image is read` fails.

- [ ] **Step 6: Update the 1.x CHANGELOG and commit**

```bash
cd ../citeck-launcher-1x
git add src/ CHANGELOG.md
git commit -m "fix(bundle): read an image written as a list, taking its first element"
```

---

### Task 11: Documentation

**Files:**
- Modify: `AGENTS.md` (the `dependencies:` section bullet, and the copy-upgrade bullet)
- Modify: `changelog/2.12.3/{en,ru,zh,es,de,fr,pt,ja}.md`
- Modify: `docs/config-layers.md`

**Interfaces:** none.

- [ ] **Step 1: Extend the `dependencies:` bullet in AGENTS.md**

Add, after the existing rule (5) about the workspace section, a rule (6):

> (6) **An image value may be a LIST, and in the `dependencies:` section that list is a LADDER.** Its last rung is the target; the rungs between it and the pin are what make a hop the vendor forbids in one step reachable in several. Everywhere else — `applications:`, `ecosAppsImages`, the typed workspace blocks, `additionalApps`, a `namespace.yml` override — a list resolves to its **FIRST** element, because a reader outside the gate has no pin, no hold and no migration, and the only honest reading there is the most conservative rung (the same principle as `LegacyImage()`). **A rung is never skipped**: `UpgradeSupport` may REFUSE a hop the ladder does not name, but it may not authorize collapsing a ladder the author wrote — the table is this release's belief about a vendor and can go stale in the permissive direction, while the ladder is the author's statement about their own vendor and is older than any of our tables. A rung the version parser cannot read invalidates the WHOLE ladder (the dependency stays on its pin), because dropping it would produce exactly the jump the ladder exists to forbid. **A ladder is invisible to launchers 2.12.0–2.12.2** — they read `image:` as a scalar or a `{repository, tag}` map only, skip a sequence with a warning and fall back to their own default — which is safe for a dependency they never carried and is NOT safe for `rabbitmq`, where that default is `4.1.2-management`, i.e. a through-minor downgrade onto Khepri data.

- [ ] **Step 2: Extend the copy-upgrade bullet in AGENTS.md**

Add:

> **A ladder is walked on ONE copy.** The copy is taken once and raised through every rung: `pre-upgrade → stop-old|stop-new → start-new → post-upgrade` per rung, with `pre-upgrade` running before EVERY rung and never AFTER the top one — not for symmetry, but because RabbitMQ's `PreUpgrade` enables the feature flags the node about to start refuses to boot without, so it has to run on the node BELOW each rung: 4.1 → 4.2 → 4.3 runs it twice, once on 4.1 and once on 4.2, and never on 4.3 itself, since there is no rung above it left to prepare for. The generation grows by exactly **one** whatever the ladder's length, the source volume is still only ever read, and the rollback is still "delete the copy". The inventory is captured once at the bottom and compared once at the top; comparing at an intermediate rung would measure work a rung legitimately did. **PostgreSQL walks the ladder in one migration too**, but by its own means: it needs a LIVE cluster of each version, so it dumps and restores per rung and reuses ONE scratch volume (`<next generation>-hop`, journalled as `ScratchVolume` so the rollback removes it) for every intermediate — the peak on disk is source + one cluster + one dump, and only the final cluster lands in the next generation.

- [ ] **Step 3: Add the release note to all 8 changelog files**

`changelog/2.12.3/en.md`, in the existing features section:

```markdown
- An infrastructure image in a bundle's `dependencies:` section may now be written as a
  list of versions. The last one is the target and the ones before it are the steps the
  launcher takes to reach it, one migration at a time — so an upgrade the vendor does not
  support in a single step is now reachable without editing anything by hand. The data
  volume is copied once and raised through every step; the original is untouched and the
  rollback is unchanged.
```

`ru.md`:

```markdown
- Образ инфраструктуры в секции `dependencies:` бандла теперь можно записать списком версий.
  Последняя — цель, предыдущие — ступени, через которые лончер проходит, всё в одной
  миграции. Обновление, которое вендор не поддерживает одним шагом, стало достижимым без
  правки вручную. Том с данными копируется один раз и поднимается по каждой ступени;
  исходный том не изменяется, откат работает как прежде.
```

Translate for zh, es, de, fr, pt, ja — real translations, not the English text copied.

- [ ] **Step 4: Update `docs/config-layers.md`**

Add a short section under the image-precedence table stating the two consumption rules and pointing at the spec.

- [ ] **Step 5: Run the full gate**

Run: `make check`
Expected: 10/10 green.

- [ ] **Step 6: Commit**

```bash
git add AGENTS.md changelog/ docs/
git commit -m "docs: image ladders, and why a rung is never skipped"
```

---

## Self-review notes

**Spec coverage:** §3.1 → Task 1 Step 3. §3.2 → Tasks 1 and 2. §3.3 → Task 10 Step 1 (documented; no code — it is a property of the released parsers). §4 → Tasks 3 and 4. §5 → Task 5. §6 → Task 6. §7 → the Global Constraints plus Task 4's `routeVerdict` (no collapse exists anywhere in the plan). §7.1 → Task 7. §7.3 → Task 8. §7.2 → Task 7 Steps 5–6 (the requirement provably does not move, and the step order that makes that true is pinned) and Task 6 Step 6 (same, for the copy plan). §8 → Task 10. §9 → the test steps of every task plus Task 9. §10 → nothing to build; the open `deps.msg.pair.vendorPath` item stays open and is restated in the spec.

**Type consistency:** `decodeImageValues` (Task 1) is used by `ImageRef` (Task 2). `DependencyEntry.Images` / `AppDef.Images` (Task 1) are read by `resolveAppImageChain` (Task 4). `deps.UpgradeRoute` (Task 3) is called by `routeVerdict` (Task 4). `DependencyUpgrade.Path` (Task 4) becomes `migrate.Path` (Task 5) in `resolveMigration`. `Path.Rungs()` (Task 5) drives both plans (Tasks 6, 7). `deps.ScratchVolumeName` and `MigrationJournal.ScratchVolume` (Task 7) are read by `volumesToRemove` (Task 7).

**Known interface widening not yet justified by a test:** Task 7 adds `Env.GenerateDefForVolume`. If the implementation finds a way to express the scratch volume as something `GenerateDefFor` already accepts, prefer that and drop the new seam — a second way for a container to learn its volume is exactly what `tempDef`'s comment warns about.
