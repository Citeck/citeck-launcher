# Citeck Launcher website for 2.x — design

Date: 2026-09-25 · Branch: `pages` (GitHub Pages, `https://citeck.github.io/citeck-launcher/`)

## Goal

Replace the 1.x download page with a modern, "selling" landing page for Citeck Launcher 2.x.

- **Audience (user decision):** people evaluating the Citeck platform (the launcher is the fastest way to try it) AND
  technical users, split into two tracks — Desktop (developers/integrators, own machine) and Server (admins, Linux/VM).
- **Primary conversions:** download the desktop installer for the visitor's OS; copy the server `install.sh` command.
- **Success:** a visitor understands in one screen what Citeck + the launcher give them and reaches a working download
  or install command in one click; the page is fully readable without JavaScript and previews well in messengers.

The current site is a Vite/React SPA whose hero renders nothing until JS detects the OS, whose texts describe a
generic "application launcher", and whose download logic picks `v1.*` releases. All of that is replaced.

## Decisions (made with the user)

| Topic | Decision |
|---|---|
| Visual direction | **B — light product style**: white, soft colour blobs, Citeck blue `#4B75B7` |
| Dark theme | The DEFAULT for every visitor (the audience is technical; user decision 2026-09-28); the toggle next to the language switch switches to light and remembers it in `localStorage.theme` |
| Languages | RU + EN; `/` is Russian, `/en/` English; first visit follows the browser language, a stored choice wins; crawlers stay on RU |
| Stack | **Astro** (static generation) + Tailwind 4; interactivity is plain TypeScript enhancing static HTML. React islands were used first and dropped (2026-09-28, user): React was ~69 KB of the ~75 KB gzipped JS for four small widgets |
| Latest release | `GET /repos/Citeck/citeck-launcher/releases/latest` — 1.x releases are published without moving `latest` |
| Publishing | Push to `pages` deploys; push only after the user approves a local preview |

## Page structure (approved)

0. **Header** (sticky): Citeck logo · Features · Desktop · Server · Docs · GitHub · RU/EN · theme toggle · "Download".
1. **Hero**: "Вся платформа Citeck — одной кнопкой" / EN equivalent; sub-headline; primary button = installer for the
   detected OS ("Download for Linux Desktop"), secondary = "Install on a server →" (opens the server tab); a link "All systems and
   formats" to the downloads table (no inline list of every file, no bare command — user feedback); meta line
   `v<version> · Free · Open source (LGPL-3.0) · macOS · Windows · Linux`; dashboard screenshot in a tilted,
   floating window.
2. **Numbers**: ~28 MB single binary · 20+ services started for you · 3 OSes × amd64/arm64 · 8 UI languages.
3. **What you run — Citeck ECOS** (for evaluators): self-hosted low-code platform replacing proprietary ECM/BPM;
   scenario cards (contracts, purchasing, HR, archive & portal); BPMN designer, document types without code, roles
   and permissions out of the box; link to citeck.ru.
4. **Two tracks**: Desktop ("On your computer": Docker Desktop, or Docker Engine on Linux — only the tested engines are named;
   Rancher Desktop / Colima are found via the docker context but a full stand was never run on them → installer → Quick Start → open in browser) and
   Server ("On a server": Docker → one `curl | bash` → wizard asks domain and HTTPS mode, shows the admin password
   → open in browser). The two tracks are TABS (Desktop | Server), not side-by-side cards: "Install on a
   server" (hero) and the header's "Server" open only the server tab, "Desktop" only the desktop one — someone
   who chose the server should not read desktop steps (user feedback). Clicking a tab only switches it (the page
   never jumps); any other link to a tab, and a `#server` visit, puts the tab bar 24 px under the header. Without
   JavaScript both panels show one under the other. The server panel is wide enough for the captioned command
   on ONE line (the user wants it whole and unwrapped). Each panel ends with a neutral first-run note (several
   GB of images, usually 10–15 minutes — stated as a fact, not a warning).
5. **Features** (bento grid): self-healing; upgrades with rollback (PostgreSQL, RabbitMQ, …); backup & restore;
   HTTPS out of the box (Let's Encrypt, incl. IP addresses); live status, resources and logs.
6. **Gallery**: fresh 2.x screenshots (see Assets).
7. **Editions**: Community (open source, free, platform core) · Enterprise (support + extra modules, licence key →
   contact Citeck).
8. **Requirements**: Docker · 16 GB RAM Community / 24–32 GB Enterprise · 50+ GB disk.
9. **All downloads**: first the smart button for the visitor's OS with the file it downloads, then the table of
   every installer per OS/arch with its `.sha256`; the visitor's OS comes first and the picked installer is
   highlighted "For your system"; link to all releases.
10. **FAQ**: macOS "developer cannot be verified"; do I need Kubernetes (no, Docker only); do I need docker compose
    (no — the launcher drives the Docker API itself); upgrading from 1.x
    (data is migrated automatically); where my data lives; Community vs Enterprise.
11. **Final CTA + footer**: "Run Citeck today" + the download button, then "Or on a server, with one command:" with
    the one-line command and the server guide link (no button that jumps back up the page) · Telegram community · contacts · docs · LGPL-3.0 · © Citeck.

## Content rules

- Tone: confident and concrete; every promise is something the launcher demonstrably does. Short sentences, "вы".
- Facts come from the launcher's `README.md` / `AGENTS.md` on `master` (requirements, commands, features,
  editions). No unverifiable numbers (no "installs in 5 minutes"); first run is described honestly (image
  download, then the stand comes up by itself).
- EN is a meaning-for-meaning translation, not word-for-word.
- Server command (static, no API needed):
  `curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash`.

## Architecture

