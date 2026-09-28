import { useEffect, useState } from 'react';
import { detectClient, type Client } from '../lib/detectOS';
import { pickInstaller, RELEASES_PAGE, type Installer, type OS } from '../lib/releases';
import { useRelease } from './useRelease';
import type { Dict } from '../i18n';

const ORDER: OS[] = ['macos', 'windows', 'linux'];
const OS_LABEL: Record<OS, string> = { macos: 'macOS', windows: 'Windows', linux: 'Linux' };

function archLabel(i: Installer, l: Dict['downloads']): string {
  if (i.os === 'macos') return i.arch === 'arm64' ? l.apple : l.macIntel;
  return i.arch === 'arm64' ? l.arm : l.intel;
}

// Apple Silicon first on macOS (every current Mac); amd64 first elsewhere (most PCs and servers).
const archRank = (i: Installer) => (i.os === 'macos' ? (i.arch === 'arm64' ? 0 : 1) : i.arch === 'amd64' ? 0 : 1);

const mb = (n: number) => (n > 0 ? `${Math.round(n / 1048576)} MB` : '');
const link = 'font-medium text-brand underline-offset-2 hover:underline dark:text-brand-light';

export default function DownloadTable({ labels }: { labels: Dict['downloads'] }) {
  const release = useRelease();
  const [client, setClient] = useState<Client | null>(null);

  useEffect(() => {
    let alive = true;
    detectClient().then((c) => {
      if (alive) setClient(c);
    });
    return () => {
      alive = false;
    };
  }, []);

  // The installer the hero button offers this visitor; its OS goes first and the row itself is marked.
  const pick = release && client?.kind === 'desktop' ? pickInstaller(release, client.os, client.arch)?.primary : undefined;
  const osRank = (os: OS) => (os === pick?.os ? -1 : ORDER.indexOf(os));
  const rows = release
    ? [...release.installers].sort(
        (a, b) =>
          osRank(a.os) - osRank(b.os) ||
          Number(b.name === pick?.name) - Number(a.name === pick?.name) ||
          archRank(a) - archRank(b) ||
          a.ext.localeCompare(b.ext),
      )
    : [];
  const yours = (i: Installer) => i.name === pick?.name;
  const badge = (
    <span className="ml-2 inline-block rounded-full bg-brand/10 px-2 py-0.5 align-middle text-[11px] font-semibold text-brand-dark dark:bg-brand-light/15 dark:text-brand-light">
      {labels.yours}
    </span>
  );

  const footer = (
    <div className="mt-5 space-y-2 text-sm text-muted dark:text-night-muted">
      <p>{labels.macNote}</p>
      <p>
        <a className={link} href={release?.htmlUrl ?? RELEASES_PAGE}>
          {labels.allReleases} →
        </a>
      </p>
    </div>
  );

  if (rows.length === 0) {
    return (
      <div>
        <p className="text-muted dark:text-night-muted">
          {labels.fallback}{' '}
          <a className={link} href={RELEASES_PAGE}>
            {labels.allReleases} →
          </a>
        </p>
        <p className="mt-3 text-sm text-muted dark:text-night-muted">{labels.macNote}</p>
      </div>
    );
  }

  return (
    <div>
      <p className="mb-4 text-sm font-semibold text-muted dark:text-night-muted">
        {labels.version} {release!.version}
      </p>
      <div className="overflow-hidden rounded-2xl border border-slate-200 dark:border-white/10">
        <table className="hidden w-full text-left text-sm sm:table">
          <thead className="bg-slate-50 text-xs uppercase tracking-wider text-muted dark:bg-white/5 dark:text-night-muted">
            <tr>
              <th className="px-5 py-3 font-semibold">{labels.os}</th>
              <th className="px-5 py-3 font-semibold">{labels.arch}</th>
              <th className="px-5 py-3 font-semibold">{labels.file}</th>
              <th className="px-5 py-3 font-semibold">{labels.checksum}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-200 dark:divide-white/10">
            {rows.map((i) => (
              <tr
                key={i.name}
                className={yours(i) ? 'bg-brand/[.06] dark:bg-brand-light/[.07]' : 'hover:bg-slate-50/70 dark:hover:bg-white/[.03]'}
              >
                <td className="px-5 py-3 font-semibold">
                  {OS_LABEL[i.os]}
                  {yours(i) && badge}
                </td>
                <td className="px-5 py-3">{archLabel(i, labels)}</td>
                <td className="px-5 py-3">
                  <a className={link} href={i.url}>
                    .{i.ext}
                  </a>{' '}
                  <span className="text-muted dark:text-night-muted">{mb(i.size)}</span>
                </td>
                <td className="px-5 py-3">
                  {i.sha256Url && (
                    <a className={link} href={i.sha256Url}>
                      .sha256
                    </a>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <ul className="divide-y divide-slate-200 sm:hidden dark:divide-white/10">
          {rows.map((i) => (
            <li
              key={i.name}
              className={`flex items-center justify-between gap-3 px-4 py-3 text-sm ${yours(i) ? 'bg-brand/[.06] dark:bg-brand-light/[.07]' : ''}`}
            >
              <span>
                <span className="font-semibold">{OS_LABEL[i.os]}</span>{' '}
                <span className="text-muted dark:text-night-muted">{archLabel(i, labels)}</span>
                {yours(i) && badge}
              </span>
              <a className={`${link} shrink-0 whitespace-nowrap`} href={i.url}>
                .{i.ext} {mb(i.size)}
              </a>
            </li>
          ))}
        </ul>
      </div>
      {footer}
    </div>
  );
}
