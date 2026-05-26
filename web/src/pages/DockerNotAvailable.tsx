import { RotateCw } from 'lucide-react'
import { openExternal } from '../lib/api'
import { useTranslation } from '../lib/i18n'

const DOCKER_INSTALL_URL = 'https://docs.docker.com/get-docker/'

interface DockerNotAvailableProps {
  /** Error message returned by the daemon's docker health check. */
  error?: string | null
  /** When true, the daemon detected docker is installed but not running. */
  installedButStopped?: boolean
  onRetry: () => void
}

/**
 * Full-screen "Docker is not available" UI shown when the daemon's health
 * check reports the docker daemon as unreachable. Replaces the previous
 * inline banner inside Dashboard with a dedicated screen (Kotlin parity —
 * docs/porting/02 §6).
 *
 * "Installed but stopped" vs "missing" is heuristic — we don't have a
 * reliable "is docker binary present" signal from the daemon, so we treat
 * connection-refused as installed-but-stopped and any other error as
 * missing. Matches Kotlin's `isDockerNotRunning` semantics (ConnectException /
 * ConnectionClosedException = installed but stopped, anything else = missing).
 */
export function DockerNotAvailable({ error, installedButStopped, onRetry }: DockerNotAvailableProps) {
  const { t } = useTranslation()
  const message = installedButStopped
    ? t('dockerUnavailable.installedButStopped')
    : t('dockerUnavailable.missing')

  return (
    <div className="flex flex-col items-center justify-center min-h-full p-8 text-center">
      <h1 className="text-2xl font-bold text-foreground mb-6">{t('dockerUnavailable.title')}</h1>
      <p className="text-sm text-muted-foreground whitespace-pre-line mb-6 max-w-md">{message}</p>
      <div className="text-sm mb-6">
        <span className="text-muted-foreground">{t('dockerUnavailable.installPrefix')}</span>{' '}
        <button
          type="button"
          className="text-primary hover:underline"
          onClick={() => void openExternal(DOCKER_INSTALL_URL)}
        >
          {DOCKER_INSTALL_URL}
        </button>
      </div>
      {error && (
        <pre className="text-xs text-muted-foreground bg-muted/30 rounded p-2 max-w-2xl whitespace-pre-wrap break-all mb-6">
          {error}
        </pre>
      )}
      <button
        type="button"
        className="inline-flex items-center gap-2 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
        onClick={onRetry}
      >
        <RotateCw size={14} /> {t('dockerUnavailable.retry')}
      </button>
    </div>
  )
}

/** Returns true if the daemon health message looks like "docker stopped" vs "docker missing". */
export function detectInstalledButStopped(message: string): boolean {
  const m = message.toLowerCase()
  return m.includes('connection refused') ||
    m.includes('connection reset') ||
    m.includes('no such file') ||
    m.includes('cannot connect') ||
    m.includes('dial unix')
}
