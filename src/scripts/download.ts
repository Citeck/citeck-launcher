// Download buttons and the downloads table. Both ship as static HTML that links to the latest release page (what a
// visitor without JavaScript, or with the GitHub API unreachable, keeps); this script swaps in the installer for the
// visitor's OS and the full table once the release and the client are known.
import { detectClient, type Client } from '../lib/detectOS';
import { pickInstaller, RELEASES_PAGE, type Installer, type OS, type ReleaseInfo } from '../lib/releases';
import { loadRelease } from './release';

const OS_LABEL: Record<OS, string> = { macos: 'macOS', windows: 'Windows', linux: 'Linux' };
const ORDER: OS[] = ['macos', 'windows', 'linux'];

interface ArchLabels {
  arm: string;
  intel: string;
  apple: string;
  macIntel: string;
  mb: string;
}
type ButtonLabels = ArchLabels & { downloadFor: string; alsoFor: string; free: string; oss: string };
type TableLabels = ArchLabels & Record<'version' | 'os' | 'arch' | 'file' | 'checksum' | 'allReleases' | 'macNote' | 'yours', string>;

const labelsOf = <T>(el: HTMLElement): T => JSON.parse(el.dataset.labels ?? '{}') as T;
const mb = (n: number, l: ArchLabels) => (n > 0 ? `${Math.round(n / 1048576)} ${l.mb}` : '');
const archLabel = (i: Installer, l: ArchLabels) =>
  i.os === 'macos' ? (i.arch === 'arm64' ? l.apple : l.macIntel) : i.arch === 'arm64' ? l.arm : l.intel;
const altLabel = (i: Installer, l: ArchLabels) =>
  i.os === 'macos' ? `macOS ${archLabel(i, l)}` : `${OS_LABEL[i.os]} ${archLabel(i, l)} · .${i.ext}`;

/** Small element builder: text children are set as text, never parsed as HTML. */
function h(tag: string, attrs: Record<string, string> = {}, ...children: (Node | string | false | undefined)[]): HTMLElement {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  for (const c of children) if (c !== false && c !== undefined) el.append(c);
  return el;
}

export function renderButton(root: HTMLElement, release: ReleaseInfo | null, client: Client): void {
  const l = labelsOf<ButtonLabels>(root);
  const $ = <T extends HTMLElement>(sel: string) => root.querySelector<T>(sel);
  if (client.kind === 'mobile') {
    // A phone cannot install the desktop app: say so, and keep only the server button (or nothing at all).
    if (!('server' in root.dataset)) {
      root.hidden = true;
      return;
    }
    $('[data-mobile]')!.hidden = false;
    $('[data-primary]')!.hidden = true;
    $('[data-more]')!.hidden = true;
    return;
  }
  const pick = release && client.kind === 'desktop' ? pickInstaller(release, client.os, client.arch) : null;
  if (pick) {
    $<HTMLAnchorElement>('[data-primary]')!.href = pick.primary.url;
    $('[data-primary-text]')!.textContent = `${l.downloadFor} ${OS_LABEL[pick.primary.os]} Desktop`;
    const detail = $('[data-detail]');
    if (detail) {
      detail.textContent =
        altLabel(pick.primary, l) + (pick.primary.os === 'macos' ? ` · .${pick.primary.ext}` : '') + (pick.primary.size > 0 ? ` · ${mb(pick.primary.size, l)}` : '');
      detail.hidden = false;
    }
    // Safari on a Mac does not reveal the architecture, so the Apple Silicon guess can be wrong: offer Intel inline.
    const macGuess =
      client.kind === 'desktop' && client.os === 'macos' && !client.archKnown
        ? pick.alternates.find((a) => a.arch !== pick.primary.arch)
        : undefined;
    if (macGuess) {
      const link = $<HTMLAnchorElement>('[data-mac-guess-link]')!;
      link.href = macGuess.url;
      link.textContent = altLabel(macGuess, l);
      $('[data-mac-guess]')!.hidden = false;
      $('[data-more]')!.hidden = false;
    }
  }
  const meta = $('[data-meta]');
  if (meta && release) {
    meta.textContent = `v${release.version} · ${l.free} · ${l.oss}`;
    meta.hidden = false;
  }
}

