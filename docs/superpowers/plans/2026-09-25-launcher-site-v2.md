# Citeck Launcher 2.x Website Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 1.x Vite/React download page on branch `pages` with a statically generated, bilingual (RU/EN), light/dark "selling" landing page for Citeck Launcher 2.x.

**Architecture:** Astro 7 generates `/` (RU) and `/en/` (EN) from one set of `.astro` section components fed by two typed dictionaries. React islands handle only interactive pieces (OS-aware download button, copy button, theme toggle, first-visit language redirect). Pure logic (`lib/releases.ts`, `lib/detectOS.ts`, `lib/lang.ts`) is unit-tested with Vitest.

**Tech Stack:** Astro 7.3, @astrojs/react 7, React 19, Tailwind CSS 4 (`@tailwindcss/vite`), TypeScript, Vitest 5 + jsdom, @fontsource-variable/inter, @fontsource/jetbrains-mono, Playwright (verification only), Yarn classic (lockfile v1), Node ≥ 22.12.

**Spec:** `docs/superpowers/specs/2026-09-25-launcher-site-v2-design.md`

## Global Constraints

- Site URL `https://citeck.github.io`, base `/citeck-launcher`; every internal URL is built from `import.meta.env.BASE_URL`.
- Routes: `/` = Russian, `/en/` = English; both built from the same components with a `lang: 'ru' | 'en'` prop.
- Latest release comes from `https://api.github.com/repos/Citeck/citeck-launcher/releases/latest`; a tag whose major is < 2 is treated as unavailable.
- Fallback download URL: `https://github.com/Citeck/citeck-launcher/releases/latest`.
- Server command, verbatim: `curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash`.
- Installer asset names: `citeck-desktop_<ver>_<os>_<arch>.<ext>`, os ∈ {windows, darwin, linux}, arch ∈ {amd64, arm64}, ext ∈ {msi, dmg, deb, rpm}; each has a `.sha256` sidecar.
- Light palette: bg `#FFFFFF`, text `#0D1B3E`, muted `#4A5877`, primary `#4B75B7`. Dark: bg `#0B1020`, card `#121A2E`, text `#E6ECF8`, accent `#7FA6E6`.
- Theme default = `prefers-color-scheme`; choice persisted in `localStorage['theme']`; applied by an inline `<head>` script before first paint.
- Language choice persisted in `localStorage['lang']`; only `/` may redirect (to `/en/`), only on a first visit from a non-Russian browser.
- No animation libraries; all motion off under `prefers-reduced-motion`; content must be visible with JavaScript disabled.
- Requirements text: Docker · 16 GB RAM Community / 24–32 GB Enterprise · 50+ GB disk.
- Facts only from the launcher's `README.md`/`AGENTS.md` on `master`; no "installs in N minutes" claims.
- CI Node version 22 (Astro 7 requires ≥ 22.12). Build output directory stays `dist/`.
- Never push `pages` before the final verification task passes (push = public deploy).

## Review Focus

1. **iPad Safari reports itself as "Macintosh"** — must be treated as mobile (no `.dmg` offered): `detectOS` checks `navigator.maxTouchPoints > 1` with a Mac UA. Test in Task 2.
2. **A release missing one arch** (e.g. no `windows_arm64.msi`) — the map keeps the other entries; a detected arch with no asset falls back to the other arch of that OS, and to the releases page if the OS has none. Test in Task 2.
3. **Visitor with a Russian browser opening a shared `/en/` link** — must stay on `/en/`; only `/` ever redirects, and never once a choice is stored. Test in Task 3.
4. **JavaScript disabled** — scroll-reveal must not hide sections (hidden state only under `html.js`), download buttons must be real links to the releases page. Checked in Task 8 (Playwright, `javaScriptEnabled: false`).
5. **GitHub API rate limit (403) or network error** — no version text, fallback links, no uncaught promise rejection in the console. Test in Task 4; checked in Task 8 with the API route aborted.

---

## File map

```
package.json, yarn.lock           deps + scripts (replaced)
astro.config.mjs                  site/base, React integration, Tailwind vite plugin
tsconfig.json                     extends astro/tsconfigs/strict, jsx react-jsx
vitest.config.ts                  jsdom, include src/**/*.test.ts(x)
.github/workflows/static.yml      node 22, `yarn install --frozen-lockfile && yarn build`
src/styles/global.css             Tailwind 4 import, theme tokens, dark variant, reveal/blob utilities
src/layouts/Base.astro            <head> (SEO, hreflang, OG, fonts, theme script), <body> slot, reveal script
src/pages/index.astro             RU page
src/pages/en/index.astro          EN page
src/components/Page.astro         assembles all sections for a lang
src/components/*.astro            Header, Hero, Numbers, WhatIsCiteck, Tracks, Features, Gallery,
                                  Editions, Requirements, Downloads, Faq, FinalCta, Footer, Icon
src/islands/DownloadButton.tsx    OS-aware primary download + alternates
src/islands/DownloadTable.tsx     all installers table (versioned when API answers)
src/islands/CopyCommand.tsx       terminal pill with copy
src/islands/ThemeToggle.tsx       light/dark switch
src/islands/LangRedirect.tsx      first-visit redirect on `/`
src/islands/useRelease.ts         fetch + sessionStorage cache of the latest release
src/lib/releases.ts               GitHub JSON → ReleaseInfo (pure)
src/lib/detectOS.ts               UA/UA-CH → Client (pure core + browser wrapper)
src/lib/lang.ts                   shouldRedirectToEn (pure), paths per lang
src/i18n/ru.ts, en.ts, index.ts   dictionaries + t()
src/**/*.test.ts(x)               Vitest
public/screenshots/*.png          2.x screenshots
public/og-ru.png, og-en.png       1200×630 previews
public/favicon.ico                kept
scripts/verify.mjs                Playwright screenshots / no-JS / API-failure checks
scripts/og.mjs                    renders OG images from an HTML template
README.md, DEPLOYMENT.md          rewritten for the new stack
```

Removed: `index.html`, `vite.config.ts`, `postcss.config.js`, `tailwind.config.js`, `src/App.tsx`, `src/main.tsx`, `src/types.ts`, `src/vite-env.d.ts`, `src/components/*.tsx`, `src/styles/index.css`, `src/utils/detectOS.ts`, `public/screenshots/screenshot1.png`, `screenshot2.png`, `screenshot_main.png` (1.x UI), `.yarnrc.yml` (Berry config unused by Yarn classic).

---

### Task 1: Astro scaffold, theme tokens, layout, CI

**Files:**
- Create: `astro.config.mjs`, `tsconfig.json`, `vitest.config.ts`, `src/styles/global.css`, `src/layouts/Base.astro`, `src/pages/index.astro`, `src/pages/en/index.astro`
- Modify: `package.json`, `.github/workflows/static.yml`, `.gitignore`
- Delete: files listed under "Removed"

**Interfaces:**
- Produces: `Base.astro` props `{ lang: 'ru' | 'en'; title: string; description: string; ogImage: string }`; CSS classes `reveal` (scroll reveal), `blob-*` backgrounds; `dark` class on `<html>`.

- [ ] **Step 1: Replace package.json**

```json
{
  "name": "citeck-launcher-website",
  "private": true,
  "version": "2.0.0",
  "type": "module",
  "engines": { "node": ">=22.12.0" },
  "scripts": {
    "dev": "astro dev",
    "build": "astro check && astro build",
    "preview": "astro preview",
    "test": "vitest run",
    "verify": "node scripts/verify.mjs",
    "og": "node scripts/og.mjs"
  },
  "dependencies": {
    "@astrojs/react": "^7.0.0",
    "@fontsource-variable/inter": "^5.3.0",
    "@fontsource/jetbrains-mono": "^5.3.0",
    "astro": "^7.3.5",
    "react": "^19.1.0",
    "react-dom": "^19.1.0"
  },
  "devDependencies": {
    "@astrojs/check": "^0.9.10",
    "@tailwindcss/vite": "^4.3.3",
    "@testing-library/react": "^16.3.0",
    "@types/react": "^19.1.0",
    "@types/react-dom": "^19.1.0",
    "jsdom": "^26.1.0",
    "playwright": "^1.63.0",
    "tailwindcss": "^4.3.3",
    "typescript": "^5.9.0",
    "vitest": "^5.0.2"
  }
}
```

