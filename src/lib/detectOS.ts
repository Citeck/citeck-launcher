import type { Arch, OS } from './releases';

export type Client =
  | { kind: 'desktop'; os: OS; arch: Arch; archKnown: boolean }
  | { kind: 'mobile' }
  | { kind: 'unknown' };

export interface ClientInput {
  ua: string;
  maxTouchPoints?: number;
  uaChArch?: string;
  uaChBitness?: string;
}

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

  if (i.uaChArch) return { kind: 'desktop', os, arch: i.uaChArch === 'arm' ? 'arm64' : 'amd64', archKnown: true };
  // Safari on Apple Silicon still says "Intel Mac OS X"; new Macs are arm64, so that is the better guess.
  if (os === 'macos') return { kind: 'desktop', os, arch: 'arm64', archKnown: false };
  if (/aarch64|arm64/i.test(ua)) return { kind: 'desktop', os, arch: 'arm64', archKnown: true };
  if (/x86_64|x64|Win64|WOW64|amd64/i.test(ua)) return { kind: 'desktop', os, arch: 'amd64', archKnown: true };
  return { kind: 'desktop', os, arch: 'amd64', archKnown: false };
}

interface UADataLike {
  getHighEntropyValues(hints: string[]): Promise<{ architecture?: string; bitness?: string }>;
}

export async function detectClient(): Promise<Client> {
  const nav = navigator as Navigator & { userAgentData?: UADataLike };
  let uaChArch: string | undefined;
  let uaChBitness: string | undefined;
  try {
    const v = await nav.userAgentData?.getHighEntropyValues(['architecture', 'bitness']);
    uaChArch = v?.architecture || undefined;
    uaChBitness = v?.bitness || undefined;
  } catch {
    /* UA client hints unavailable */
  }
  return classifyClient({ ua: nav.userAgent, maxTouchPoints: nav.maxTouchPoints, uaChArch, uaChBitness });
}