```
astro.config.mjs        site https://citeck.github.io, base /citeck-launcher
src/
  i18n/ru.ts, en.ts     all site texts; same keys in both
  i18n/index.ts         t(lang, key); build fails when the dictionaries' key sets differ
  layouts/Base.astro    <head>: title, description, hreflang (ru, en, x-default), canonical, OG/Twitter,
                        favicon, fonts, inline no-flash theme script; on the RU page an inline
                        language redirect (lib/lang.ts enRedirectTarget) runs first, before paint
  pages/index.astro     RU (/)
  pages/en/index.astro  EN (/en/)
  components/           one .astro component per section above; each takes `lang`
                        DownloadButton, DownloadTable, CopyCommand, ThemeToggle render the no-JS HTML
  scripts/              plain TS that enhances it:
                        release.ts      latest 2.x release, fetched once, cached for the session
                        download.ts     OS/arch-aware button, the table with the visitor's installer marked
                        copy.ts         copy-to-clipboard; "✓ Copied" only on success, else "Press Ctrl+C"
                        theme.ts        light/dark, persisted
  lib/releases.ts       pure: GitHub release JSON → typed asset map (unit-tested)
  lib/detectOS.ts       ported from the current site, extended with UA-CH architecture
public/                 screenshots/, og-ru.png, og-en.png, favicon
```

- Both pages render the same components with a `lang` prop; no duplicated markup.
- Every internal URL goes through `import.meta.env.BASE_URL` (the current hero hard-codes `/citeck-launcher/`).
- `.github/workflows/static.yml` keeps its shape; only the build command changes. Build output stays `dist/`.

## Downloads and release data

- `lib/releases.ts` reads `/releases/latest`. If the tag is not `v2.*` (or higher major) the result is treated as
  unavailable. Assets are matched by name `citeck-desktop_<ver>_<os>_<arch>.<ext>`:
  Windows `.msi` (amd64 primary, arm64), macOS `.dmg` (arm64 primary, amd64/Intel), Linux `.deb` (amd64 primary),
  `.rpm`, arm64; each with its `.sha256` sidecar.
- OS/arch: User-Agent, plus `navigator.userAgentData.getHighEntropyValues(['architecture'])` where available.
  Safari on Apple Silicon reports Intel, so macOS defaults to arm64 with an Intel link beneath (the only
  alternate shown inline, and only when the browser does not reveal the architecture). Mobile shows
  "open this page on a computer" plus the server command instead of an installer button.
- Fallbacks: no JS / API error / rate limit (60 req/h/IP) → buttons link to
  `https://github.com/Citeck/citeck-launcher/releases/latest`, no version shown. The response is cached in
  `sessionStorage` for the session.

## Visual system

- Light: background `#FFFFFF`; text `#0D1B3E`, secondary `#4A5877`; primary `#4B75B7` (+ darker hover); hero and
  final CTA carry soft radial blobs (blue, pink, mint).
- Dark: background `#0B1020`, cards `#121A2E`, text `#E6ECF8`, accent `#7FA6E6`; blobs at 20–30% intensity;
  screenshots get a light window outline so the dark product UI does not dissolve into the page.
- Theme: default = dark regardless of `prefers-color-scheme`; light only when stored by the toggle; an inline `<head>` script applies the class before
  first paint (no flash).
- Type: Inter (text) and JetBrains Mono (commands), self-hosted via `@fontsource` with Cyrillic subsets,
  `font-display: swap`; headings 48–64 px desktop, weight 800, negative tracking.
- Components: primary button with coloured shadow, outline secondary; cards radius 16–20 px, thin border, hover
  lift; dark terminal pill with copy button.
- Hover never moves an element (no translate): a card that lifts away from the cursor at its bottom edge loses
  hover, drops back and jitters. Hover changes shadow, border and colour only; `yarn verify` checks it.
- Copy: plain sentences; the long dash only where Russian grammar needs it (user feedback).
- Motion: sections reveal on scroll (IntersectionObserver + CSS), hero entrance on load; all disabled under
  `prefers-reduced-motion`. No animation libraries.
- Responsive, mobile-first; checked at 375 / 768 / 1280 / 1536 px; header collapses to a menu on mobile, hero
  becomes one column with the screenshot below the text.
- Accessibility: WCAG AA contrast in both themes, visible focus, ARIA on interactive elements, semantic sections.

## Assets

- Fresh 2.x screenshots, taken on an **isolated** test stand under the solution's `.agents/tmp` (never the user's
  working stand): dashboard with all services running; a service's log viewer; the dependencies dialog with an
  upgrade offered; the server setup wizard in a terminal (tmux → `aha` → Playwright). If an isolated stand cannot be
  brought up, fall back to `readme/screenshots/running.png` from `master` and report what is missing.
- OG images `og-ru.png`, `og-en.png` (1200×630) rendered from an HTML template with Playwright and committed.

## Verification (before asking for approval)

- `astro check` and `astro build` clean; dictionary key parity enforced by the build.
- Vitest for `lib/releases.ts` (real API payload fixtures: 2.x latest, non-2.x tag, missing assets) and `detectOS`,
  and for the interactive components: the real `.astro` rendered through the Container API, their scripts run in jsdom.
- Playwright screenshots of `/` and `/en/`, light and dark, at 375 / 768 / 1280 — reviewed by the agent.
- Lighthouse: Performance, SEO, Accessibility ≥ 95 on both pages.
- The page with JavaScript disabled: all content visible, download links go to `releases/latest`.
- API failure simulated: fallback links, no version text, no console errors.

## Delivery

Commits on `pages`. Pushing deploys to GitHub Pages (public), so the push happens only after the user approves a
local preview. README.md and DEPLOYMENT.md of the `pages` branch are updated to the new stack.

## Out of scope

More than two languages; a blog/changelog page; analytics; a custom domain.