- [ ] **Step 2: Delete the old SPA files and install**

```bash
git rm -q index.html vite.config.ts postcss.config.js tailwind.config.js src/App.tsx src/main.tsx src/types.ts \
  src/vite-env.d.ts src/components/*.tsx src/styles/index.css src/utils/detectOS.ts .yarnrc.yml \
  public/screenshots/screenshot1.png public/screenshots/screenshot2.png public/screenshots/screenshot_main.png
rm -f yarn.lock && yarn install
```
Expected: install completes, new `yarn.lock` (lockfile v1).

- [ ] **Step 3: Config files**

`astro.config.mjs`:
```js
import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  site: 'https://citeck.github.io',
  base: '/citeck-launcher',
  trailingSlash: 'always',
  integrations: [react()],
  vite: { plugins: [tailwindcss()] },
});
```

`tsconfig.json`:
```json
{
  "extends": "astro/tsconfigs/strict",
  "include": [".astro/types.d.ts", "src/**/*", "scripts/**/*"],
  "exclude": ["dist"],
  "compilerOptions": { "jsx": "react-jsx", "jsxImportSource": "react" }
}
```

`vitest.config.ts`:
```ts
import { defineConfig } from 'vitest/config';
export default defineConfig({ test: { environment: 'jsdom', include: ['src/**/*.test.{ts,tsx}'] } });
```

- [ ] **Step 4: `src/styles/global.css`**

```css
@import 'tailwindcss';
@import '@fontsource-variable/inter';
@import '@fontsource/jetbrains-mono/400.css';
@import '@fontsource/jetbrains-mono/600.css';

@custom-variant dark (&:where(.dark, .dark *));

@theme {
  --font-sans: 'Inter Variable', -apple-system, 'Segoe UI', sans-serif;
  --font-mono: 'JetBrains Mono', ui-monospace, monospace;
  --color-brand: #4b75b7;
  --color-brand-dark: #3e5b8f;
  --color-brand-light: #7fa6e6;
  --color-ink: #0d1b3e;
  --color-muted: #4a5877;
  --color-night: #0b1020;
  --color-night-card: #121a2e;
  --color-night-ink: #e6ecf8;
  --color-night-muted: #9aa8c4;
}

html { scroll-behavior: smooth; }
body { @apply bg-white text-ink antialiased dark:bg-night dark:text-night-ink; font-family: var(--font-sans); }

.blob-hero {
  background:
    radial-gradient(700px 360px at 88% -5%, rgb(255 217 240 / .9), transparent 60%),
    radial-gradient(760px 420px at 55% 25%, rgb(215 230 255 / .9), transparent 60%),
    radial-gradient(600px 320px at 0% 100%, rgb(217 255 242 / .9), transparent 60%);
}
.dark .blob-hero {
  background:
    radial-gradient(700px 360px at 88% -5%, rgb(190 80 150 / .22), transparent 60%),
    radial-gradient(760px 420px at 55% 25%, rgb(75 117 183 / .28), transparent 60%),
    radial-gradient(600px 320px at 0% 100%, rgb(40 160 130 / .18), transparent 60%);
}

/* Hidden only when JS runs (html.js set by the head script) — no-JS visitors see everything. */
html.js .reveal { opacity: 0; transform: translateY(24px); transition: opacity .7s ease, transform .7s ease; }
html.js .reveal.is-visible { opacity: 1; transform: none; }
@media (prefers-reduced-motion: reduce) {
  html { scroll-behavior: auto; }
  html.js .reveal { opacity: 1; transform: none; transition: none; }
  .float { animation: none !important; }
}
@keyframes float { 0%, 100% { transform: perspective(1400px) rotateX(6deg) translateY(0); } 50% { transform: perspective(1400px) rotateX(6deg) translateY(-8px); } }
.float { animation: float 7s ease-in-out infinite; }
```

- [ ] **Step 5: `src/layouts/Base.astro`**

```astro
---
import '../styles/global.css';
interface Props { lang: 'ru' | 'en'; title: string; description: string; ogImage: string }
const { lang, title, description, ogImage } = Astro.props;
const base = import.meta.env.BASE_URL;
const site = Astro.site!.toString().replace(/\/$/, '');
const ruUrl = `${site}${base}`;
const enUrl = `${site}${base}en/`;
const canonical = lang === 'ru' ? ruUrl : enUrl;
const ogUrl = `${site}${base}${ogImage}`;
---
<!doctype html>
<html lang={lang}>
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>{title}</title>
    <meta name="description" content={description} />
    <link rel="canonical" href={canonical} />
    <link rel="alternate" hreflang="ru" href={ruUrl} />
    <link rel="alternate" hreflang="en" href={enUrl} />
    <link rel="alternate" hreflang="x-default" href={ruUrl} />
    <meta property="og:type" content="website" />
    <meta property="og:title" content={title} />
    <meta property="og:description" content={description} />
    <meta property="og:url" content={canonical} />
    <meta property="og:image" content={ogUrl} />
    <meta property="og:locale" content={lang === 'ru' ? 'ru_RU' : 'en_US'} />
    <meta name="twitter:card" content="summary_large_image" />
    <link rel="icon" href={`${base}favicon.ico`} />
    <meta name="theme-color" content="#4b75b7" />
    <script is:inline>
      (function () {
        var d = document.documentElement; d.classList.add('js');
        var t = null; try { t = localStorage.getItem('theme'); } catch (e) {}
        if (t === 'dark' || (t !== 'light' && window.matchMedia('(prefers-color-scheme: dark)').matches)) d.classList.add('dark');
      })();
    </script>
  </head>
  <body class="min-h-screen">
    <slot />
    <script>
      const io = new IntersectionObserver((entries) => {
        for (const e of entries) if (e.isIntersecting) { e.target.classList.add('is-visible'); io.unobserve(e.target); }
      }, { rootMargin: '0px 0px -10% 0px' });
      document.querySelectorAll('.reveal').forEach((el) => io.observe(el));
    </script>
  </body>
</html>
```

- [ ] **Step 6: Temporary pages, CI, gitignore**

`src/pages/index.astro` and `src/pages/en/index.astro` (temporary, replaced in Task 5):
```astro
---
import Base from '../layouts/Base.astro';   // '../../layouts/Base.astro' in en/
---
<Base lang="ru" title="Citeck Launcher" description="Citeck Launcher" ogImage="og-ru.png"><h1 class="p-10 text-5xl font-extrabold">Citeck Launcher</h1></Base>
```

`.github/workflows/static.yml`: `node-version: '22'`, build step `run: yarn install --frozen-lockfile && yarn build`.

`.gitignore`: add `.astro`, `.superpowers`, `.playwright-mcp`, `verify-out`.

- [ ] **Step 7: Build**

Run: `yarn build`
Expected: `astro check` 0 errors, `dist/index.html` and `dist/en/index.html` exist.

- [ ] **Step 8: Commit** — `git add -A && git commit -m "Move the site to Astro 7 with Tailwind 4 and a light/dark base layout"`

---

### Task 2: Release and OS logic (pure, tested)

**Files:**
- Create: `src/lib/releases.ts`, `src/lib/detectOS.ts`, `src/lib/releases.test.ts`, `src/lib/detectOS.test.ts`, `src/lib/fixtures/release-2.15.6.json`

