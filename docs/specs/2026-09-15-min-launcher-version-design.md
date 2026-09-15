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

> The owner's words: «если в бандле стоит минимальная версия лончера и текущий лончер старее
> указанной минимальной версии, то создать/отредактировать NS с выбором этого бандла нельзя.
> Показывается ошибка и просьба обновить лончер. Пока этим не будем пользоваться, но на будущее
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

## The open question: what `LATEST` should mean

`LATEST` is resolved to a concrete version at create time and stored concrete
(`findLatestBundle` picks the newest by filename; `ListBundleVersions` never opens the files).
When the newest bundle in a repo declares a `minLauncherVersion` this launcher does not meet,
there are two coherent answers:

- **(A) Refuse, like any other selection.** One rule, no surprise about which version you got.
  But it means an old launcher can create NO namespace at all from a repo that has moved on —
  including through Quick Start, which always uses `LATEST`. That is the bricking the design
  avoids at load time, reappearing at create time.
- **(B) `LATEST` means the newest bundle this launcher can run.** Walk the version list from the
  newest down, skip the ones that require more, take the first that fits; log a warning naming
  what was skipped. An explicitly PINNED version still refuses loudly. Costs a bundle parse per
  skipped version, only at create.

**Recommendation: (B).** `LATEST` is a request for "the current one", and the honest current one
for a launcher is the newest it can actually run. (A) turns a compatibility fence into a wall.
The counter-argument is real — the operator asked for LATEST and got 2026.2 without looking —
and it is answered by the warning plus the fact that the resolved version is what gets stored
and displayed. **This is the one decision I would like confirmed before the plan is written.**

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
  - Marking the offending version in the dropdown is **out of scope** and should stay out: it
    would require parsing every bundle file during `handleListBundles` (which today only lists
    filenames), plus a new field on `BundleInfoDto`, plus per-option disabling in `Select`
    (whose option type is `{label, value}`). The refusal on confirm is enough for a feature
    nobody uses yet.

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
- Mutation checks for each new guard, since a green test after a mutation means the mutation
  broke something else, not that the rule is uncovered.

## Docs to update when it lands

- `AGENTS.md`, a new rule in **Key Technical Decisions**, next to the `dependencies:` section
  rule — they are the two halves of the same forward-compatibility story.
- `changelog/<version>/*.md` in all 8 locales.
- `docs/config-layers.md` — one line, since the key is a property of the bundle the layer table
  describes.
