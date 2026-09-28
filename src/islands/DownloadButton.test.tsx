import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import fixture from '../lib/fixtures/release-2.15.6.json';
import { ru } from '../i18n/ru';
import DownloadButton from './DownloadButton';
import { __resetReleaseForTests } from './useRelease';

const labels = { ...ru.hero, ...ru.downloads };
const MAC = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15';
const RELEASES = 'https://github.com/Citeck/citeck-launcher/releases/latest';

function setUA(ua: string, touch = 0) {
  vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(ua);
  // jsdom has no maxTouchPoints; define it per test.
  Object.defineProperty(navigator, 'maxTouchPoints', { value: touch, configurable: true });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  sessionStorage.clear();
  __resetReleaseForTests();
});

describe('DownloadButton', () => {
  it('first render links to the releases page (what a no-JS visitor gets)', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    setUA(MAC);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    expect(screen.getByRole('link', { name: /Скачать/ }).getAttribute('href')).toBe(RELEASES);
  });

  it('offers the Apple Silicon dmg and an Intel alternate when the API answers', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(MAC);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    await waitFor(() =>
      expect(screen.getByRole('link', { name: /Скачать для macOS/ }).getAttribute('href')).toMatch(/darwin_arm64\.dmg$/),
    );
    expect(screen.getByText(/2\.15\.6/)).toBeTruthy();
    expect(screen.getByRole('link', { name: /Intel/ }).getAttribute('href')).toMatch(/darwin_amd64\.dmg$/);
  });

  it('rate limit: keeps the fallback link, shows no version, does not throw', async () => {
    const fetchMock = vi.fn(async () => new Response('{"message":"API rate limit exceeded"}', { status: 403 }));
    vi.stubGlobal('fetch', fetchMock);
    setUA(MAC);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByText(/2\.15/)).toBeNull();
    expect(screen.getByRole('link', { name: /Скачать/ }).getAttribute('href')).toBe(RELEASES);
  });

  it('iPad/phone shows the "open on a computer" message instead of an installer', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(MAC, 5);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    await waitFor(() => expect(screen.getByText(/Откройте эту страницу на компьютере/)).toBeTruthy());
    expect(screen.queryByRole('link', { name: /Скачать для macOS/ })).toBeNull();
    expect(screen.getByRole('link', { name: /Установить на сервер/ }).getAttribute('href')).toBe('#server');
  });
});

describe('DownloadButton, what it offers besides the main installer', () => {
  const LINUX = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/140.0 Safari/537.36';

  it('Linux: one button and a link to every download, not a list of rpm/arm files', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(LINUX);
    render(<DownloadButton labels={labels} serverHref="#server" />);
    await waitFor(() => expect(screen.getByRole('link', { name: /Скачать для Linux/ }).getAttribute('href')).toMatch(/linux_amd64\.deb$/));
    expect(screen.queryByText(/\.rpm/)).toBeNull();
    expect(screen.queryByText(labels.alsoFor)).toBeNull();
    expect(screen.getByRole('link', { name: labels.allDownloads }).getAttribute('href')).toBe('#downloads');
  });

  it('without a server target there is no server button', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise(() => {})));
    setUA(LINUX);
    render(<DownloadButton labels={labels} />);
    expect(screen.queryByRole('link', { name: /Установить на сервер/ })).toBeNull();
  });

  it('can name the file it downloads', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(fixture))));
    setUA(LINUX);
    render(<DownloadButton labels={labels} detail moreLink={false} />);
    await waitFor(() => expect(screen.getByText(/Intel\/AMD \(amd64\) · \.deb · \d+ MB/)).toBeTruthy());
    expect(screen.queryByRole('link', { name: labels.allDownloads })).toBeNull();
  });
});