**Interfaces:**
- Produces:
  - `type OS = 'windows' | 'macos' | 'linux'`; `type Arch = 'amd64' | 'arm64'`
  - `interface Installer { os: OS; arch: Arch; ext: 'msi'|'dmg'|'deb'|'rpm'; name: string; url: string; sha256Url?: string; size: number }`
  - `interface ReleaseInfo { version: string; publishedAt: string; htmlUrl: string; installers: Installer[] }`
  - `parseRelease(json: unknown): ReleaseInfo | null`
  - `pickInstaller(r: ReleaseInfo, os: OS, arch: Arch): { primary: Installer; alternates: Installer[] } | null`
  - `type Client = { kind: 'desktop'; os: OS; arch: Arch; archKnown: boolean } | { kind: 'mobile' } | { kind: 'unknown' }`
  - `classifyClient(input: { ua: string; platform?: string; maxTouchPoints?: number; uaChArch?: string; uaChBitness?: string }): Client`
  - `detectClient(): Promise<Client>` (browser wrapper, uses `navigator.userAgentData` when present)
  - `RELEASES_PAGE = 'https://github.com/Citeck/citeck-launcher/releases/latest'`

- [ ] **Step 1: Save a real fixture**

```bash
curl -fsS https://api.github.com/repos/Citeck/citeck-launcher/releases/tags/v2.15.6 > src/lib/fixtures/release-2.15.6.json
```

- [ ] **Step 2: Write failing tests `src/lib/releases.test.ts`**

```ts
import { describe, expect, it } from 'vitest';
import fixture from './fixtures/release-2.15.6.json';
import { parseRelease, pickInstaller } from './releases';

describe('parseRelease', () => {
  it('maps every desktop installer of a real 2.x release', () => {
    const r = parseRelease(fixture)!;
    expect(r.version).toBe('2.15.6');
    const keys = r.installers.map((i) => `${i.os}/${i.arch}/${i.ext}`).sort();
    expect(keys).toEqual([
      'linux/amd64/deb', 'linux/amd64/rpm', 'linux/arm64/deb', 'linux/arm64/rpm',
      'macos/amd64/dmg', 'macos/arm64/dmg', 'windows/amd64/msi', 'windows/arm64/msi',
    ]);
    const mac = r.installers.find((i) => i.os === 'macos' && i.arch === 'arm64')!;
    expect(mac.url).toMatch(/citeck-desktop_2\.15\.6_darwin_arm64\.dmg$/);
    expect(mac.sha256Url).toMatch(/\.dmg\.sha256$/);
  });
  it('rejects a 1.x tag and garbage', () => {
    expect(parseRelease({ ...fixture, tag_name: 'v1.4.2' })).toBeNull();
    expect(parseRelease(null)).toBeNull();
    expect(parseRelease({ message: 'API rate limit exceeded' })).toBeNull();
  });
  it('ignores server tarballs and signatures', () => {
    const r = parseRelease(fixture)!;
    expect(r.installers.every((i) => i.name.startsWith('citeck-desktop_'))).toBe(true);
  });
});

describe('pickInstaller', () => {
  const r = parseRelease(fixture)!;
  it('prefers the detected arch, offers the rest of the OS as alternates', () => {
    const p = pickInstaller(r, 'macos', 'arm64')!;
    expect(p.primary.arch).toBe('arm64');
    expect(p.alternates.map((a) => a.arch)).toEqual(['amd64']);
  });
  it('linux primary is .deb of the arch; rpm and other arch are alternates', () => {
    const p = pickInstaller(r, 'linux', 'amd64')!;
    expect(p.primary.ext).toBe('deb');
    expect(p.alternates.map((a) => `${a.arch}.${a.ext}`)).toEqual(['amd64.rpm', 'arm64.deb', 'arm64.rpm']);
  });
  it('falls back to the other arch when the detected one is missing', () => {
    const noArm = { ...r, installers: r.installers.filter((i) => !(i.os === 'windows' && i.arch === 'arm64')) };
    expect(pickInstaller(noArm, 'windows', 'arm64')!.primary.arch).toBe('amd64');
  });
  it('returns null when the OS has no installer', () => {
    expect(pickInstaller({ ...r, installers: r.installers.filter((i) => i.os !== 'windows') }, 'windows', 'amd64')).toBeNull();
  });
});
```

- [ ] **Step 3: Run** `yarn test` — Expected: FAIL, module `./releases` not found.

- [ ] **Step 4: Implement `src/lib/releases.ts`**

```ts
export type OS = 'windows' | 'macos' | 'linux';
export type Arch = 'amd64' | 'arm64';
export type Ext = 'msi' | 'dmg' | 'deb' | 'rpm';
export interface Installer { os: OS; arch: Arch; ext: Ext; name: string; url: string; sha256Url?: string; size: number }
export interface ReleaseInfo { version: string; publishedAt: string; htmlUrl: string; installers: Installer[] }

export const RELEASES_PAGE = 'https://github.com/Citeck/citeck-launcher/releases/latest';
export const LATEST_API = 'https://api.github.com/repos/Citeck/citeck-launcher/releases/latest';

const NAME = /^citeck-desktop_([\d.]+)_(windows|darwin|linux)_(amd64|arm64)\.(msi|dmg|deb|rpm)$/;
const OS_OF: Record<string, OS> = { windows: 'windows', darwin: 'macos', linux: 'linux' };
const EXT_ORDER: Ext[] = ['msi', 'dmg', 'deb', 'rpm'];

interface GhAsset { name?: unknown; browser_download_url?: unknown; size?: unknown }

export function parseRelease(json: unknown): ReleaseInfo | null {
  if (!json || typeof json !== 'object') return null;
  const j = json as { tag_name?: unknown; published_at?: unknown; html_url?: unknown; assets?: unknown };
  const m = typeof j.tag_name === 'string' ? /^v?(\d+)\.(\d+)\.(\d+)/.exec(j.tag_name) : null;
  if (!m || Number(m[1]) < 2 || !Array.isArray(j.assets)) return null;
  const assets = j.assets as GhAsset[];
  const byName = new Map(assets.filter((a) => typeof a.name === 'string').map((a) => [a.name as string, a]));
  const installers: Installer[] = [];
  for (const a of assets) {
    if (typeof a.name !== 'string' || typeof a.browser_download_url !== 'string') continue;
    const n = NAME.exec(a.name);
    if (!n) continue;
    const sha = byName.get(`${a.name}.sha256`);
    installers.push({
      os: OS_OF[n[2]], arch: n[3] as Arch, ext: n[4] as Ext, name: a.name, url: a.browser_download_url,
      sha256Url: typeof sha?.browser_download_url === 'string' ? sha.browser_download_url : undefined,
      size: typeof a.size === 'number' ? a.size : 0,
    });
  }
  return {
    version: `${m[1]}.${m[2]}.${m[3]}`,
    publishedAt: typeof j.published_at === 'string' ? j.published_at : '',
    htmlUrl: typeof j.html_url === 'string' ? j.html_url : RELEASES_PAGE,
    installers,
  };
}

export function pickInstaller(r: ReleaseInfo, os: OS, arch: Arch): { primary: Installer; alternates: Installer[] } | null {
  const mine = r.installers
    .filter((i) => i.os === os)
    .sort((a, b) => (a.arch === b.arch ? EXT_ORDER.indexOf(a.ext) - EXT_ORDER.indexOf(b.ext) : a.arch === arch ? -1 : b.arch === arch ? 1 : a.arch.localeCompare(b.arch)));
  if (mine.length === 0) return null;
  return { primary: mine[0], alternates: mine.slice(1) };
}
```

- [ ] **Step 5: Write failing tests `src/lib/detectOS.test.ts`**

