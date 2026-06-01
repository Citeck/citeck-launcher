import { useEffect, useState } from 'react'
import { FileText, Download } from 'lucide-react'
import { openSecondaryView, triggerSystemDump } from '../lib/desktop'
import { useTranslation } from '../lib/i18n'
import { toast } from '../lib/toast'

const HINT_DELAY_MS = 30_000
const TICK_MS = 5_000

function hasOpenDialog(): boolean {
  return typeof document !== 'undefined' && document.querySelector('dialog[open]') !== null
}

interface LoadingHintProps {
  /** When true, the parent is in a loading state — start the 30s timer. */
  active: boolean
}

/**
 * Inline "Still loading…" hint. Mirrors Kotlin's LoadingScreen behavior:
 * after {@link HINT_DELAY_MS} of continuous loading the hint becomes
 * visible with two recovery buttons.
 *
 * The component renders nothing during the first 30s, so embedding it is
 * cheap; parents just include it next to their loading skeleton.
 */
export function LoadingHint({ active }: LoadingHintProps) {
  const { t } = useTranslation()
  const [timerFired, setTimerFired] = useState(false)

  useEffect(() => {
    if (!active) return
    // Kotlin parity (LoadingScreen.kt:33-35): if a modal dialog is open, the
    // user is in an interactive flow — reset the start time instead of
    // accusing them of being stuck.
    let startedAt = Date.now()
    const interval = setInterval(() => {
      if (hasOpenDialog()) {
        startedAt = Date.now()
        return
      }
      if (Date.now() - startedAt >= HINT_DELAY_MS) {
        setTimerFired(true)
      }
    }, TICK_MS)
    return () => {
      clearInterval(interval)
      // Reset on cleanup (active→false) so a subsequent activation starts fresh.
      setTimerFired(false)
    }
  }, [active])

  // show only while actively loading AND the timer has fired
  if (!active || !timerFired) return null

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
          onClick={() => triggerSystemDump()
            .then((path) => toast(path ? t('dashboard.systemDump.saved', { path }) : t('dashboard.systemDump.success'), 'success'))
            .catch((e) => toast((e as Error).message, 'error'))}
        >
          <Download size={12} /> {t('loadingHint.dumpSystemInfo')}
        </button>
      </div>
    </div>
  )
}
