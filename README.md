# Citeck Launcher website

The landing page for Citeck Launcher 2.x, published on GitHub Pages at
<https://citeck.github.io/citeck-launcher/> (Russian) and `/en/` (English).

Built with [Astro 7](https://astro.build) (static generation) and Tailwind CSS 4, no UI framework: the few interactive
parts are plain TypeScript scripts that enhance static HTML (about 3 KB of JavaScript, gzipped).
The design and its decisions are in `docs/superpowers/specs/2026-09-25-launcher-site-v2-design.md`.

## Commands

Node ≥ 22.22.2 (jsdom 30 needs it for the tests) and Yarn classic.

```bash
yarn install
yarn dev        # http://localhost:4321/citeck-launcher/
yarn build      # astro check + static build into dist/
yarn test       # Vitest: release parsing, OS detection, language rule, dictionaries, and the interactive
                #   components rendered for real (Astro Container API) with their scripts run in jsdom
yarn verify     # after build: Playwright checks of both pages (themes, widths, axe WCAG AA, no-JS, API down, language redirect)
yarn og         # re-render public/og-ru.png and public/og-en.png
```

`yarn verify` writes full-page screenshots into `verify-out/` (git-ignored) — look at them after layout changes.

## Where things are

| What | Where |
|---|---|
| All texts, both languages | `src/i18n/ru.ts` (source of truth), `src/i18n/en.ts` — same keys; a test fails if the shapes differ or Russian text is left in EN |
| Page sections | `src/components/*.astro`, assembled by `src/components/Page.astro` |
| Pages | `src/pages/index.astro` (RU), `src/pages/en/index.astro` (EN) |
| Interactive parts | `src/components/{DownloadButton,DownloadTable,CopyCommand,ThemeToggle}.astro` render the no-JS HTML; `src/scripts/` enhances it (release loading, OS-aware button and table, copying, theme) |
| Component tests | `src/components/*.test.ts`; `test/jsdom-ssr-env.ts` is jsdom with SSR transforms so `.astro` files render in Vitest, `test/mount.ts` renders one into the page |
| Release parsing, OS detection, language rule | `src/lib/` (pure, unit-tested) |
| Language redirect | `enRedirectTarget` in `src/lib/lang.ts`, inlined by `src/layouts/Base.astro` as the first script in the Russian page's `<head>` (runs before paint; keeps query and hash; skips crawlers and automated browsers) — it must stay self-contained |
| Screenshots | `src/assets/screenshots/*.png` — optimised to AVIF/WebP at build time; a gallery item whose file is missing is simply not shown |
| Theme tokens | `src/styles/global.css` |

## How the download buttons work

On load the page asks `api.github.com/repos/Citeck/citeck-launcher/releases/latest` for the current 2.x release
(cached in `sessionStorage`) and offers the installer for the visitor's OS and architecture. Without JavaScript,
or when the API is unreachable or rate-limited, every button links to the latest release page on GitHub.
The server command is static: `curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash`.

## Screenshots

`dashboard.png` comes from the launcher repository (`readme/screenshots/running.png` on `master`).
`server-wizard.png` is a render of the real `citeck install` wizard (tmux capture → HTML → Playwright).
To add a screenshot, drop a PNG into `src/assets/screenshots/` and reference its name (without `.png`)
in `gallery.items` of both dictionaries.