```ts
import { describe, expect, it } from 'vitest';
import { classifyClient } from './detectOS';

const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';
const WIN = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36';
const LNX = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36';
const LNX_ARM = 'Mozilla/5.0 (X11; Linux aarch64; rv:130.0) Gecko/20100101 Firefox/130.0';
const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148';
const ANDROID = 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Chrome/140.0 Mobile Safari/537.36';

describe('classifyClient', () => {
  it('mac defaults to arm64 (Safari hides Apple Silicon) with archKnown=false', () => {
    expect(classifyClient({ ua: MAC, maxTouchPoints: 0 })).toEqual({ kind: 'desktop', os: 'macos', arch: 'arm64', archKnown: false });
  });
  it('mac with UA-CH x86 is Intel', () => {
    expect(classifyClient({ ua: MAC, maxTouchPoints: 0, uaChArch: 'x86' })).toEqual({ kind: 'desktop', os: 'macos', arch: 'amd64', archKnown: true });
  });
  it('iPad (Mac UA + touch) is mobile', () => {
    expect(classifyClient({ ua: MAC, maxTouchPoints: 5 })).toEqual({ kind: 'mobile' });
  });
  it('windows x64 and windows arm via UA-CH', () => {
    expect(classifyClient({ ua: WIN })).toMatchObject({ os: 'windows', arch: 'amd64' });
    expect(classifyClient({ ua: WIN, uaChArch: 'arm', uaChBitness: '64' })).toMatchObject({ os: 'windows', arch: 'arm64', archKnown: true });
  });
  it('linux x86_64 and aarch64', () => {
    expect(classifyClient({ ua: LNX })).toMatchObject({ os: 'linux', arch: 'amd64', archKnown: true });
    expect(classifyClient({ ua: LNX_ARM })).toMatchObject({ os: 'linux', arch: 'arm64', archKnown: true });
  });
  it('phones are mobile, android is not linux', () => {
    expect(classifyClient({ ua: IPHONE })).toEqual({ kind: 'mobile' });
    expect(classifyClient({ ua: ANDROID })).toEqual({ kind: 'mobile' });
  });
  it('unknown UA', () => {
    expect(classifyClient({ ua: 'curl/8.0' })).toEqual({ kind: 'unknown' });
  });
});
```

- [ ] **Step 6: Implement `src/lib/detectOS.ts`**

```ts
import type { Arch, OS } from './releases';

export type Client = { kind: 'desktop'; os: OS; arch: Arch; archKnown: boolean } | { kind: 'mobile' } | { kind: 'unknown' };
export interface ClientInput { ua: string; platform?: string; maxTouchPoints?: number; uaChArch?: string; uaChBitness?: string }

export function classifyClient(i: ClientInput): Client {
  const ua = i.ua;
  if (/iPhone|iPad|iPod|Android|Mobile/i.test(ua)) return { kind: 'mobile' };
  let os: OS | null = null;
  if (/Macintosh|Mac OS X/i.test(ua)) {
    if ((i.maxTouchPoints ?? 0) > 1) return { kind: 'mobile' }; // iPadOS presents a Mac UA
    os = 'macos';
  } else if (/Windows/i.test(ua)) os = 'windows';
  else if (/Linux|X11|CrOS/i.test(ua)) os = 'linux';
  if (!os) return { kind: 'unknown' };

  if (i.uaChArch) {
    const arch: Arch = i.uaChArch === 'arm' ? 'arm64' : 'amd64';
    return { kind: 'desktop', os, arch, archKnown: true };
  }
  if (os === 'macos') return { kind: 'desktop', os, arch: 'arm64', archKnown: false };
  if (/aarch64|arm64|ARM64/.test(ua)) return { kind: 'desktop', os, arch: 'arm64', archKnown: true };
  if (/x86_64|x64|Win64|WOW64|amd64/i.test(ua)) return { kind: 'desktop', os, arch: 'amd64', archKnown: true };
  return { kind: 'desktop', os, arch: 'amd64', archKnown: false };
}

interface UADataLike { getHighEntropyValues(h: string[]): Promise<{ architecture?: string; bitness?: string }> }

export async function detectClient(): Promise<Client> {
  const nav = navigator as Navigator & { userAgentData?: UADataLike };
  let uaChArch: string | undefined;
  let uaChBitness: string | undefined;
  try {
    const v = await nav.userAgentData?.getHighEntropyValues(['architecture', 'bitness']);
    uaChArch = v?.architecture || undefined;
    uaChBitness = v?.bitness || undefined;
  } catch { /* UA-CH unavailable */ }
  return classifyClient({ ua: nav.userAgent, maxTouchPoints: nav.maxTouchPoints, uaChArch, uaChBitness });
}
```

- [ ] **Step 7: Run** `yarn test` — Expected: all tests PASS.

- [ ] **Step 8: Commit** — `git add src/lib && git commit -m "Parse 2.x releases and detect the visitor's OS and architecture"`

---

### Task 3: Dictionaries, language logic

**Files:**
- Create: `src/i18n/ru.ts`, `src/i18n/en.ts`, `src/i18n/index.ts`, `src/i18n/i18n.test.ts`, `src/lib/lang.ts`, `src/lib/lang.test.ts`

**Interfaces:**
- Produces:
  - `type Lang = 'ru' | 'en'`; `type Dict = typeof ru` (en is typed `Dict`, so key parity is a compile error)
  - `t(lang: Lang): Dict`
  - `pagePath(lang: Lang, base: string): string` → `${base}` or `${base}en/`
  - `shouldRedirectToEn(input: { path: string; base: string; stored: string | null; languages: readonly string[] }): boolean`

- [ ] **Step 1: Write `src/i18n/ru.ts`** — the full Russian copy (source of truth). Content:

