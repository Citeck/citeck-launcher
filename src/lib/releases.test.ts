import { describe, expect, it } from 'vitest';
import fixture from './fixtures/release-2.15.6.json';
import { parseRelease, pickInstaller } from './releases';

describe('parseRelease', () => {
  it('maps every desktop installer of a real 2.x release', () => {
    const r = parseRelease(fixture)!;
    expect(r.version).toBe('2.15.6');
    const keys = r.installers.map((i) => `${i.os}/${i.arch}/${i.ext}`).sort();
    expect(keys).toEqual([
      'linux/amd64/deb', 'linux/amd64/rpm', 'linux/arm64/deb', 'linux/arm64/rpm',
      'macos/amd64/dmg', 'macos/arm64/dmg', 'windows/amd64/msi', 'windows/arm64/msi',
    ]);
    const mac = r.installers.find((i) => i.os === 'macos' && i.arch === 'arm64')!;
    expect(mac.url).toMatch(/citeck-desktop_2\.15\.6_darwin_arm64\.dmg$/);
    expect(mac.sha256Url).toMatch(/\.dmg\.sha256$/);
  });
  it('rejects a 1.x tag and garbage', () => {
    expect(parseRelease({ ...fixture, tag_name: 'v1.4.2' })).toBeNull();
    expect(parseRelease(null)).toBeNull();
    expect(parseRelease({ message: 'API rate limit exceeded' })).toBeNull();
  });
  it('ignores server tarballs, signatures and wixpdb files', () => {
    const r = parseRelease(fixture)!;
    expect(r.installers.every((i) => /^citeck-desktop_.*\.(msi|dmg|deb|rpm)$/.test(i.name))).toBe(true);
  });
});

describe('pickInstaller', () => {
  const r = parseRelease(fixture)!;
  it('prefers the detected arch, offers the rest of the OS as alternates', () => {
    const p = pickInstaller(r, 'macos', 'arm64')!;
    expect(p.primary.arch).toBe('arm64');
    expect(p.alternates.map((a) => a.arch)).toEqual(['amd64']);
  });
  it('linux primary is .deb of the arch; rpm and other arch are alternates', () => {
    const p = pickInstaller(r, 'linux', 'amd64')!;
    expect(p.primary.ext).toBe('deb');
    expect(p.alternates.map((a) => `${a.arch}.${a.ext}`)).toEqual(['amd64.rpm', 'arm64.deb', 'arm64.rpm']);
  });
  it('falls back to the other arch when the detected one is missing', () => {
    const noArm = { ...r, installers: r.installers.filter((i) => !(i.os === 'windows' && i.arch === 'arm64')) };
    expect(pickInstaller(noArm, 'windows', 'arm64')!.primary.arch).toBe('amd64');
  });
  it('returns null when the OS has no installer', () => {
    expect(pickInstaller({ ...r, installers: r.installers.filter((i) => i.os !== 'windows') }, 'windows', 'amd64')).toBeNull();
  });
});
