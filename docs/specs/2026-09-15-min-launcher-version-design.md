# `minLauncherVersion` — a bundle declares the launcher it needs

Status: DRAFT, awaiting the owner's review. Nothing implemented.

Branch: `feat/min-launcher-version`, cut from `fix/bundle-numeric-tag` (the tip of the
ladder stack) rather than from `master`. Not to mix the work — the ladder is untouched here —
but because the key has to be read from the YAML **node tree**, and the node-tree reading of
a bundle exists only from that commit on. Writing this against `master`'s generic-map parser
would reintroduce the defect that commit removed (an unquoted `2.13` arrives as `float64`).

## What it is for

A bundle can start requiring launcher behaviour that older launchers do not have. Today the
only tool for that is silence: both parsers ignore keys they do not know, so a bundle moves an
image into `dependencies:` and older launchers simply never see it (`AGENTS.md`, rule about the
`dependencies:` section). That works when the new thing can be HIDDEN from an old launcher. It
does not work when the bundle needs something an old launcher must actively do — an image
ladder it has to walk, a migration it has to run, a field it has to honour. An old launcher
handed such a bundle does not fail; it does something ELSE, quietly.

`minLauncherVersion` is the bundle author's way of saying "below this version, do not try".

> The owner's words: «если в бандле стоит минимальная версия лаунчера и текущий лаунчер старее
> указанной минимальной версии, то создать/отредактировать NS с выбором этого бандла нельзя.
> Показывается ошибка и просьба обновить лаунчер. Пока этим не будем пользоваться, но на будущее
> пригодится.» The key name was proposed and agreed.

## Non-goals

- **Not a load-time gate.** A namespace that already runs on such a bundle keeps loading,
  starting, reconciling and showing its apps. See "The way back" below — this is the property
  the whole design is arranged around.
- **Not a 1.x feature.** Kotlin 1.x ignores the key by construction (below), and it is not
  taught to read it. The owner asked for it in 2.x.
- **Not a bundle-format version.** It says which LAUNCHER is needed, not which schema the file
  is written in. There is no `apiVersion` for bundles today and this does not introduce one.

## The key

Top level of the bundle file, a plain string:

```yaml
minLauncherVersion: "2.13.0"
```

- **Read from the node tree**, through the same second parse `parseBundleDependencies` uses
  (`internal/bundle/resolver.go`). In the generic `map[string]any` an unquoted `2.13` is already
  `float64(2.13)`, and `2.10` would come back as `2.1` — a requirement nobody wrote. The quotes
  in the example above are the recommended spelling; the node read is what makes the unquoted
  one safe anyway.
- **Stored on `bundle.Def`** as `MinLauncherVersion string` with `omitempty`. `Def` is
  serialised into the namespace's `CachedBundle` and read by `internal/h2migrate`; `omitempty`
  keeps both compatible in the old→new direction, and an old `Def` simply decodes the field as
  empty, which means "no requirement".
- **Named in the app walk's skip**, next to `bundleDependenciesKey`. Strictly it is not needed:
  the walk hands each top-level entry to `extractBundleImage`, which answers "" for a scalar, so
  a scalar key is already ignored. (An earlier note of mine claimed the skip was mandatory —
  that was true of the pre-node-walk parser and is no longer.) It is still worth adding, in one
  named place, so that the list of top-level keys that are NOT applications is readable in one
  spot rather than inferred from a type switch.
- **Absent or blank means no requirement.** This has to be explicit, because the comparator
  treats an unparseable version as newer than everything (below), and a blank
  `minLauncherVersion:` would otherwise refuse every bundle it appears in.

Surveyed 429 real bundle files in `launcher-public-workspace` and `docker-compose-kit`: the key
does not occur anywhere, and neither does `launcherVersion`, `minVersion`, `schemaVersion` or
`apiVersion`. The only top-level non-application keys in the field are `dependencies` (2 files),
`ecos` (18, a nesting wrapper the parser recurses into) and a legacy `applications` (1 file).

## The rule

**A namespace config being WRITTEN may not name a bundle whose `minLauncherVersion` is newer
than this launcher.** Refused with an error naming both versions and asking the operator to
update.

Three consequences worth stating, because each is a thing a later reader might "fix":

