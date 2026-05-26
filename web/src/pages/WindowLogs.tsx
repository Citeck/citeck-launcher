import { useParams } from 'react-router'
import { LogViewer } from '../components/LogViewer'
import { DaemonLogsViewer } from '../components/DaemonLogsViewer'
import { useTranslation } from '../lib/i18n'
import { useEffect } from 'react'

/**
 * Standalone log viewer page used by native multi-window mode.
 * Renders without the main app shell (no TabBar, no global layout) — the
 * window chrome is provided by the OS, so the SPA reduces to just the
 * viewer plus a thin header showing what is being tailed.
 *
 * Routes:
 *   /window/logs/:name        — per-app container logs
 *   /window/daemon-logs       — launcher daemon logs
 */
export function WindowLogs() {
  const { t } = useTranslation()
  const { name } = useParams<{ name: string }>()

  useEffect(() => {
    document.title = name ? `Logs — ${name}` : 'Launcher Logs'
  }, [name])

  return (
    <div className="h-screen bg-background text-text flex flex-col">
      <header className="px-3 py-1.5 border-b border-border bg-bg-secondary text-sm flex items-center gap-2">
        <span className="text-text-muted">{t('window.logs.heading')}</span>
        <span className="font-medium">{name ?? 'Launcher daemon'}</span>
      </header>
      <main className="flex-1 min-h-0">
        {name
          ? <LogViewer appName={name} compact />
          : <DaemonLogsViewer />}
      </main>
    </div>
  )
}
