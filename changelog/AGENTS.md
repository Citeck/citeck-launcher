# Changelog authoring (Spec 2b)

The desktop launcher fetches these files at runtime to show users what changes
when they update. Keep them short and factual.

## Structure
- One folder per release: `changelog/<version>/` (version is bare semver, e.g. `2.5.0` — no `v`).
- One Markdown file per locale inside it: `en.md ru.md zh.md es.md de.md fr.md pt.md ja.md`.
- **All 8 locales are REQUIRED** for every release (CI test `internal/update/changelog_repo_test.go` fails otherwise). `en` is the runtime fallback if a locale is somehow missing — but ship all 8.
- Register the release in `changelog/index.json`: append `{ "version": "<version>", "date": "<YYYY-MM-DD>" }`.

## Style
- Small bullets, one change each. Get to the point — no rambling, no paragraphs.
- Lead with user-visible impact, not implementation detail.
- A short `#` heading with the version is fine; then `-` bullets.
- Group with `##` only if a release is large (e.g. `## Fixes`, `## New`).

## Example (`2.5.0/en.md`)

```md
# 2.5.0
- Desktop auto-update: install the latest release from the app, with changelog.
- Faster namespace reload on large bundles.
- Fixed: tray "Open" sometimes left the window behind others on Linux.
```
