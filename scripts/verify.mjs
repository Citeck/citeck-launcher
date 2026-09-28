// End-to-end checks of the built site: `yarn build && yarn verify`.
// Starts `astro preview`, then with Playwright (chromium):
//  1. both pages × light/dark × 375/768/1280: full-page screenshot into verify-out/, no console errors,
//     no failed same-origin requests, no horizontal overflow, no visible scrollbar on command blocks, every command on one line at 1280, no wrapped status pill, no axe WCAG 2.1 A/AA violations at 375 and 1280;
//  2. JavaScript disabled: every section visible, hero download link points at the releases page;
//  3. GitHub API unreachable: no version text, fallback link, no console errors;
//  4. language: de → /en/, ru stays, stored choice wins both ways, /en/ never redirects, query and hash survive,
//     crawlers and automated browsers stay; the redirect script is the first thing in the Russian <head>;
//  5. the install tracks are tabs: server links open only the server tab, Desktop only the desktop one.
// Exits non-zero on any failure.
import { spawn } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import net from 'node:net';
import { chromium } from 'playwright';

const AXE = readFileSync(createRequire(import.meta.url).resolve('axe-core/axe.min.js'), 'utf8');
const RELEASES = 'https://github.com/Citeck/citeck-launcher/releases/latest';
const OUT = 'verify-out';
mkdirSync(OUT, { recursive: true });

const port = await new Promise((resolve) => {
  const s = net.createServer().listen(0, () => {
    const { port: p } = s.address();
    s.close(() => resolve(p));
  });
});
const preview = spawn('npx', ['astro', 'preview', '--ignore-lock', '--port', String(port)], { stdio: 'ignore' });
const origin = `http://localhost:${port}`;
const ru = `${origin}/citeck-launcher/`;
const en = `${origin}/citeck-launcher/en/`;

const failures = [];
const fail = (msg) => {
  failures.push(msg);
  console.log(`  ✗ ${msg}`);
};
const ok = (msg) => console.log(`  ✓ ${msg}`);

