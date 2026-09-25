import { describe, expect, it } from 'vitest';
import { enRedirectTarget, pagePath } from './lang';

const base = '/citeck-launcher/';
const enPath = `${base}en/`;
type Env = Parameters<typeof enRedirectTarget>[0];
const env = (o: Partial<Env>): Env => ({
  path: base,
  search: '',
  hash: '',
  base,
  enPath,
  stored: null,
  languages: ['en-US', 'en'],
  userAgent: 'Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0',
  webdriver: false,
  ...o,
});

describe('enRedirectTarget', () => {
  it('redirects a first-time non-Russian visitor from the root', () => expect(enRedirectTarget(env({}))).toBe(enPath));
  it('keeps Russian-speaking visitors (any ru-* in the list)', () =>
    expect(enRedirectTarget(env({ languages: ['en-US', 'ru-RU'] }))).toBeNull());
  it('keeps a visitor who chose Russian', () => expect(enRedirectTarget(env({ stored: 'ru', languages: ['de'] }))).toBeNull());
  it('sends a visitor who chose English to /en/, even with a Russian browser', () =>
    expect(enRedirectTarget(env({ stored: 'en', languages: ['ru-RU'] }))).toBe(enPath));
  it('never redirects away from /en/ (shared links)', () =>
    expect(enRedirectTarget(env({ path: enPath, stored: 'en' }))).toBeNull());
  it('treats a path without the trailing slash as the root', () =>
    expect(enRedirectTarget(env({ path: '/citeck-launcher', languages: ['fr'] }))).toBe(enPath));
  it('keeps the query string and the hash', () =>
    expect(enRedirectTarget(env({ search: '?utm_source=x', hash: '#faq' }))).toBe(`${enPath}?utm_source=x#faq`));
  it('leaves crawlers and audits on the Russian page', () => {
    for (const userAgent of ['Mozilla/5.0 (compatible; Googlebot/2.1)', 'YandexBot/3.0', 'Chrome-Lighthouse', 'ahrefs crawler', 'Baiduspider'])
      expect(enRedirectTarget(env({ userAgent }))).toBeNull();
  });
  it('leaves automated browsers on the Russian page', () => expect(enRedirectTarget(env({ webdriver: true }))).toBeNull());
  it('is self-contained, so it can be inlined into <head>', () => {
    const fn = new Function(`return (${enRedirectTarget.toString()})`)() as typeof enRedirectTarget;
    expect(fn(env({ hash: '#x' }))).toBe(`${enPath}#x`);
  });
});

describe('pagePath', () => {
  it('builds both pages', () => {
    expect(pagePath('ru', base)).toBe(base);
    expect(pagePath('en', base)).toBe(`${base}en/`);
  });
});
