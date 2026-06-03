# Changelog authoring (Spec 2b)

The desktop launcher fetches these files at runtime to show users what changes
when they update. Keep them short and factual.

## Structure
- One folder per release: `changelog/<version>/` (version is bare semver, e.g. `2.5.0` — no `v`).
- One Markdown file per locale inside it: `en.md ru.md zh.md es.md de.md fr.md pt.md ja.md`.
- **New releases must ship all 8 locales** (CI test `internal/update/changelog_repo_test.go` fails otherwise). `en` is the runtime fallback. (Historical releases ≤ 2.4.0, migrated from the old `CHANGELOG.md`, are en-only — the test only requires `en` for those.)
- Register the release in `changelog/index.json`: append `{ "version": "<version>", "date": "<YYYY-MM-DD>" }`.

## Style
- **Do NOT add a version heading.** The folder name *is* the version; the launcher UI and the GitHub release already show it above the notes, so a `# <version>` line just duplicates it.
- Start with `##` section groups — e.g. `## New features`, `## Fixes`, `## Updates`. Localize the section headers too (e.g. ru `## Исправления`).
- Small bullets, one change each. Get to the point — no rambling, no paragraphs.
- Lead with user-visible impact, not implementation detail.

## Example (`2.5.0/en.md`)

```md
## New features
- Desktop auto-update: install the latest release from the app, with a changelog.
- Faster namespace reload on large bundles.

## Fixes
- Tray "Open" sometimes left the window behind others on Linux.
```
