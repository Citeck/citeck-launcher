# Changelog authoring

The desktop launcher fetches these files at runtime to show users what changes
when they update. Keep them short and factual.

## Structure
- One folder per release: `changelog/<version>/` (version is bare semver, e.g. `2.4.0` — no `v`).
- One Markdown file per locale inside it: `en.md ru.md zh.md es.md de.md fr.md pt.md ja.md`.
- **New releases must ship all 8 locales** (CI test `internal/update/changelog_repo_test.go` fails otherwise). `en` is the runtime fallback. 2.4.0 is the first full-locale release; historical releases ≤ 2.3.2, migrated from the old `CHANGELOG.md`, are en-only — the test only requires `en` for those.
- Register the release in `changelog/index.json`: append `{ "version": "<version>", "date": "<YYYY-MM-DD>" }`.
- **Legacy pre-2.0 (`1.*`) releases are archived** under `changelog/archive/<version>/` (en-only, with their own `changelog/archive/index.json`). They are not in the main `index.json`, not validated by the CI test, and never fetched by the updater. Don't add new releases there.

## What to include (scope)
Write the changelog from the **net diff against the previous released tag**
(`git diff <prev-tag>..HEAD`), not from the commit list. Describe the end state a
user upgrading *from the last release* will actually see.
- **Net effect only.** If something was introduced AND later removed or fully
  reworked within this same range, it never shipped to users — leave it out. Do
  not list a "fix" for a bug in code that was itself first added in this same
  range (the feature simply works; describe the feature, not its in-development
  fixes). Check the boundary: `git show <prev-tag>:<path>` shows what users had
  before, so you can tell a genuine change from intra-range churn.
- **User-meaningful only.** Skip anything a user can't perceive in this release:
  internal refactors, CI/build/test changes, dependency bumps, and capabilities
  that are **off by default or not reachable** in the shipped configuration
  (e.g. don't announce a web-UI feature while the server web UI is disabled by
  default). If a line wouldn't change what a user does or sees, drop it.
- When in doubt about whether a change is net-new vs. a refinement of something
  already released, diff against the previous tag rather than guessing.

## Style
- **Do NOT add a version heading.** The folder name *is* the version; the launcher UI and the GitHub release already show it above the notes, so a `# <version>` line just duplicates it.
- Start with `##` section groups — e.g. `## New features`, `## Fixes`, `## Updates`. Localize the section headers too (e.g. ru `## Исправления`).
- Small bullets, one change each. Get to the point — no rambling, no paragraphs.
- Lead with user-visible impact, not implementation detail.

## Example (`en.md`)

```md
## New features
- Desktop auto-update: install the latest release from the app, with a changelog.
- Faster namespace reload on large bundles.

## Fixes
- Tray "Open" sometimes left the window behind others on Linux.
```
