import { useEffect, useState } from 'react';

import Hero from './components/Hero';
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
    title: 'No-Code Platform',
    description: 'Build powerful applications without writing a single line of code. Intuitive drag-and-drop interface for rapid development.',
    icon: <SparklesIcon />,
  },
  {
    title: 'Low-Code Flexibility',
    description: 'Need more control? Extend your applications with custom code when needed, combining the best of both worlds.',
    icon: <CodeIcon />,
  },
  {
    title: 'AI-Powered',
    description: 'Leverage artificial intelligence to accelerate your development process and create smarter applications.',
    icon: <RocketIcon />,
  },
];

const downloads = {
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

// Placeholder for screenshots - add actual screenshot URLs here
const screenshots: string[] = [
  '/screenshots/screenshot1.png',
  '/screenshots/screenshot2.png',
];

export default function App() {
  const [client, setClient] = useState<DetectedOS>();

  useEffect(() => {
    setClient(detectOS());
  }, []);

  return (
    <div className="min-h-screen bg-white dark:bg-gray-900">
      <Hero
        title="Citeck Launcher"
        description="A powerful no-code and low-code platform that empowers you to create custom applications with AI assistance. Start building your vision today."
        client={client}
        downloads={downloads}
      />

      <Features features={features} />

      {screenshots.length > 0 && <Screenshots images={screenshots} />}

      <Downloads version="1.1.10" downloads={downloads} />

      <Footer />
    </div>
  );
}
