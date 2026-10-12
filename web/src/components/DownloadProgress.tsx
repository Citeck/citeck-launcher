import { useEffect, useState } from 'react'
import { useTranslation } from '../lib/i18n'
import type { DownloadProgressDto } from '../lib/types'

/** Bytes and phases come from the backend; animation never implies measured progress. */
export function DownloadProgress({ progress, fallbackPhase = 'downloading' }: { progress?: DownloadProgressDto; fallbackPhase?: string }) {
  const { t } = useTranslation()
  const [now, setNow] = useState(Date.now)
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1_000)
    return () => clearInterval(timer)
  }, [])
  const phase = progress?.phase || fallbackPhase
  const received = Math.max(0, progress?.receivedBytes ?? 0)
  const total = progress?.totalBytes ?? 0
  const downloading = phase === 'downloading'
  const pct = downloading && total > 0 ? Math.min(100, Math.max(0, Math.floor(received / total * 100))) : undefined
  const seconds = Math.max(0, Math.floor((now - (progress?.startedAt || now)) / 1_000))
  const idle = progress?.updatedAt ? Math.max(0, Math.floor((now - progress.updatedAt) / 1_000)) : 0
  const bytes = (n: number) => `${(n / 1048576).toFixed(1)} MiB`
  const labels = { checking: 'download.phase.checking', downloading: 'download.phase.downloading', verifying: 'download.phase.verifying', extracting: 'download.phase.extracting', ready: 'download.phase.ready', importing: 'download.phase.importing' } as const
  const label = labels[phase as keyof typeof labels] ?? labels.downloading
  return (
    <div className="w-full space-y-2 text-xs" aria-live="polite">
      <p>{t(label)}</p>
      <div role="progressbar" aria-label={t(label)} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="h-2 w-full overflow-hidden rounded bg-muted">
        <div className={pct === undefined ? 'h-full w-1/3 animate-pulse rounded bg-primary' : 'h-full bg-primary transition-[width] duration-200'} style={pct === undefined ? undefined : { width: `${pct}%` }} />
      </div>
      {downloading && <p className="text-right tabular-nums">{bytes(received)}{total > 0 ? ` / ${bytes(total)} · ${pct}%` : ''}</p>}
      <p className="text-muted-foreground">{t('download.elapsed', { seconds })}</p>
      {idle >= 30 && <p className="text-amber-400">{t('download.idle', { seconds: idle })}</p>}
    </div>
  )
}
