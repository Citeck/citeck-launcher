import { AlertTriangle } from 'lucide-react'
import { useDashboardStore } from '../lib/store'
import { useTranslation } from '../lib/i18n'

/**
 * Namespace-level banner for `NamespaceDto.stateWriteError`.
 *
 * The daemon retries a state write its store refused, and reports the failure
 * streak once in its own log — but every action that produces such a write
 * answers SUCCESS, because the action itself succeeded: detaching an app really
 * stops the container, saving the gear editor really applies the patch to the
 * live one, editing a mounted file really rewrites it. Only the RECORD of the
 * intent was refused. So on a full disk, a permission change or a damaged
 * SQLite file, the UI looked completely normal and the operator found out at the
 * next daemon start, where the detached app was running again and the edit was
 * gone.
 *
 * Deliberately NOT dismissible, for the same reason as BundleErrorBanner: it
 * describes a condition that is true RIGHT NOW, and hiding it re-creates exactly
 * the invisibility it exists to remove. It costs nothing to leave up, because
 * the daemon derives it from the live failure streak — the first write that
 * lands clears it with no action from anyone.
 */
export function StateWriteBanner() {
  const stateWriteError = useDashboardStore((s) => s.namespace?.stateWriteError ?? '')
  const { t } = useTranslation()

  if (!stateWriteError) return null

  return (
    <div
      role="alert"
      className="flex shrink-0 items-start gap-2 border-b border-destructive/40 bg-destructive/10 px-3 py-1.5 text-xs text-destructive"
    >
      <AlertTriangle size={14} className="mt-0.5 shrink-0" />
      <div className="min-w-0 flex-1">
        <div className="font-medium">{t('dashboard.stateWrite.title')}</div>
        {/* The store's own message, verbatim — it is the only part that says
            WHICH failure this is (out of space, denied, database locked) and
            therefore the only actionable part. */}
        <div className="text-muted-foreground">{t('dashboard.stateWrite.reason', { reason: stateWriteError })}</div>
        <div className="text-muted-foreground">{t('dashboard.stateWrite.hint')}</div>
      </div>
    </div>
  )
}
