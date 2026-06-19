import { Loader2 } from 'lucide-react'
import { useTranslation } from '../lib/i18n'
import { useLongOpStore, type LongOpProgress } from '../lib/longOp'
import { useModalDialog } from '../hooks/useModalDialog'

interface LoadingOverlayProps {
  open: boolean
  title: string
  progress?: LongOpProgress
  stalled?: boolean
  onDismiss?: () => void
}

/**
 * Strictly blocking modal shown during long-running snapshot operations.
 * Kotlin parity: LoadingDialog.show(ActionStatus) — "Please, wait..." + spinner.
 * No close button, Escape suppressed, backdrop non-dismissable — except when
 * the watchdog has flagged the op as stalled (SSE down + no progress for
 * ~30s), in which case a Dismiss button appears so the user can recover
 * from a daemon crash mid-op.
 */
export function LoadingOverlay({ open, title, progress, stalled, onDismiss }: LoadingOverlayProps) {
  const { t } = useTranslation()
  const dialogRef = useModalDialog(open)

  const pct =
    progress && progress.total > 0
      ? Math.min(100, Math.round((progress.current / progress.total) * 100))
      : null

  const detail = progress?.message
    ? progress.message
    : progress && progress.total > 0
      ? t('longOp.progress.volumes', { current: progress.current, total: progress.total })
      : t('longOp.progress.indeterminate')

  return (
    <dialog
      ref={dialogRef}
      className="fixed inset-0 z-[100] m-auto max-w-sm w-full rounded-lg border border-border bg-card p-0 text-foreground backdrop:bg-black/60"
      onCancel={(e) => {
        // Always cancel the native Escape close — the dialog is a controlled
        // component (open driven by long-op store). When stalled, surface the
        // Escape gesture as a Dismiss so the consumer can clear the store;
        // otherwise the native close would race the next render's showModal
        // and the overlay would silently re-open without sync state.
        e.preventDefault()
        if (stalled) onDismiss?.()
      }}
    >
      <div className="px-6 py-8 flex flex-col items-center gap-4">
        <Loader2 className="animate-spin text-primary" size={40} />
        <h2 className="text-base font-semibold text-center">{title}</h2>
        <p className="text-xs text-muted-foreground text-center min-h-[1rem]">{detail}</p>
        {pct !== null && (
          <div className="w-full">
            <div className="h-1.5 w-full overflow-hidden rounded bg-muted">
              <div
                className="h-full bg-primary transition-[width] duration-200"
                style={{ width: `${pct}%` }}
              />
            </div>
            <p className="mt-1 text-[10px] text-muted-foreground text-right tabular-nums">{pct}%</p>
          </div>
        )}
        {stalled ? (
          <>
            <p className="text-[11px] text-amber-400 text-center">
              {t('longOp.stalled')}
            </p>
            <button
              type="button"
              onClick={onDismiss}
              className="mt-1 rounded border border-border bg-muted px-4 py-1.5 text-xs hover:bg-muted/80"
            >
              {t('longOp.dismiss')}
            </button>
          </>
        ) : (
          <p className="text-[11px] text-muted-foreground text-center">
            {t('longOp.pleaseWait')}
          </p>
        )}
      </div>
    </dialog>
  )
}

/**
 * Mounts the overlay globally and subscribes to the long-op store so any
 * surface (SnapshotsDialog, programmatic flows, future callers) can block the
 * UI without owning a local <dialog>.
 */
export function LoadingOverlayHost() {
  const current = useLongOpStore((s) => s.current)
  const end = useLongOpStore((s) => s.end)
  return (
    <LoadingOverlay
      open={current !== null}
      title={current?.title ?? ''}
      progress={current?.progress}
      stalled={current?.stalled ?? false}
      onDismiss={end}
    />
  )
}
