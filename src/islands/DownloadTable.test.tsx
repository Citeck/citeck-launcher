import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import fixture from '../lib/fixtures/release-2.15.6.json';
import { ru } from '../i18n/ru';
import DownloadTable from './DownloadTable';
import { __resetReleaseForTests } from './useRelease';

const WIN = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/140.0 Safari/537.36';
const LINUX = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/140.0 Safari/537.36';
const PHONE = 'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/140.0 Mobile Safari/537.36';

function setUA(ua: string) {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(ua);
  Object.defineProperty(navigator, 'maxTouchPoints', { value: 0, configurable: true });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  sessionStorage.clear();
  __resetReleaseForTests();
});

const rows = () => within(screen.getByRole('table')).getAllByRole('row').slice(1);

describe('DownloadTable', () => {
  it("puts the visitor's OS first and marks the installer picked for them", async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(WIN);
    render(<DownloadTable labels={ru.downloads} />);
    await waitFor(() => expect(screen.getAllByText(ru.downloads.yours).length).toBeGreaterThan(0));
    const first = rows()[0];
    expect(first.textContent).toContain('Windows');
    expect(within(first).getByRole('link', { name: /\.msi/ }).getAttribute('href')).toMatch(/windows_amd64\.msi$/);
    expect(first.textContent).toContain(ru.downloads.yours);
    expect(rows().filter((r) => r.textContent?.includes(ru.downloads.yours))).toHaveLength(1);
  });

  it('marks the .deb, not the .rpm, on Linux', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(LINUX);
    render(<DownloadTable labels={ru.downloads} />);
    await waitFor(() => expect(screen.getAllByText(ru.downloads.yours).length).toBeGreaterThan(0));
    expect(within(rows()[0]).getByRole('link', { name: /\.deb/ }).getAttribute('href')).toMatch(/linux_amd64\.deb$/);
  });

  it('keeps the plain order and marks nothing when the OS is not a desktop one', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(PHONE);
    render(<DownloadTable labels={ru.downloads} />);
    await waitFor(() => expect(screen.getByRole('table')).toBeTruthy());
    await new Promise((r) => setTimeout(r, 20));
    expect(rows()[0].textContent).toContain('macOS');
    expect(screen.queryByText(ru.downloads.yours)).toBeNull();
  });
});