```ts
export const ru = {
  meta: {
    title: 'Citeck Launcher — вся платформа Citeck одной кнопкой',
    description: 'Лаунчер скачает, настроит и запустит open-source low-code платформу Citeck на вашем компьютере или сервере. Бесплатно, macOS, Windows, Linux.',
  },
  nav: { features: 'Возможности', desktop: 'Desktop', server: 'Сервер', docs: 'Документация', download: 'Скачать', menu: 'Меню', theme: 'Сменить тему', lang: 'English' },
  hero: {
    badge: 'Лаунчер 2.x',
    title1: 'Вся платформа Citeck —',
    title2: 'одной кнопкой',
    lead: 'Лаунчер скачает, настроит и запустит open-source low-code платформу Citeck на вашем компьютере или сервере. Следит за сервисами, обновляет и бэкапит — вам остаётся работать.',
    downloadFor: 'Скачать для',
    download: 'Скачать',
    server: 'Установить на сервер',
    alsoFor: 'Другие варианты:',
    mobile: 'Лаунчер работает на компьютере и сервере. Откройте эту страницу на компьютере или установите на сервер:',
    free: 'Бесплатно',
    oss: 'Open source (LGPL-3.0)',
    shotAlt: 'Дашборд Citeck Launcher: все сервисы стенда запущены',
  },
  numbers: [
    { value: '~28 МБ', label: 'один бинарник, без JVM' },
    { value: '20+', label: 'сервисов поднимаются сами' },
    { value: '3 ОС', label: 'macOS, Windows, Linux · amd64 и arm64' },
    { value: '8', label: 'языков интерфейса' },
  ],
  what: {
    kicker: 'Что вы запускаете',
    title: 'Citeck — low-code платформа для документов и процессов',
    lead: 'Self-hosted и open source замена проприетарным ECM/BPM-системам. Маршруты рисуются в BPMN-дизайнере, типы документов настраиваются без кода, пользователи, роли и права — из коробки.',
    cases: [
      { title: 'Договоры', text: 'Согласование, подписание, реестр' },
      { title: 'Закупки', text: 'Заявки и маршруты согласования' },
      { title: 'HR-процессы', text: 'Кадровые документы и заявления' },
      { title: 'Архив и портал', text: 'Электронный архив, корпоративный портал' },
    ],
    more: 'Подробнее о платформе на citeck.ru',
  },
  tracks: {
    title: 'Два пути к работающему стенду',
    lead: 'Выберите, где будет работать Citeck.',
    desktop: {
      title: 'На своём компьютере',
      tag: 'Desktop-приложение',
      steps: ['Установите Docker Desktop', 'Скачайте и установите лаунчер', 'Нажмите «Быстрый старт» и откройте Citeck в браузере'],
      note: 'Обычное окно приложения, без командной строки. Citeck продолжает работать, даже если окно закрыто.',
    },
    server: {
      title: 'На сервере',
      tag: 'Linux · VM · по SSH',
      steps: ['Установите Docker на сервер', 'Выполните одну команду', 'Мастер спросит домен, HTTPS и пароль — откройте Citeck в браузере'],
      note: 'Управление через команду citeck: статус, логи, обновления, бэкапы.',
    },
  },
  features: {
    kicker: 'Возможности',
    title: 'Лаунчер берёт эксплуатацию на себя',
    items: [
      { title: 'Само поднимется', text: 'Проверки живости перезапускают упавший сервис, а лаунчер записывает, почему он упал.' },
      { title: 'Обновления без страха', text: 'PostgreSQL, RabbitMQ, ZooKeeper и Qdrant обновляются через миграцию копии данных — и откатываются одной командой.' },
      { title: 'Бэкап за минуту', text: 'Все тома — в один архив. Восстановление на этом или другом сервере.' },
      { title: 'HTTPS без боли', text: "Let's Encrypt с автопродлением — для домена и даже для IP-адреса. Или свой сертификат." },
      { title: 'Всё видно', text: 'Статус, CPU, память и живые логи каждого сервиса — в приложении или в терминале.' },
    ],
  },
  gallery: {
    kicker: 'Как это выглядит',
    title: 'Один экран — весь стенд',
    items: [
      { src: 'screenshots/dashboard.png', alt: 'Дашборд: все сервисы и их ресурсы', caption: 'Дашборд: статус, CPU и память каждого сервиса' },
      { src: 'screenshots/logs.png', alt: 'Просмотр логов сервиса', caption: 'Живые логи с поиском и фильтром по уровню' },
      { src: 'screenshots/dependencies.png', alt: 'Диалог обновления зависимостей', caption: 'Обновление PostgreSQL и других зависимостей с откатом' },
      { src: 'screenshots/server-wizard.png', alt: 'Мастер установки на сервере', caption: 'Мастер установки на сервере' },
    ],
  },
  editions: {
    title: 'Community или Enterprise',
    community: { title: 'Community', price: 'Бесплатно', text: 'Полностью open source. Ядро платформы: документы, процессы, BPMN, права.', cta: 'Скачать' },
    enterprise: { title: 'Enterprise', price: 'По лицензии', text: 'Профессиональная поддержка и дополнительные модули. Нужен лицензионный ключ от Citeck.', cta: 'Связаться с нами' },
    note: 'Лаунчер устанавливает обе редакции.',
  },
  req: {
    title: 'Что нужно',
    items: [
      { title: 'Docker', text: 'На Windows и macOS — Docker Desktop' },
      { title: '16 ГБ RAM', text: 'для Community · 24–32 ГБ для Enterprise' },
      { title: '50+ ГБ диска', text: 'под образы и данные' },
    ],
  },
  downloads: {
    title: 'Все загрузки',
    version: 'Версия',
    os: 'Система', arch: 'Архитектура', file: 'Файл', checksum: 'SHA-256',
    allReleases: 'Все релизы на GitHub',
    fallback: 'Установщики для всех систем — на странице последнего релиза.',
    macNote: 'macOS: приложение пока не нотаризовано Apple. При первом запуске откройте его через правый клик → «Открыть».',
    arm: 'ARM (arm64)', intel: 'Intel / AMD (amd64)', apple: 'Apple Silicon', macIntel: 'Intel',
  },
  faq: {
    title: 'Частые вопросы',
    items: [
      { q: 'macOS пишет, что разработчик не проверен', a: 'Приложение пока не нотаризовано Apple. Откройте его один раз через правый клик → «Открыть» (или «Системные настройки → Конфиденциальность и безопасность → Всё равно открыть») — дальше запускается как обычно.' },
      { q: 'Нужен ли Kubernetes?', a: 'Нет. Лаунчеру нужен только Docker: он сам запускает и связывает все сервисы Citeck.' },
      { q: 'Как перейти с лаунчера 1.x?', a: 'Установите 2.x поверх — пространства имён, настройки и данные переносятся автоматически. Контейнеры продолжают работать.' },
      { q: 'Где хранятся мои данные?', a: 'В томах Docker на вашей машине или сервере. Ничего не отправляется наружу; бэкап — одним архивом.' },
      { q: 'Чем Community отличается от Enterprise?', a: 'Community — бесплатная open-source редакция с ядром платформы. Enterprise добавляет поддержку и модули и требует лицензионного ключа.' },
    ],
  },
  cta: { title: 'Запустите Citeck сегодня', lead: 'Бесплатно, open source, на вашем железе.' },
  footer: { community: 'Telegram-сообщество', contacts: 'Контакты', docs: 'Документация', github: 'GitHub', license: 'Лаунчер распространяется по лицензии LGPL-3.0', copy: 'Citeck' },
  copy: { copy: 'Копировать', copied: 'Скопировано' },
  links: {
    citeck: 'https://www.citeck.ru',
    contacts: 'https://www.citeck.ru/contacts/',
    telegram: 'https://telegram.me/citeck',
    docs: 'https://citeck-ecos.readthedocs.io/ru/latest/admin/launch_setup/launcher.html',
    github: 'https://github.com/Citeck/citeck-launcher',
  },
};
```

- [ ] **Step 2: Write `src/i18n/en.ts`** — same shape, typed `export const en: Dict = { … }`, English meaning-for-meaning (hero: "All of Citeck —" / "one click away"; tracks "On your computer" / "On a server"; features "Heals itself", "Upgrade without fear", "Backup in a minute", "HTTPS without pain", "See everything"; docs link `https://citeck-ecos.readthedocs.io/en/latest/admin/launch_setup/launcher.html`; numbers "~28 MB", "20+", "3 OSes", "8"; FAQ answers translated from the RU entries above). Every RU string has an EN counterpart; arrays have equal length.

- [ ] **Step 3: `src/i18n/index.ts`**

```ts
import { ru } from './ru';
import { en } from './en';
export type Lang = 'ru' | 'en';
export type Dict = typeof ru;
const dicts: Record<Lang, Dict> = { ru, en };
export const t = (lang: Lang): Dict => dicts[lang];
```
(`en.ts` imports `type Dict` from `./index` would be circular — instead `en.ts` does `import type { ru } from './ru'; export const en: typeof ru = {…}`.)

- [ ] **Step 4: Write tests `src/i18n/i18n.test.ts` and `src/lib/lang.test.ts`**

```ts
// i18n.test.ts
import { describe, expect, it } from 'vitest';
import { ru } from './ru';
import { en } from './en';

function shape(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(shape);
  if (v && typeof v === 'object') return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, shape(x)]));
  return typeof v;
}
describe('dictionaries', () => {
  it('have the same shape, including array lengths', () => expect(shape(en)).toEqual(shape(ru)));
  it('translate every sentence (no RU text left in EN)', () => {
    const strings: string[] = [];
    const walk = (v: unknown) => { if (typeof v === 'string') strings.push(v); else if (v && typeof v === 'object') Object.values(v).forEach(walk); };
    walk(en);
    expect(strings.filter((s) => /[А-Яа-яЁё]/.test(s))).toEqual([]);
  });
});
```

```ts
// lang.test.ts
import { describe, expect, it } from 'vitest';
import { pagePath, shouldRedirectToEn } from './lang';
const base = '/citeck-launcher/';
describe('shouldRedirectToEn', () => {
  it('redirects a first-time non-Russian visitor from the root', () =>
    expect(shouldRedirectToEn({ path: base, base, stored: null, languages: ['en-US', 'en'] })).toBe(true));
  it('keeps Russian-speaking visitors (any ru-* in the list)', () =>
    expect(shouldRedirectToEn({ path: base, base, stored: null, languages: ['en-US', 'ru-RU'] })).toBe(false));
  it('never redirects once a choice is stored', () =>
    expect(shouldRedirectToEn({ path: base, base, stored: 'ru', languages: ['de'] })).toBe(false));
  it('never redirects away from /en/ (shared links)', () =>
    expect(shouldRedirectToEn({ path: `${base}en/`, base, stored: null, languages: ['ru'] })).toBe(false));
  it('treats a path without the trailing slash as the root', () =>
    expect(shouldRedirectToEn({ path: '/citeck-launcher', base, stored: null, languages: ['fr'] })).toBe(true));
});
describe('pagePath', () => {
  it('builds both pages', () => { expect(pagePath('ru', base)).toBe(base); expect(pagePath('en', base)).toBe(`${base}en/`); });
});
```