async function waitUp() {
  for (let i = 0; i < 60; i++) {
    try {
      if ((await fetch(ru)).ok) return;
    } catch {
      /* not yet */
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error('preview did not start');
}

function watch(page, label) {
  const errors = [];
  // The browser itself logs a failed or refused GitHub API call (offline, 403 rate limit); the page cannot
  // silence that, and handling it is exactly what the fallback is for — so it is not a page error.
  page.on('console', (m) => m.type() === 'error' && !m.location().url.startsWith('https://api.github.com/') && errors.push(m.text()));
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('requestfailed', (r) => r.url().startsWith(origin) && errors.push(`request failed: ${r.url()}`));
  page.on('response', (r) => r.url().startsWith(origin) && r.status() >= 400 && errors.push(`${r.status()} ${r.url()}`));
  return () => (errors.length ? fail(`${label}: ${errors.join(' | ')}`) : ok(`${label}: no errors`));
}

const revealAll = (page) => page.evaluate(() => document.querySelectorAll('.reveal').forEach((e) => e.classList.add('is-visible')));

try {
  await waitUp();
  // Real scrollbars, as a desktop visitor sees them (headless hides them by default).
  const browser = await chromium.launch({ ignoreDefaultArgs: ['--hide-scrollbars'] });

  console.log('1. layouts');
  for (const [name, url, locale] of [['ru', ru, 'ru-RU'], ['en', en, 'en-US']]) {
    for (const theme of ['light', 'dark']) {
      for (const width of [375, 768, 1280]) {
        // Dark is the default for everyone, even with a light system theme; light is only a stored choice.
        const ctx = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: 'light', locale });
        if (theme === 'light') await ctx.addInitScript(() => localStorage.setItem('theme', 'light'));
        const page = await ctx.newPage();
        const done = watch(page, `${name}/${theme}/${width}`);
        await page.goto(url, { waitUntil: 'networkidle' });
        await revealAll(page);
        await page.waitForTimeout(400);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        if (overflow > 0) fail(`${name}/${theme}/${width}: horizontal overflow ${overflow}px`);
        const bars = await page.evaluate(() => [...document.querySelectorAll('main code')].filter((c) => c.offsetHeight - c.clientHeight > 0).length);
        if (bars) fail(`${name}/${theme}/${width}: ${bars} command block(s) show a scrollbar`);
        if (width === 1280) {
          const cut = await page.evaluate(() => [...document.querySelectorAll('main code')].filter((c) => c.scrollWidth > c.clientWidth + 1).length);
          if (cut) fail(`${name}/${theme}/${width}: ${cut} command block(s) do not fit on one line`);
        }
        const wrapped = await page.evaluate(() =>
          [...document.querySelectorAll('.pulse-status > span')].filter((e) => e.scrollHeight > parseFloat(getComputedStyle(e).lineHeight) * 1.5 + 8).length,
        );
        if (wrapped) fail(`${name}/${theme}/${width}: a status pill wraps onto two lines`);
        const dark = await page.evaluate(() => document.documentElement.classList.contains('dark'));
        if (dark !== (theme === 'dark')) fail(`${name}/${theme}/${width}: theme class is ${dark ? 'dark' : 'light'}`);
        await page.screenshot({ path: `${OUT}/${name}-${theme}-${width}.png`, fullPage: true });
        if (width !== 768) {
          // Contrast is only meaningful once the reveal fades have finished (the looping decorations never do).
          await page.waitForFunction(() => document.getAnimations().every((a) => !(a instanceof CSSTransition) || a.playState === 'finished'));
          await page.addScriptTag({ content: AXE });
          const violations = await page.evaluate(async () =>
            (await window.axe.run(document, { runOnly: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] })).violations.map(
              (v) => `${v.id} (${v.nodes.map((n) => n.target.join(' ')).join(', ')})`,
            ),
          );
          violations.length ? fail(`${name}/${theme}/${width}: axe ${violations.join('; ')}`) : ok(`${name}/${theme}/${width}: axe clean`);
        }
        done();
        await ctx.close();
      }
    }
  }

  console.log('2. without JavaScript');
  {
    const ctx = await browser.newContext({ javaScriptEnabled: false, locale: 'de-DE' });
    const page = await ctx.newPage();
    await page.goto(ru);
    const hidden = await page.evaluate(() =>
      [...document.querySelectorAll('main section')].filter((s) => {
        const r = s.getBoundingClientRect();
        return r.height === 0 || getComputedStyle(s).opacity === '0' || [...s.querySelectorAll('.reveal')].some((e) => getComputedStyle(e).opacity === '0');
      }).length,
    );
    hidden ? fail(`${hidden} sections hidden without JS`) : ok('all sections visible without JS');
    const href = await page.locator('main a', { hasText: 'Скачать' }).first().getAttribute('href');
    href === RELEASES ? ok('download link goes to the releases page') : fail(`no-JS download href = ${href}`);
    if (!page.url().startsWith(ru)) fail(`no-JS page moved to ${page.url()}`);
    await ctx.close();
  }

  console.log('3. GitHub API unreachable');
  {
    const ctx = await browser.newContext({ locale: 'ru-RU' });
    await ctx.route('https://api.github.com/**', (r) => r.abort());
    const page = await ctx.newPage();
    const done = watch(page, 'api-down');
    await page.goto(ru, { waitUntil: 'networkidle' });
    await page.waitForTimeout(800);
    const heroText = await page.locator('section').first().innerText();
    /v\d+\.\d+\.\d+/.test(heroText) ? fail('version shown while the API is down') : ok('no version shown');
    const href = await page.locator('main a', { hasText: 'Скачать' }).first().getAttribute('href');
    href === RELEASES ? ok('fallback download link') : fail(`api-down download href = ${href}`);
    done();
    await ctx.close();
  }

  console.log('4. language');
  const head = (await (await fetch(ru)).text()).match(/<head>([\s\S]*?)<\/head>/)[1];
  /^<meta charset="utf-8"><script>\(function \(\) \{[^<]*enRedirectTarget/.test(head)
    ? ok('redirect script runs first in <head>, before any stylesheet or font')
    : fail('the redirect script is not the first thing in the Russian <head>');
  (await (await fetch(en)).text()).includes('enRedirectTarget') ? fail('/en/ carries the redirect script') : ok('/en/ has no redirect script');
  // Playwright's chromium reports navigator.webdriver = true; the page skips automated browsers on purpose,
  // so every case except the "automated" one pretends to be a regular browser.
  for (const [label, url, locale, stored, expected, o = {}] of [
    ['de on / → /en/', ru, 'de-DE', null, en],
    ['ru on / stays', ru, 'ru-RU', null, ru],
    ['de with stored ru stays', ru, 'de-DE', 'ru', ru],
    ['ru with stored en → /en/', ru, 'ru-RU', 'en', en],
    ['ru on /en/ stays', en, 'ru-RU', null, en],
    ['de keeps query and hash', `${ru}?utm_source=x#faq`, 'de-DE', null, `${en}?utm_source=x#faq`],
    ['de crawler stays', ru, 'de-DE', null, ru, { userAgent: 'Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)' }],
    ['de automated browser stays', ru, 'de-DE', null, ru, { webdriver: true }],
  ]) {
    const ctx = await browser.newContext({ locale, ...(o.userAgent ? { userAgent: o.userAgent } : {}) });
    if (!o.webdriver) await ctx.addInitScript(() => Object.defineProperty(Navigator.prototype, 'webdriver', { get: () => false }));
    if (stored) await ctx.addInitScript((v) => localStorage.setItem('lang', v), stored);
    const page = await ctx.newPage();
    await page.goto(url, { waitUntil: 'networkidle' });
    await page.waitForTimeout(300);
    page.url() === expected ? ok(label) : fail(`${label}: ended on ${page.url()}`);
    await ctx.close();
  }

  console.log('8. editions comparison link');
  for (const [name, url] of [['ru', ru], ['en', en]]) {
    const html = await (await fetch(url)).text();
    const n = (html.match(/href="https:\/\/citeck-ecos\.readthedocs\.io\/[a-z]{2}\/latest\/introduction\/modules\.html#community-enterprise"/g) ?? []).length;
    n >= 2 ? ok(`${name}: editions and FAQ link to the comparison (${n})`) : fail(`${name}: ${n} link(s) to the editions comparison, expected 2`);
  }

  console.log('7. theme');
  {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, colorScheme: 'light', locale: 'ru-RU' });
    const page = await ctx.newPage();
    await page.goto(ru, { waitUntil: 'networkidle' });
    const isDark = () => page.evaluate(() => document.documentElement.classList.contains('dark'));
    (await isDark()) ? ok('first visit is dark, even with a light system theme') : fail('first visit is not dark');
    await page.locator('header button[aria-pressed]').first().click();
    const stored = await page.evaluate(() => localStorage.getItem('theme'));
    !(await isDark()) && stored === 'light' ? ok('the toggle switches to light and remembers it') : fail(`after toggle: dark=${await isDark()} stored=${stored}`);
    await page.reload({ waitUntil: 'networkidle' });
    !(await isDark()) ? ok('light survives a reload') : fail('light was lost on reload');
    await page.locator('header button[aria-pressed]').first().click();
    await page.reload({ waitUntil: 'networkidle' });
    (await isDark()) ? ok('and dark again after toggling back') : fail('toggling back to dark did not stick');
    await ctx.close();
  }

  console.log('6. hover never moves an element from under the cursor');
  {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: 'ru-RU' });
    const page = await ctx.newPage();
    await page.goto(ru, { waitUntil: 'networkidle' });
    await revealAll(page);
    for (const [label, sel] of [
      ['feature card', 'main section:has(.pulse-status) article:nth-of-type(2)'],
      ['use-case card', 'main .group'],
      ['hero download button', 'main section a[href*="releases"], main section a[href$=".msi"], main section a[href$=".deb"], main section a[href$=".dmg"]'],
    ]) {
      const el = page.locator(sel).first();
      await el.scrollIntoViewIfNeeded();
      await page.waitForTimeout(700);
      const box = await el.boundingBox();
      await page.mouse.move(box.x + box.width / 2, box.y + box.height - 1);
      await page.waitForTimeout(400);
      // Tailwind 4 moves things with the \`translate\` property, not \`transform\`; check both.
      const t = await el.evaluate((e) => [getComputedStyle(e).transform, getComputedStyle(e).translate].filter((v) => v !== 'none' && v !== '0px').join(' '));
      t === '' ? ok(`${label}: stays put on hover`) : fail(`${label}: moves on hover (${t}), so it jitters at its edge`);
    }
    await ctx.close();
  }

  console.log('5. install tracks are tabs');
  {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, locale: 'ru-RU' });
    const page = await ctx.newPage();
    const done = watch(page, 'tabs');
    const shown = () =>
      page.evaluate(() => ['desktop', 'server'].filter((id) => document.getElementById(id)?.getClientRects().length));
    const expectOnly = async (label, id) => {
      const v = await shown();
      const inView = await page.evaluate((x) => {
        const r = document.getElementById(x).getBoundingClientRect();
        return r.top < innerHeight && r.bottom > 0;
      }, id);
      const selected = await page.evaluate((x) => document.querySelector(`[role="tab"][aria-controls="${x}"]`)?.getAttribute('aria-selected') ?? null, id);
      v.length === 1 && v[0] === id && inView && selected === 'true' ? ok(label) : fail(`${label}: shown=${v} inView=${inView} selected=${selected}`);
    };
    await page.goto(ru, { waitUntil: 'networkidle' });
    await page.locator('main a', { hasText: 'Установить на сервер' }).first().click();
    await page.waitForTimeout(600);
    await expectOnly('hero "install on a server" opens only the server tab', 'server');
    await page.locator('header nav a', { hasText: 'Desktop' }).click();
    await page.waitForTimeout(600);
    await expectOnly('header "Desktop" opens only the desktop tab', 'desktop');
    await page.goto(`${ru}#server`, { waitUntil: 'networkidle' });
    await page.waitForTimeout(600);
    await expectOnly('a shared #server link opens the server tab', 'server');
    const gap = () =>
      page.evaluate(() => document.querySelector('[role="tablist"]').getBoundingClientRect().top - document.querySelector('[data-header]').getBoundingClientRect().bottom);
    const g0 = await gap();
    g0 >= 16 && g0 <= 48 ? ok(`#server: the tabs sit ${Math.round(g0)}px under the header`) : fail(`#server: tabs ${Math.round(g0)}px under the header`);
    const y0 = await page.evaluate(() => scrollY);
    await page.locator('[role="tab"][aria-controls="desktop"]').click();
    await page.waitForTimeout(500);
    await page.locator('[role="tab"][aria-controls="server"]').click();
    await page.waitForTimeout(500);
    const y1 = await page.evaluate(() => scrollY);
    y1 === y0 ? ok('switching tabs does not scroll the page') : fail(`switching tabs scrolled ${y0} → ${y1}`);
    const hash = await page.evaluate(() => location.hash);
    hash === '#server' ? ok('the address follows the tab') : fail(`hash after switching = ${hash}`);
    await page.goto(ru, { waitUntil: 'networkidle' });
    await page.locator('main a', { hasText: 'Установить на сервер' }).first().click();
    await page.waitForTimeout(1200);
    const g1 = await gap();
    Math.abs(g1 - g0) <= 2 ? ok('the hero button lands at the same spot') : fail(`hero button lands ${Math.round(g1)}px under the header, #server at ${Math.round(g0)}px`);
    const cmd = await page.evaluate(() => {
      const c = document.querySelector('#server code');
      return c ? c.scrollWidth <= c.clientWidth + 1 : null;
    });
    cmd ? ok('the server tab shows the whole command on one line') : fail(`server tab command fits = ${cmd}`);
    done();
    await ctx.close();
  }

  await browser.close();
} catch (e) {
  fail(String(e));
} finally {
  preview.kill();
}

console.log(failures.length ? `\nFAILED: ${failures.length}` : '\nALL CHECKS PASSED');
process.exit(failures.length ? 1 : 0);
