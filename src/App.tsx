import { useEffect, useState } from 'react';
import type { DownloadLink } from './types';

import Hero from './components/Hero';
import Header from './components/Header';
import Features from './components/Features';
import Downloads from './components/Downloads';
import Screenshots from './components/Screenshots';
import Footer from './components/Footer';
import { type DetectedOS, detectOS } from './utils/detectOS';

const SparklesIcon = () => (
  <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 3v4M3 5h4M6 17v4m-2-2h4m5-16l2.286 6.857L21 12l-5.714 2.143L13 21l-2.286-6.857L5 12l5.714-2.143L13 3z" />
  </svg>
);

const CodeIcon = () => (
  <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 20l4-16m4 4l4 4-4 4M6 16l-4-4 4-4" />
  </svg>
);

const RocketIcon = () => (
  <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
  </svg>
);

const features = [
  {
    title: 'Cross-Platform Launcher',
    description: 'A lightweight launcher that runs on macOS, Windows and Linux — install, launch and manage applications with a single click.',
    icon: <SparklesIcon />,
  },
  {
    title: 'Automatic Updates',
    description: 'Last updates and delta downloads keep your apps current without interrupting your workflow.',
    icon: <CodeIcon />,
  },
  {
    title: 'Secure & Lightweight',
    description: 'All secrets are protected by your personal master password — it is never stored anywhere.',
    icon: <RocketIcon />,
  },
];

type DownloadsShape = {
  macos: { primary: DownloadLink; secondary: DownloadLink[] };
  windows: DownloadLink;
  linux: DownloadLink;
};

const initialDownloads: DownloadsShape = {
  macos: {
    primary: {
      platform: 'macos' as const,
      architecture: 'arm64' as const,
      url: 'https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck-launcher_1.1.10_macos_arm64.dmg',
      label: 'macOS (Apple Silicon)',
    },
    secondary: [
      {
        platform: 'macos' as const,
        architecture: 'intel' as const,
        url: 'https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck-launcher_1.1.10_macos_x64.dmg',
        label: 'macOS (Intel)',
      },
    ],
  },
  windows: {
    platform: 'windows' as const,
    architecture: 'x64' as const,
    url: 'https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck-launcher_1.1.10_windows_x64.msi',
    label: 'Windows',
  },
  linux: {
    platform: 'linux' as const,
    architecture: 'x64' as const,
    url: 'https://github.com/Citeck/citeck-launcher/releases/latest/download/citeck-launcher_1.1.10_linux_x64.deb',
    label: 'Linux',
  },
};

const screenshots: string[] = [
  '/screenshots/screenshot1.png',
  '/screenshots/screenshot2.png',
];

export default function App() {
  const [client, setClient] = useState<DetectedOS>();

  useEffect(() => {
    setClient(detectOS());
  }, []);

  const [downloadsState, setDownloadsState] = useState<DownloadsShape>(initialDownloads);

  useEffect(() => {
    let mounted = true;

    async function loadLatestRelease() {
      try {
        const res = await fetch('https://api.github.com/repos/Citeck/citeck-launcher/releases/latest');
        if (!res.ok) return;
        const data = await res.json();
        const assets: any[] = data.assets || [];

        const find = (re: RegExp) => assets.find(a => re.test(a.name));

        const macArm = find(/macos?.*(arm|arm64|aarch64)|arm64.*macos?/i);
        const macIntel = find(/macos?.*(x86|x64|intel|x86_64)|mac.*x64/i);
        const winAsset = find(/windows|win.*x64|.*\.msi$/i);
        const linuxAsset = find(/linux|.*\.deb$|.*\.AppImage$|.*\.tar\.gz$/i);

        if (!mounted) return;

        setDownloadsState(prev => ({
          macos: {
            primary: macArm
              ? { ...prev.macos.primary, url: macArm.browser_download_url }
              : macIntel
              ? { ...prev.macos.primary, url: macIntel.browser_download_url }
              : prev.macos.primary,
            secondary:
              macArm && macIntel
                ? [
                    { ...prev.macos.secondary[0], url: macIntel.browser_download_url },
                  ]
                : prev.macos.secondary,
          },
          windows: winAsset
            ? { platform: 'windows', architecture: 'x64', url: winAsset.browser_download_url, label: 'Windows' }
            : prev.windows,
          linux: linuxAsset
            ? { platform: 'linux', architecture: 'x64', url: linuxAsset.browser_download_url, label: 'Linux' }
            : prev.linux,
        }));
      } catch (e) {
        // ignore network errors silently but log for debugging
        // eslint-disable-next-line no-console
        console.error('Failed to load latest release', e);
      }
    }

    loadLatestRelease();

    return () => {
      mounted = false;
    };
  }, []);

  return (
    <div className="min-h-screen bg-white dark:bg-gray-900">
      <Header />

      <Hero
        title="Citeck Launcher"
        description="A fast, secure cross-platform launcher for installing, updating and running applications on macOS, Windows and Linux. Small footprint, automatic updates, and an intuitive interface."
        client={client}
        downloads={downloadsState}
      />

      <Features features={features} />

      {screenshots.length > 0 && <Screenshots images={screenshots} />}

      <Downloads downloads={downloadsState} />

      <Footer />
    </div>
  );
}
