export type OS = 'windows' | 'macos' | 'linux';
export type Arch = 'amd64' | 'arm64';
export type Ext = 'msi' | 'dmg' | 'deb' | 'rpm';

export interface Installer {
  os: OS;
  arch: Arch;
  ext: Ext;
  name: string;
  url: string;
  sha256Url?: string;
  size: number;
}

export interface ReleaseInfo {
  version: string;
  publishedAt: string;
  htmlUrl: string;
  installers: Installer[];
}

export const RELEASES_PAGE = 'https://github.com/Citeck/citeck-launcher/releases/latest';
export const LATEST_API = 'https://api.github.com/repos/Citeck/citeck-launcher/releases/latest';
export const SERVER_COMMAND =
  'curl -fsSL https://github.com/Citeck/citeck-launcher/releases/latest/download/install.sh | bash';

const NAME = /^citeck-desktop_([\d.]+)_(windows|darwin|linux)_(amd64|arm64)\.(msi|dmg|deb|rpm)$/;
const OS_OF: Record<string, OS> = { windows: 'windows', darwin: 'macos', linux: 'linux' };
const EXT_ORDER: Ext[] = ['msi', 'dmg', 'deb', 'rpm'];

interface GhAsset {
  name?: unknown;
  browser_download_url?: unknown;
  size?: unknown;
}

/** GitHub "release" JSON → installers of a 2.x (or newer) desktop release; null for anything else. */
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
      os: OS_OF[n[2]],
      arch: n[3] as Arch,
      ext: n[4] as Ext,
      name: a.name,
      url: a.browser_download_url,
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

/** The installer to offer first for an OS/arch, and the rest of that OS; the other arch stands in when one is missing. */
export function pickInstaller(r: ReleaseInfo, os: OS, arch: Arch): { primary: Installer; alternates: Installer[] } | null {
  const rank = (i: Installer) => (i.arch === arch ? 0 : 1) * 10 + EXT_ORDER.indexOf(i.ext);
  const mine = r.installers.filter((i) => i.os === os).sort((a, b) => rank(a) - rank(b));
  if (mine.length === 0) return null;
  return { primary: mine[0], alternates: mine.slice(1) };
}