1. **The gate is on the RESULT, not on the change.** It asks what the config being written
   names, not whether the bundle ref changed. So an edit that KEEPS a too-new bundle is refused,
   and an edit that MOVES OFF it is allowed. That is the way back, and it falls out of the rule
   rather than needing a special case.
2. **Loading is never gated.** `handleGetNamespaceEdit` — the call that OPENS the edit dialog —
   reads the stored bundle ref and must keep working, or the operator cannot reach the control
   that changes it. Same for `loadNamespace`, `doReload`, `handleActivateNamespace`,
   `handleListNamespaces`, the reload planner and `citeck setup`. The refusal happens on the
   write, after the operator has already been able to look.
3. **A namespace that exists keeps running.** Nothing in the start path, the reconciler or the
   generator consults the key.

## Comparing versions

Reuse `update.Greater(min, current)` — refuse when it answers true. Do not write a second
comparator. Measured, not reasoned (probe run against the real function):

| `minLauncherVersion` | launcher | `Greater(min, cur)` | outcome |
|---|---|---|---|
| `2.13.0` | `2.12.2` | true | refused |
| `2.12.2` | `2.12.2` | false | allowed — equal is not greater |
| `2.12.2` | `2.13.0` | false | allowed |
| `2.13` | `2.12.2` | true | refused — a two-part minimum means `.0` |
| `2.13` | `2.13.5` | false | allowed — `2.13` is `2.13.0`, and `2.13.5` clears it |
| `v2.13.0` | `2.12.2` | true | refused — the `v` prefix is normalised on both sides |
| `2.13.0` | `dev-20260915-124919` | **false** | allowed — a dev build is never blocked |
| `nonsense` | `2.12.2` | **true** | refused |
| `2.13.0` | `2.13.0-rc1` | true | refused — a release candidate is not the release |

Two of these rows are load-bearing and non-obvious, and both come from the SAME clause in
`Greater` ("an invalid version sorts highest"):

- **A dev build is never refused.** `make` stamps `dev-<date>` by default, which is not semver,
  so as the `b` argument it makes every requirement fail to be greater. This is the property
  that keeps a developer's own build usable against any bundle, and it is why the comparator is
  reused instead of replaced.
- **An unreadable requirement refuses a released launcher.** `nonsense` as the `a` argument is
  "newer than any real release". So a typo in `minLauncherVersion` fails CLOSED. That is the
  right direction — the key exists precisely to stop a launcher that does not understand
  something, and silently ignoring a requirement we cannot read reverses it — but it is a
  side effect of the dev-build clause rather than an explicit branch, so it must be tested by
  name or a later cleanup will remove it.

## Where the gate lives

Two enforcement points, because there are two writers:

1. **The daemon's single write path**, `persistNamespaceConfig` (`internal/daemon/ns_config_store.go`).
   It already exists as the one place every config write goes through, documented as such, and
   its four callers are exactly the four ways a bundle ref reaches disk: create
   (`routes_ns.go`), edit (`routes_ns.go`), `POST .../upgrade` (`routes_config.go`) and the raw
   `PUT /api/v1/config` (`routes_config.go`, reachable by HTTP only — no CLI or web caller).
   Loading uses `loadNamespaceConfigFromStore`, a different function, so the "never on load"
   property is structural here rather than a matter of remembering.
2. **`citeck install`** (`internal/cli/install.go`), which does NOT go through the daemon — it
   marshals the namespace config and writes it with `fsutil.AtomicWriteFile`. A daemon-side gate
   alone would leave the CLI able to create exactly the namespace the gate exists to prevent.

The gate needs the bundle and the launcher version:

- **The bundle** must be read WITHOUT a git sync. `Resolver.Resolve` calls `syncBundleRepo`,
  which can hit the network (it honours a pull period, so not on every call — but "can" is
  enough: renaming a namespace must never wait on git). The resolver already has an `offline`
  flag; the plan should expose a no-sync read rather than invent a second resolution path. A
  bundle file that is not on disk yields no requirement and no refusal — there is nothing to
  read, and the create path already refuses an unsynced `LATEST` with `BUNDLE_NOT_SYNCED`.
