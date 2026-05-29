import { useParams } from 'react-router'
import { LogViewer } from '../components/LogViewer'
import { DaemonLogsViewer } from '../components/DaemonLogsViewer'
import { useTranslation } from '../lib/i18n'
import { useEffect } from 'react'
import { useInheritedTheme } from '../hooks/useInheritedTheme'

/**
 * Standalone log viewer page used by native multi-window mode.
 * Renders without the main app shell (no TabBar, no global layout) — the
 * window chrome is provided by the OS title bar, so the SPA is just the
 * viewer. Window title carries the source label (no in-page heading).
 *
 * Routes:
 *   /window/logs/:name        — per-app container logs
 *   /window/daemon-logs       — launcher daemon logs
 */
export function WindowLogs() {
  const { t } = useTranslation()
  const { name } = useParams<{ name: string }>()

  useInheritedTheme()

  useEffect(() => {
    const label = name ?? t('daemonLogs.title')
    document.title = `${t('window.logs.heading')} ${label}`
  }, [name, t])

  // Escape closes the secondary window; skip when typing in the search input so
  // LogViewer's existing Esc-to-clear behaviour is preserved.
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== 'Escape') return
      const tag = (document.activeElement as HTMLElement | null)?.tagName
      if (tag === 'INPUT' || tag === 'TEXTAREA') return
      window.close()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  return (
    <div className="h-screen bg-background text-foreground flex flex-col">
      <main className="flex-1 min-h-0">
        {name
          ? <LogViewer appName={name} compact />
          : <DaemonLogsViewer compact />}
      </main>
    </div>
  )
}
