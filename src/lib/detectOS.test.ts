import { describe, expect, it } from 'vitest';
import { classifyClient } from './detectOS';

const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15';
const WIN = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36';
const LNX = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36';
const LNX_ARM = 'Mozilla/5.0 (X11; Linux aarch64; rv:130.0) Gecko/20100101 Firefox/130.0';
const IPHONE = 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148';
const ANDROID = 'Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 Chrome/140.0 Mobile Safari/537.36';

describe('classifyClient', () => {
  it('mac defaults to arm64 (Safari hides Apple Silicon) with archKnown=false', () => {
    expect(classifyClient({ ua: MAC, maxTouchPoints: 0 })).toEqual({ kind: 'desktop', os: 'macos', arch: 'arm64', archKnown: false });
  });
  it('mac with UA-CH x86 is Intel', () => {
    expect(classifyClient({ ua: MAC, maxTouchPoints: 0, uaChArch: 'x86' })).toEqual({ kind: 'desktop', os: 'macos', arch: 'amd64', archKnown: true });
  });
  it('iPad (Mac UA + touch) is mobile', () => {
    expect(classifyClient({ ua: MAC, maxTouchPoints: 5 })).toEqual({ kind: 'mobile' });
  });
  it('windows x64 and windows arm via UA-CH', () => {
    expect(classifyClient({ ua: WIN })).toMatchObject({ os: 'windows', arch: 'amd64' });
    expect(classifyClient({ ua: WIN, uaChArch: 'arm', uaChBitness: '64' })).toMatchObject({ os: 'windows', arch: 'arm64', archKnown: true });
  });
  it('linux x86_64 and aarch64', () => {
    expect(classifyClient({ ua: LNX })).toMatchObject({ os: 'linux', arch: 'amd64', archKnown: true });
    expect(classifyClient({ ua: LNX_ARM })).toMatchObject({ os: 'linux', arch: 'arm64', archKnown: true });
  });
  it('phones are mobile, android is not linux', () => {
    expect(classifyClient({ ua: IPHONE })).toEqual({ kind: 'mobile' });
    expect(classifyClient({ ua: ANDROID })).toEqual({ kind: 'mobile' });
  });
  it('unknown UA', () => {
    expect(classifyClient({ ua: 'curl/8.0' })).toEqual({ kind: 'unknown' });
  });
});
