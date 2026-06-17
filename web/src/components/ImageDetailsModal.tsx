import { useCallback, useEffect, useState } from 'react'
import { Copy, Download, Loader2 } from 'lucide-react'
import { Modal } from './Modal'
import { getAppImage, pullAppImage } from '../lib/api'
import type { AppImageDto } from '../lib/types'
import { useTranslation } from '../lib/i18n'
import { copyText } from '../lib/clipboard'
import { toast } from '../lib/toast'
import { formatDateTime } from '../lib/datetime'
import { formatBytes } from '../lib/format'

/** Label + value row; value is monospace and optionally copyable. */
function Row({ label, value, copyable }: { label: string; value: string; copyable?: boolean }) {
  const { t } = useTranslation()
  return (
    <>
      <div className="text-muted-foreground">{label}</div>
      <div className="flex items-start gap-1 min-w-0 font-mono break-all">
        <span className="min-w-0">{value}</span>
        {copyable && value !== '—' && (
          <button
            type="button"
            className="shrink-0 text-muted-foreground hover:text-foreground"
            title={t('logViewer.copy')}
            onClick={() => { void copyText(value); toast(t('clipboard.copied'), 'success') }}
          >
            <Copy size={12} />
          </button>
        )}
      </div>
    </>
  )
}

/**
 * Image-details popup opened from the drawer's image row (eye button). Shows the
 * local image's sha256 / digests / size / platform / created date. When the
 * image isn't pulled it says so and offers an explicit Pull (even for release
 * tags); the dialog polls while the pull runs.
 */
export function ImageDetailsModal({ appName, open, onClose }: { appName: string; open: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const [info, setInfo] = useState<AppImageDto | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pulling, setPulling] = useState(false)

  const load = useCallback(async () => {
    setError(null)
    try {
      const dto = await getAppImage(appName)
      setInfo(dto)
      setPulling(!!dto.pulling)
      if (dto.pullError) setError(dto.pullError)
    } catch (e) {
      setError((e as Error).message)
    }
  }, [appName])

  useEffect(() => {
    if (!open) return
    // Intentional one-shot reset on (re)open, then fetch — not a cascading
    // render (load only flips state from network results).
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setInfo(null)
    setError(null)
    void load()
  }, [open, load])

  // Poll while a pull is in flight so the dialog flips to details on completion.
  useEffect(() => {
    if (!open || !pulling) return
    const id = setInterval(() => { void load() }, 2000)
    return () => clearInterval(id)
  }, [open, pulling, load])

  const startPull = async () => {
    setPulling(true)
    setError(null)
    try {
      await pullAppImage(appName)
      void load()
    } catch (e) {
      setPulling(false)
      setError((e as Error).message)
    }
  }

  return (
    <Modal open={open} title={t('imageDetails.title')} onClose={onClose} width="lg">
      <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
        <Row label={t('drawer.image')} value={info?.ref ?? appName} copyable />
        {info?.present && (
          <>
            <Row label={t('imageDetails.id')} value={info.id ?? '—'} copyable />
            <Row label={t('imageDetails.digest')} value={info.repoDigests?.[0] ?? '—'} copyable />
            <Row label={t('imageDetails.size')} value={formatBytes(info.size)} />
            <Row label={t('imageDetails.platform')} value={info.os && info.architecture ? `${info.os}/${info.architecture}` : '—'} />
            <Row label={t('imageDetails.created')} value={info.created ? formatDateTime(info.created) : '—'} />
          </>
        )}
      </div>

      {/* Not-pulled / pulling / error states */}
      {info && !info.present && (
        <div className="mt-4 rounded border border-border bg-muted/40 px-3 py-3 text-xs">
          {pulling ? (
            <div className="flex items-center gap-2 text-muted-foreground">
              <Loader2 size={14} className="animate-spin" />
              {t('imageDetails.pulling')}
            </div>
          ) : (
            <div className="flex items-center justify-between gap-3 flex-wrap">
              <span className="text-muted-foreground">{t('imageDetails.notPulled')}</span>
              <button
                type="button"
                className="flex items-center gap-1 rounded bg-primary px-3 py-1.5 font-medium text-primary-foreground hover:bg-primary/90"
                onClick={() => { void startPull() }}
              >
                <Download size={13} />
                {t('imageDetails.pull')}
              </button>
            </div>
          )}
        </div>
      )}

      {error && !pulling && (
        <div className="mt-3 text-[11px] text-destructive break-all">{error}</div>
      )}
    </Modal>
  )
}