- [ ] **Step 5: Run** `yarn test` — Expected: FAIL (`./lang` missing).

- [ ] **Step 6: Implement `src/lib/lang.ts`**

```ts
import type { Lang } from '../i18n';
export const pagePath = (lang: Lang, base: string) => (lang === 'ru' ? base : `${base}en/`);
export function shouldRedirectToEn(i: { path: string; base: string; stored: string | null; languages: readonly string[] }): boolean {
  const root = i.base.endsWith('/') ? i.base : `${i.base}/`;
  const p = i.path.endsWith('/') ? i.path : `${i.path}/`;
  if (p !== root || i.stored) return false;
  return !i.languages.some((l) => l.toLowerCase().startsWith('ru'));
}
```

- [ ] **Step 7: Run** `yarn test && yarn build` — Expected: PASS, build clean.

- [ ] **Step 8: Commit** — `git add src/i18n src/lib/lang* && git commit -m "Add RU/EN copy and the first-visit language rule"`

---

### Task 4: Interactive islands

**Files:**
- Create: `src/islands/useRelease.ts`, `src/islands/DownloadButton.tsx`, `src/islands/DownloadTable.tsx`, `src/islands/CopyCommand.tsx`, `src/islands/ThemeToggle.tsx`, `src/islands/LangRedirect.tsx`, `src/islands/DownloadButton.test.tsx`

**Interfaces:**
- Consumes: `parseRelease`, `pickInstaller`, `RELEASES_PAGE`, `LATEST_API` (Task 2); `detectClient`, `Client` (Task 2); `Dict`, `Lang` (Task 3); `shouldRedirectToEn` (Task 3).
- Produces:
  - `useRelease(): ReleaseInfo | null` — fetches `LATEST_API` once per session, caches the raw JSON in `sessionStorage['citeck-release']`, never throws.
  - `<DownloadButton labels={Dict['hero'] & Dict['downloads']} serverHref="#server" />`
  - `<DownloadTable labels={Dict['downloads']} />`
  - `<CopyCommand command={string} labels={Dict['copy']} />`
  - `<ThemeToggle label={string} />`
  - `<LangRedirect base={string} enPath={string} />` (renders nothing)
  - `SERVER_COMMAND` exported from `src/lib/releases.ts`: `'curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash'`

- [ ] **Step 1: Write failing test `src/islands/DownloadButton.test.tsx`**

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import fixture from '../lib/fixtures/release-2.15.6.json';
import { ru } from '../i18n/ru';
import DownloadButton from './DownloadButton';

const labels = { ...ru.hero, ...ru.downloads };
const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15';

function setUA(ua: string, touch = 0) {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(ua);
  vi.spyOn(navigator, 'maxTouchPoints', 'get').mockReturnValue(touch);
}
afterEach(() => { cleanup(); vi.restoreAllMocks(); sessionStorage.clear(); });

describe('DownloadButton', () => {
  it('server-rendered state links to the releases page (works without JS)', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    setUA(MAC);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    expect(screen.getByRole('link', { name: /Скачать/ })).toHaveProperty('href', 'https://github.com/Citeck/citeck-launcher/releases/latest');
  });
  it('offers the Apple Silicon dmg and an Intel alternate when the API answers', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(MAC);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    await waitFor(() => expect(screen.getByRole('link', { name: /macOS/ }).getAttribute('href')).toMatch(/darwin_arm64\.dmg$/));
    expect(screen.getByText(/2\.15\.6/)).toBeTruthy();
    expect(screen.getByRole('link', { name: /Intel/ }).getAttribute('href')).toMatch(/darwin_amd64\.dmg$/);
  });
  it('rate limit: keeps the fallback link, shows no version, does not throw', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"message":"API rate limit exceeded"}', { status: 403 })));
    setUA(MAC);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(screen.queryByText(/2\.15/)).toBeNull();
    expect(screen.getByRole('link', { name: /Скачать/ }).getAttribute('href')).toBe('https://github.com/Citeck/citeck-launcher/releases/latest');
  });
  it('mobile shows the "open on a computer" message instead of an installer', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(MAC, 5);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    await waitFor(() => expect(screen.getByText(/Откройте эту страницу на компьютере/)).toBeTruthy());
    expect(screen.queryByRole('link', { name: /\.dmg|macOS/ })).toBeNull();
  });
});
```

- [ ] **Step 2: Run** `yarn test` — Expected: FAIL (module missing).

- [ ] **Step 3: Implement `useRelease.ts`**

```ts
import { useEffect, useState } from 'react';
import { LATEST_API, parseRelease, type ReleaseInfo } from '../lib/releases';

const KEY = 'citeck-release';
let inflight: Promise<ReleaseInfo | null> | null = null;

async function load(): Promise<ReleaseInfo | null> {
  try {
    const cached = sessionStorage.getItem(KEY);
    if (cached) return parseRelease(JSON.parse(cached));
  } catch { /* storage unavailable */ }
  try {
    const res = await fetch(LATEST_API, { headers: { Accept: 'application/vnd.github+json' } });
    if (!res.ok) return null;
    const json: unknown = await res.json();
    const info = parseRelease(json);
    if (info) try { sessionStorage.setItem(KEY, JSON.stringify(json)); } catch { /* ignore */ }
    return info;
  } catch { return null; }
}