- **The launcher version** has no global. It lives as `Daemon.version` (injected through
  `cli.BuildInfo.Version` → `start.go` → `daemon.Options.Version`) and as `BuildInfo.Version` in
  the CLI. Both gates have it in hand; no `internal/version` package needs inventing, and adding
  one is a bigger change than this feature justifies.

## `LATEST` means the newest bundle this launcher can run

Decided by the owner («давай второе»). `LATEST` is resolved to a concrete version at create time
and stored concrete (`findLatestBundle` picks the newest by filename; `ListBundleVersions` never
opens the files). When the newest bundle declares a `minLauncherVersion` this launcher does not
meet, `LATEST` walks the version list from the newest DOWN and takes the first one that fits.
An explicitly PINNED version still refuses loudly — asking for a specific bundle and getting a
different one silently would be a different bug.

The alternative — refusing `LATEST` like any other selection — was rejected because it leaves an
old launcher unable to create ANY namespace from a repo that has moved on, Quick Start included
(it always uses `LATEST`). That is the bricking this design avoids at load time, transplanted to
create time.

Two obligations follow from the choice, and both are part of this feature rather than nice-to-haves:

1. **Say what was skipped.** Resolving `LATEST` past a rung logs a warning naming the versions
   passed over and the launcher version each one wanted. Silence here is what makes the operator
   think the repo has not moved.
2. **Show that a newer bundle exists.** Otherwise the operator asked for LATEST, got 2026.2, and
   nothing on the screen ever says 2026.3 is out. This is the indicator described below — the
   owner asked for it in the same breath as the decision, and it is the honest half of (B).

Cost: one bundle parse per skipped version, only at create. The skipped set is normally empty and
is bounded by however many versions the repo has published above this launcher's floor.

## Error surface

- New code `LAUNCHER_TOO_OLD` in the `internal/api` error-code block (the block has an AST gate
  test requiring the `ErrCode` prefix and a doc comment).
- HTTP status `409 Conflict`, matching `BUNDLE_NOT_SYNCED` and `NO_BUNDLE_CONFIGURED`: the
  request is well-formed, the state refuses it.
- **Localised.** Build the sentence as data with `internal/msg` and render it at the edge with
  `translatorFor(r)`, the way `DEPENDENCY_VERSION_LOCKED` does — it is the only localised
  refusal in the codebase and it is the model to copy. Keys in
  `internal/i18n/locales/{en,ru,zh,es,de,fr,pt,ja}.json`.
- What the operator actually sees, as the code stands today:
  - **Web** — the message in the existing inline banner of `NamespaceEditDialog`. The dialog
    keeps `(err as Error).message` and DROPS the code; there is no "code → text" catalogue on
    the front end for any error. So the server sentence has to be self-sufficient. Good enough,
    and it needs no front-end work.
  - **CLI** — the message as a line in the terminal. `client.APIError` has no `Code` field at
    all, so the CLI cannot branch on the code even if it wanted to.
  - Marking every offending version **inside the dropdown** stays out of scope: it would mean
    parsing every bundle file during `handleListBundles` (which today only lists filenames),
    a new field on `BundleInfoDto`, and per-option disabling in `Select` (whose option type is
    `{label, value}`). The refusal on confirm is enough there. Note this is NOT the same as the
    indicator below, which parses at most a handful of files — only the versions above the one
    the namespace runs, and only until one of them fits.

## The indicator: "there is a newer bundle"

Asked for by the owner in the same breath as the `LATEST` decision («какой-нибудь индикатор мб на
шестерёнке настройки NS или рядом, который бы говорил "есть бандл свежее"»), and it is the honest
half of that decision: `LATEST` may now quietly hand back an older bundle, so something on the
screen has to say the repo has moved on.

### What it says

Three states, on the per-namespace settings gear in the top bar (`web/src/components/TabBar.tsx`,
the `Settings` button that opens `NamespaceEditDialog` — the control the operator would use to act
on it):

| state | dot | hover text |
|---|---|---|
| the namespace runs the newest version its repo has | none | unchanged — the gear keeps `dashboard.nsConfig` |
| a newer version exists and this launcher can run it | emerald | "A newer bundle is available: `<version>`. Open the namespace settings to switch to it." |
| a newer version exists, but every newer one wants a newer launcher | amber | "A newer bundle is available (`<version>`), but it needs launcher `<min>` or newer. Update the launcher." |

