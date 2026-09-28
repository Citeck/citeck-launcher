import { LATEST_API, parseRelease, type ReleaseInfo } from '../lib/releases';

const KEY = 'citeck-release';
let inflight: Promise<ReleaseInfo | null> | null = null;

async function load(): Promise<ReleaseInfo | null> {
  try {
    const cached = sessionStorage.getItem(KEY);
    if (cached) return parseRelease(JSON.parse(cached));
  } catch {
    /* storage unavailable or corrupt: fetch instead */
  }
  try {
    const res = await fetch(LATEST_API, { headers: { Accept: 'application/vnd.github+json' } });
    if (!res.ok) return null;
    const json: unknown = await res.json();
    const info = parseRelease(json);
    if (info) {
      try {
        sessionStorage.setItem(KEY, JSON.stringify(json));
      } catch {
        /* quota or privacy mode */
      }
    }
    return info;
  } catch {
    return null; // offline, blocked, rate limited: the fallback links stay
  }
}

/** The latest 2.x release, fetched once per page and cached for the session; null when unavailable. */
export const loadRelease = (): Promise<ReleaseInfo | null> => (inflight ??= load());

export const __resetReleaseForTests = (): void => {
  inflight = null;
};
