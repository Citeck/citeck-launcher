import DownloadButton from './DownloadButton';
import type { DownloadLink } from '../types';

export interface Downloads {
  macos: {
    primary: DownloadLink;
    secondary: DownloadLink[];
  };
  windows: DownloadLink;
  linux: DownloadLink;
}

interface DownloadsProps {
  version: string;
  downloads: Downloads;
}

export const AppleIcon = () => (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M18.71 19.5C17.88 20.74 17 21.95 15.66 21.97C14.32 22 13.89 21.18 12.37 21.18C10.84 21.18 10.37 21.95 9.1 22C7.79 22.05 6.8 20.68 5.96 19.47C4.25 17 2.94 12.45 4.7 9.39C5.57 7.87 7.13 6.91 8.82 6.88C10.1 6.86 11.32 7.75 12.11 7.75C12.89 7.75 14.37 6.68 15.92 6.84C16.57 6.87 18.39 7.1 19.56 8.82C19.47 8.88 17.39 10.1 17.41 12.63C17.44 15.65 20.06 16.66 20.09 16.67C20.06 16.74 19.67 18.11 18.71 19.5ZM13 3.5C13.73 2.67 14.94 2.04 15.94 2C16.07 3.17 15.6 4.35 14.9 5.19C14.21 6.04 13.07 6.7 11.95 6.61C11.8 5.46 12.36 4.26 13 3.5Z" />
  </svg>
);

export const WindowsIcon = () => (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M3 5.45L11 4.12V11.55H3V5.45M11 12.45V19.88L3 18.55V12.45H11M12 4L22 2.28V11.55H12V4M22 12.45V21.72L12 20V12.45H22Z" />
  </svg>
);

export const LinuxIcon = () => (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    <path d="M14.62 8.35C14.2 8.63 12.87 9.39 12.67 9.54C12.28 9.85 11.92 9.83 11.53 9.53C11.33 9.37 10 8.61 9.58 8.34C9.1 8.03 9.13 7.64 9.66 7.42C11.3 6.73 12.94 6.78 14.57 7.45C15.06 7.66 15.08 8.05 14.62 8.35M21.84 15.63C20.91 13.54 19.64 11.64 18 9.97C17.47 9.42 17.14 8.8 16.94 8.09C16.84 7.76 16.77 7.42 16.7 7.08C16.5 6.2 16.41 5.3 16 4.47C15.27 2.89 14 2.07 12.16 2C10.35 2.05 9.05 2.86 8.3 4.45C7.73 5.65 7.79 6.78 7.57 8C7.5 8.35 7.41 8.7 7.29 9.04C7.08 9.7 6.75 10.25 6.29 10.73C4.43 12.83 3.02 15.19 2.14 17.88C1.85 18.69 1.93 19.28 2.77 19.54L3.4 19.73C3.58 19.78 3.77 19.8 3.96 19.8C4.86 19.8 5.33 19.18 5.7 18.5C6.59 16.97 7.89 15.82 9.45 15C10.03 14.71 10.63 14.5 11.28 14.5C11.93 14.5 12.53 14.71 13.11 15C14.67 15.82 15.97 16.97 16.86 18.5C17.23 19.18 17.7 19.8 18.6 19.8C18.79 19.8 18.98 19.78 19.16 19.73L19.79 19.54C20.63 19.28 20.71 18.69 20.42 17.88C20.29 17.5 20.12 17.13 19.95 16.77C19.5 15.87 18.96 15 18.32 14.19L17.5 13.06C17.42 12.94 17.34 12.82 17.27 12.69C17.04 12.33 17.04 12 17.46 11.78C17.85 11.58 18.28 11.58 18.67 11.78C19.11 12.03 19.46 12.38 19.77 12.77C20.37 13.58 20.87 14.44 21.31 15.35C21.38 15.5 21.45 15.64 21.52 15.79L21.84 15.63M9.53 10.13C9 10.13 8.56 9.7 8.56 9.16C8.56 8.63 9 8.19 9.53 8.19S10.5 8.63 10.5 9.16C10.5 9.7 10.06 10.13 9.53 10.13M14.47 10.13C13.94 10.13 13.5 9.7 13.5 9.16C13.5 8.63 13.94 8.19 14.47 8.19S15.44 8.63 15.44 9.16C15.44 9.7 15 10.13 14.47 10.13Z" />
  </svg>
);

export default function Downloads({ version, downloads }: DownloadsProps) {
  return (
    <section className="px-4 py-12 sm:px-6 sm:py-16 lg:px-8 lg:py-20">
      <div className="mx-auto max-w-7xl">
        <div className="text-center">
          <h2 className="mb-3 text-3xl font-bold text-gray-900 dark:text-white sm:text-4xl">
            Download Citeck Launcher
          </h2>
          <p className="mb-8 text-sm text-gray-600 dark:text-gray-300 sm:mb-12 sm:text-base">
            Version {version}
          </p>

          <div className="flex flex-col items-center justify-center gap-4 sm:flex-row sm:gap-6">
            <DownloadButton
              primaryLink={downloads.macos.primary}
              secondaryLinks={downloads.macos.secondary}
              icon={<AppleIcon />}
            />
            <DownloadButton
              primaryLink={downloads.windows}
              icon={<WindowsIcon />}
            />
            <DownloadButton
              primaryLink={downloads.linux}
              icon={<LinuxIcon />}
            />
          </div>
        </div>
      </div>
    </section>
  );
}