export function useRelease(): ReleaseInfo | null {
  const [info, setInfo] = useState<ReleaseInfo | null>(null);
  useEffect(() => {
    let alive = true;
    (inflight ??= load()).then((r) => { if (alive) setInfo(r); });
    return () => { alive = false; };
  }, []);
  return info;
}
export const __resetReleaseForTests = () => { inflight = null; };
```
(The test file's `afterEach` also calls `__resetReleaseForTests()`.)

- [ ] **Step 4: Implement `DownloadButton.tsx`**

Behaviour: initial render (and SSR) = primary `<a href={RELEASES_PAGE}>` labelled `labels.download` with a download icon, plus secondary `<a href={serverHref}>` labelled `labels.server →`. After `detectClient()` and `useRelease()`:
- `kind === 'mobile'` → replace the primary with a paragraph `labels.mobile`;
- desktop + `pickInstaller` result → primary `href = primary.url`, text `${labels.downloadFor} ${OS_LABEL[os]}` (`macOS` / `Windows` / `Linux`), a small line under the buttons `v${version} · ${labels.free} · ${labels.oss}`, and `labels.alsoFor` followed by links for each alternate, labelled per `ALT_LABEL` (`macos/amd64` → `labels.macIntel`, `macos/arm64` → `labels.apple`, others `${arch === 'arm64' ? labels.arm : labels.intel} · .${ext}`);
- desktop but no installer for the OS, or no release → keep the fallback link, no version line.
Styling: primary `inline-flex items-center gap-2 rounded-xl bg-brand px-6 py-3.5 text-base font-semibold text-white shadow-[0_10px_30px_-8px_rgb(75_117_183/.7)] hover:bg-brand-dark focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand dark:bg-brand-light dark:text-night`; secondary `rounded-xl border border-slate-300 bg-white/70 px-6 py-3.5 font-semibold backdrop-blur hover:border-brand dark:border-white/15 dark:bg-white/5`.

- [ ] **Step 5: Implement `DownloadTable.tsx`**

Rows for macOS (Apple Silicon dmg, Intel dmg), Windows (amd64 msi, arm64 msi), Linux (amd64 deb, amd64 rpm, arm64 deb, arm64 rpm). Without a release: render one line `labels.fallback` with a link `labels.allReleases` → `RELEASES_PAGE`. With a release: a `<table>` (columns os/arch/file/checksum), file cell links `installer.url` showing `installer.name` and size in MB (`(size/1048576).toFixed(0) + ' MB'`), checksum cell links `sha256Url` labelled `.sha256`; caption `${labels.version} ${version}`; below: `labels.macNote` and the all-releases link. On < 640 px the table becomes stacked cards (`hidden sm:table` + `sm:hidden` list).

- [ ] **Step 6: Implement `CopyCommand.tsx`**

```tsx
import { useState } from 'react';
export default function CopyCommand({ command, labels }: { command: string; labels: { copy: string; copied: string } }) {
  const [done, setDone] = useState(false);
  async function copy() {
    try { await navigator.clipboard.writeText(command); } catch {
      const r = document.createRange(); const el = document.getElementById('server-cmd');
      if (el) { r.selectNodeContents(el); const s = getSelection(); s?.removeAllRanges(); s?.addRange(r); document.execCommand('copy'); }
    }
    setDone(true); setTimeout(() => setDone(false), 2000);
  }
  return (
    <div className="group flex max-w-full items-center gap-3 rounded-xl bg-ink px-4 py-3 font-mono text-sm text-[#c9d8ff] shadow-lg dark:bg-black/40 dark:ring-1 dark:ring-white/10">
      <span className="select-none text-brand-light">$</span>
      <code id="server-cmd" className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap">{command}</code>
      <button type="button" onClick={copy} aria-live="polite"
        className="shrink-0 rounded-lg bg-white/10 px-3 py-1.5 font-sans text-xs font-semibold text-white hover:bg-white/20 focus-visible:outline-2 focus-visible:outline-brand-light">
        {done ? `✓ ${labels.copied}` : labels.copy}
      </button>
    </div>
  );
}
```

- [ ] **Step 7: Implement `ThemeToggle.tsx` and `LangRedirect.tsx`**

```tsx
// ThemeToggle.tsx
import { useEffect, useState } from 'react';
export default function ThemeToggle({ label }: { label: string }) {
  const [dark, setDark] = useState(false);
  useEffect(() => setDark(document.documentElement.classList.contains('dark')), []);
  function toggle() {
    const next = !dark; setDark(next);
    document.documentElement.classList.toggle('dark', next);
    try { localStorage.setItem('theme', next ? 'dark' : 'light'); } catch { /* ignore */ }
  }
  return (
    <button type="button" onClick={toggle} aria-label={label} aria-pressed={dark}
      className="grid size-9 place-items-center rounded-lg text-muted hover:bg-slate-100 hover:text-ink dark:text-night-muted dark:hover:bg-white/10 dark:hover:text-night-ink">
      {dark
        ? <svg aria-hidden="true" viewBox="0 0 24 24" className="size-5" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>
        : <svg aria-hidden="true" viewBox="0 0 24 24" className="size-5" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>}
    </button>
  );
}
```
```tsx
// LangRedirect.tsx
import { useEffect } from 'react';
import { shouldRedirectToEn } from '../lib/lang';
export default function LangRedirect({ base, enPath }: { base: string; enPath: string }) {
  useEffect(() => {
    let stored: string | null = null;
    try { stored = localStorage.getItem('lang'); } catch { /* ignore */ }
    if (shouldRedirectToEn({ path: location.pathname, base, stored, languages: navigator.languages ?? [navigator.language] }))
      location.replace(enPath + location.hash);
  }, [base, enPath]);
  return null;
}
```
The RU/EN switch in the header is a plain link with `onclick` storing `localStorage.lang` (Task 5).

- [ ] **Step 8: Run** `yarn test` — Expected: PASS.

- [ ] **Step 9: Commit** — `git add src/islands src/lib && git commit -m "Add the download, copy, theme and language islands"`

---

### Task 5: Page sections, part 1 — header, hero, numbers, what-is-Citeck, tracks

**Files:**
- Create: `src/components/Icon.astro`, `Header.astro`, `Hero.astro`, `Numbers.astro`, `WhatIsCiteck.astro`, `Tracks.astro`, `Page.astro`
- Modify: `src/pages/index.astro`, `src/pages/en/index.astro`
- Create: `public/screenshots/dashboard.png` (copy of `readme/screenshots/running.png` from `master` until Task 7 replaces it)

**Interfaces:**
- Consumes: `t`, `Lang` (Task 3); islands (Task 4); `SERVER_COMMAND` (Task 4); `pagePath` (Task 3).
- Produces: `Page.astro` props `{ lang: Lang }`; section anchors `#features`, `#desktop`, `#server`, `#downloads`, `#faq`.
- `Icon.astro` props `{ name: 'download'|'server'|'heart'|'shield'|'archive'|'lock'|'activity'|'check'|'arrow'|'github'|'telegram'|'doc'|'desktop'|'docker'|'cpu'|'disk'|'menu'; class?: string }` — inline 24×24 stroke SVGs (lucide paths).

- [ ] **Step 1: Header** — sticky `top-0 z-50`, translucent `bg-white/75 backdrop-blur dark:bg-night/75`, border-bottom on scroll (class toggled by a tiny inline script). Left: Citeck mark (inline SVG diamond in `text-brand`) + "Citeck Launcher". Centre (≥ md): anchors features/desktop/server/docs(external)/GitHub. Right: RU/EN link (`pagePath(other)`, `onclick="try{localStorage.setItem('lang','<other>')}catch(e){}"`), `<ThemeToggle client:load>`, primary "Скачать" → `#downloads`. < md: menu button toggling a `<details>`-based dropdown (works without JS).

- [ ] **Step 2: Hero** — `section.blob-hero relative overflow-hidden pt-16 pb-24 lg:pt-24`. Grid `lg:grid-cols-[1.05fr_1fr] gap-12 items-center max-w-7xl mx-auto px-6`. Left: badge pill (`hero.badge`), `h1` (`text-5xl sm:text-6xl lg:text-[4rem] font-extrabold tracking-[-0.035em] leading-[1.02]`) with `title1` then `<span class="text-brand dark:text-brand-light">{title2}</span>`, lead (`text-lg text-muted dark:text-night-muted max-w-xl`), `<DownloadButton client:load>`, `<CopyCommand client:visible>` below. Right: screenshot in a window frame (`rounded-2xl ring-1 ring-slate-200 shadow-[0_40px_80px_-20px_rgb(13_27_62/.35)] dark:ring-white/15`) with class `float`, `<img width=1102 height=800 fetchpriority="high">`.

- [ ] **Step 3: Numbers** — 4 stat cards in `grid-cols-2 lg:grid-cols-4`, value `text-4xl font-extrabold text-brand dark:text-brand-light`, label muted; each `.reveal`.

- [ ] **Step 4: WhatIsCiteck** — kicker (uppercase, tracking-wide, brand), `h2` `text-4xl font-extrabold`, lead, 4 case cards with icons, link to `links.citeck` with arrow.

- [ ] **Step 5: Tracks** — two big cards side by side (`lg:grid-cols-2`), ids `desktop` and `server`; each: tag pill, title, numbered steps (circles with numbers), note. Desktop card CTA: `<DownloadButton client:visible>` compact variant is NOT reused — a link to `#downloads`. Server card: `<CopyCommand client:visible>`.

- [ ] **Step 6: Page.astro + pages** — `Page.astro` renders `<Base lang title description ogImage={`og-${lang}.png`}>`, `<LangRedirect client:load base enPath>` only when `lang === 'ru'`, then Header, main(Hero, Numbers, WhatIsCiteck, Tracks). `src/pages/index.astro`: `<Page lang="ru" />`; `src/pages/en/index.astro`: `<Page lang="en" />`.

