import { useEffect, useState, useCallback } from 'react'
import { getAppInspect, postAppRestart } from '../lib/api'
import type { AppInspectDto } from '../lib/types'
import { useDashboardStore } from '../lib/store'
import { initProgressOf } from '../lib/initProgress'
import { openSecondaryView } from '../lib/desktop'
import { formatDateTime } from '../lib/datetime'
import { RegistryCredentialsDialog } from './RegistryCredentialsDialog'
import { RestartEvents } from './RestartEvents'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'
import { copyText } from '../lib/clipboard'
import { RotateCw, FileText, Settings, KeyRound, Copy } from 'lucide-react'

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
  const [credsDialogOpen, setCredsDialogOpen] = useState(false)
  const nsApps = useDashboardStore((s) => s.namespace?.apps)
  const appMeta = nsApps?.find((a) => a.name === appName)
  const initProg = appMeta ? initProgressOf(appMeta) : null
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
      {/* Init-container progress while STARTING — long eapps starts run a chain
          of init steps; "Init 2/5: ecos-app-x" tells the user which one is live. */}
      {initProg && (
        <div className="text-[11px] text-muted-foreground">
          {t('drawer.initStep', { step: initProg.step, total: initProg.total, name: initProg.name })}
        </div>
      )}

      {/* The launcher status badge is in the drawer header (subtitle) and the
          Docker container state is the "Состояние" row; here we only surface
          statusText (e.g. failure detail), when present. */}
      {appMeta?.statusText && (
        <div className="text-[11px] text-muted-foreground">{appMeta.statusText}</div>
      )}

      {/* Details grid */}
      <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
        <D l={t('drawer.container')} v={inspect.containerId?.slice(0, 12) || '—'} dim={isStopped} />
        <D l={t('drawer.image')} v={inspect.image} copy={inspect.image} />
        <D l={t('drawer.state')} v={inspect.state} dim={isStopped} />
        <D l={t('drawer.network')} v={inspect.network} />
        <D l={t('drawer.started')} v={inspect.startedAt ? formatDateTime(inspect.startedAt) : '—'} dim={isStopped} />
        <D l={t('drawer.uptime')} v={formatUptime(inspect.uptime)} dim={isStopped} />
        {/* Single source of truth with the app-table "↻N" badge: the launcher's
            own restart count (appMeta), NOT Docker's container RestartCount —
            we don't use Docker restart policies, so inspect.restartCount is
            always 0 and would diverge from the badge. */}
        <D l={t('drawer.restarts')} v={String(appMeta?.restartCount ?? 0)} />
        <D l={t('drawer.ports')} v={inspect.ports?.join(', ') || '—'} />
      </div>

      {/* Volumes */}
      {(inspect.volumes?.length ?? 0) > 0 && (
        <div>
          <div className="text-xs font-medium mb-0.5">{t('drawer.volumes')}</div>
          <div className="max-h-24 overflow-y-auto">
            {inspect.volumes!.map((v, i) => (
              <div key={i} className="text-[11px] font-mono text-foreground break-all">{v}</div>
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
                <div key={i} className="text-[11px] font-mono overflow-hidden text-ellipsis whitespace-nowrap text-foreground" title={e}>
                  {isMasked ? (
                    <><span>{e.slice(0, e.length - 3)}</span><span className="text-muted-foreground">***</span></>
                  ) : (
                    <span>{e}</span>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* Restart log (per-app) — moved here from the former bottom "Перезапуски" tab. */}
      <div className="pt-2 border-t border-border">
        <RestartEvents appName={appName} />
      </div>

      {/* Action buttons */}
      <div className="flex gap-2 pt-2 border-t border-border flex-wrap">
        <button
          type="button"
          className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-foreground hover:bg-muted"
          onClick={() => openSecondaryView({ id: `logs:${appName}`, type: 'logs', title: t('logs.title', { name: appName }), appName })}
        >
          <FileText size={12} /> {t('drawer.viewLogs')}
        </button>
        <button
          type="button"
          className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-foreground hover:bg-muted"
          onClick={() => openSecondaryView({ id: `app-config:${appName}`, type: 'app-config', title: t('appConfig.tabTitle', { name: appName }), appName })}
        >
          <Settings size={12} /> {t('drawer.editConfig')}
        </button>
        <button
          type="button"
          className="flex items-center gap-1 rounded border border-border px-2 py-1 text-xs text-foreground hover:bg-muted disabled:opacity-50"
          onClick={handleRestart}
          disabled={restarting}
        >
          <RotateCw size={12} /> {restarting ? t('drawer.restarting') : t('drawer.restart')}
        </button>
        {/* Surface "Configure registry credentials" when pull fails with an auth error. */}
        {appMeta?.status === 'PULL_FAILED' && isAuthErrorText(appMeta?.statusText) && registryHostOf(appMeta?.image) && (
          <button
            type="button"
            className="flex items-center gap-1 rounded border border-warning/40 bg-warning/10 px-2 py-1 text-xs text-warning hover:bg-warning/20"
            onClick={() => setCredsDialogOpen(true)}
            title={t('registryCreds.bannerTooltip')}
          >
            <KeyRound size={12} /> {t('registryCreds.banner')}
          </button>
        )}
      </div>

      <RegistryCredentialsDialog
        open={credsDialogOpen}
        host={registryHostOf(appMeta?.image) || ''}
        onClose={() => setCredsDialogOpen(false)}
      />
    </div>
  )
}

/** Extracts the registry host from a Docker image reference. */
function registryHostOf(image: string | undefined): string {
  if (!image) return ''
  const slash = image.indexOf('/')
  if (slash < 0) return ''
  const head = image.slice(0, slash)
  // Docker convention: if first segment contains a dot or colon, it's a registry host.
  if (!head.includes('.') && !head.includes(':')) return ''
  return head
}

/** Heuristic: does the daemon's statusText look like a pull-auth failure? */
function isAuthErrorText(text: string | undefined): boolean {
  if (!text) return false
  const t = text.toLowerCase()
  return t.includes('authentication') || t.includes('unauthorized') || t.includes('401') || t.includes('denied')
}

function D({ l, v, dim, copy }: { l: string; v: string; dim?: boolean; copy?: string }) {
  return <>
    <span className="text-muted-foreground">{l}</span>
    {copy != null ? (
      <span className="group flex items-center gap-1 min-w-0">
        <span className={`font-mono truncate ${dim ? 'text-muted-foreground/50' : 'text-foreground'}`} title={v}>{v}</span>
        <CopyButton text={copy} />
      </span>
    ) : (
      <span className={`font-mono truncate ${dim ? 'text-muted-foreground/50' : 'text-foreground'}`} title={v}>{v}</span>
    )}
  </>
}

// Copy button revealed on hover of its row (the image+tag value), so the full
// image reference can be copied from the right panel — the apps-table tag cell
// no longer copies on click.
function CopyButton({ text }: { text: string }) {
  const { t } = useTranslation()
  return (
    <button
      type="button"
      className="shrink-0 p-0.5 rounded text-muted-foreground opacity-0 group-hover:opacity-100 hover:text-foreground hover:bg-muted transition-opacity"
      title={t('table.copy', { image: text })}
      onClick={async () => { if (await copyText(text)) toast(t('clipboard.copied'), 'success') }}
    >
      <Copy size={12} />
    </button>
  )
}
