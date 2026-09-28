import { useEffect, useState } from 'react';
import { detectClient, type Client } from '../lib/detectOS';
import { pickInstaller, RELEASES_PAGE, type Installer, type OS } from '../lib/releases';
import { useRelease } from './useRelease';
import { ArrowIcon, DownloadIcon } from './icons';
import type { Dict } from '../i18n';

type Labels = Dict['hero'] & Dict['downloads'];

const OS_LABEL: Record<OS, string> = { macos: 'macOS', windows: 'Windows', linux: 'Linux' };

export function altLabel(i: Installer, l: Labels): string {
  if (i.os === 'macos') return `macOS ${i.arch === 'arm64' ? l.apple : l.macIntel}`;
  return `${OS_LABEL[i.os]} ${i.arch === 'arm64' ? l.arm : l.intel} · .${i.ext}`;
}

const primaryCls =
  'inline-flex items-center justify-center gap-2 rounded-xl bg-brand px-6 py-3.5 text-base font-semibold text-white shadow-[0_12px_32px_-10px_rgb(75_117_183/.8)] transition hover:bg-brand-dark focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand dark:bg-brand-light dark:text-night dark:hover:bg-white';
const secondaryCls =
  'inline-flex items-center justify-center gap-2 rounded-xl border border-slate-300 bg-white/70 px-6 py-3.5 text-base font-semibold text-ink backdrop-blur transition hover:border-brand hover:text-brand focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand dark:border-white/15 dark:bg-white/5 dark:text-night-ink dark:hover:border-brand-light dark:hover:text-brand-light';

const linkCls = 'font-medium text-brand underline-offset-2 hover:underline dark:text-brand-light';

/**
 * The installer for the visitor's OS. `serverHref` adds the server button; `detail` names the file under the button;
 * `moreLink` points at the full downloads table. The only alternate offered inline is the Intel build on a Mac whose
 * architecture the browser does not reveal (Safari), because there the guess can be wrong.
 */
export default function DownloadButton({
  labels,
  serverHref,
  showMeta = true,
  detail = false,
  moreLink = true,
}: {
  labels: Labels;
  serverHref?: string;
  showMeta?: boolean;
  detail?: boolean;
  moreLink?: boolean;
}) {
  const release = useRelease();
  const mb = (n: number) => (n > 0 ? ` · ${Math.round(n / 1048576)} ${labels.mb}` : '');
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

  const pick = release && client?.kind === 'desktop' ? pickInstaller(release, client.os, client.arch) : null;
  const mobile = client?.kind === 'mobile';
  const macGuess =
    pick && client?.kind === 'desktop' && client.os === 'macos' && !client.archKnown
      ? pick.alternates.find((a) => a.arch !== pick.primary.arch)
      : undefined;

  if (mobile && !serverHref) return null;

  return (
    <div className="flex flex-col gap-3">
      {mobile && <p className="max-w-md text-sm text-muted dark:text-night-muted">{labels.mobile}</p>}
      <div className="flex flex-wrap gap-3">
        {!mobile && (
          <a className={primaryCls} href={pick ? pick.primary.url : RELEASES_PAGE}>
            <DownloadIcon />
            {pick ? `${labels.downloadFor} ${OS_LABEL[pick.primary.os]} Desktop` : labels.download}
          </a>
        )}
        {serverHref && (
          <a className={secondaryCls} href={serverHref}>
            {labels.server}
            <ArrowIcon />
          </a>
        )}
      </div>
      {detail && pick && (
        <p className="text-sm text-muted dark:text-night-muted">
          {altLabel(pick.primary, labels)}
          {pick.primary.os === 'macos' ? ` · .${pick.primary.ext}` : ''}
          {mb(pick.primary.size)}
        </p>
      )}
      {!mobile && (macGuess || moreLink) && (
        <p className="text-sm text-muted dark:text-night-muted">
          {macGuess && (
            <>
              {labels.alsoFor}{' '}
              <a className={linkCls} href={macGuess.url}>
                {altLabel(macGuess, labels)}
              </a>
              {moreLink && ' · '}
            </>
          )}
          {moreLink && (
            <a className={linkCls} href="#downloads">
              {labels.allDownloads}
            </a>
          )}
        </p>
      )}
      {showMeta && release && (
        <p className="text-sm text-muted dark:text-night-muted">
          v{release.version} · {labels.free} · {labels.oss}
        </p>
      )}
    </div>
  );
}
