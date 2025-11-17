import type { Platform } from '../types';

export interface DetectedOS {
  platform: Platform;
  architecture?: 'arm64' | 'intel' | 'x64';
}

export function detectOS(): DetectedOS {
  const userAgent = window.navigator.userAgent.toLowerCase();
  const platform = window.navigator.platform.toLowerCase();

  if (platform.includes('mac') || userAgent.includes('mac')) {
    const isAppleSilicon = 
      userAgent.includes('mac os x') && 
      !userAgent.includes('intel') &&
      (platform === 'macos' || platform === 'macintosh');

    return {
      platform: 'macos',
      architecture: isAppleSilicon ? 'arm64' : 'intel',
    };
  }

  if (platform.includes('win') || userAgent.includes('windows')) {
    return {
      platform: 'windows',
      architecture: 'x64',
    };
  }

  if (platform.includes('linux') || userAgent.includes('linux')) {
    return {
      platform: 'linux',
      architecture: 'x64',
    };
  }

  return {
    platform: 'macos',
    architecture: 'arm64',
  };
}

export function getOSDisplayName(os: DetectedOS): string {
  switch (os.platform) {
    case 'macos':
      return os.architecture === 'arm64'
        ? 'macOS (Apple Silicon)' 
        : 'macOS (Intel)';
    case 'windows':
      return 'Windows';
    case 'linux':
      return 'Linux';
    default:
      return 'Download';
  }
}
