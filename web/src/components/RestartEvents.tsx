import { useState, useEffect } from 'react'
import { fetchRestartEvents } from '../lib/api'
import { useTranslation } from '../lib/i18n'
import type { RestartEventDto } from '../lib/types'

interface RestartEventsProps {
  active: boolean
}

export function RestartEvents({ active }: RestartEventsProps) {
  const { t } = useTranslation()
  const [events, setEvents] = useState<RestartEventDto[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!active) return
    let cancelled = false
    setLoading(true)
    fetchRestartEvents().then(data => {
      if (!cancelled) {
        setEvents([...data].reverse())
        setLoading(false)
      }
    })
    return () => { cancelled = true }
  }, [active])

  if (loading && active) {
    return <div className="p-4 text-muted-foreground text-sm">{t('common.loading')}</div>
  }

  if (events.length === 0) {
    return <div className="p-4 text-muted-foreground text-sm">{t('restartEvents.empty')}</div>
  }

  return (
    <div className="overflow-auto h-full">
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-background border-b border-border">
          <tr className="text-left text-muted-foreground">
            <th className="px-3 py-1.5 font-medium">{t('restartEvents.time')}</th>
            <th className="px-3 py-1.5 font-medium">{t('restartEvents.app')}</th>
            <th className="px-3 py-1.5 font-medium">{t('restartEvents.reason')}</th>
            <th className="px-3 py-1.5 font-medium">{t('restartEvents.detail')}</th>
          </tr>
        </thead>
        <tbody>
          {events.map((e, i) => (
            <tr key={i} className="border-b border-border/50 hover:bg-muted/30">
              <td className="px-3 py-1 text-muted-foreground whitespace-nowrap">{new Date(e.ts).toLocaleString()}</td>
              <td className="px-3 py-1 font-medium">{e.app}</td>
              <td className="px-3 py-1">
                <span className={`inline-flex items-center rounded px-1.5 py-0 text-[10px] font-medium leading-4 ${
                  e.reason === 'oom' ? 'bg-destructive/10 text-destructive' :
                  e.reason === 'liveness' ? 'bg-warning/10 text-warning' :
                  'bg-muted text-muted-foreground'
                }`}>{e.reason}</span>
              </td>
              <td className="px-3 py-1 text-muted-foreground">{e.detail}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
