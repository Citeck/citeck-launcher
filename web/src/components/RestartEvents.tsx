import { useState, useEffect, useTransition, useCallback } from 'react'
import { Trash2 } from 'lucide-react'
import { fetchRestartEvents, clearAppRestartEvents } from '../lib/api'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'
import type { RestartEventDto } from '../lib/types'
import { formatDateTime } from '../lib/datetime'
import { toast } from '../lib/toast'

interface RestartEventsProps {
  /** App to scope the restart log to — the drawer is per-service. */
  appName: string
}

/**
 * Per-app restart-event log, rendered inside the service right-drawer. Restart
 * events are namespace-wide on the daemon; we fetch them and filter to this
 * app. user_restart is hidden (the log is reserved for non-user causes: OOM,
 * liveness, crash, pull-retry, …). The clear button wipes this app's events
 * server-side.
 */
export function RestartEvents({ appName }: RestartEventsProps) {
  const { t } = useTranslation()
  const [events, setEvents] = useState<RestartEventDto[]>([])
  const [isPending, startTransition] = useTransition()
  const [clearing, setClearing] = useState(false)

  // Re-fetch when any app's restart count changes (SSE updates the namespace).
  const totalRestarts = useDashboardStore((s) =>
    s.namespace?.apps?.reduce((sum, a) => sum + (a.restartCount ?? 0), 0) ?? 0,
  )
  // Refresh the namespace after a clear so the ↻N badge (driven by the
  // namespace DTO) drops to zero immediately, not on the next natural poll.
  const refreshNamespace = useDashboardStore((s) => s.fetchData)

  const reload = useCallback(() => {
    let cancelled = false
    startTransition(async () => {
      const data = await fetchRestartEvents()
      if (!cancelled) {
        setEvents([...data].reverse().filter((e) => e.app === appName && e.reason !== 'user_restart'))
      }
    })
    return () => { cancelled = true }
  }, [appName])

  useEffect(() => reload(), [reload, totalRestarts])

  const handleClear = async () => {
    setClearing(true)
    try {
      await clearAppRestartEvents(appName)
      setEvents([])
      refreshNamespace()
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setClearing(false)
    }
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-1">
        <div className="text-xs font-medium">{t('dashboard.restartEvents')}</div>
        {events.length > 0 && (
          <button
            type="button"
            onClick={handleClear}
            disabled={clearing}
            className="flex items-center gap-1 rounded border border-border px-1.5 py-0.5 text-[11px] text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-50"
          >
            <Trash2 size={11} /> {t('logViewer.clear')}
          </button>
        )}
      </div>
      {events.length === 0 ? (
        <div className="text-[11px] text-muted-foreground">
          {isPending ? t('common.loading') : t('restartEvents.empty')}
        </div>
      ) : (
        <div className="max-h-40 overflow-y-auto rounded border border-border">
          <table className="w-full text-[11px]">
            <tbody>
              {events.map((e, i) => (
                <tr key={i} className="border-b border-border/40 last:border-0 align-top">
                  <td className="px-2 py-1 whitespace-nowrap font-mono text-muted-foreground">{formatDateTime(e.ts)}</td>
                  <td className="px-2 py-1 whitespace-nowrap">{e.reason}</td>
                  <td className="px-2 py-1 text-muted-foreground break-all">{e.detail}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
