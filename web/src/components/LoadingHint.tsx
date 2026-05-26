import { useEffect, useState } from 'react'
import { FileText, Download } from 'lucide-react'
import { getSystemDump } from '../lib/api'
import { openSecondaryView } from '../lib/desktop'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

const HINT_DELAY_MS = 30_000

interface LoadingHintProps {
  /** When true, the parent is in a loading state — start the 30s timer. */
  active: boolean
}

/**
 * Inline "Still loading…" hint. Mirrors Kotlin's LoadingScreen behavior
 * (docs/porting/02 §5.2): after {@link HINT_DELAY_MS} of continuous loading
 * the hint becomes visible with two recovery buttons.
 *
 * The component renders nothing during the first 30s, so embedding it is
 * cheap; parents just include it next to their loading skeleton.
 */
export function LoadingHint({ active }: LoadingHintProps) {
  const { t } = useTranslation()
  const [show, setShow] = useState(false)

  useEffect(() => {
    if (!active) {
      setShow(false)
      return
    }
    const timer = setTimeout(() => setShow(true), HINT_DELAY_MS)
    return () => clearTimeout(timer)
  }, [active])

  if (!show) return null

  return (
    <div className="mt-6 max-w-md text-center">
      <p className="text-xs text-muted-foreground whitespace-pre-line mb-3">
        {t('loadingHint.stillLoading')}
      </p>
      <div className="flex items-center justify-center gap-2 text-xs">
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded border border-border px-2.5 py-1 hover:bg-muted text-foreground"
          onClick={() => openSecondaryView({ id: 'daemon-logs', type: 'daemon-logs', title: t('daemonLogs.title') })}
        >
          <FileText size={12} /> {t('loadingHint.showLogs')}
        </button>
        <span className="text-muted-foreground">|</span>
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded border border-border px-2.5 py-1 hover:bg-muted text-foreground"
          onClick={() => getSystemDump('zip')
            .then(() => toast(t('dashboard.systemDump.success'), 'success'))
            .catch((e) => toast((e as Error).message, 'error'))}
        >
          <Download size={12} /> {t('loadingHint.dumpSystemInfo')}
        </button>
      </div>
    </div>
  )
}
