import type { DetectedOS } from './utils/detectOS';

export type Platform = 'macos' | 'windows' | 'linux';

export interface DownloadLink extends DetectedOS {
  url: string;
  label: string;
}