The dot is the SAME 6×6 corner dot `UpdateNotification` already uses for the launcher's own
auto-update (`absolute right-1 top-1 h-1.5 w-1.5 rounded-full`). That component already carries two
colours for two meanings (emerald available, red rolled back), so a third case in the same visual
language costs nothing to learn. The wording of the third state mirrors the dependency feature,
which already draws exactly this (a)/(b) distinction — `deps.status.upgradeAvailable` vs
`deps.status.requiresLauncherUpdate`, `deps.banner.needLauncher`, and the `LauncherUpdateHint`
component. Reuse that vocabulary rather than inventing a second one for bundles.

**The hover text, and the collision it has to resolve.** The owner asked for a tooltip that says
what the dot means. The app's tooltip mechanism is the native `title` attribute — there is no
Tooltip component anywhere in `web/src`, and the i18n convention for it already exists
(`table.cog.tooltip`, `table.initStep.tooltip`, `imageDetails.tooltip`). Use it; adding a styled
tooltip primitive for one dot is scope nobody asked for.

But the gear button **already has a `title`** (`dashboard.nsConfig`, "Namespace config"), and an
element has only one. So: while the dot is showing, the gear's `title` IS the indicator sentence —
including the clause about what to do — and it returns to `dashboard.nsConfig` when the dot is
gone. One hover target, one message, no two titles fighting over the same pixel. The alternative,
a separate hoverable element beside the gear with its own title, was rejected: a 6px dot is a poor
hover target on its own, and the operator hovering the gear is the one who needs the message.

Because the sentence is now the only place the meaning lives, it has to carry the action, not just
the fact — "a newer bundle exists" leaves the operator with nowhere to go, while "…open the
namespace settings to switch" and "…update the launcher" each name the next move.

### What "newer" means

- **Same repo only.** `community` and `community-rc` are separate `bundleRepos` entries, so a
  namespace on `community` is never told about a release candidate. Nothing extra to do — the
  comparison simply never leaves the namespace's own repo.
- **Same scope only.** `ListBundleVersions` also returns nested keys (`archive/2025.5`), and
  `compareBundleVersions` ranks EVERY unscoped version above EVERY scoped one, so a namespace
  pinned inside `archive/` would otherwise be told that all of mainline is "newer". Compare only
  within the scope the current version is in.
- **Against the RESOLVED version, not the config string.** A config may still say `LATEST`
  (legacy, YAML-edited, or a workspace template); the version the namespace actually runs is
  `bundleDef.Key.Version`. With no resolved bundle yet there is nothing to compare and no
  indicator. `ResolveDisplayBundleRef` already encodes this rule for display; the indicator obeys
  the same one.
- Ordering is `compareBundleVersions`, not string order. (`internal/cli/upgrade.go` sorts its
  version lists lexicographically today, which puts `2026.10` below `2026.9`; if the indicator
  ever reuses those lists, that has to be fixed there, not worked around here.)

### What it costs, and when it is computed

Finding out whether a newer version EXISTS is free of file I/O: `ListBundleVersions` is a
directory walk that never opens a bundle (the key comes from the filename), and the answer is the
first entry that compares above the current one.

Telling state 2 from state 3 does need the file — but only for versions ABOVE the current one, and
only until one of them fits. That is the same downward walk `LATEST` resolution performs, so it is
ONE function used twice, not two rules that can drift: *the newest version in this repo, at or
above X, that this launcher can run*. In the field the set above a namespace's pin is normally
empty or one or two files.

**Computed where the bundle is already resolved** — namespace load and reload
(`loadNamespace`, `doReloadEx`) and after an explicit bundle-repo pull — and cached on
`activeNamespace` next to `bundleError`, which has the same lifetime. NOT in `handleGetNamespace`:
that runs on every SSE-triggered refetch, and a directory walk per refetch buys nothing, because
the answer can only change when the repo is synced. A consequence worth stating plainly: **the
indicator reflects what has been synced, not what exists upstream.** It appears after the next
sync (bundle repos honour a one-hour pull period) or immediately after the ↻ button. It must never
trigger a sync of its own — a namespace list that waits on git is a worse bug than a late dot.

