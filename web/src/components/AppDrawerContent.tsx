import { useEffect, useState, useCallback } from 'react'
import { getAppInspect, postAppRestart } from '../lib/api'
import type { AppInspectDto } from '../lib/types'
import { useDashboardStore } from '../lib/store'
import { usePanelStore } from '../lib/panels'
import { useTranslation } from '../lib/i18n'
import { StatusBadge } from './StatusBadge'
import { toast } from '../lib/toast'
import { RotateCw, FileText, Settings } from 'lucide-react'

interface AppDrawerContentProps {
  appName: string
}

function formatUptime(ms: number): string {
  if (ms <= 0) return '—'
  const s = Math.floor(ms / 1000), m = Math.floor(s / 60), h = Math.floor(m / 60), d = Math.floor(h / 24)
  if (d > 0) return `${d}d ${h % 24}h ${m % 60}m`
  if (h > 0) return `${h}h ${m % 60}m ${s % 60}s`
  if (m > 0) return `${m}m ${s % 60}s`
  return `${s}s`
}

export function AppDrawerContent({ appName }: AppDrawerContentProps) {
  const [inspect, setInspect] = useState<AppInspectDto | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [restarting, setRestarting] = useState(false)
  const nsApps = useDashboardStore((s) => s.namespace?.apps)
  const appMeta = nsApps?.find((a) => a.name === appName)
  const openBottomTab = usePanelStore((s) => s.openBottomTab)
  const { t } = useTranslation()

  const load = useCallback(() => {
    const controller = new AbortController()
    getAppInspect(appName)
      .then((d) => { if (!controller.signal.aborted) { setInspect(d); setError(null) } })
      .catch((e) => { if (!controller.signal.aborted) setError(e.message) })
    return controller
  }, [appName])

  useEffect(() => {
    const controller = load()
    return () => controller.abort()
  }, [load])

  const handleRestart = async () => {
    setRestarting(true)
    try {
      await postAppRestart(appName)
      toast(t('drawer.restartRequested'), 'success')
    } catch (e) {
      toast((e as Error).message, 'error')
    } finally {
      setRestarting(false)
    }
  }

  if (error) {
    return <div className="text-destructive text-xs">{t('drawer.error', { error })}</div>
  }

  if (!inspect) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 4 }).map((_, i) => (
          <div key={i} className="h-3 w-full bg-muted rounded animate-pulse" />
        ))}
      </div>
    )
  }

  const isStopped = !inspect.containerId

  return (
    <div className="space-y-3">
      {/* Live status from SSE */}
      {appMeta && (
        <div className="flex items-center gap-2">
          <StatusBadge status={appMeta.status} />
          {appMeta.statusText && <span className="text-[10px] text-muted-foreground">{appMeta.statusText}</span>}
        </div>
      )}

      {/* Details grid */}
      <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
        <D l={t('drawer.container')} v={inspect.containerId?.slice(0, 12) || '—'} dim={isStopped} />
        <D l={t('drawer.image')} v={inspect.image} />
        <D l={t('drawer.state')} v={inspect.state} dim={isStopped} />
        <D l={t('drawer.network')} v={inspect.network} />
        <D l={t('drawer.started')} v={inspect.startedAt ? new Date(inspect.startedAt).toLocaleString() : '—'} dim={isStopped} />
        <D l={t('drawer.uptime')} v={formatUptime(inspect.uptime)} dim={isStopped} />
        <D l={t('drawer.restarts')} v={String(inspect.restartCount)} />
        <D l={t('drawer.ports')} v={inspect.ports?.join(', ') || '—'} />
      </div>

      {/* Volumes */}
      {(inspect.volumes?.length ?? 0) > 0 && (
        <div>
          <div className="text-xs font-medium mb-0.5">{t('drawer.volumes')}</div>
          <div className="max-h-24 overflow-y-auto">
            {inspect.volumes!.map((v, i) => (
              <div key={i} className="text-[11px] font-mono text-muted-foreground break-all">{v}</div>
            ))}
          </div>
        </div>
      )}

      {/* Environment */}
      {(inspect.env?.length ?? 0) > 0 && (
        <div>
          <div className="text-xs font-medium mb-0.5">{t('drawer.env')}</div>
          <div className="max-h-32 overflow-y-auto">
            {inspect.env!.map((e, i) => {
              const isMasked = e.endsWith('=***')
              return (
                <div key={i} className="text-[11px] font-mono overflow-hidden text-ellipsis whitespace-nowrap" title={e}>
                  {isMasked ? (
                    <><span className="text-muted-foreground">{e.slice(0, e.length - 3)}</span><span className="text-muted-foreground/50">***</span></>
                  ) : (
                    <span className="text-muted-foreground">{e}</span>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Action buttons */}
      <div className="flex gap-2 pt-2 border-t border-border">
        <button
          type="button"
          className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-muted"
          onClick={() => openBottomTab({ id: `logs:${appName}`, type: 'logs', title: t('logs.title', { name: appName }), appName })}
        >
          <FileText size={12} /> {t('drawer.viewLogs')}
        </button>
        <button
          type="button"
          className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-muted"
          onClick={() => openBottomTab({ id: `app-config:${appName}`, type: 'app-config', title: t('appConfig.tabTitle', { name: appName }), appName })}
        >
          <Settings size={12} /> {t('drawer.editConfig')}
        </button>
        <button
          type="button"
          className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-muted-foreground hover:text-foreground hover:bg-muted disabled:opacity-50"
          onClick={handleRestart}
          disabled={restarting}
        >
          <RotateCw size={12} /> {restarting ? t('drawer.restarting') : t('drawer.restart')}
        </button>
      </div>
    </div>
  )
}

function D({ l, v, dim }: { l: string; v: string; dim?: boolean }) {
  return <>
    <span className="text-muted-foreground">{l}</span>
    <span className={`font-mono truncate ${dim ? 'text-muted-foreground/50' : ''}`} title={v}>{v}</span>
  </>
}