export function renderTable(root: HTMLElement, release: ReleaseInfo | null, client: Client): void {
  if (!release || release.installers.length === 0) return; // the static fallback text stays
  const l = labelsOf<TableLabels>(root);
  const pick = client.kind === 'desktop' ? pickInstaller(release, client.os, client.arch)?.primary : undefined;
  // The visitor's OS first, the installer picked for them first within it; Apple Silicon first on macOS, amd64 elsewhere.
  const osRank = (os: OS) => (os === pick?.os ? -1 : ORDER.indexOf(os));
  const archRank = (i: Installer) => (i.os === 'macos' ? (i.arch === 'arm64' ? 0 : 1) : i.arch === 'amd64' ? 0 : 1);
  const rows = [...release.installers].sort(
    (a, b) =>
      osRank(a.os) - osRank(b.os) ||
      Number(b.name === pick?.name) - Number(a.name === pick?.name) ||
      archRank(a) - archRank(b) ||
      a.ext.localeCompare(b.ext),
  );
  const yours = (i: Installer) => i.name === pick?.name;
  const link = 'font-medium text-brand underline-offset-2 hover:underline dark:text-brand-light';
  const muted = 'text-muted dark:text-night-muted';
  // The tint lowers the contrast of brand-blue links below AA, so links in the marked row take the darker blue.
  // A brand bar on the left plus a tint, so the row stands apart from the header row above it in both themes.
  const marked =
    'bg-brand/[.06] shadow-[inset_3px_0_0_var(--color-brand)] dark:bg-brand-light/[.12] dark:shadow-[inset_3px_0_0_var(--color-brand-light)] [&_a]:text-brand-dark dark:[&_a]:text-brand-light';
  const badge = () =>
    h('span', { class: 'ml-2 inline-block rounded-full bg-brand/10 px-2 py-0.5 align-middle text-[11px] font-semibold text-brand-dark dark:bg-brand-light/15 dark:text-brand-light' }, l.yours);
  const cell = (...c: (Node | string | false | undefined)[]) => h('td', { class: 'px-5 py-3' }, ...c);

  const table = h(
    'table',
    { class: 'hidden w-full text-left text-sm sm:table' },
    h(
      'thead',
      { class: `border-b border-slate-300/80 bg-slate-50 text-xs uppercase tracking-wider ${muted} dark:border-white/15 dark:bg-white/5` },
      h('tr', {}, ...[l.os, l.arch, l.file, l.checksum].map((t) => h('th', { class: 'px-5 py-3 font-semibold' }, t))),
    ),
    h(
      'tbody',
      { class: 'divide-y divide-slate-200 dark:divide-white/10' },
      ...rows.map((i) =>
        h(
          'tr',
          { class: yours(i) ? marked : 'hover:bg-slate-50/70 dark:hover:bg-white/[.03]' },
          h('td', { class: 'px-5 py-3 font-semibold' }, OS_LABEL[i.os], yours(i) && badge()),
          cell(archLabel(i, l)),
          cell(h('a', { class: link, href: i.url }, `.${i.ext}`), ' ', h('span', { class: muted }, mb(i.size, l))),
          cell(i.sha256Url && h('a', { class: link, href: i.sha256Url }, '.sha256')),
        ),
      ),
    ),
  );
  const list = h(
    'ul',
    { class: 'divide-y divide-slate-200 sm:hidden dark:divide-white/10' },
    ...rows.map((i) =>
      h(
        'li',
        { class: `flex items-center justify-between gap-3 px-4 py-3 text-sm ${yours(i) ? marked : ''}` },
        h('span', {}, h('span', { class: 'font-semibold' }, OS_LABEL[i.os]), ' ', h('span', { class: muted }, archLabel(i, l)), yours(i) && badge()),
        h('a', { class: `${link} shrink-0 whitespace-nowrap`, href: i.url }, `.${i.ext} ${mb(i.size, l)}`),
      ),
    ),
  );
  root.replaceChildren(
    h('p', { class: `mb-4 text-sm font-semibold ${muted}` }, `${l.version} ${release.version}`),
    h('div', { class: 'overflow-hidden rounded-2xl border border-slate-300/80 dark:border-white/10' }, table, list),
    h(
      'div',
      { class: `mt-5 space-y-2 text-sm ${muted}` },
      h('p', {}, l.macNote),
      h('p', {}, h('a', { class: link, href: release.htmlUrl || RELEASES_PAGE }, `${l.allReleases} →`)),
    ),
  );
}

/** Enhance every download button and table on the page; safe to call more than once. */
export async function initDownloads(): Promise<void> {
  const buttons = [...document.querySelectorAll<HTMLElement>('[data-download-button]:not([data-ready])')];
  const tables = [...document.querySelectorAll<HTMLElement>('[data-download-table]:not([data-ready])')];
  if (buttons.length + tables.length === 0) return;
  for (const el of [...buttons, ...tables]) el.dataset.ready = '';
  const clientP = detectClient();
  // The phone message does not need the release; show it without waiting for the API.
  const client = await clientP;
  if (client.kind === 'mobile') buttons.forEach((b) => renderButton(b, null, client));
  const release = await loadRelease();
  if (client.kind !== 'mobile') buttons.forEach((b) => renderButton(b, release, client));
  tables.forEach((t) => renderTable(t, release, client));
  // The page just changed height; let an anchor that was opened directly line itself up again (anchor.ts).
  dispatchEvent(new Event('citeck:layout'));
}
