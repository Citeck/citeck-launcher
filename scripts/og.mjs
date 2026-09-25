// Renders the social preview images (public/og-ru.png, public/og-en.png, 1200×630) with Playwright.
// Run after changing the hero copy or the dashboard screenshot: `yarn og`.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import { ru } from '../src/i18n/ru.ts';
import { en } from '../src/i18n/en.ts';

const root = fileURLToPath(new URL('..', import.meta.url));
const logo = readFileSync(`${root}src/assets/citeck-logo.svg`, 'utf8');
const shot = readFileSync(`${root}src/assets/screenshots/dashboard.png`).toString('base64');
const inter = readFileSync(`${root}node_modules/@fontsource-variable/inter/files/inter-cyrillic-wght-normal.woff2`).toString('base64');
const interLatin = readFileSync(`${root}node_modules/@fontsource-variable/inter/files/inter-latin-wght-normal.woff2`).toString('base64');

const page = (d) => `<!doctype html><meta charset="utf-8"><style>
@font-face{font-family:I;src:url(data:font/woff2;base64,${interLatin}) format('woff2');font-weight:100 900;unicode-range:U+0000-00FF,U+2000-206F}
@font-face{font-family:I;src:url(data:font/woff2;base64,${inter}) format('woff2');font-weight:100 900;unicode-range:U+0400-04FF}
*{box-sizing:border-box}body{margin:0}
.c{width:1200px;height:630px;position:relative;overflow:hidden;font-family:I,sans-serif;color:#0d1b3e;background:
radial-gradient(700px 380px at 95% -5%,#ffd9f0,transparent 60%),radial-gradient(800px 460px at 55% 30%,#d7e6ff,transparent 60%),radial-gradient(600px 340px at 0% 105%,#d9fff2,transparent 60%),#fff}
.l{position:absolute;left:72px;top:64px;display:flex;align-items:center;gap:14px;color:#4b75b7}
.l .s{width:150px;height:35px}.l .s svg{width:100%;height:100%}
.l b{font-size:18px;background:rgba(75,117,183,.12);padding:5px 10px;border-radius:8px}
h1{position:absolute;left:72px;top:170px;width:560px;margin:0;font-size:66px;line-height:1.03;letter-spacing:-2.2px;font-weight:800}
h1 span{background:linear-gradient(90deg,#4b75b7,#8f6fd8);-webkit-background-clip:text;color:transparent}
p{position:absolute;left:72px;bottom:62px;width:580px;margin:0;font-size:21px;font-weight:600;color:#4a5877}
.w{position:absolute;left:680px;top:120px;width:640px;border-radius:18px;overflow:hidden;box-shadow:0 40px 90px -30px rgba(13,27,62,.6);transform:perspective(1400px) rotateY(-10deg) rotateX(4deg)}
.w img{display:block;width:100%}
</style><div class="c"><div class="l"><span class="s">${logo}</span><b>Launcher</b></div>
<h1>${d.hero.title1}<br><span>${d.hero.title2}</span></h1>
<p>${d.hero.free} · Open source · macOS · Windows · Linux</p>
<div class="w"><img src="data:image/png;base64,${shot}"></div></div>`;

const browser = await chromium.launch();
for (const [lang, d] of [['ru', ru], ['en', en]]) {
  const p = await browser.newPage({ viewport: { width: 1200, height: 630 } });
  await p.setContent(page(d), { waitUntil: 'load' });
  await p.evaluate(() => document.fonts.ready);
  await p.screenshot({ path: `${root}public/og-${lang}.png` });
  await p.close();
  console.log(`public/og-${lang}.png`);
}
await browser.close();