- [ ] **Step 7: Build and look** — `yarn build && yarn preview --port 4391 &`; Playwright MCP screenshot of `http://localhost:4391/citeck-launcher/` at 1280 and 375, light and dark (toggle). Fix anything clipped, overlapping or low-contrast before committing.

- [ ] **Step 8: Commit** — `git commit -am "Build the hero, numbers, platform and two-track sections"` (with `git add` of new files).

---

### Task 6: Page sections, part 2 — features, gallery, editions, requirements, downloads, FAQ, CTA, footer

**Files:**
- Create: `src/components/Features.astro`, `Gallery.astro`, `Editions.astro`, `Requirements.astro`, `Downloads.astro`, `Faq.astro`, `FinalCta.astro`, `Footer.astro`
- Modify: `src/components/Page.astro`

**Interfaces:**
- Consumes: dictionary keys `features`, `gallery`, `editions`, `req`, `downloads`, `faq`, `cta`, `footer`, `links`; `DownloadTable`, `DownloadButton`, `CopyCommand`.

- [ ] **Step 1: Features** (`id="features"`) — bento grid `lg:grid-cols-3 lg:grid-rows-2`: item 0 spans 2 rows (`lg:row-span-2`) and shows a mini "status" illustration (CSS-only list of 5 service rows with green "Running" pills and one row flipping from red to green on a 4 s loop, off under reduced motion); items 1–4 regular cards with icons `shield/archive/lock/activity`. Cards: `rounded-3xl border border-slate-200/80 bg-white p-7 dark:border-white/10 dark:bg-night-card`, hover `-translate-y-0.5 shadow-lg`.

- [ ] **Step 2: Gallery** — horizontal scroll-snap strip (`snap-x overflow-x-auto`) on mobile, 2×2 grid on ≥ lg; each image in a window frame with `loading="lazy"`, `width/height` set, caption below. Only items whose file exists in `public/` are rendered (checked at build with `fs.existsSync`), so a missing screenshot cannot produce a broken image.

- [ ] **Step 3: Editions** — two cards; Enterprise card has a brand gradient border (`bg-gradient-to-br from-brand to-[#9b6bd6] p-px` wrapper). CTAs: Community → `#downloads`, Enterprise → `links.contacts` (`target=_blank rel=noopener`). Note line under.

- [ ] **Step 4: Requirements** — three inline items with icons `docker/cpu/disk`.

- [ ] **Step 5: Downloads** (`id="downloads"`) — title + `<DownloadTable client:visible labels={d.downloads} />`.

- [ ] **Step 6: FAQ** (`id="faq"`) — native `<details>`/`<summary>` list (works without JS), chevron rotating on open, `summary` focus-visible ring.

- [ ] **Step 7: FinalCta + Footer** — CTA section with `blob-hero`, big title, lead, `<DownloadButton client:visible>` and `<CopyCommand client:visible>`. Footer: links (Telegram, contacts, docs, GitHub), license line, `© {year} Citeck`.

- [ ] **Step 8: Wire into Page.astro, add `reveal` to every section heading block and card group; build and look** — same Playwright check as Task 5 Step 7 for the whole page, both languages. Fix issues.

- [ ] **Step 9: Commit** — `git add -A src && git commit -m "Add features, gallery, editions, requirements, downloads, FAQ and footer"`

---

### Task 7: Screenshots and OG images

**Files:**
- Create: `public/screenshots/dashboard.png`, `logs.png`, `dependencies.png`, `server-wizard.png`, `scripts/og.mjs`, `scripts/og-template.html`, `public/og-ru.png`, `public/og-en.png`

- [ ] **Step 1: Isolated stand for screenshots.** Build the launcher from `master` in a worktree under `/home/spk/.spk/sawe/ss/citeck-launcher/.agents/tmp/site-shots/` (copy `internal/daemon/webdist` is produced by `make build`). Run the server daemon with its own `CITECK_HOME` in that directory and `CITECK_SERVER_WEBUI=1` on a free port; create a namespace from the public workspace's community bundle. Never touch containers named `*_iwdsjaa_*` (the user's stand) — if the host lacks memory for a second stand (< 16 GB free: `free -g`), skip to Step 3's fallback.
- [ ] **Step 2: Capture** with Playwright at 1600×1000, dark theme of the launcher UI: dashboard with all apps Running → `dashboard.png`; a service's log panel → `logs.png`; the dependencies dialog → `dependencies.png`. Server wizard: `tmux new -s wiz -x 110 -y 34`, run `citeck install` against the isolated home, capture with `tmux capture-pane -e | aha`, render the HTML with Playwright → `server-wizard.png`. Optimise every PNG (`pngquant --quality 70-90` if present, else Playwright JPEG at quality 85 and adjust dictionary `src` extensions). Stop and remove the isolated stand afterwards (its containers, network, volumes, CITECK_HOME).
- [ ] **Step 3: Fallback** — if the stand cannot run: `dashboard.png` from `git show master:readme/screenshots/running.png`, render only `server-wizard.png` (the wizard's first screens need no stand), and report which gallery items are absent (Task 6's existence check hides them).
- [ ] **Step 4: OG images** — `scripts/og-template.html` (1200×630, `blob-hero` background, Citeck mark, title from `meta.title` split at the dash, a cropped `dashboard.png` on the right); `scripts/og.mjs` renders `?lang=ru|en` with Playwright to `public/og-ru.png` / `public/og-en.png`. Run `yarn og`, open both PNGs and check them.
- [ ] **Step 5: Commit** — `git add public scripts && git commit -m "Add 2.x screenshots and social preview images"`

---

### Task 8: Verification, docs, publish

**Files:**
- Create: `scripts/verify.mjs`
- Modify: `README.md`, `DEPLOYMENT.md`

- [ ] **Step 1: `scripts/verify.mjs`** — starts `astro preview` on a free port, then with Playwright (chromium):
  1. for `lang ∈ {'/', '/en/'}`, `theme ∈ {light, dark}` (via `localStorage.theme` in an init script), `width ∈ {375, 768, 1280}`: full-page screenshot to `verify-out/<lang>-<theme>-<width>.png`; fail on any console error or failed same-origin request; assert no horizontal overflow (`document.documentElement.scrollWidth <= innerWidth`);
  2. no-JS context: every `section` has non-zero opacity and height; the hero download link's href is the releases page;
  3. API failure: route `api.github.com/**` → abort; assert no version text in the hero, fallback href, no console errors;
  4. language: context with `locale: 'de-DE'` on `/` ends on `/en/`; `locale: 'ru-RU'` stays on `/`; `de-DE` with `localStorage.lang='ru'` stays; `ru-RU` on `/en/` stays;
  5. Exit non-zero on any failure, printing which.
- [ ] **Step 2: Run** `yarn test && yarn build && yarn verify` — Expected: all green. Open and review every screenshot in `verify-out/` (Read tool); fix visual defects and re-run.
- [ ] **Step 3: Lighthouse** — `npx lighthouse http://localhost:<port>/citeck-launcher/ --only-categories=performance,accessibility,seo,best-practices --preset=desktop --output=json --chrome-flags="--headless"` and the same for `/en/` and with mobile preset. Target ≥ 95 for performance, accessibility, SEO. Fix findings (image sizes, contrast, missing labels) and re-run.
- [ ] **Step 4: Docs** — rewrite `README.md` (stack, `yarn dev/build/test/verify/og`, structure, how to edit copy in `src/i18n`, how screenshots and OG images are produced) and `DEPLOYMENT.md` (push to `pages` → workflow, Node 22, Pages source = GitHub Actions). Remove stale mentions of Vite/React SPA, `website/` dir and `deploy-pages.yml`.
- [ ] **Step 5: Commit** — `git add -A && git commit -m "Verify the site end to end and document the new stack"`
- [ ] **Step 6: Publish** — `git push origin pages`; watch the "Deploy to GitHub Pages" run (GitHub API, public) to `completed/success`; then load `https://citeck.github.io/citeck-launcher/` and `/en/` with Playwright and screenshot both to confirm the live site.
