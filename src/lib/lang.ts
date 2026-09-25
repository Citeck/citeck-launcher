import type { Lang } from '../i18n';

export const pagePath = (lang: Lang, base: string): string => (lang === 'ru' ? base : `${base}en/`);

/**
 * Where the Russian root should send this visitor, or null to stay. Only the root redirects: to /en/ when English
 * was chosen before, or on a first visit from a browser that lists no Russian. Crawlers and automated browsers
 * stay, so the Russian page is what gets indexed and audited. Query and hash survive the redirect.
 *
 * Runs inline in <head> (via toString), so it must not reference anything outside itself.
 */
export function enRedirectTarget(i: {
  path: string;
  search: string;
  hash: string;
  base: string;
  enPath: string;
  stored: string | null;
  languages: readonly string[];
  userAgent: string;
  webdriver: boolean;
}): string | null {
  const root = i.base.charAt(i.base.length - 1) === '/' ? i.base : i.base + '/';
  const path = i.path.charAt(i.path.length - 1) === '/' ? i.path : i.path + '/';
  if (path !== root || i.webdriver || /bot|crawl|spider|lighthouse/i.test(i.userAgent)) return null;
  const toEn = i.stored === 'en' || (!i.stored && !i.languages.some((l) => l.toLowerCase().indexOf('ru') === 0));
  return toEn ? i.enPath + i.search + i.hash : null;
}
