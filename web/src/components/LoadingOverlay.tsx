import { useEffect, useRef } from 'react'
import { Loader2 } from 'lucide-react'
import { useTranslation } from '../lib/i18n'
import { useLongOpStore, type LongOpProgress } from '../lib/longOp'

interface LoadingOverlayProps {
  open: boolean
  title: string
  progress?: LongOpProgress
}

/**
 * Strictly blocking modal shown during long-running snapshot operations.
 * Kotlin parity: LoadingDialog.show(ActionStatus) — "Please, wait..." + spinner.
 * No close button, Escape suppressed, backdrop non-dismissable.
 */
export function LoadingOverlay({ open, title, progress }: LoadingOverlayProps) {
  const { t } = useTranslation()
  const dialogRef = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    else if (!open && dialog.open) dialog.close()
  }, [open])

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
      onCancel={(e) => e.preventDefault()}
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
        <p className="text-[11px] text-muted-foreground text-center">
          {t('longOp.pleaseWait')}
        </p>
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
  return (
    <LoadingOverlay
      open={current !== null}
      title={current?.title ?? ''}
      progress={current?.progress}
    />
  )
}