### Carried to the front end

- A field on `NamespaceDto` (`internal/api/dto.go`), e.g.
  `newerBundle?: { version: string; ref: string; requiresLauncher?: string }` — absent when there
  is nothing to say. `requiresLauncher` present is exactly state 3.
- Filled in `handleGetNamespace` from the cached value, alongside `DependencyUpgrades` and
  `BundleError`, which it resembles in every way.
- No new SSE event type. The existing debounced refetch already runs on reload and on
  `namespace_updating`, which is when the value can change.
- New i18n keys in all 8 locale files (`web/src/locales/*.ts` — `locales.test.ts` fails on a key
  present in one locale and missing in another, and also flags untranslated English). Prefix: the
  existing `namespace.*` group, since `bundle.*` does not exist yet and this is a property of a
  namespace as the operator sees it. Two keys, both with `{version}` (and `{min}` on the second),
  named `.tooltip` after the existing convention:
  `namespace.newerBundle.tooltip`, `namespace.newerBundle.needsLauncher.tooltip`.

### Deliberately not in this feature

- The CLI. `citeck upgrade` already lists versions with `(latest)` and `(current)` markers; adding
  "you are N behind" there is a separate, CLI-shaped question. The gate still refuses a pinned
  too-new bundle from the CLI — that is a refusal, not an indicator.
- Per-version annotation in the bundle dropdown (see the note under Error surface).
- Any notion of "dismiss" or "remind me later". A dot with no state is a dot that cannot go stale.

## What older launchers do

Nothing, and that is the point — the key protects against launchers that will never know about
it, so its protection begins with the release that implements it.

- **Go 2.12.x and earlier** hand every top-level key to the app walk, which ignores a scalar
  entry (no `image:`). The bundle parses as before, and the namespace is created as before.
- **Kotlin 1.x** does the same: `BundleUtils.readBundleFile`'s `processApp` calls `readImage` on
  the entry, which is blank for a scalar, so no application is added and nothing else looks at
  the key. Verified by reading the code, not assumed.

So a bundle carrying `minLauncherVersion` is safe to publish today; it simply does not protect
anyone until launchers that enforce it are in the field. That is the same shape as the
`dependencies:` section, and the same limitation.

## Testing

- Comparator behaviour, as a table test naming the two non-obvious rows (dev build never
  refused, unreadable requirement refused). The dev-build row is the one that breaks silently if
  someone swaps the argument order.
- HTTP-level refusal on create, in the shape of `TestCreateNamespace_LatestUnsyncedRepoRefused`:
  assert the status, assert the code appears in the body, **and assert nothing was persisted**.
- Edit KEEPING the too-new bundle → refused. Edit MOVING OFF it → accepted. This pair is the
  way back and must be a test, not a comment.
- `handleGetNamespaceEdit` on a namespace bound to a too-new bundle → still 200. Loading and
  activating such a namespace → still works.
- `citeck install` refuses, and writes no config file.
- Parsing: the key read from the node tree keeps `2.10` as `2.10`; a blank value means no
  requirement; a bundle without the key is unchanged.
- The gear's `title` is the indicator sentence while the dot shows and `dashboard.nsConfig` when
  it does not — the one assertion that catches the two-titles collision.
- Indicator: newest version equals the current one → no indicator; a newer runnable version →
  state 2; a newer version that wants more launcher, with nothing runnable above the current one →
  state 3; a newer version that wants more launcher WITH a runnable one between → state 2 naming
  the runnable one, not the blocked one. Scoped (`archive/…`) versions never make an unscoped
  namespace look behind, and a config still saying `LATEST` compares against the resolved version.
- Mutation checks for each new guard, since a green test after a mutation means the mutation
  broke something else, not that the rule is uncovered.

## Docs to update when it lands

- `AGENTS.md`, a new rule in **Key Technical Decisions**, next to the `dependencies:` section
  rule — they are the two halves of the same forward-compatibility story.
- `changelog/<version>/*.md` in all 8 locales.
- `docs/config-layers.md` — one line, since the key is a property of the bundle the layer table
  describes.
